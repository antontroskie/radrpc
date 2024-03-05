
.PHONY: run-service run-client gen-radrpc get-deps format lint help test

default: clean

help:
	@echo 'Management commands for radrpc:'
	@echo
	@echo 'Usage:'
	@echo '    make run-service     Run the service.'
	@echo '    make run-client      Run the client.'
	@echo '    make gen-radrpc     Generate the rad rpc interface.'
	@echo '    make get-deps        Get required dependencies.'
	@echo '    make format          Run format on the project.'
	@echo '    make test            Run all tests.'
	@echo '    make lint            Run lint the project.'

run-service:
	@echo "running service"
	go run ./examples/service/main.go

run-client:
	@echo "running client"
	go run ./examples/client/main.go

gen-radrpc:
	@echo "generating rad rpc"
	go run ./cmd/generator/main.go ./examples/userservice.UserServiceRPC

get-deps:
	@echo "getting dependencies"
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.54.0
	go install github.com/segmentio/golines@latest
	go install mvdan.cc/gofumpt@latest

format:
	@echo formatting
	golines -w .
	gofumpt -l -w .

lint:
	@echo "linting"
	golangci-lint run --fix

test:
	go test ./...
