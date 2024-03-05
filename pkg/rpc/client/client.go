package rdc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	connhandler "github.com/antontroskie/radrpc/pkg/rpc/connhandler"
	"github.com/antontroskie/radrpc/pkg/rpc/itf"
	"github.com/antontroskie/radrpc/pkg/utils"
	"github.com/google/uuid"
)

const MessageFlushTimeout = time.Millisecond * 10

type RPCClientConfig struct {
	Host                    string
	MaxConnectionRetries    int
	ConnectionRetryInterval time.Duration
	MaxRPCCallWaitTime      time.Duration
	UseTLS                  bool
	TLSConfig               *tls.Config
	LoggerInterface         itf.RPCLogger
}

type RPCClientMessagePool struct {
	addToMsgPoolHandler    chan RPCClientMessagePoolMsg
	readFromMsgPoolHandler chan RPCClientMessagePoolMsg
	reconnectSignal        chan bool
	ctx                    context.Context
	cancel                 context.CancelFunc
	mu                     *sync.Mutex
	// Protects the messages map
	messages map[string]RPCClientMessagePoolMsg
}

func NewRPCClientMessagePool() RPCClientMessagePool {
	ctx, cancel := context.WithCancel(context.Background())
	return RPCClientMessagePool{
		mu:       &sync.Mutex{},
		messages: make(map[string]RPCClientMessagePoolMsg),
		// TODO: Should this be buffered?
		addToMsgPoolHandler: make(chan RPCClientMessagePoolMsg),
		// TODO: Should this be buffered?
		readFromMsgPoolHandler: make(chan RPCClientMessagePoolMsg),
		reconnectSignal:        make(chan bool),
		ctx:                    ctx,
		cancel:                 cancel,
	}
}

type messageStatus int

const (
	Pending messageStatus = iota
	Handled
	Failed
)

// RPCClientMessagePoolMsg is a message in the message pool.
type RPCClientMessagePoolMsg struct {
	// messageRequest is the request message
	messageRequest itf.RPCMessageReq
	// messageResponse is the response message
	messageResponse itf.RPCMessageRes
	// msgStatus is the status of the message
	msgStatus messageStatus
}

// RPCClientInterace is the general interface for the client.
type RPCClientInterace interface {
	// GetConn gets the connection
	GetConn() net.Conn
	// SetConn sets the connection
	SetConn(net.Conn)
	// SetConfig sets the configuration
	SetConfig(RPCClientConfig)
	// GetConfig gets the configuration
	GetConfig() RPCClientConfig
	// GetMessagePool gets the message pool
	GetMessagePool() RPCClientMessagePool
	// SetMessagePool sets the message pool
	SetMessagePool(RPCClientMessagePool)
}

// GetConn gets the connection.
func validateConfig(config *RPCClientConfig) error {
	if config == nil || itf.IsDefaultValue(*config) {
		return fmt.Errorf("no configuration applied")
	}
	if config.ConnectionRetryInterval < time.Microsecond {
		return fmt.Errorf(
			"invalid configuration: connection retry interval must be at least 1 microsecond",
		)
	}
	if config.MaxRPCCallWaitTime < time.Microsecond {
		return fmt.Errorf(
			"invalid configuration: max rpc call wait time must be at least 1 microsecond",
		)
	}
	if config.LoggerInterface == nil {
		config.LoggerInterface = itf.NewDefaultLogger()
	}
	return nil
}

func handleReconnectOnNetworkError(
	cancel context.CancelFunc,
	client RPCClientInterace,
	clientConfig RPCClientConfig,
	conn net.Conn,
	reconnectSignal chan bool,
) {
	if <-reconnectSignal {
		logger := clientConfig.LoggerInterface
		// Check if it's a network error
		logger.LogWarn("connection lost, attempting to reconnect")
		cancel()

		// Close the connection
		if closeErr := conn.Close(); closeErr != nil {
			logger.LogError(
				fmt.Errorf("error closing connection: %s", closeErr.Error()),
			)
		}
		// Set the connection to nil
		client.SetConn(nil)
		// Restart the connection process
		if connectErr := Connect(client); connectErr != nil {
			logger.LogError(
				fmt.Errorf("error reconnecting: %s", connectErr.Error()),
			)
		}
	}
}

