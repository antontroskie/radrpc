// Code Generated by generator, DO NOT EDIT.
package userservicerad

import (
	"fmt"
	"net"
	"sync"
)

import (
	"encoding/gob"

	userservice "github.com/antontroskie/radrpc/examples/userservice"
	rdc "github.com/antontroskie/radrpc/pkg/rpc/client"
	itf "github.com/antontroskie/radrpc/pkg/rpc/itf"
	rds "github.com/antontroskie/radrpc/pkg/rpc/service"
)

type userServiceRPCRDS struct {
	userServiceRPCRDSConns  []net.Conn
	userServiceRPCRDSConfig rds.RPCServiceConfig
	userServiceRPCRDSMutex  *sync.RWMutex
	userServiceRPCRDSInterf userservice.UserServiceRPC
}
type userServiceRPCRDC struct {
	userServiceRPCRDCConn        net.Conn
	userServiceRPCRDCMutex       *sync.RWMutex
	userServiceRPCRDCConfig      rdc.RPCClientConfig
	userServiceRPCRDCMessagePool rdc.RPCClientMessagePool
}
type UserServiceRPCRDS struct {
	service *userServiceRPCRDS
}
type UserServiceRPCRDC struct {
	client *userServiceRPCRDC
}

func NewUserServiceRPCRDService() *userServiceRPCRDS {
	return &userServiceRPCRDS{
		userServiceRPCRDSMutex: new(sync.RWMutex),
	}
}

func NewUserServiceRPCRDClient() *userServiceRPCRDC {
	return &userServiceRPCRDC{
		userServiceRPCRDCMutex: new(sync.RWMutex),
	}
}

func (s *userServiceRPCRDS) parseRPCMessage(msg itf.RPCMessageReq) itf.RPCMessageRes {
	id := msg.ID
	switch msg.Method {
	case "CreateNewUser":
		argCount := 2
		if len(msg.Args) != argCount {
			return itf.RPCMessageRes{
				ID:            id,
				ResponseError: fmt.Sprintf("CreateNewUser: expected %d arguments, got %d", argCount, len(msg.Args)),
			}
		}
		args := msg.Args
		arg0, ok := args[0].(string)
		if !ok {
			return itf.RPCMessageRes{
				ID:            id,
				ResponseError: fmt.Sprintf("CreateNewUser: expected argument 0 to be of type %T, got %T", " string ", args[0]),
			}
		}
		arg1, ok := args[1].(int)
		if !ok {
			return itf.RPCMessageRes{
				ID:            id,
				ResponseError: fmt.Sprintf("CreateNewUser: expected argument 1 to be of type %T, got %T", " int ", args[1]),
			}
		}
		s.userServiceRPCRDSMutex.Lock()
		defer s.userServiceRPCRDSMutex.Unlock()
		s.userServiceRPCRDSInterf.CreateNewUser(arg0, arg1)
		return itf.RPCMessageRes{
			ID: id,
		}
	case "GetUsers":
		argCount := 0
		if len(msg.Args) != argCount {
			return itf.RPCMessageRes{
				ID:            id,
				ResponseError: fmt.Sprintf("GetUsers: expected %d arguments, got %d", argCount, len(msg.Args)),
			}
		}
		s.userServiceRPCRDSMutex.Lock()
		defer s.userServiceRPCRDSMutex.Unlock()
		returnVal := s.userServiceRPCRDSInterf.GetUsers()
		return itf.RPCMessageRes{
			ID:              id,
			ResponseSuccess: returnVal,
		}
	default:
		return itf.RPCMessageRes{
			ID:            id,
			ResponseError: fmt.Sprintf("unknown method: %s", msg.Method),
		}
	}
}

func (s *userServiceRPCRDS) GetConns() []net.Conn {
	s.userServiceRPCRDSMutex.RLock()
	defer s.userServiceRPCRDSMutex.RUnlock()
	return s.userServiceRPCRDSConns
}

func (s *userServiceRPCRDS) SetConns(conns []net.Conn) {
	s.userServiceRPCRDSMutex.Lock()
	defer s.userServiceRPCRDSMutex.Unlock()
	s.userServiceRPCRDSConns = conns
}

func (s *userServiceRPCRDS) SetNewConn(conn net.Conn) {
	s.userServiceRPCRDSMutex.Lock()
	defer s.userServiceRPCRDSMutex.Unlock()
	s.userServiceRPCRDSConns = append(s.userServiceRPCRDSConns, conn)
}

