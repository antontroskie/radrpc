//nolint:forbidigo // Needs default logger implementation that prints to stdout
package itf

import (
	"fmt"
	"reflect"
)

// IsDefaultValue checks if a value is equal to its default value.
func IsDefaultValue(value any) bool {
	// Get the reflect.Value of the input value
	v := reflect.ValueOf(value)

	// Check if the value is zero or nil
	return v.IsZero()
}

// GetDisplayCallStructureFromReq gets the display call structure from a request.
func GetDisplayCallStructureFromReq(
	rpcMsgReq RPCMessageReq,
) string {
	if len(rpcMsgReq.Args) > 0 {
		args := ""
		for i, arg := range rpcMsgReq.Args {
			if i == len(rpcMsgReq.Args)-1 {
				args += fmt.Sprintf("%v", arg)
				break
			}
			args += fmt.Sprintf("%v, ", arg)
		}
		return fmt.Sprintf("%v(%v)", rpcMsgReq.Method, args)
	}
	return fmt.Sprintf("%v()", rpcMsgReq.Method)
}

// RPCMessageRequestType is the type of an RPC message request.
type RPCMessageRequestType int

const (
	// HeartbeatRequest is a heartbeat request.
	HeartbeatRequest RPCMessageRequestType = iota
	// HeartbeatResponse is a heartbeat response.
	HeartbeatResponse
	// ExecMethodRequest is an RPC call from the client to execute a remote function.
	ExecMethodRequest
	// ExecMethodResponse is an RPC response from the server after executing a remote function.
	ExecMethodResponse
)

// RPCMessageReq is a request for an RPC message.
type RPCMessageReq struct {
	// ID is the ID of the request
	ID string
	// Type is the type of the request
	Type RPCMessageRequestType
	// TimeStamp is the time the request was sent
	TimeStamp int64
	// Method is the method to call
	Method string
	//
	Args []any
}

// RPCMessageRes is a response to an RPC message.
type RPCMessageRes struct {
	// ID is the ID of the request
	ID string
	// Type is the type of the response
	Type RPCMessageRequestType
	// TimeStamp is the time the response was sent
	TimeStamp int64
	// ResponseError is the error message if the response is an error
	ResponseError string
	// ResponseSuccess is the response if the response is a success
	ResponseSuccess any
}

// RPCLogger is an interface for logging RPC messages.
type RPCLogger interface {
	LogInfo(info string)
	LogDebug(debug string)
	LogWarn(warn string)
	LogError(err error)
}

// defaultLogger is the default logger for RPC messages.
type defaultLogger struct{}

// LogInfo logs an info message.
func (l *defaultLogger) LogInfo(info string) {
	println(info)
}

// LogDebug logs a debug message.
func (l *defaultLogger) LogDebug(debug string) {
	println(debug)
}

// LogWarn logs a warning message.
func (l *defaultLogger) LogWarn(warn string) {
	println(warn)
}

// LogError logs an error message.
func (l *defaultLogger) LogError(err error) {
	println(err.Error())
}

// NewDefaultLogger creates a new default logger.
func NewDefaultLogger() RPCLogger {
	return &defaultLogger{}
}