func handlePoolInjection(
	ctx context.Context,
	logger itf.RPCLogger,
	clientMessagePool RPCClientMessagePool,
	addToMsgPool chan RPCClientMessagePoolMsg,
) {
	for {
		select {
		case message := <-addToMsgPool:
			clientMessagePool.mu.Lock()
			switch message.msgStatus {
			case Pending:
				// Add message to the pool
				clientMessagePool.messages[message.messageResponse.ID] = message

			case Handled:
				// Remove message from the pool
				delete(clientMessagePool.messages, message.messageResponse.ID)
				timeStampDateVal := time.Unix(0, message.messageResponse.TimeStamp)
				requestCallStructure := itf.GetDisplayCallStructureFromReq(
					message.messageRequest,
				)
				logger.LogDebug(
					fmt.Sprintf(
						"request handled: [%s] %s -> %v -> %v",
						timeStampDateVal.Format(time.RFC3339Nano),
						message.messageResponse.ID,
						requestCallStructure,
						message.messageResponse.ResponseSuccess,
					),
				)

			case Failed:
				// Remove message from the pool
				delete(clientMessagePool.messages, message.messageResponse.ID)
				logger.LogError(
					fmt.Errorf(
						"request %v failed: %s",
						message.messageResponse.ID,
						message.messageResponse.ResponseError,
					),
				)
			}
			clientMessagePool.mu.Unlock()
		case <-ctx.Done():
			return // Stop the handler
		}
	}
}

func handlePoolFlushing(
	ctx context.Context,
	maxRPCCallWaitTime time.Duration,
	readFromMsgPool chan RPCClientMessagePoolMsg,
	addToMsgPool chan RPCClientMessagePoolMsg,
	clientMessagePool RPCClientMessagePool,
) {
	for {
		select {
		case <-ctx.Done():
			return // Stop the handler

		case <-time.After(MessageFlushTimeout):
			clientMessagePool.mu.Lock()
			for _, message := range clientMessagePool.messages {
				if time.Now().
					UnixNano()-
					message.messageResponse.TimeStamp > int64(
					maxRPCCallWaitTime,
				) {
					// Message has been pending for too long; let's update its status
					message.msgStatus = Failed
					message.messageResponse.ResponseError = "timed out waiting for response handler"
					addToMsgPool <- message
				} else {
					// Send message through
					readFromMsgPool <- message
				}
			}
			clientMessagePool.mu.Unlock()
		}
	}
}

func handleIncomingMessages(
	conn net.Conn,
	logger itf.RPCLogger,
	addToMsgPool chan RPCClientMessagePoolMsg,
	reconnectSignal chan bool,
) {
	// This is the main loop that will receive all messages
	for {
		receivedMessage, readErr := connhandler.ReadMessage(conn)
		// Check if error is because of a closed connection
		if readErr != nil {
			reconnectSignal <- true
			return
		}
		decodedMessage, decodeErr := connhandler.DecodeMessage[itf.RPCMessageRes](receivedMessage)
		if decodeErr != nil {
			logger.LogError(fmt.Errorf("error decoding message: %s", decodeErr.Error()))
			continue
		}
		switch decodedMessage.Type {
		case itf.HeartbeatRequest:
			if writeHBErr := connhandler.WriteHeartbeatResponse(conn, decodedMessage.ID); writeHBErr != nil {
				reconnectSignal <- true
				return
			}
		case itf.MethodRequest, itf.HeartbeatResponse, itf.MethodResponse:
			// Send the message as a pending message to the handler channel
			addToMsgPool <- RPCClientMessagePoolMsg{
				messageResponse: decodedMessage,
				msgStatus:       Pending,
			}
		}
	}
}

// handleMessagePool handles messages.
func handleMessagePool(
	conn net.Conn,
	clientConfig RPCClientConfig,
	clientMessagePool RPCClientMessagePool,
) {
	wg := &sync.WaitGroup{}

	ctx := clientMessagePool.ctx

	clientLogger := clientConfig.LoggerInterface
	readFromMsgPool := clientMessagePool.readFromMsgPoolHandler
	addToMsgPool := clientMessagePool.addToMsgPoolHandler
	reconnectSignal := clientMessagePool.reconnectSignal
	maxRPCCallWaitTime := clientConfig.MaxRPCCallWaitTime

	wg.Add(1)
	go func() {
		defer wg.Done()
		handlePoolInjection(ctx, clientLogger, clientMessagePool, addToMsgPool)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		handlePoolFlushing(
			ctx,
			maxRPCCallWaitTime,
			readFromMsgPool,
			addToMsgPool,
			clientMessagePool,
		)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		handleIncomingMessages(
			conn,
			clientLogger,
			addToMsgPool,
			reconnectSignal,
		)
	}()

	// Wait for other go routines to finish
	wg.Wait()
}

