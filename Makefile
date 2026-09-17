VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build build-linux build-windows check generate test test-integration

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/stu-trade ./cmd/stu-trade

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/stu-trade-linux-amd64 ./cmd/stu-trade

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/stu-trade-windows-amd64.exe ./cmd/stu-trade

generate:
	sqlc generate

test:
	go test ./...

test-integration:
	go test -tags=integration ./migrations

check: generate
	git diff --exit-code -- internal/db
	test -z "$$(gofmt -l $$(find . -path './engine' -prune -o -path './web' -prune -o -name '*.go' -print))"
	go vet ./...
	go test -race ./...
