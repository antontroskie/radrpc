package rds

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
	ants "github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
)

// RPCServiceConfig is the configuration for the service.
type RPCServiceConfig struct {
	// Host is the address to listen on
	Host string
	// MaxConnections is the maximum amount of connections allowed
	MaxConnections int
	// MaxRPCCallWaitTime is the maximum amount of time to wait
	// for a response from executing a function
	MaxRPCCallWaitTime time.Duration
	// MaxConcurrentCalls is the maximum amount of concurrent calls allowed
	MaxConcurrentCalls int
	// MaxMessageRetries is the maximum amount of retries allowed
	MaxMessageRetries int
	// HeartbeatInterval is the interval to check for heartbeats
	HeartbeatInterval time.Duration
	// UseTLS is a flag to use tls
	UseTLS bool
	// TLSConfig is the tls configuration
	TLSConfig *tls.Config
	// LoggerInterface is the logger interface
	LoggerInterface itf.RPCLogger
}

// RPCServiceInterface is the general interface for the service.
type RPCServiceInterface interface {
	GetConns() []net.Conn
	SetConns([]net.Conn)
	SetNewConn(net.Conn)

	GetConfig() RPCServiceConfig
	SetConfig(RPCServiceConfig)
}

// ConnsWithCancel is a map of connections with cancel functions.
type ConnsWithCancel struct {
	mu              *sync.Mutex
	connsWithCancel map[net.Conn]context.CancelFunc
}

// NewConnsWithCancel creates a new ConnsWithCancel.
func NewConnsWithCancel() ConnsWithCancel {
	return ConnsWithCancel{
		mu:              &sync.Mutex{},
		connsWithCancel: make(map[net.Conn]context.CancelFunc),
	}
}

// RemoveWithRemoteAddr removes a connection with a remote address.
func (c *ConnsWithCancel) RemoveWithRemoteAddr(remoteAddr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for conn, cancel := range c.connsWithCancel {
		if conn.RemoteAddr().String() == remoteAddr {
			cancel()
			delete(c.connsWithCancel, conn)
		}
	}
}

// Remove removes a connection.
func (c *ConnsWithCancel) Remove(conn net.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.connsWithCancel, conn)
}

// Add adds a connection.
func (c *ConnsWithCancel) Add(conn net.Conn, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connsWithCancel[conn] = cancel
}

// Get gets a connection.
func (c *ConnsWithCancel) Get(conn net.Conn) (context.CancelFunc, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cancel, ok := c.connsWithCancel[conn]
	return cancel, ok
}

// validateConfig validates the service configuration.
func validateConfig(serviceConfig *RPCServiceConfig) error {
	if serviceConfig == nil || itf.IsDefaultValue(serviceConfig) {
		return fmt.Errorf("no configuration applied")
	}
	if serviceConfig.MaxConnections < 1 {
		return fmt.Errorf("invalid configuration: must allow at least one connection")
	}
	if serviceConfig.Host == "" {
		return fmt.Errorf("invalid configuration: no host specified")
	}
	if serviceConfig.MaxRPCCallWaitTime < time.Millisecond {
		return fmt.Errorf(
			"invalid configuration: max rpc call wait time must be at least 1 millisecond",
		)
	}
	if serviceConfig.MaxConcurrentCalls < 1 {
		return fmt.Errorf("invalid configuration: max concurrent calls must be at least 1")
	}
	if serviceConfig.MaxMessageRetries < 1 {
		return fmt.Errorf("invalid configuration: max message retries must be at least 1")
	}
	if serviceConfig.HeartbeatInterval < time.Second {
		return fmt.Errorf(
			"invalid configuration: heartbeat interval must be at least 1 second",
		)
	}
	if serviceConfig.UseTLS && serviceConfig.TLSConfig == nil {
		return fmt.Errorf("invalid configuration: no tls config specified")
	}
	if serviceConfig.LoggerInterface == nil {
		serviceConfig.LoggerInterface = itf.NewDefaultLogger()
	}
	return nil
}

// spawnConnectionHandler spawns a connection handler.
func spawnConnectionHandler(
	wg *sync.WaitGroup,
	conn net.Conn,
	serviceConfig RPCServiceConfig,
	serviceInterface RPCServiceInterface,
	connsWithCancel ConnsWithCancel,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
) {
	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Add new connection
	connsWithCancel.Add(conn, cancel)
	serviceInterface.SetNewConn(conn)

	// Spawn connection handler
	wg.Add(1)
	go func(net.Conn) {
		handleConnection(
			ctx,
			serviceInterface,
			serviceConfig,
			conn,
			parseRPCMessage,
			connsWithCancel,
		)
		wg.Done()
	}(conn)
}

