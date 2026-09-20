PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install uninstall test clean

build:
	go build -ldflags "$(LDFLAGS)" -o tori ./cmd/tori

install: build
	install -Dm755 tori "$(BINDIR)/tori"
	@echo "installed $(BINDIR)/tori ($(VERSION))"
	@command -v aria2c >/dev/null || echo "note: aria2c not found; install aria2 for downloads"

uninstall:
	rm -f "$(BINDIR)/tori"
	@echo "removed $(BINDIR)/tori (config and keyring entry kept; run 'tori forget-key' first to drop the key)"

test:
	go vet ./...
	go test ./...

clean:
	rm -rf tori dist
