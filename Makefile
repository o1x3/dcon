BINARY := dcon
PREFIX ?= /usr/local
BINDIR := $(PREFIX)/bin

.PHONY: all build install uninstall clean test vet fmt link-docker unlink-docker

all: build

build:
	go build -o $(BINARY) .

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

vet:
	go vet ./...

fmt:
	gofmt -w .