// acceptNewConnections accepts new connections.
func acceptNewConnections(
	listener net.Listener,
	serviceInterface RPCServiceInterface,
	serviceConfig RPCServiceConfig,
	connsWithCancel ConnsWithCancel,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
) error {
	wg := sync.WaitGroup{}
	defer wg.Wait()
	wg.Add(1)
	go checkActiveConnections(serviceConfig, serviceInterface, connsWithCancel)
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return fmt.Errorf("error accepting: %w", acceptErr)
		}
		// Check if maximum amount of connections have been reached
		if len(serviceInterface.GetConns()) >= serviceConfig.MaxConnections {
			serviceConfig.LoggerInterface.LogInfo(
				"Maximum connections reached, closing new connection",
			)
			response := itf.RPCMessageRes{
				ID:              "",
				Type:            0,
				TimeStamp:       0,
				ResponseError:   "maximum connections reached",
				ResponseSuccess: nil,
			}
			ms, encodeErr := connhandler.EncodeMessage(response)
			if encodeErr != nil {
				return fmt.Errorf("error encoding: %w", encodeErr)
			}
			if writeErr := connhandler.WriteMessage(conn, ms); writeErr != nil {
				return fmt.Errorf("error writing: %w", writeErr)
			}
			if closeErr := conn.Close(); closeErr != nil {
				return fmt.Errorf("error closing: %w", closeErr)
			}
			continue
		}

		incomingConn := conn.RemoteAddr().String()
		_, alreadyConnected := connsWithCancel.Get(conn)
		if !alreadyConnected {
			serviceConfig.LoggerInterface.LogInfo(
				fmt.Sprintf("adding connection: %s", incomingConn),
			)
			spawnConnectionHandler(
				&wg,
				conn,
				serviceConfig,
				serviceInterface,
				connsWithCancel,
				parseRPCMessage,
			)
		}
	}
}

// StartService starts the service.
func StartService(
	serviceInterface RPCServiceInterface,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
) error {
	connsWithCancel := NewConnsWithCancel()

	// Check if service is already started
	if len(serviceInterface.GetConns()) > 0 {
		return fmt.Errorf("service already started")
	}
	serviceConfig := serviceInterface.GetConfig()
	if err := validateConfig(&serviceConfig); err != nil {
		return err
	}

	serviceConfig.LoggerInterface.LogInfo(fmt.Sprintf("starting service on %s", serviceConfig.Host))
	var listener net.Listener
	if serviceConfig.UseTLS {
		var listenErr error
		listener, listenErr = tls.Listen("tcp", serviceConfig.Host, serviceConfig.TLSConfig)
		if listenErr != nil {
			return fmt.Errorf("error listening: %w", listenErr)
		}
	} else {
		var listenErr error
		listener, listenErr = net.Listen("tcp", serviceConfig.Host)
		if listenErr != nil {
			return fmt.Errorf("error listening: %w", listenErr)
		}
	}
	return acceptNewConnections(
		listener,
		serviceInterface,
		serviceConfig,
		connsWithCancel,
		parseRPCMessage,
	)
}

// StopService stops the service.
func StopService(serviceInterface RPCServiceInterface) error {
	for _, conn := range serviceInterface.GetConns() {
		if err := conn.Close(); err != nil {
			return fmt.Errorf("error closing: %w", err)
		}
	}
	return nil
}

// removeConnection removes a connection.
func removeConnection(
	serviceConfig RPCServiceConfig,
	serviceInterface RPCServiceInterface,
	connsWithCancel ConnsWithCancel,
	conn net.Conn,
) context.CancelFunc {
	connRemoteAddr := conn.RemoteAddr().String()
	allConns := serviceInterface.GetConns()
	serviceConfig.LoggerInterface.LogInfo(
		fmt.Sprintf("removing connection: %s", connRemoteAddr),
	)
	cancel, ok := connsWithCancel.Get(conn)
	for i, c := range allConns {
		if c.RemoteAddr().String() == connRemoteAddr {
			allConns = append(allConns[:i], allConns[i+1:]...)
			serviceInterface.SetConns(allConns)
			connsWithCancel.Remove(conn)
		}
	}
	if !ok {
		serviceConfig.LoggerInterface.LogError(
			fmt.Errorf("error getting cancel handler for: %s", connRemoteAddr),
		)
	}
	return cancel
}

// isNetworkError checks if an error is a network error.
func isNetworkError(err error) bool {
	var opError *net.OpError
	return errors.As(err, &opError)
}

// checkActiveConnections checks which connections are still active.
func checkActiveConnections(
	serviceConfig RPCServiceConfig,
	serviceInterface RPCServiceInterface,
	connsWithCancel ConnsWithCancel,
) {
	for {
		allConns := serviceInterface.GetConns()
		for _, conn := range allConns {
			if err := connhandler.WriteHeartbeatRequest(conn); err != nil {
				switch {
				case isNetworkError(errors.Cause(err)):
					cancel := removeConnection(
						serviceConfig,
						serviceInterface,
						connsWithCancel,
						conn,
					)
					if cancel != nil {
						cancel()
					}
					if closeErr := conn.Close(); closeErr != nil {
						serviceConfig.LoggerInterface.LogError(closeErr)
					}
				default:
					serviceConfig.LoggerInterface.LogError(
						fmt.Errorf("error writing heartbeat: %w", err),
					)
				}
			}
		}
		time.Sleep(serviceConfig.HeartbeatInterval)
	}
}

