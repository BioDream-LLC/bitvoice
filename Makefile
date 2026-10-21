GO      ?= go
BIN     := bin/bitvoice
PKG     := ./cmd/bitvoice
# The released version, written here so a build from a source archive reports the same version as a checkout.
VERSION ?= v0.0.0
# RELEASE_KEY is BioDream's release public key (bvsign -new prints it); a build with it installs only
# upgrades signed with the matching private key.
RELEASE_KEY ?=
# BioDream's release public key: release builds carry it, and then install only upgrades signed with it.
BIODREAM_KEY := M6UaGaLM1T0SekufRzAlUSeSTjfxpVL18YHE5eKkKxY=
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.releaseKey=$(RELEASE_KEY)

.PHONY: all build test vet fmt check clean web e2e manual release site

all: check build

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l -w .

# check fails on unformatted code rather than rewriting it.
check: vet test
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

clean:
	rm -rf bin

# web rebuilds the interface into internal/web/dist, which is committed so `go build` needs no Node.
web:
	cd web && pnpm install --frozen-lockfile && pnpm build

# e2e runs the Playwright sweep of every screen against a fresh server.
e2e: build
	cd web && pnpm exec playwright test

# manual writes the manual (docs/manual) from the Help articles in web/src/manual.ts, as HTML and PDF.
manual:
	cd web && pnpm exec tsc src/manual.ts src/figures.ts --outDir /tmp/bitvoice-manual --module es2022 --target es2022 --moduleResolution bundler --skipLibCheck && \
		cp /tmp/bitvoice-manual/manual.js /tmp/bitvoice-manual/manual.mjs && cp /tmp/bitvoice-manual/figures.js /tmp/bitvoice-manual/figures.mjs && \
		node scripts/manual.mjs /tmp/bitvoice-manual/manual.mjs $(CURDIR)/docs/manual/index.html $(CURDIR)/docs/manual/bitvoice-manual.pdf

# release builds the six downloads into dist/: each program carries BioDream's release key and is signed
# (bitvoice.sig or bitvoice.exe.sig beside it, for the Updates page), packed with LICENSE, NOTICE and the manual (Linux
# archives also carry bitvoice-dahdi, for telephony cards), with
# SHA256SUMS. Archives name no person: files are owned by root, and zips keep no owner at all.
#   make release VERSION=v0.1.3 SIGNING_KEY=path/to/release.key
# DIST is where they go (dist by default); PLATFORMS may be narrowed, as the installer tests do.
# WHISPER is a directory of whisper-server builds, WHISPER/<os>-<arch>/ as the whisper-server workflow
# makes them (speech to text on this server); each goes into its download beside bitvoice, signed.
DIST ?= dist
# Archive entries owned by root, in GNU tar's words or BSD tar's (macOS).
TAR_ROOT := $(shell tar --version 2>/dev/null | grep -q GNU && echo '--owner=root:0 --group=root:0' || echo '--uid 0 --gid 0 --uname root --gname root')
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
release:
	@case "$(VERSION)" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "set VERSION, like VERSION=v0.1.3"; exit 1;; esac
	@test -n "$(SIGNING_KEY)" || { echo "set SIGNING_KEY to the release private key"; exit 1; }
	rm -rf $(DIST) && mkdir -p $(DIST)
	$(GO) build -o $(DIST)/.bvsign ./cmd/bvsign
	@set -e; for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; name=bitvoice-$(VERSION)-$$os-$$arch; \
		exe=bitvoice; [ $$os = windows ] && exe=bitvoice.exe; mkdir -p $(DIST)/$$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.releaseKey=$(BIODREAM_KEY)" -o $(DIST)/$$name/$$exe $(PKG); \
		$(DIST)/.bvsign -key "$(SIGNING_KEY)" $(DIST)/$$name/$$exe > /dev/null; \
		if [ $$os = linux ]; then CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST)/$$name/bitvoice-dahdi ./cmd/bitvoice-dahdi; \
			$(DIST)/.bvsign -key "$(SIGNING_KEY)" $(DIST)/$$name/bitvoice-dahdi > /dev/null; fi; \
		if [ -n "$(WHISPER)" ]; then w=$(WHISPER)/$$os-$$arch; wexe=whisper-server; [ $$os = windows ] && wexe=whisper-server.exe; \
			test -f $$w/$$wexe || { echo "$$w/$$wexe is missing (see .github/workflows/whisper.yml)"; exit 1; }; \
			cp $$w/$$wexe $(DIST)/$$name/ && chmod 0755 $(DIST)/$$name/$$wexe && cp $$w/whisper.cpp-LICENSE $(DIST)/$$name/; \
			$(DIST)/.bvsign -key "$(SIGNING_KEY)" $(DIST)/$$name/$$wexe > /dev/null; fi; \
		cp LICENSE NOTICE docs/manual/bitvoice-manual.pdf $(DIST)/$$name/; \
		if [ $$os = windows ]; then (cd $(DIST) && TZ=UTC zip -qrX $$name.zip $$name); \
		else COPYFILE_DISABLE=1 tar -C $(DIST) $(TAR_ROOT) -czf $(DIST)/$$name.tar.gz $$name; fi; \
		echo "built $$name"; done
	rm $(DIST)/.bvsign
	cd $(DIST) && shasum -a 256 $$(ls *.tar.gz *.zip 2>/dev/null) > SHA256SUMS

# The bitvoice.org website, built from README.md, docs/manual and docs/assets (site/ holds its pages).
# Needs cwebp for the screenshots (brew install webp; without it they stay PNG). Publish with
# deploy/site.sh.
site:
	go run ./cmd/bitvoicesite -out dist-site -base https://bitvoice.org
