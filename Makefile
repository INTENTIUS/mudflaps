# Makefile mirrors the justfile. `just` is the primary task runner; this exists
# for contributors who prefer make. Keep the two in sync.

BINARY   := mudflaps
PKG      := ./...
IMAGE    := ghcr.io/intentius/mudflaps
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: build test race lint cover docker run tidy fmt docs-build docs-serve

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/mudflaps

test:
	go test $(PKG)

race:
	go test -race $(PKG)

lint:
	golangci-lint run $(PKG)

cover:
	go test -race -coverprofile=coverage.out $(PKG)
	go tool cover -func=coverage.out | tail -1

docker:
	docker build -t $(IMAGE):$(VERSION) .

run: build
	./$(BINARY)

tidy:
	go mod tidy

fmt:
	gofmt -w .

docs-build:
	mkdocs build --strict

docs-serve:
	mkdocs serve
