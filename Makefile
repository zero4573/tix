-include config.mk

PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DATADIR ?= $(PREFIX)/share
GO ?= go

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X github.com/zero4573/tix/internal/version.Version=$(VERSION)
PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

ZSHDIR = $(DATADIR)/zsh/site-functions
BASHDIR = $(DATADIR)/bash-completion/completions
FISHDIR = $(DATADIR)/fish/vendor_completions.d

.PHONY: all build test bench check eval install uninstall cross clean

all: build

build: tix

tix: go.mod go.sum $(shell find cmd internal assets -type f)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o tix ./cmd/tix

test:
	$(GO) test ./...

bench:
	TIX_BENCH=1 $(GO) test ./internal/vbench -run TestReport -v -timeout 30m

TESTFLAGS ?= -p 4

TESTLOG ?=

check:
	$(GO) vet ./...
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
ifeq ($(TESTLOG),)
	$(GO) test $(TESTFLAGS) ./...
else
	@command -v jq >/dev/null 2>&1 || { echo "make check TESTLOG needs jq on PATH; run make check without TESTLOG" >&2; exit 1; }
	@rc=$$(mktemp); { $(GO) test $(TESTFLAGS) -json ./... ; echo $$? > $$rc; } | tee '$(TESTLOG)' | jq -j 'select(.Action=="output" or .Action=="build-output").Output // empty'; \
	  r=$$(cat $$rc); rm -f $$rc; exit $$r
endif

eval:
	./evals/run.sh $(EVAL_ARGS)

install: tix
	install -d '$(DESTDIR)$(BINDIR)' '$(DESTDIR)$(ZSHDIR)' '$(DESTDIR)$(BASHDIR)' '$(DESTDIR)$(FISHDIR)'
	install -m 0755 tix '$(DESTDIR)$(BINDIR)/tix'
	./tix completion zsh > '$(DESTDIR)$(ZSHDIR)/_tix'
	./tix completion bash > '$(DESTDIR)$(BASHDIR)/tix'
	./tix completion fish > '$(DESTDIR)$(FISHDIR)/tix.fish'
	@echo "installed tix $(VERSION) into $(DESTDIR)$(BINDIR); next: tix vault init <name>"

uninstall:
	rm -f '$(DESTDIR)$(BINDIR)/tix' '$(DESTDIR)$(ZSHDIR)/_tix' '$(DESTDIR)$(BASHDIR)/tix' '$(DESTDIR)$(FISHDIR)/tix.fish'

cross:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  echo "dist/tix-$$os-$$arch$$ext"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/tix-$$os-$$arch$$ext ./cmd/tix || exit 1; \
	done

clean:
	rm -rf tix dist config.mk
