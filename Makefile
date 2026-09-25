BINARY      := acestep
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_DIR   := bin
LDFLAGS     := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)
GO          ?= go
GOOS_LIST   := linux darwin windows
GOARCH_LIST := amd64 arm64
PLATFORMS    := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.DEFAULT_GOAL := help
.PHONY: help build install test race cover lint fmt vet tidy check clean \
        cross run doctor setup-smoke release-dry

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the acestep binary into bin/
	@mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/acestep
	@echo "built $(BUILD_DIR)/$(BINARY) ($(VERSION))"

install: build ## Install acestep into GOBIN
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/acestep

test: ## Run the unit tests
	$(GO) test ./...

race: ## Run the unit tests with the race detector
	$(GO) test -race -count=1 ./...

cover: ## Run tests and report coverage
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

fmt: ## Format the Go sources
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

check: fmt vet test ## Format, vet and test

cross: ## Cross-compile for every release platform (CGO disabled)
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		goos=$${p%/*}; goarch=$${p#*/}; \
		out=$(BUILD_DIR)/$(BINARY)_$${goos}_$${goarch}; \
		if [ "$$goos" = "windows" ]; then out=$$out.exe; fi; \
		echo "  $$goos/$$goarch"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd || exit 1; \
	done

run: build ## Build then run, e.g. make run ARGS="generate -p 'lofi beats'"
	@./$(BUILD_DIR)/$(BINARY) $(ARGS)

doctor: build ## Check the local installation
	@./$(BUILD_DIR)/$(BINARY) doctor

clean: ## Remove build artefacts
	rm -rf $(BUILD_DIR) dist coverage.out

release-dry: ## Check the GoReleaser configuration without publishing
	@goreleaser check || echo "goreleaser not installed; skipping"
