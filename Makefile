.PHONY: help up test lint build format
help:
	@echo "up test lint build format"
up:
	go run ./cmd/tui
test:
	go test ./...
lint:
	go vet ./...
build:
	go build ./...
format:
	gofmt -w .
