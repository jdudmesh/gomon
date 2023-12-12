# Simple Makefile for a Go project

# Build the application
all: build

bindir:
	@mkdir -p bin

build: bindir client
	@echo "Building..."
	@go build --race -o gomon main.go

install: client
	@echo "Installing..."
	@go install github.com/jdudmesh/gomon

# Run the application
run:
	@go run main.go

# Create DB container
docker-run:
	@if docker compose up 2>/dev/null; then \
		: ; \
	else \
		echo "Falling back to Docker Compose V1"; \
		docker-compose up; \
	fi

# Shutdown DB container
docker-down:
	@if docker compose down 2>/dev/null; then \
		: ; \
	else \
		echo "Falling back to Docker Compose V1"; \
		docker-compose down; \
	fi

# Test the application
test:
	@echo "Testing..."
	@go test ./... -v

# Clean the binary
clean:
	@echo "Cleaning..."
	@rm -f main

client:
	@echo "Building client..."
	@cd client-bundle && pnpm i && pnpm run build

.PHONY: all build run test clean