func (s *userServiceRPCRDS) GetConfig() rds.RPCServiceConfig {
	s.userServiceRPCRDSMutex.RLock()
	defer s.userServiceRPCRDSMutex.RUnlock()
	return s.userServiceRPCRDSConfig
}

func (s *userServiceRPCRDS) SetConfig(config rds.RPCServiceConfig) {
	s.userServiceRPCRDSMutex.Lock()
	defer s.userServiceRPCRDSMutex.Unlock()
	s.userServiceRPCRDSConfig = config
}

func (s *userServiceRPCRDS) StartService(m userservice.UserServiceRPC, config rds.RPCServiceConfig) error {
	gob.Register([]userservice.User{})
	s.SetConfig(config)
	s.userServiceRPCRDSInterf = m
	return rds.StartService(s, s.parseRPCMessage)
}

func (s *userServiceRPCRDS) StopService() error {
	return rds.StopService(s)
}

func (s *UserServiceRPCRDC) CreateNewUser(name string, age int) {
	config := s.client.userServiceRPCRDCConfig
	logger := config.LoggerInterface
	msg := itf.RPCMessageReq{
		Method: "CreateNewUser",
		Args: []any{
			name, age,
		},
		Type: itf.ExecMethodRequest,
	}
	res := rdc.SendAndReceiveMessage(s.client, msg)
	if res.ResponseError != "" {
		logger.LogError(fmt.Errorf("error executing method: %v", res.ResponseError))
	}
}

func (s *UserServiceRPCRDC) GetUsers() []userservice.User {
	config := s.client.userServiceRPCRDCConfig
	logger := config.LoggerInterface
	msg := itf.RPCMessageReq{
		Method: "GetUsers",
		Args:   []any{},
		Type:   itf.ExecMethodRequest,
	}
	res := rdc.SendAndReceiveMessage(s.client, msg)
	if res.ResponseError != "" {
		logger.LogError(fmt.Errorf("error executing method: %v", res.ResponseError))
	}
	if res.ResponseSuccess == nil {
		logger.LogError(fmt.Errorf("received %v response from request with ID: %v", res.ResponseSuccess, res.ID))
		return nil
	}
	response, ok := res.ResponseSuccess.([]userservice.User)
	if !ok {
		panic(fmt.Sprintf("error type asserting response: %v", res.ResponseSuccess))
	}
	return response
}

func (s *userServiceRPCRDC) GetConn() net.Conn {
	s.userServiceRPCRDCMutex.RLock()
	defer s.userServiceRPCRDCMutex.RUnlock()
	return s.userServiceRPCRDCConn
}

func (s *userServiceRPCRDC) SetConn(conn net.Conn) {
	s.userServiceRPCRDCMutex.Lock()
	defer s.userServiceRPCRDCMutex.Unlock()
	s.userServiceRPCRDCConn = conn
}

func (s *userServiceRPCRDC) GetConfig() rdc.RPCClientConfig {
	s.userServiceRPCRDCMutex.RLock()
	defer s.userServiceRPCRDCMutex.RUnlock()
	return s.userServiceRPCRDCConfig
}

func (s *userServiceRPCRDC) SetConfig(config rdc.RPCClientConfig) {
	s.userServiceRPCRDCMutex.Lock()
	defer s.userServiceRPCRDCMutex.Unlock()
	s.userServiceRPCRDCConfig = config
}

func (s *userServiceRPCRDC) GetMessagePool() rdc.RPCClientMessagePool {
	s.userServiceRPCRDCMutex.RLock()
	defer s.userServiceRPCRDCMutex.RUnlock()
	return s.userServiceRPCRDCMessagePool
}

func (s *userServiceRPCRDC) SetMessagePool(config rdc.RPCClientMessagePool) {
	s.userServiceRPCRDCMutex.Lock()
	defer s.userServiceRPCRDCMutex.Unlock()
	s.userServiceRPCRDCMessagePool = config
}

func (s *userServiceRPCRDC) Connect(config rdc.RPCClientConfig) (*UserServiceRPCRDC, error) {
	s.SetConfig(config)
	gob.Register([]userservice.User{})
	interf := &UserServiceRPCRDC{
		client: s,
	}
	return interf, rdc.Connect(s)
}

func (s *userServiceRPCRDC) Disconnect() error {
	return rdc.Disconnect(s)
}
