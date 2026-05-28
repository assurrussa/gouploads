.DEFAULT_GOAL := check
.PHONY: check release-readiness tidy-check test-surface test-surface-integration externalconsumer-local externalconsumer-published import-policy-site tidy generate fmt lint vet test test-race bench-all cover-html
GO_MODULE := $(shell go list -m)
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.cache/*' -not -path './.go-cache/*' -not -path './tmp/*' -not -path './vendor/*')
SITE_REPO ?= ../site
GOCACHE ?= $(CURDIR)/.go-cache/gocache
GOMODCACHE ?= $(CURDIR)/.go-cache/gomodcache
GOPATH ?= $(CURDIR)/.go-cache/gopath
export GOCACHE
export GOPATH

check: tidy generate fmt vet lint test test-race cover-html

release-readiness: tidy-check vet test-surface test-surface-integration externalconsumer-local test import-policy-site

tidy-check:
	go mod tidy -diff

test-surface:
	go test ./host ./hosttest ./reference/externalconsumer ./internal/importpolicy ./cmd/importpolicy ./internal/externalconsumerprobe ./cmd/externalconsumerprobe -count=1

test-surface-integration:
	go test -tags integration ./hosttest -count=1

externalconsumer-local:
	mkdir -p "$(GOMODCACHE)"
	go run ./cmd/externalconsumerprobe --local-path "$(CURDIR)" --go-mod-cache "$(GOMODCACHE)"

externalconsumer-published:
	@test -n "$(VERSION)" || (echo "VERSION is required, for example: make externalconsumer-published VERSION=v0.8.0" && exit 2)
	mkdir -p "$(GOMODCACHE)"
	go run ./cmd/externalconsumerprobe --version "$(VERSION)" --go-mod-cache "$(GOMODCACHE)"

import-policy-site:
	@if [ -d "$(SITE_REPO)" ]; then \
		go run ./cmd/importpolicy --repo-root "$(SITE_REPO)" --consumers backend,goadmin,fixtures/second-go-host; \
	else \
		echo "Skipping site import policy: $(SITE_REPO) not found"; \
	fi

tidy:
	go mod tidy

generate:
	go generate ./...

fmt:
	go fmt ./...
	gofumpt -l -w $(GO_FILES)
	gci write -s standard -s default -s "prefix($(GO_MODULE))" .

lint:
	golangci-lint run -v --fix --timeout=5m ./...

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race -count=5 ./...

bench-all:
	go test -bench=. -benchmem ./...

cover-html:
	@go test -coverprofile=./coverage.text -covermode=atomic $(shell go list ./...)
	@go tool cover -html=./coverage.text -o ./cover.html && rm ./coverage.text
