BINARY=xray-subscription
BUILD_DIR=./build
VERSION=$(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build install clean deps

all: build

deps:
	go mod tidy
	go mod download

build: deps
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) .

build-linux-amd64: deps
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .

install: build
	install -Dm755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	install -Dm644 config.json.example /etc/xray-subscription/config.json.example
	install -Dm644 xray-subscription.service /etc/systemd/system/xray-subscription.service
	@echo "Edit /etc/xray-subscription/config.json, then:"
	@echo "  systemctl daemon-reload"
	@echo "  systemctl enable --now xray-subscription"

clean:
	rm -rf $(BUILD_DIR)
