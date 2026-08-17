.PHONY: build test install clean

BINARY := herd
VERSION ?= 0.1.0
LDFLAGS := -X github.com/timjonez/herd-orchestrator-cli/internal/cli.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/herd

test:
	go test ./...

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/herd

clean:
	rm -rf bin
