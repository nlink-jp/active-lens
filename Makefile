BINARY   := active-lens
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION)"
DIST_DIR := dist

# active-lens uses cgo (CoreGraphics) and is darwin/arm64 only. SQLite is
# pure-Go (modernc.org/sqlite), so cgo is confined to the signal bridge.
export CGO_ENABLED := 1

# macOS Developer ID signing / notarization (see nlink-jp/.github
# CONVENTIONS.md §Code Signing). Builds without a cert fall back to ad-hoc with
# a one-line warning — see scripts/codesign-darwin.sh.
CODESIGN_IDENTITY ?= Developer ID Application
NOTARY_PROFILE    ?= nlink-jp-notary

.PHONY: build build-all package test vet clean

## build: compile the binary into dist/ (never use `go build` directly)
build:
	@mkdir -p $(DIST_DIR)
	go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY) .
	@scripts/codesign-darwin.sh $(DIST_DIR)/$(BINARY) "$(CODESIGN_IDENTITY)"

## build-all: build the release binary (darwin/arm64; cgo requires the native
## toolchain, and the tool is Apple-Silicon only).
build-all:
	@mkdir -p $(DIST_DIR)
	GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-arm64 .
	@scripts/codesign-darwin.sh $(DIST_DIR)/$(BINARY)-darwin-arm64 "$(CODESIGN_IDENTITY)"

## package: build, zip (with README.md and the canonical binary name inside),
## and notarize. Matches the release asset naming:
## active-lens-vX.Y.Z-darwin-arm64.zip
package: build-all
	@cd $(DIST_DIR) && for f in $(BINARY)-darwin-*; do \
		case "$$f" in *.zip) continue ;; esac; \
		suffix=$${f#$(BINARY)-}; \
		cp ../README.md .; \
		stage="_pkg"; rm -rf "$$stage"; mkdir -p "$$stage"; \
		cp "$$f" "$$stage/$(BINARY)"; \
		zip -j "$(BINARY)-$(VERSION)-$${suffix}.zip" "$$stage/$(BINARY)" README.md; \
		rm -rf "$$stage"; \
		rm -f README.md; \
	done
	@scripts/notarize-darwin.sh $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip "$(NOTARY_PROFILE)"

## test: run all tests
test:
	go test ./...

## vet: static checks. The darwin pass covers the cgo signal bridge; the linux
## pass (cgo off) checks the non-darwin stubs still compile.
vet:
	go vet ./...
	CGO_ENABLED=0 GOOS=linux go vet ./...

## clean: remove build artifacts
clean:
	rm -rf $(DIST_DIR)
