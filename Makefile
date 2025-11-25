ROOT_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
BINARY_NAME ?= ai-aggregator
PROTOC_BIN ?= $(ROOT_DIR).tools/protoc/bin/protoc
PROTO_SRC_DIR := proto
PROTO_DEST_DIR := api/grpc
PROTO_FILES := $(shell find $(PROTO_SRC_DIR) -name '*.proto')

GO_BIN_DIR := $(shell go env GOBIN)
ifeq ($(GO_BIN_DIR),)
GO_BIN_DIR := $(shell go env GOPATH | cut -d: -f1)/bin
endif

SWAG_VERSION ?= v1.16.3
PROTOC_GEN_GO_VERSION ?= v1.34.2
PROTOC_GEN_GO_GRPC_VERSION ?= v1.5.1

SWAG_BIN := $(GO_BIN_DIR)/swag
PROTOC_GEN_GO_BIN := $(GO_BIN_DIR)/protoc-gen-go
PROTOC_GEN_GO_GRPC_BIN := $(GO_BIN_DIR)/protoc-gen-go-grpc

.PHONY: build run test lint migrate docker-build docker-up docker-down docker-migrate swagger proto tools

MIGRATIONS := $(sort $(wildcard migrations/*.sql))
COMPOSE ?= docker-compose

build:
	go build -o bin/$(BINARY_NAME) ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./...

lint:
	go vet ./...

swagger: $(SWAG_BIN)
	go generate ./cmd/server

proto: $(PROTOC_GEN_GO_BIN) $(PROTOC_GEN_GO_GRPC_BIN)
	@if [ ! -x "$(PROTOC_BIN)" ]; then \
		echo "protoc binary not found at $(PROTOC_BIN). Override PROTOC_BIN or download protoc." >&2; \
		exit 1; \
	fi
	@PATH="$(GO_BIN_DIR):$$PATH" "$(PROTOC_BIN)" -I "$(PROTO_SRC_DIR)" $(PROTO_FILES) \
		--go_out="$(PROTO_DEST_DIR)" --go_opt=paths=source_relative \
		--go-grpc_out="$(PROTO_DEST_DIR)" --go-grpc_opt=paths=source_relative

tools: $(SWAG_BIN) $(PROTOC_GEN_GO_BIN) $(PROTOC_GEN_GO_GRPC_BIN)
	@echo "Tooling ready under $(GO_BIN_DIR)"

$(SWAG_BIN):
	@echo "Installing swag CLI ($(SWAG_VERSION))"
	GO111MODULE=on go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)

$(PROTOC_GEN_GO_BIN):
	@echo "Installing protoc-gen-go ($(PROTOC_GEN_GO_VERSION))"
	GO111MODULE=on go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)

$(PROTOC_GEN_GO_GRPC_BIN):
	@echo "Installing protoc-gen-go-grpc ($(PROTOC_GEN_GO_GRPC_VERSION))"
	GO111MODULE=on go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

migrate:
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "DATABASE_URL is required" >&2; \
		exit 1; \
	fi
	@for file in $(MIGRATIONS); do \
		echo "Applying $$file"; \
		psql "$$DATABASE_URL" -f "$$file" || exit 1; \
	done

docker-build:
	$(COMPOSE) build

docker-up:
	$(COMPOSE) up

docker-down:
	$(COMPOSE) down

docker-migrate:
	$(COMPOSE) run --rm migrate