// handleNewMessage handles a new message.
func handleNewMessage(
	msg []byte,
	conn net.Conn,
	serviceConfig RPCServiceConfig,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
) {
	rpcMsgReq, decodeErr := connhandler.DecodeMessage[itf.RPCMessageReq](msg)
	if decodeErr != nil {
		serviceConfig.LoggerInterface.LogError(fmt.Errorf("error decoding: %w", decodeErr))
	}

	// Check if the message is a heartbeat request
	switch rpcMsgReq.Type {
	case itf.HeartbeatRequest:
		// We've received a heartbeat request, we should respond with a heartbeat response
		heartBeatErr := connhandler.WriteHeartbeatResponse(conn, rpcMsgReq.ID)
		if heartBeatErr != nil {
			serviceConfig.LoggerInterface.LogError(fmt.Errorf("error writing: %w", heartBeatErr))
		}
		return
	case itf.HeartbeatResponse:
		// We've received a heartbeat response, we should do nothing
		return

	case itf.ExecMethodRequest:
		timeStampDateVal := time.Unix(0, rpcMsgReq.TimeStamp)
		requestCallStructure := itf.GetDisplayCallStructureFromReq(rpcMsgReq)
		serviceConfig.LoggerInterface.LogDebug(fmt.Sprintf("received request: [%s] %v -> %v",
			timeStampDateVal.Format(time.RFC3339Nano),
			rpcMsgReq.ID,
			requestCallStructure,
		))

		rpcMsgRes, executeErr := utils.RunWithTimeout(
			func() itf.RPCMessageRes {
				return parseRPCMessage(rpcMsgReq)
			},
			serviceConfig.MaxRPCCallWaitTime,
		)
		if executeErr != nil {
			rpcMsgRes.ID = rpcMsgReq.ID
			serviceConfig.LoggerInterface.LogError(fmt.Errorf("error executing: %w", executeErr))
			rpcMsgRes.ResponseError = fmt.Sprintf("error executing: %s", executeErr)
		}

		rpcMsgRes.TimeStamp = time.Now().UnixNano()
		rpcMsgRes.Type = itf.ExecMethodResponse

		trySendMessage := func() error {
			ms, encodeErr := connhandler.EncodeMessage(rpcMsgRes)
			if encodeErr != nil {
				return fmt.Errorf("error encoding: %w", encodeErr)
			}
			writeErr := connhandler.WriteMessage(conn, ms)
			if writeErr != nil {
				return fmt.Errorf("error writing: %w", writeErr)
			}
			return nil
		}

		if err := utils.RetryOperation(trySendMessage, serviceConfig.MaxMessageRetries, serviceConfig.MaxRPCCallWaitTime, func(err error) {
			serviceConfig.LoggerInterface.LogError(fmt.Errorf("error sending: %w", err))
		}); err != nil {
			serviceConfig.LoggerInterface.LogError(
				fmt.Errorf("max retries reached, error sending: %w", err),
			)
		}

	case itf.ExecMethodResponse:
		serviceConfig.LoggerInterface.LogError(
			fmt.Errorf("cannot handle message type: %v", rpcMsgReq.Type),
		)
	}
}

// handleConnection handles a service connection.
func handleConnection(
	ctx context.Context,
	serviceInterface RPCServiceInterface,
	serviceConfig RPCServiceConfig,
	conn net.Conn,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
	connsWithCancel ConnsWithCancel,
) {
	wg := sync.WaitGroup{}
	mp, _ := ants.NewPoolWithFunc(serviceConfig.MaxConcurrentCalls, func(msg any) {
		wg.Add(1)
		handleNewMessage(msg.([]byte), conn, serviceConfig, parseRPCMessage)
	})
	defer wg.Wait()
	defer func() {
		if releaseErr := mp.ReleaseTimeout(serviceConfig.MaxRPCCallWaitTime); releaseErr != nil {
			serviceConfig.LoggerInterface.LogError(errors.Wrap(releaseErr, "error releasing"))
		}
	}()
	for {
		select {
		// Killed from heartbeat messages failing
		case <-ctx.Done():
			return
		default:
			msg, readErr := connhandler.ReadMessage(conn)
			if readErr != nil {
				removeConnection(serviceConfig, serviceInterface, connsWithCancel, conn)
				if closeErr := conn.Close(); closeErr != nil {
					serviceConfig.LoggerInterface.LogError(closeErr)
				}
				return
			}
			if msg == nil {
				continue
			}
			if invokeErr := mp.Invoke(msg); invokeErr != nil {
				serviceConfig.LoggerInterface.LogError(fmt.Errorf("error invoking: %w", invokeErr))
			}
		}
	}
}
