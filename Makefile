BINARY  := kad
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/partofaplan/kad/internal/cli.Version=$(VERSION)

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

## build: compile ./bin/kad for this machine
build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/kad

## install: install kad onto your PATH
install:
	go install -ldflags '$(LDFLAGS)' ./cmd/kad

## test: the cluster-free test suite — the Definition of Done
test:
	go test -race -cover ./...
	@echo
	@./hack/next-version_test.sh

## lint: vet, formatting and shellcheck
lint:
	go vet ./...
	@out=$$(gofmt -l . | grep -v '^$$' || true); \
	 if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@# The shell scripts decide what gets tagged and published, so they are
	@# linted too. CI always has shellcheck; locally it is optional.
	@if command -v shellcheck >/dev/null 2>&1; then \
	   shellcheck hack/*.sh scripts/*.sh && echo "shellcheck: clean"; \
	 else \
	   echo "shellcheck: not installed, skipped (CI enforces it)"; \
	 fi

## tidy: prune go.mod
tidy:
	go mod tidy

## catalog-verify: confirm every pinned chart version exists upstream.
## Needs network and helm; NOT part of 'test', which must pass offline.
catalog-verify: build
	@./scripts/catalog-verify.sh

## dist: cross-compile release archives for every supported platform
dist: clean
	@mkdir -p dist
	@set -e; for target in \
	    darwin/amd64 darwin/arm64 \
	    linux/amd64 linux/arm64 \
	    windows/amd64 windows/arm64; do \
	  os=$${target%/*}; arch=$${target#*/}; \
	  ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	  stage="dist/stage/$(BINARY)_$${os}_$${arch}"; \
	  mkdir -p "$$stage"; \
	  echo "  $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	    go build -trimpath -ldflags '$(LDFLAGS)' -o "$$stage/$(BINARY)$$ext" ./cmd/kad; \
	  cp README.md LICENSE "$$stage/"; \
	  if [ "$$os" = "windows" ]; then \
	    (cd dist/stage && zip -qr "../$(BINARY)_$(VERSION)_$${os}_$${arch}.zip" "$(BINARY)_$${os}_$${arch}"); \
	  else \
	    tar -czf "dist/$(BINARY)_$(VERSION)_$${os}_$${arch}.tar.gz" -C dist/stage "$(BINARY)_$${os}_$${arch}"; \
	  fi; \
	done
	@rm -rf dist/stage
	@cd dist && shasum -a 256 * > checksums.txt 2>/dev/null || sha256sum * > checksums.txt
	@echo
	@ls -1 dist/

## clean: remove build output
clean:
	rm -rf bin/ dist/

.PHONY: help build install test lint tidy catalog-verify dist clean
