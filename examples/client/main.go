package main

import (
	"fmt"
	"time"

	userservicerad "github.com/antontroskie/radrpc/examples/userservice_rad_gen"
	rdc "github.com/antontroskie/radrpc/pkg/rpc/client"
)

func main() {
	// Create a new client configuration
	clientConfig := rdc.RPCClientConfig{
		Host:                    ":8080",
		MaxConnectionRetries:    0, // Retry indefinitely
		ConnectionRetryInterval: time.Second * 1,
		MaxRPCCallWaitTime:      time.Second * 5,
		UseTLS:                  false,
	}

	// Create new client
	clientRPC := userservicerad.NewUserServiceRPCRDClient()

	// Attempt to connect to the service
	rpcInterface, err := clientRPC.Connect(clientConfig)
	if err != nil {
		panic(err)
	}

	// Create a new user from entered flags
	rpcInterface.CreateNewUser("Anton Troskie", 28)

	// Get all users
	users := rpcInterface.GetUsers()

	// Print users
	for _, user := range users {
		fmt.Printf("User: %v\n", user)
	}
}
