BINARY_NAME ?= ai-aggregator

.PHONY: build run test lint migrate docker-build docker-up docker-down docker-migrate swagger

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

swagger:
	go generate ./cmd/server

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
