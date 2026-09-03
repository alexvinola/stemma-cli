# Stemma development commands.
#
# Every target here is also documented in README.md and AGENTS.md.

GO ?= go
BINARY ?= stemma
PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
MODULE := github.com/alexvinola/stemma-cli

# VERSION overrides what `stemma version` reports, via -ldflags -X. Leave it
# empty for an ordinary local build, which keeps the default in
# internal/version.Version. CI sets it for the binaries it publishes, e.g.:
#   make cross VERSION=0.1.0-dev+a1b2c3d
VERSION ?=
LDFLAGS := $(if $(VERSION),-X '$(MODULE)/internal/version.Version=$(VERSION)',)

.PHONY: all
all: fmt vet test build

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: fmt-check
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

.PHONY: build
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/stemma

# Regenerating golden fixtures is an explicit action, never a side effect of
# running the tests. Review the diff before committing.
.PHONY: golden
golden:
	$(GO) test ./internal/compiler -run TestGolden -update-golden

# Short fuzzing pass over the parsers and path handling.
.PHONY: fuzz
fuzz:
	$(GO) test -run xxx -fuzz FuzzParse -fuzztime 30s ./internal/parser
	$(GO) test -run xxx -fuzz FuzzFrontMatter -fuzztime 30s ./internal/parser
	$(GO) test -run xxx -fuzz FuzzMatch -fuzztime 30s ./internal/globs
	$(GO) test -run xxx -fuzz FuzzNormalizeRel -fuzztime 30s ./internal/workspace
	$(GO) test -run xxx -fuzz FuzzUnmarshalProject -fuzztime 30s ./internal/canonical
	$(GO) test -run xxx -fuzz FuzzClassify -fuzztime 30s ./internal/discovery

.PHONY: cross
cross:
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" \
			-o dist/$(BINARY)-$$os-$$arch$$ext ./cmd/stemma; \
	done

.PHONY: verify
verify: fmt-check vet test test-race cross

.PHONY: clean
clean:
	rm -rf dist $(BINARY)
