GO_SHARED_CACHE_ROOT ?= $(HOME)/dev/projects/.cache/go
GOCACHE ?= $(GO_SHARED_CACHE_ROOT)/build
GOMODCACHE ?= $(GO_SHARED_CACHE_ROOT)/mod
GOLANGCI_LINT_CACHE ?= $(GO_SHARED_CACHE_ROOT)/lint
GOPATH ?= $(shell GOTOOLCHAIN=local go env GOPATH)
export GOCACHE GOMODCACHE GOLANGCI_LINT_CACHE

.DEFAULT_GOAL := check
.PHONY: source-readiness test-originals-integration anonymous-source anonymous-published full prepare check publish-readiness release-readiness release-version-check tidy-check test-surface test-surface-integration test-tus-postgres-integration test-source-url-s3-integration test-portable-media-e2e test-portable-s3-media-e2e test-portable-local-media-e2e externalconsumer-local externalconsumer-published import-policy-site tidy generate fmt fmt-check lint lint-fix vet test test-full test-race bench-all cover-html
GO_MODULE := $(shell go list -m)
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.cache/*' -not -path './.go-cache/*' -not -path './tmp/*' -not -path './vendor/*')
IMPORT_POLICY_REPO_ROOT ?= ..
IMPORT_POLICY_CONSUMERS ?= site/backend,goadmin,site/fixtures/second-go-host
export GOCACHE
export GOMODCACHE
export GOPATH
export GOLANGCI_LINT_CACHE

full: prepare check

prepare: tidy generate fmt lint-fix

check: tidy-check fmt-check vet lint test-full

release-version-check:
	@printf '%s\n' "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$$' || \
		(echo "VERSION must be an exact semver tag" && exit 2)

source-readiness: check test-surface test-surface-integration test-tus-postgres-integration test-media-continuation-postgres-integration test-source-url-s3-integration test-portable-media-e2e test-originals-integration externalconsumer-local anonymous-source import-policy-site

publish-readiness: release-version-check prepare source-readiness
	@git diff --exit-code

release-readiness: publish-readiness externalconsumer-published anonymous-published

tidy-check:
	go mod tidy -diff

test-surface:
	go test ./host ./hosttest ./reference/externalconsumer ./internal/importpolicy ./cmd/importpolicy ./internal/externalconsumerprobe ./cmd/externalconsumerprobe -count=1

test-surface-integration:
	go test -tags integration ./hosttest -count=1

test-tus-postgres-integration:
	sh ./scripts/with-integration-postgres.sh go test -tags integration ./domain/files/service/tusupload -run TestIntegration_PostgresSessionRepository -count=1

.PHONY: test-media-continuation-postgres-integration
test-media-continuation-postgres-integration:
	sh ./scripts/with-integration-postgres.sh go test -race -tags integration ./integration/portablemedia -run '^TestIntegrationAdmissionContinuationRestart$$' -count=1

test-source-url-s3-integration:
	go test -tags integration ./infrastructure/storage/files/sourceurl -run TestIntegrationPrivateS3SourceDirectPresignedGET -count=1

test-portable-media-e2e: test-portable-s3-media-e2e test-portable-local-media-e2e

test-portable-s3-media-e2e:
	sh ./scripts/with-integration-postgres.sh go test -tags integration ./integration/portablemedia -run TestIntegrationPortableS3Media -count=1

test-portable-local-media-e2e:
	sh ./scripts/with-integration-postgres.sh go test -tags integration ./integration/portablemedia -run TestIntegrationLocalMedia -count=1

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

fmt-check:
	@unformatted="$$(gofumpt -l $(GO_FILES))"; \
		test -z "$$unformatted" || { printf 'gofumpt changes are required:\n%s\nRun: make prepare\n' "$$unformatted" >&2; exit 1; }
	@import_diff="$$(gci diff -s standard -s default -s "prefix($(GO_MODULE))" $(GO_FILES))"; \
		test -z "$$import_diff" || { printf 'gci changes are required:\n%s\nRun: make prepare\n' "$$import_diff" >&2; exit 1; }

lint:
	golangci-lint run -v --timeout=5m ./...

lint-fix:
	golangci-lint run -v --fix --timeout=5m ./...

vet:
	go vet ./...

test:
	go test ./...

test-full:
	go test -race -cover -covermode=atomic -count=1 ./...

test-race:
	go test -race -count=5 ./...

bench-all:
	go test -bench=. -benchmem ./...

cover-html:
	@go test -coverprofile=./coverage.text -covermode=atomic ./...
	@go tool cover -html=./coverage.text -o ./cover.html && rm ./coverage.text

test-originals-integration:
	@test -n "$(TEST_S3_ENDPOINT)" || (echo "TEST_S3_ENDPOINT is required for local + S3 acceptance" && exit 2)
	sh ./scripts/with-integration-postgres.sh go test -race -tags integration ./integration/originals -count=1

anonymous-source:
	sh ./scripts/anonymous-consumer.sh source "$(CURDIR)"

anonymous-published: release-version-check
	sh ./scripts/anonymous-consumer.sh published "$(VERSION)"
