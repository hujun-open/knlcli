KNLCLIVER ?= $(shell git describe --tags --always --dirty)

.PHONY: build
build: ## Build binary
	go vet;CGO_ENABLED=0 go build -ldflags="-X main.VERSION=$(KNLCLIVER)" -o knlcli-linux-x86-64
