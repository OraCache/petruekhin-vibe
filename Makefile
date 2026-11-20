GOWORK := off
export GOWORK

NODES ?= 127.0.0.1:28091,127.0.0.1:28092,127.0.0.1:28093
ARGS ?=
STORAGE_DIR ?= ../oracache-storage
READ_MODE ?= local
COMPOSE_PROJECT ?= oracache-load-test
COMPOSE = docker compose -p "$(COMPOSE_PROJECT)" -f "$(STORAGE_DIR)/compose.storage.yaml"

.PHONY: build test run cluster-up cluster-down
build:
	go build -o bin/storage-load ./cmd/storage-load

test:
	go test -race ./...

run: build
	./bin/storage-load -nodes "$(NODES)" $(ARGS)

cluster-up:
	ORACACHE_STORAGE_READ_MODE=$(READ_MODE) $(COMPOSE) up --build -d

cluster-down:
	$(COMPOSE) down
