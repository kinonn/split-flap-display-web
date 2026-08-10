# Makefile for the split-flap-display-web Go backend.
# Run from inside the backend-go/ directory: `make build`, `make test`, etc.

BINARY  ?= bin/server
GOFLAGS ?= -trimpath -ldflags="-s -w"
GO      ?= go

.PHONY: all build run test vet fmt tidy clean docker help

all: build

build: ## Build the native server binary into ./bin
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/server

build-linux: ## Cross-compile a static Linux amd64 binary
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/server

build-arm64: ## Cross-compile a static Linux arm64 binary (e.g., Raspberry Pi)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/server

run: ## Run the server in development
	$(GO) run ./cmd/server

test: ## Run unit tests
	$(GO) test ./... -timeout 30s

race: ## Run unit tests with the race detector
	$(GO) test -race ./... -timeout 60s

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format the source
	$(GO) fmt ./...

tidy: ## Tidy module dependencies
	$(GO) mod tidy

clean: ## Remove built artefacts
	rm -rf bin

docker: ## Build the production Docker image (from repo root)
	cd .. && docker build -f backend-go/Dockerfile -t split-flap-web:latest .

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN { FS = ":.*?## " }; { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }'