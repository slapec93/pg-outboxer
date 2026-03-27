.PHONY: build run test clean install help

# Build variables
BINARY_NAME=pg-outboxer
VERSION?=dev
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)"

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/pg-outboxer

run: ## Run from source
	go run ./cmd/pg-outboxer run --config=config.example.yaml

test: ## Run tests
	go test -v -race -coverprofile=coverage.out ./...

test-coverage: test ## Run tests with coverage report
	go tool cover -html=coverage.out

clean: ## Clean build artifacts
	rm -f $(BINARY_NAME)
	rm -f coverage.out

install: ## Install the binary to $GOPATH/bin
	go install $(LDFLAGS) ./cmd/pg-outboxer

fmt: ## Format code
	go fmt ./...

lint: ## Run linter (requires golangci-lint)
	golangci-lint run

tidy: ## Tidy go modules
	go mod tidy

validate: build ## Validate example config
	./$(BINARY_NAME) validate-config --config=config.example.yaml

version: build ## Show version
	./$(BINARY_NAME) version
