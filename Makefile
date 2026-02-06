.PHONY: build test test-race test-coverage clean run lint fmt help

BINARY_NAME=mindcli
BUILD_DIR=bin
COVERAGE_FILE=coverage.out

# Version info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Build flags
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

## build: Build the binary
build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/mindcli

## run: Run the application
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

## test: Run tests
test:
	go test -v ./...

## test-race: Run tests with race detector
test-race:
	go test -race -v ./...

## test-coverage: Run tests with coverage report
test-coverage:
	go test -coverprofile=$(COVERAGE_FILE) ./...
	go tool cover -html=$(COVERAGE_FILE) -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: Run linter
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run

## fmt: Format code
fmt:
	go fmt ./...
	@which goimports > /dev/null || go install golang.org/x/tools/cmd/goimports@latest
	goimports -w .

## clean: Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -f $(COVERAGE_FILE) coverage.html

## help: Show this help
help:
	@echo "MindCLI - Personal Knowledge Search"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
