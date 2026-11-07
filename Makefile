# SPDX-License-Identifier: Apache-2.0
# Copyright The Jaga Authors

GO      ?= go
PKG     := github.com/ryandinulfatah12/jaga
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)

# Tool versions, run via `go run` so they never land in go.mod.
GO_LICENSES := github.com/google/go-licenses/v2@v2.0.1
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

# Keep in sync with docs/09-legal.md.
ALLOWED_LICENSES := MIT,BSD-2-Clause,BSD-3-Clause,ISC,Apache-2.0,MPL-2.0,0BSD,Zlib

.PHONY: all build test vet lint fmt spdx licenses vulncheck check clean

all: check build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/jaga ./cmd/jaga

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

spdx:
	scripts/check-spdx.sh

licenses:
	$(GO) run $(GO_LICENSES) check ./... --allowed_licenses=$(ALLOWED_LICENSES)

vulncheck:
	$(GO) run $(GOVULNCHECK) ./...

check: vet test spdx

clean:
	rm -rf bin dist