// SendAndReceiveMessage sends a message and waits for a response.
func SendAndReceiveMessage(
	client RPCClientInterace,
	message itf.RPCMessageReq,
) itf.RPCMessageRes {
	clientConfig := client.GetConfig()
	messagePool := client.GetMessagePool()
	maxWaitTime := clientConfig.MaxRPCCallWaitTime
	message.ID = uuid.New().String()
	timeStamp := time.Now().UnixNano()
	message.TimeStamp = timeStamp
	conn := client.GetConn()
	createResponseError := func(err error) itf.RPCMessageRes {
		return itf.RPCMessageRes{
			ID:            message.ID,
			TimeStamp:     timeStamp,
			ResponseError: err.Error(),
		}
	}

	if conn == nil {
		return createResponseError(fmt.Errorf(
			"no connection established, cannot execute method %s",
			message.Method,
		))
	}

	if err := conn.SetReadDeadline(time.Now().Add(maxWaitTime)); err != nil {
		return createResponseError(err)
	}
	result, err := utils.RunWithTimeout(
		func() itf.RPCMessageRes {
			encodedMessage, err := connhandler.EncodeMessage(message)
			if err != nil {
				return createResponseError(err)
			}
			err = connhandler.WriteMessage(conn, encodedMessage)
			if err != nil {
				return createResponseError(err)
			}
			for {
				select {
				case unhandledMsg := <-messagePool.readFromMsgPoolHandler:
					if unhandledMsg.messageResponse.ID == message.ID {
						unhandledMsg.msgStatus = Handled
						unhandledMsg.messageRequest = message
						// Mark message as handled so it can be removed
						messagePool.addToMsgPoolHandler <- unhandledMsg
						return unhandledMsg.messageResponse
					}
				case <-time.After(maxWaitTime):
					return createResponseError(
						fmt.Errorf("timed out trying to read response message"),
					)
				}
			}
		}, maxWaitTime)
	if err != nil {
		return createResponseError(err)
	}
	return result
}

// Disconnect disconnects from the server.
func Disconnect(client RPCClientInterace) error {
	clientConfig := client.GetConfig()
	conn := client.GetConn()
	if err := validateConfig(&clientConfig); err != nil {
		return err
	}
	if conn == nil {
		return fmt.Errorf("no connection to disconnect")
	}
	clientMsgPool := client.GetMessagePool()
	clientMsgPoolCancel := clientMsgPool.cancel
	clientReconnectSignal := clientMsgPool.reconnectSignal
	if clientMsgPoolCancel == nil {
		return fmt.Errorf("no message pool cancel function found")
	}
	clientReconnectSignal <- false
	clientMsgPoolCancel()
	if err := conn.Close(); err != nil {
		return fmt.Errorf("error disconnecting: %w", err)
	}
	client.SetConn(nil)
	clientConfig.LoggerInterface.LogInfo(fmt.Sprintf("disconnected from %s", clientConfig.Host))
	return nil
}

// Connect connects to the server.
func Connect(client RPCClientInterace) error {
	clientConfig := client.GetConfig()
	if err := validateConfig(&clientConfig); err != nil {
		return err
	}
	clientMsgPool := client.GetMessagePool()
	if clientMsgPool.mu == nil {
		clientMsgPool = NewRPCClientMessagePool()
		client.SetMessagePool(clientMsgPool)
	}
	clientConn := client.GetConn()
	tryConnect := func() error {
		if clientConn != nil {
			return fmt.Errorf("connection already established")
		}
		if clientConfig.UseTLS {
			conn, err := tls.Dial("tcp", clientConfig.Host, clientConfig.TLSConfig)
			if err != nil {
				return fmt.Errorf("error connecting: %w", err)
			}
			clientConn = conn
			client.SetConn(conn)
			return nil
		}
		// connect to the server
		conn, err := net.Dial("tcp", clientConfig.Host)
		if err != nil {
			return fmt.Errorf("error connecting: %w", err)
		}
		clientConn = conn
		client.SetConn(conn)
		return nil
	}
	if err := utils.RetryOperation(
		tryConnect,
		clientConfig.MaxConnectionRetries,
		clientConfig.ConnectionRetryInterval,
		clientConfig.LoggerInterface.LogError,
	); err != nil {
		return fmt.Errorf("error connecting: %w", err)
	}
	clientConfig.LoggerInterface.LogInfo(fmt.Sprintf("connected to %s", clientConfig.Host))
	cancel := clientMsgPool.cancel
	reconnectSignal := clientMsgPool.reconnectSignal
	go handleMessagePool(clientConn, clientConfig, clientMsgPool)
	go handleReconnectOnNetworkError(cancel, client, clientConfig, clientConn, reconnectSignal)
	return nil
}
