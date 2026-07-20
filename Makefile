.DEFAULT_GOAL := check
.PHONY: check publish-readiness release-readiness release-version-check tidy-check test-surface test-surface-integration test-tus-postgres-integration test-source-url-s3-integration test-portable-s3-media-e2e externalconsumer-local externalconsumer-published import-policy-site tidy generate fmt lint vet test test-race bench-all cover-html
GO_MODULE := $(shell go list -m)
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.cache/*' -not -path './.go-cache/*' -not -path './tmp/*' -not -path './vendor/*')
IMPORT_POLICY_REPO_ROOT ?= ..
IMPORT_POLICY_CONSUMERS ?= site/backend,goadmin,site/fixtures/second-go-host
GOCACHE := $(CURDIR)/.go-cache/gocache
GOMODCACHE := $(CURDIR)/.go-cache/gomodcache
GOPATH := $(CURDIR)/.go-cache/gopath
GOLANGCI_LINT_CACHE := $(CURDIR)/.cache/golangci-lint
export GOCACHE
export GOMODCACHE
export GOPATH
export GOLANGCI_LINT_CACHE

check: tidy generate fmt vet lint test test-race cover-html

release-version-check:
	@printf '%s\n' "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$$' || \
		(echo "VERSION must be an exact semver tag" && exit 2)

publish-readiness: release-version-check check test-surface test-surface-integration test-tus-postgres-integration test-source-url-s3-integration test-portable-s3-media-e2e externalconsumer-local import-policy-site
	@git diff --exit-code

release-readiness: publish-readiness externalconsumer-published

tidy-check:
	go mod tidy -diff

test-surface:
	go test ./host ./hosttest ./reference/externalconsumer ./internal/importpolicy ./cmd/importpolicy ./internal/externalconsumerprobe ./cmd/externalconsumerprobe -count=1

test-surface-integration:
	go test -tags integration ./hosttest -count=1

test-tus-postgres-integration:
	go test -tags integration ./domain/files/service/tusupload -run TestIntegration_PostgresSessionRepository -count=1

test-source-url-s3-integration:
	go test -tags integration ./infrastructure/storage/files/sourceurl -run TestIntegrationPrivateS3SourceDirectPresignedGET -count=1

test-portable-s3-media-e2e:
	go test -tags integration ./integration/portablemedia -run TestIntegrationPortableS3Media -count=1

externalconsumer-local:
	mkdir -p "$(GOMODCACHE)"
	go run ./cmd/externalconsumerprobe --local-path "$(CURDIR)" --go-mod-cache "$(GOMODCACHE)"

externalconsumer-published:
	@test -n "$(VERSION)" || (echo "VERSION is required, for example: make externalconsumer-published VERSION=v0.9.0-alpha.0" && exit 2)
	mkdir -p "$(GOMODCACHE)"
	go run ./cmd/externalconsumerprobe --version "$(VERSION)" --go-mod-cache "$(GOMODCACHE)"

import-policy-site:
	@if [ -d "$(IMPORT_POLICY_REPO_ROOT)" ]; then \
		go run ./cmd/importpolicy --repo-root "$(IMPORT_POLICY_REPO_ROOT)" --consumers "$(IMPORT_POLICY_CONSUMERS)"; \
	else \
		echo "Skipping site import policy: $(IMPORT_POLICY_REPO_ROOT) not found"; \
	fi

tidy:
	go mod tidy

generate:
	go generate ./...

fmt:
	go fmt ./...
	gofumpt -l -w $(GO_FILES)
	gci write -s standard -s default -s "prefix($(GO_MODULE))" $(GO_FILES)

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
	@go test -coverprofile=./coverage.text -covermode=atomic ./...
	@go tool cover -html=./coverage.text -o ./cover.html && rm ./coverage.text
