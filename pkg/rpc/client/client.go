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

// MessageFlushTimeout is the message flush timeout.
const MessageFlushTimeout = time.Millisecond * 10

type RPCClientConfig struct {
	// Host is the host to connect to
	Host string
	// MaxConnectionRetries is the maximum number of connection
	// retries, if zero it will retry indefinitely
	MaxConnectionRetries int
	// ConnectionRetryInterval is the interval between connection retries
	ConnectionRetryInterval time.Duration
	// MaxRPCCallWaitTime is the maximum time to wait for an RPC call
	MaxRPCCallWaitTime time.Duration
	// UseTLS is whether to use TLS
	UseTLS bool
	// TLSConfig is the TLS configuration
	TLSConfig *tls.Config
	// LoggerInterface is the logger interface
	LoggerInterface itf.RPCLogger
}

type RPCClientMessagePool struct {
	// addToMsgPoolHandler is the channel to add messages to the message pool
	addToMsgPoolHandler chan RPCClientMessagePoolMsg
	// readFromMsgPoolHandler is the channel to read messages from the message pool
	readFromMsgPoolHandler chan RPCClientMessagePoolMsg
	// reconnectSignal is the channel to signal a reconnect
	reconnectSignal chan bool
	// cancel is the cancel function for the message pool
	cancel context.CancelFunc
	// mu is the mutex for the message pool
	mu *sync.Mutex
	// Protects the messages map
	messages map[string]RPCClientMessagePoolMsg
}

// SetCancelFunc sets the cancel function for the message pool.
func (pool *RPCClientMessagePool) SetCancelFunc(cancel context.CancelFunc) {
	pool.cancel = cancel
}

// NewRPCClientMessagePool creates a new message pool.
func NewRPCClientMessagePool() RPCClientMessagePool {
	return RPCClientMessagePool{
		mu:       &sync.Mutex{},
		messages: make(map[string]RPCClientMessagePoolMsg),
		// TODO: Should this be buffered?
		addToMsgPoolHandler: make(chan RPCClientMessagePoolMsg),
		// TODO: Should this be buffered?
		readFromMsgPoolHandler: make(chan RPCClientMessagePoolMsg),
		reconnectSignal:        make(chan bool),
		cancel:                 nil,
	}
}

// messageStatus is the status of a message.
type messageStatus int

const (
	// Pending is the status of a pending message.
	Pending messageStatus = iota
	// Handled is the status of a handled message.
	Handled
	// Failed is the status of a failed message.
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

// RPCClientInterace is the general interface for the clientInterface.
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

// handleReconnectOnNetworkError handles reconnecting on network error.
func handleReconnectOnNetworkError(
	cancel context.CancelFunc,
	clientInterface RPCClientInterace,
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
		clientInterface.SetConn(nil)
		// Restart the connection process
		if connectErr := Connect(clientInterface); connectErr != nil {
			logger.LogError(
				fmt.Errorf("error reconnecting: %s", connectErr.Error()),
			)
		}
	}
}

// handlePoolInjection handles the message pool.
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

// handlePoolFlushing handles the message pool flusing to available handlers.
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

// handleIncomingMessages handles incoming messages.
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
		case itf.ExecMethodRequest, itf.HeartbeatResponse, itf.ExecMethodResponse:
			// Send the message as a pending message to the handler channel
			addToMsgPool <- RPCClientMessagePoolMsg{
				messageRequest: itf.RPCMessageReq{
					ID:        decodedMessage.ID,
					Type:      decodedMessage.Type,
					TimeStamp: decodedMessage.TimeStamp,
					Method:    "",
					Args:      []any{},
				},
				messageResponse: decodedMessage,
				msgStatus:       Pending,
			}
		}
	}
}

// handleMessagePool handles incoming messages, including passing messages to handlers.
func handleMessagePool(
	ctx context.Context,
	conn net.Conn,
	clientConfig RPCClientConfig,
	clientMessagePool RPCClientMessagePool,
) {
	wg := &sync.WaitGroup{}

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
	clientInterface RPCClientInterace,
	message itf.RPCMessageReq,
) itf.RPCMessageRes {
	clientConfig := clientInterface.GetConfig()
	messagePool := clientInterface.GetMessagePool()
	maxWaitTime := clientConfig.MaxRPCCallWaitTime
	message.ID = uuid.New().String()
	timeStamp := time.Now().UnixNano()
	message.TimeStamp = timeStamp
	conn := clientInterface.GetConn()
	createResponseError := func(err error) itf.RPCMessageRes {
		return itf.RPCMessageRes{
			ID:              message.ID,
			Type:            itf.ExecMethodRequest,
			TimeStamp:       timeStamp,
			ResponseError:   err.Error(),
			ResponseSuccess: nil,
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
func Disconnect(clientInterface RPCClientInterace) error {
	clientConfig := clientInterface.GetConfig()
	conn := clientInterface.GetConn()
	if err := validateConfig(&clientConfig); err != nil {
		return err
	}
	if conn == nil {
		return fmt.Errorf("no connection to disconnect")
	}
	clientMsgPool := clientInterface.GetMessagePool()
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
	clientInterface.SetConn(nil)
	clientConfig.LoggerInterface.LogInfo(fmt.Sprintf("disconnected from %s", clientConfig.Host))
	return nil
}

// Connect connects to the server.
func Connect(clientInterface RPCClientInterace) error {
	clientConfig := clientInterface.GetConfig()
	if err := validateConfig(&clientConfig); err != nil {
		return err
	}
	clientMsgPool := clientInterface.GetMessagePool()
	if clientMsgPool.mu == nil {
		clientMsgPool = NewRPCClientMessagePool()
		clientInterface.SetMessagePool(clientMsgPool)
	}
	clientConn := clientInterface.GetConn()
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
			clientInterface.SetConn(conn)
			return nil
		}
		// connect to the server
		conn, err := net.Dial("tcp", clientConfig.Host)
		if err != nil {
			return fmt.Errorf("error connecting: %w", err)
		}
		clientConn = conn
		clientInterface.SetConn(conn)
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
	reconnectSignal := clientMsgPool.reconnectSignal
	ctx, cancel := context.WithCancel(context.Background())
	clientMsgPool.SetCancelFunc(cancel)
	go handleMessagePool(ctx, clientConn, clientConfig, clientMsgPool)
	go handleReconnectOnNetworkError(
		cancel,
		clientInterface,
		clientConfig,
		clientConn,
		reconnectSignal,
	)
	return nil
}
