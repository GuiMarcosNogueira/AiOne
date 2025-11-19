BINARY_NAME ?= ai-aggregator

.PHONY: build run test lint

build:
	go build -o bin/$(BINARY_NAME) ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./...

lint:
	go vet ./...
