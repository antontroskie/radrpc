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
func validateConfig(config *RPCServiceConfig) error {
	if itf.IsDefaultValue(config) {
		return fmt.Errorf("no configuration applied")
	}
	if config.MaxConnections < 1 {
		return fmt.Errorf("invalid configuration: must allow at least one connection")
	}
	if config.Host == "" {
		return fmt.Errorf("invalid configuration: no host specified")
	}
	if config.MaxRPCCallWaitTime < time.Millisecond {
		return fmt.Errorf(
			"invalid configuration: max rpc call wait time must be at least 1 millisecond",
		)
	}
	if config.MaxConcurrentCalls < 1 {
		return fmt.Errorf("invalid configuration: max concurrent calls must be at least 1")
	}
	if config.MaxMessageRetries < 1 {
		return fmt.Errorf("invalid configuration: max message retries must be at least 1")
	}
	if config.HeartbeatInterval < time.Second {
		return fmt.Errorf(
			"invalid configuration: heartbeat interval must be at least 1 second",
		)
	}
	if config.UseTLS && config.TLSConfig == nil {
		return fmt.Errorf("invalid configuration: no tls config specified")
	}
	if config.LoggerInterface == nil {
		config.LoggerInterface = itf.NewDefaultLogger()
	}
	return nil
}

// StartService starts the service.
func StartService(
	m RPCServiceInterface,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
) error {
	connsWithCancel := NewConnsWithCancel()

	// Check if service is already started
	if len(m.GetConns()) > 0 {
		return fmt.Errorf("service already started")
	}
	serviceConfig := m.GetConfig()
	if err := validateConfig(&serviceConfig); err != nil {
		return err
	}

	wg := sync.WaitGroup{}
	defer wg.Wait()
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
	wg.Add(1)
	go checkActiveConnections(serviceConfig, m, connsWithCancel)
	for {
		var acceptErr error
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return fmt.Errorf("error accepting: %w", acceptErr)
		}
		// Check if maximum amount of connections have been reached
		if len(m.GetConns()) >= serviceConfig.MaxConnections {
			serviceConfig.LoggerInterface.LogInfo(
				"Maximum connections reached, closing new connection",
			)
			response := itf.RPCMessageRes{ResponseError: "maximum connections reached"}
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
		allConns := m.GetConns()
		alreadyConnected := false
		serviceConfig.LoggerInterface.LogInfo(
			fmt.Sprintf("new connection request from: %s", incomingConn),
		)
		for _, c := range allConns {
			if c.RemoteAddr().String() == incomingConn {
				alreadyConnected = true
				break
			}
		}
		if !alreadyConnected {
			serviceConfig.LoggerInterface.LogInfo(
				fmt.Sprintf("adding connection: %s", incomingConn),
			)

			// Add new connection
			m.SetNewConn(conn)
			wg.Add(1)
			ctx, cancel := context.WithCancel(context.Background())
			connsWithCancel.Add(conn, cancel)
			go func(net.Conn) {
				handleServiceConnection(
					m,
					serviceConfig,
					conn,
					parseRPCMessage,
					connsWithCancel,
					ctx,
				)
				wg.Done()
			}(conn)
		}
	}
}

func StopService(m RPCServiceInterface) error {
	for _, conn := range m.GetConns() {
		if err := conn.Close(); err != nil {
			return fmt.Errorf("error closing: %w", err)
		}
	}
	return nil
}

func removeConnection(
	config RPCServiceConfig,
	m RPCServiceInterface,
	connsWithCancel ConnsWithCancel,
	conn net.Conn,
) context.CancelFunc {
	connRemoteAddr := conn.RemoteAddr().String()
	allConns := m.GetConns()
	config.LoggerInterface.LogInfo(
		fmt.Sprintf("removing connection: %s", connRemoteAddr),
	)
	cancel, ok := connsWithCancel.Get(conn)
	for i, c := range allConns {
		if c.RemoteAddr().String() == connRemoteAddr {
			allConns = append(allConns[:i], allConns[i+1:]...)
			m.SetConns(allConns)
			connsWithCancel.Remove(conn)
		}
	}
	if !ok {
		config.LoggerInterface.LogError(
			fmt.Errorf("error getting cancel handler for: %s", connRemoteAddr),
		)
	}
	return cancel
}

func isNetworkError(err error) bool {
	var opError *net.OpError
	return errors.As(err, &opError)
}

