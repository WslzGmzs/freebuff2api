# Dual CPA plugins from one tree:
#   freebuff.*  — models + executor + Freebuff OAuth (auth.identifier=freebuff)
#   codebuff.*  — Codebuff OAuth only (auth.identifier=codebuff)
#
# CPA lists one OAuth entry per auth.identifier; install both libraries for both
# Freebuff OAuth and Codebuff OAuth on /oauth.

GO ?= go
CGO_ENABLED ?= 1
VERSION ?= 0.1.0
VERSION_PKG := github.com/WslzGmzs/freebuff2api/plugin.PluginVer
IDENTITY_PKG := github.com/WslzGmzs/freebuff2api/plugin.Identity

GOOS ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)

ifeq ($(GOOS),windows)
  EXT := dll
else ifeq ($(GOOS),darwin)
  EXT := dylib
else
  EXT := so
endif

OUT_DIR := dist
FREEBUFF_LIB := $(OUT_DIR)/freebuff.$(EXT)
CODEBUFF_LIB := $(OUT_DIR)/codebuff.$(EXT)

LDFLAGS_FREEBUFF := -s -w -X $(VERSION_PKG)=$(VERSION) -X $(IDENTITY_PKG)=freebuff
LDFLAGS_CODEBUFF := -s -w -X $(VERSION_PKG)=$(VERSION) -X $(IDENTITY_PKG)=codebuff

.PHONY: all build test tidy clean plugin plugins package package-all

all: test plugins

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./... -count=1

vet:
	$(GO) vet ./...

build: plugins

plugin: freebuff-plugin

plugins: freebuff-plugin codebuff-plugin

freebuff-plugin:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -buildmode=c-shared \
		-ldflags "$(LDFLAGS_FREEBUFF)" \
		-o $(FREEBUFF_LIB) .
	@rm -f $(OUT_DIR)/freebuff.h freebuff.h 2>/dev/null || true
	@echo "built $(FREEBUFF_LIB) (Freebuff OAuth + executor)"

codebuff-plugin:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -buildmode=c-shared \
		-ldflags "$(LDFLAGS_CODEBUFF)" \
		-o $(CODEBUFF_LIB) .
	@rm -f $(OUT_DIR)/codebuff.h codebuff.h 2>/dev/null || true
	@echo "built $(CODEBUFF_LIB) (Codebuff OAuth only)"

package: freebuff-plugin
	$(GO) run ./.github/scripts/package-release.go \
		-library $(FREEBUFF_LIB) \
		-archive freebuff_$(VERSION)_$(GOOS)_$(GOARCH).zip \
		-checksum freebuff_$(VERSION)_$(GOOS)_$(GOARCH).zip.sha256
	@echo "packaged freebuff_$(VERSION)_$(GOOS)_$(GOARCH).zip"

package-all: plugins
	$(GO) run ./.github/scripts/package-release.go \
		-library $(FREEBUFF_LIB) \
		-archive freebuff_$(VERSION)_$(GOOS)_$(GOARCH).zip \
		-checksum freebuff_$(VERSION)_$(GOOS)_$(GOARCH).zip.sha256
	$(GO) run ./.github/scripts/package-release.go \
		-library $(CODEBUFF_LIB) \
		-archive codebuff_$(VERSION)_$(GOOS)_$(GOARCH).zip \
		-checksum codebuff_$(VERSION)_$(GOOS)_$(GOARCH).zip.sha256
	@echo "packaged freebuff + codebuff zips for $(GOOS)/$(GOARCH)"

clean:
	rm -rf $(OUT_DIR) *.h *.zip *.sha256

release-notes:
	@echo "Install both libraries into CPA plugins/:"
	@echo "  freebuff.$(EXT)  → Freebuff OAuth + models + chat"
	@echo "  codebuff.$(EXT)  → Codebuff OAuth (credentials execute via freebuff)"
	@echo "Tag: git tag v$(VERSION) && git push origin v$(VERSION)"
