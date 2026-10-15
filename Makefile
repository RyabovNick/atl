BIN ?= $(HOME)/.local/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint install uninstall dist

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o atl .

test:
	go vet ./...
	go test ./...

lint:
	golangci-lint run ./...

install: test build
	./atl install --bin-dir '$(BIN)'

uninstall: build
	./atl uninstall --bin-dir '$(BIN)'

dist:
	rm -rf dist
	mkdir -p dist
	for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/atl-$$os-$$arch$$ext . || exit 1; \
	done
	cd dist && if command -v sha256sum >/dev/null; then sha256sum atl-*; else shasum -a 256 atl-*; fi > SHA256SUMS