// checkActiveConnections checks which connections are still active.
func checkActiveConnections(
	config RPCServiceConfig,
	m RPCServiceInterface,
	connsWithCancel ConnsWithCancel,
) {
	for {
		allConns := m.GetConns()
		for _, conn := range allConns {
			if err := connhandler.WriteHeartbeatRequest(conn); err != nil {
				switch {
				case isNetworkError(errors.Cause(err)):
					cancel := removeConnection(config, m, connsWithCancel, conn)
					if cancel != nil {
						cancel()
					}
					if closeErr := conn.Close(); closeErr != nil {
						config.LoggerInterface.LogError(closeErr)
					}
				default:
					config.LoggerInterface.LogError(fmt.Errorf("error writing heartbeat: %w", err))
				}
			}
		}
		time.Sleep(config.HeartbeatInterval)
	}
}

// handleNewMessage handles a new message.
func handleNewMessage(
	msg []byte,
	conn net.Conn,
	config RPCServiceConfig,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
) {
	rpcMsgReq, decodeErr := connhandler.DecodeMessage[itf.RPCMessageReq](msg)
	if decodeErr != nil {
		config.LoggerInterface.LogError(fmt.Errorf("error decoding: %w", decodeErr))
	}

	// Check if the message is a heartbeat request
	switch rpcMsgReq.Type {
	case itf.HeartbeatRequest:
		// We've received a heartbeat request, we should respond with a heartbeat response
		heartBeatErr := connhandler.WriteHeartbeatResponse(conn, rpcMsgReq.ID)
		if heartBeatErr != nil {
			config.LoggerInterface.LogError(fmt.Errorf("error writing: %w", heartBeatErr))
		}
		return
	case itf.HeartbeatResponse:
		// We've received a heartbeat response, we should do nothing
		return

	case itf.MethodRequest:
		timeStampDateVal := time.Unix(0, rpcMsgReq.TimeStamp)
		requestCallStructure := itf.GetDisplayCallStructureFromReq(rpcMsgReq)
		config.LoggerInterface.LogDebug(fmt.Sprintf("received request: [%s] %v -> %v",
			timeStampDateVal.Format(time.RFC3339Nano),
			rpcMsgReq.ID,
			requestCallStructure,
		))

		rpcMsgRes, executeErr := utils.RunWithTimeout(
			func() itf.RPCMessageRes {
				return parseRPCMessage(rpcMsgReq)
			},
			config.MaxRPCCallWaitTime,
		)
		if executeErr != nil {
			rpcMsgRes.ID = rpcMsgReq.ID
			config.LoggerInterface.LogError(fmt.Errorf("error executing: %w", executeErr))
			rpcMsgRes.ResponseError = fmt.Sprintf("error executing: %s", executeErr)
		}

		rpcMsgRes.TimeStamp = time.Now().UnixNano()
		rpcMsgRes.Type = itf.MethodResponse

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

		if err := utils.RetryOperation(trySendMessage, config.MaxMessageRetries, config.MaxRPCCallWaitTime, func(err error) {
			config.LoggerInterface.LogError(fmt.Errorf("error sending: %w", err))
		}); err != nil {
			config.LoggerInterface.LogError(
				fmt.Errorf("max retries reached, error sending: %w", err),
			)
		}

	case itf.MethodResponse:
		config.LoggerInterface.LogError(
			fmt.Errorf("cannot handle message type: %v", rpcMsgReq.Type),
		)
	}
}

// handleServiceConnection handles a service connection.
func handleServiceConnection(
	m RPCServiceInterface,
	config RPCServiceConfig,
	conn net.Conn,
	parseRPCMessage func(msg itf.RPCMessageReq) itf.RPCMessageRes,
	connsWithCancel ConnsWithCancel,
	ctx context.Context,
) {
	wg := sync.WaitGroup{}
	mp, _ := ants.NewPoolWithFunc(config.MaxConcurrentCalls, func(msg any) {
		wg.Add(1)
		handleNewMessage(msg.([]byte), conn, config, parseRPCMessage)
	})
	defer wg.Wait()
	defer func() {
		if releaseErr := mp.ReleaseTimeout(config.MaxRPCCallWaitTime); releaseErr != nil {
			config.LoggerInterface.LogError(errors.Wrap(releaseErr, "error releasing"))
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
				removeConnection(config, m, connsWithCancel, conn)
				if closeErr := conn.Close(); closeErr != nil {
					config.LoggerInterface.LogError(closeErr)
				}
				return
			}
			if msg == nil {
				continue
			}
			if invokeErr := mp.Invoke(msg); invokeErr != nil {
				config.LoggerInterface.LogError(fmt.Errorf("error invoking: %w", invokeErr))
			}
		}
	}
}
