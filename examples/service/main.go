package main

import (
	"time"

	userservice "github.com/antontroskie/radrpc/examples/userservice"
	userservicerad "github.com/antontroskie/radrpc/examples/userservice_rad_gen"
	rds "github.com/antontroskie/radrpc/pkg/rpc/service"
)

func main() {
	// Create a new service configuration
	serviceConfig := rds.RPCServiceConfig{
		Host:               ":8080",
		MaxConnections:     2,
		MaxRPCCallWaitTime: time.Second * 5,
		HeartbeatInterval:  time.Second * 1,
		MaxMessageRetries:  3,
		MaxConcurrentCalls: 5,
		UseTLS:             false,
	}

	// Create new service
	serviceRPC := userservicerad.NewUserServiceRPCRDService()

	// Create handler for the service
	handler := new(userservice.UserService)

	// Start the service with designated target
	if err := serviceRPC.StartService(handler, serviceConfig); err != nil {
		panic(err)
	}
}
