VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
ACTIONLINT_VERSION := v1.7.7
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

BIN_DIR := bin
DIST_DIR := dist
BACKEND := $(BIN_DIR)/stu-trade
LINUX_BACKEND := $(BIN_DIR)/stu-trade-linux-amd64
WINDOWS_BACKEND := $(BIN_DIR)/stu-trade-windows-amd64.exe

.PHONY: build build-image build-linux build-windows check deploy-production \
	develop engine-check format-check generate generate-check migrate-up native \
	pr-check production release-smoke run submodules submodules-check test \
	test-integration vet web-build web-check workflow-check

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BACKEND) ./cmd/stu-trade

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(LINUX_BACKEND) ./cmd/stu-trade

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(WINDOWS_BACKEND) ./cmd/stu-trade

build-image:
	docker build --build-arg VERSION="$(VERSION)" --build-arg COMMIT="$(COMMIT)" --target production --tag stu-trade:$(COMMIT) .

submodules:
	git submodule update --init --recursive

submodules-check: submodules
	@test -z "$$(git submodule status --recursive | sed -n '/^[-+U]/p')" || \
		(echo "submodules are not at the pinned commits"; git submodule status --recursive; exit 1)

generate: submodules
	sqlc generate
	go generate ./internal/httpapi

generate-check: generate
	git diff --exit-code -- internal/db internal/httpapi/contract.gen.go

format-check:
	@test -z "$$(gofmt -l $$(find cmd internal migrations -name '*.go' -print))" || \
		(echo "gofmt: files need formatting"; gofmt -l $$(find cmd internal migrations -name '*.go' -print); exit 1)

vet:
	GOFLAGS=-mod=readonly go vet ./...

test:
	GOFLAGS=-mod=readonly go test -race -timeout 10m ./...

test-integration:
	GOFLAGS=-mod=readonly go test -count=1 -race -timeout 10m -tags=integration ./internal/auth ./internal/listing ./migrations

engine-check: submodules-check
	@test -z "$$(cd engine && gofmt -l .)" || (echo "engine: files need formatting"; cd engine && gofmt -l .; exit 1)
	cd engine && GOFLAGS=-mod=readonly go vet ./...
	cd engine && GOFLAGS=-mod=readonly go test -race -timeout 10m ./...

web-check: submodules-check
	cd web && bun install --frozen-lockfile
	cd web && bun run check

web-build: submodules-check
	cd web && bun install --frozen-lockfile
	cd web && bun run build

check: generate-check format-check vet test engine-check web-check

workflow-check:
	GOFLAGS=-mod=readonly go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

pr-check: check test-integration workflow-check

release-smoke:
	@test -n "$(BACKEND_IMAGE)" || (echo "BACKEND_IMAGE is required"; exit 2)
	@test -n "$(WEB_IMAGE)" || (echo "WEB_IMAGE is required"; exit 2)
	BACKEND_IMAGE="$(BACKEND_IMAGE)" WEB_IMAGE="$(WEB_IMAGE)" ./deploy/smoke.sh

deploy-production:
	@test -n "$(BACKEND_IMAGE)" || (echo "BACKEND_IMAGE is required"; exit 2)
	@test -n "$(WEB_IMAGE)" || (echo "WEB_IMAGE is required"; exit 2)
	STUTRADE_DEPLOY_DIR="$(or $(STUTRADE_DEPLOY_DIR),/opt/stu-trade)" \
		./deploy/deploy.sh "$(BACKEND_IMAGE)" "$(WEB_IMAGE)"

run:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
		go run -ldflags "$(LDFLAGS)" ./cmd/stu-trade serve

migrate-up:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
		go run -ldflags "$(LDFLAGS)" ./cmd/stu-trade migrate up

develop: submodules
	docker compose up --build --remove-orphans

native: build-windows web-build
	rm -rf $(DIST_DIR)/native
	install -d $(DIST_DIR)/native/backend $(DIST_DIR)/native/web
	install -m 0755 $(WINDOWS_BACKEND) $(DIST_DIR)/native/backend/stu-trade.exe
	cp -R web/dist/. $(DIST_DIR)/native/web/

production: build-linux web-build build-image
	rm -rf $(DIST_DIR)/production
	install -d $(DIST_DIR)/production/backend $(DIST_DIR)/production/web
	install -m 0755 $(LINUX_BACKEND) $(DIST_DIR)/production/backend/stu-trade
	cp -R web/dist/. $(DIST_DIR)/production/web/
