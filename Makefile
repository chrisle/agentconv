.PHONY: build test release

VERSION ?= dev
LDFLAGS = -s -w -X main.version=$(VERSION)

build:
	go build -ldflags="$(LDFLAGS)" -o bin/agentconv ./cmd/agentconv

test:
	go test ./...

release:
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/agentconv-darwin-arm64 ./cmd/agentconv
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/agentconv-darwin-amd64 ./cmd/agentconv
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/agentconv-linux-amd64 ./cmd/agentconv
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/agentconv-linux-arm64 ./cmd/agentconv
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/agentconv-windows-amd64.exe ./cmd/agentconv
