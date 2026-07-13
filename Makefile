PLUGIN_ID ?= freebuff
GO ?= go
CGO_ENABLED ?= 1
VERSION ?= 0.1.0
VERSION_PKG := github.com/WslzGmzs/freebuff2api/plugin.PluginVer

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
LIB := $(OUT_DIR)/$(PLUGIN_ID).$(EXT)
ARCHIVE := $(PLUGIN_ID)_$(VERSION)_$(GOOS)_$(GOARCH).zip

.PHONY: all build test tidy clean plugin package release-notes

all: test build

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./... -count=1

vet:
	$(GO) vet ./...

build: plugin

plugin:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -buildmode=c-shared \
		-ldflags "-s -w -X $(VERSION_PKG)=$(VERSION)" \
		-o $(LIB) .
	@rm -f $(OUT_DIR)/$(PLUGIN_ID).h $(PLUGIN_ID).h 2>/dev/null || true
	@echo "built $(LIB)"

# Local store-compatible zip + per-file sha256 (does not create checksums.txt)
package: plugin
	$(GO) run ./.github/scripts/package-release.go \
		-library $(LIB) \
		-archive $(ARCHIVE) \
		-checksum $(ARCHIVE).sha256
	@echo "packaged $(ARCHIVE)"

# Cross-compile helpers (native CGO toolchain for target required unless using CI)
plugin-linux-amd64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -buildmode=c-shared \
		-ldflags "-s -w -X $(VERSION_PKG)=$(VERSION)" \
		-o $(OUT_DIR)/$(PLUGIN_ID).so .

plugin-windows-amd64:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -buildmode=c-shared \
		-ldflags "-s -w -X $(VERSION_PKG)=$(VERSION)" \
		-o $(OUT_DIR)/$(PLUGIN_ID).dll .

clean:
	rm -rf $(OUT_DIR) *.h *.zip *.sha256

release-notes:
	@echo "Tag and push to trigger CI release:"
	@echo "  git tag v$(VERSION)"
	@echo "  git push origin v$(VERSION)"
	@echo "Assets (Plugins Store):"
	@echo "  $(PLUGIN_ID)_$(VERSION)_<goos>_<goarch>.zip"
	@echo "  checksums.txt"
