BINARY := dcon
PREFIX ?= /usr/local
BINDIR := $(PREFIX)/bin

# Version metadata injected at build time.
VERSION ?= $(shell git describe --tags --match 'v*' --always --dirty 2>/dev/null || echo 1.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X dcon/cmd.Version=$(VERSION) \
	-X dcon/cmd.Commit=$(COMMIT) \
	-X dcon/cmd.Date=$(DATE)

.PHONY: all build install uninstall clean test test-race cover vet fmt lint link-docker unlink-docker bench \
	app-build app-test app-bundle app-dmg app-run

all: build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

install: build
	install -d $(BINDIR)
	install -m 0755 $(BINARY) $(BINDIR)/$(BINARY)
	@echo "Installed $(BINDIR)/$(BINARY)"
	@echo "For a full drop-in replacement, run:  make link-docker"

# Symlink `docker` -> `dcon` so existing scripts/tools just work.
link-docker: install
	ln -sf $(BINDIR)/$(BINARY) $(BINDIR)/docker
	@echo "Linked $(BINDIR)/docker -> $(BINARY)"

unlink-docker:
	@if [ -L "$(BINDIR)/docker" ]; then rm -f "$(BINDIR)/docker" && echo "Removed docker symlink"; else echo "$(BINDIR)/docker is not a dcon symlink; left untouched"; fi

uninstall:
	rm -f $(BINDIR)/$(BINARY)

clean:
	rm -f $(BINARY)

test:
	go test ./...

test-race:
	go test -race ./...

cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

fmt:
	gofmt -w .

bench:
	./scripts/bench.sh

# --- Desktop app (app/) ------------------------------------------------------

app-build:
	swift build --package-path app

app-test:
	swift test --package-path app

app-bundle:
	./app/scripts/package-app.sh

app-dmg: app-bundle
	./app/scripts/make-dmg.sh

app-run: app-bundle
	open app/dist/Dcon.app
