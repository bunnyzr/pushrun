VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test lint run-dev web-install web-build web-test web-lint

build:
	mkdir -p dist
	go build -ldflags "$(LDFLAGS)" -o dist/pushrun ./cmd/pushrun

test:
	go test ./...

lint:
	go vet ./...
	golangci-lint run --max-same-issues 0 --max-issues-per-linter 0 ./...

run-dev:
	PUSHRUN_ROOT=./local/data go run -ldflags "$(LDFLAGS)" ./cmd/pushrun serve

web-install:
	npm ci --prefix web

web-build:
	npm run build --prefix web

web-test:
	npm run test --prefix web

web-lint:
	npm run lint --prefix web
