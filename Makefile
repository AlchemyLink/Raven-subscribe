BINARY=xray-subscription
BUILD_DIR=./build
VERSION=$(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build install clean deps release \
        build-linux-amd64 build-linux-arm64 build-linux-arm build-darwin-amd64 build-darwin-arm64 build-all \
        test-build test-build-all \
        docker-test-up docker-test-down docker-test-logs docker-test-e2e

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

build-linux-arm64: deps
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 .

build-linux-arm: deps
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm .

build-darwin-amd64: deps
	mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 .

build-darwin-arm64: deps
	mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 .

build-all: build-linux-amd64 build-linux-arm64 build-linux-arm build-darwin-amd64 build-darwin-arm64

# Verify build produces a working binary
test-build: build
	@test -f $(BUILD_DIR)/$(BINARY) || (echo "ERROR: binary not found"; exit 1)
	@test -x $(BUILD_DIR)/$(BINARY) || (echo "ERROR: binary not executable"; exit 1)
	@echo "test-build: OK"

# Verify build-all produces all platform binaries
test-build-all: build-all
	@for f in $(BUILD_DIR)/$(BINARY)-linux-amd64 $(BUILD_DIR)/$(BINARY)-linux-arm64 $(BUILD_DIR)/$(BINARY)-linux-arm $(BUILD_DIR)/$(BINARY)-darwin-amd64 $(BUILD_DIR)/$(BINARY)-darwin-arm64; do \
		test -f "$$f" || (echo "ERROR: missing $$f"; exit 1); \
		test -x "$$f" || (echo "ERROR: not executable $$f"; exit 1); \
	done
	@echo "test-build-all: OK"

# release: tag, build all platforms, push tag → triggers CI release workflow
# Usage: make release VERSION=v0.1.0
release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=v0.1.0"; exit 1; fi
	@echo "Releasing $(VERSION)..."
	go test ./... -race -timeout 2m -count=1
	git tag $(VERSION)
	git push origin $(VERSION)
	@echo "Tag $(VERSION) pushed. GitHub Actions will build and publish the release."

install: build
	install -Dm755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	install -Dm644 config.json.example /etc/xray-subscription/config.json.example
	install -Dm644 xray-subscription.service /etc/systemd/system/xray-subscription.service
	@echo "Edit /etc/xray-subscription/config.json, then:"
	@echo "  systemctl daemon-reload"
	@echo "  systemctl enable --now xray-subscription"

clean:
	rm -rf $(BUILD_DIR)

docker-test-up: build
	docker compose -f docker-compose.test.yml up -d

docker-test-down:
	docker compose -f docker-compose.test.yml down -v

docker-test-logs:
	docker compose -f docker-compose.test.yml logs -f --tail=200

docker-test-e2e:
	E2E_DOCKER=1 go test ./integration -v
