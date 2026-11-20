GOWORK := off
export GOWORK

NODES ?= 127.0.0.1:28091,127.0.0.1:28092,127.0.0.1:28093
GATEWAY ?= 127.0.0.1:28080
ARGS ?=
STORAGE_DIR ?= ../oracache-storage
READ_MODE ?= local
COMPOSE_PROJECT ?= oracache-load-test
COMPOSE = docker compose -p "$(COMPOSE_PROJECT)" -f "$(STORAGE_DIR)/compose.storage.yaml"
ORACACHE_CP_IMAGE ?= $(COMPOSE_PROJECT)-cp:local
export ORACACHE_CP_IMAGE
CP_COMPOSE = docker compose -p "$(COMPOSE_PROJECT)-cp" -f "$(STORAGE_DIR)/compose.cp.yaml"

.PHONY: build test run run-gateway cluster-up cluster-down gateway-up gateway-down
build:
	go build -o bin/storage-load ./cmd/storage-load

test:
	go test -race ./...

run: build
	./bin/storage-load -nodes "$(NODES)" $(ARGS)

run-gateway: build
	./bin/storage-load -gateway "$(GATEWAY)" $(ARGS)

cluster-up:
	ORACACHE_STORAGE_READ_MODE=$(READ_MODE) $(COMPOSE) up --build -d

cluster-down:
	$(COMPOSE) down

gateway-up:
	$(CP_COMPOSE) build api-gateway
	$(CP_COMPOSE) up --no-build -d

gateway-down:
	$(CP_COMPOSE) down
