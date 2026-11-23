GO ?= $(shell which go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
LDFLAGS := -s -w

.PHONY: all xo1 native test clean

all: xo1

## Build a static, zero-dependency 32-bit binary for the OLPC XO-1 (AMD Geode LX, no SSE2)
xo1:
	mkdir -p build
	CGO_ENABLED=0 GOOS=linux GOARCH=386 GO386=softfloat $(GO) build -ldflags="$(LDFLAGS)" -o build/xo1-reader ./cmd/xo1-reader
	@ls -lh build/xo1-reader

## Build for the current host machine (for local testing)
native:
	mkdir -p build
	CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o build/xo1-reader-native ./cmd/xo1-reader
	@ls -lh build/xo1-reader-native

## Run all unit tests
test:
	$(GO) test -v ./...

clean:
	rm -rf build
