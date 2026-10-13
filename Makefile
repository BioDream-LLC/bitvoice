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

.PHONY: all build test vet fmt check clean web e2e manual release

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
# (bitvoice.sig beside it, for the Updates page), packed with LICENSE, NOTICE and the manual, with
# SHA256SUMS. Archives name no person: files are owned by root, and zips keep no owner at all.
#   make release VERSION=v0.1.3 SIGNING_KEY=path/to/release.key
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
release:
	@case "$(VERSION)" in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "set VERSION, like VERSION=v0.1.3"; exit 1;; esac
	@test -n "$(SIGNING_KEY)" || { echo "set SIGNING_KEY to the release private key"; exit 1; }
	rm -rf dist && mkdir -p dist
	$(GO) build -o dist/.bvsign ./cmd/bvsign
	@set -e; for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; name=bitvoice-$(VERSION)-$$os-$$arch; \
		exe=bitvoice; [ $$os = windows ] && exe=bitvoice.exe; mkdir -p dist/$$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.releaseKey=$(BIODREAM_KEY)" -o dist/$$name/$$exe $(PKG); \
		dist/.bvsign -key "$(SIGNING_KEY)" dist/$$name/$$exe > /dev/null; \
		cp LICENSE NOTICE docs/manual/bitvoice-manual.pdf dist/$$name/; \
		if [ $$os = windows ]; then (cd dist && TZ=UTC zip -qrX $$name.zip $$name); \
		else COPYFILE_DISABLE=1 tar -C dist --uid 0 --gid 0 --uname root --gname root -czf dist/$$name.tar.gz $$name; fi; \
		echo "built $$name"; done
	rm dist/.bvsign
	cd dist && shasum -a 256 *.tar.gz *.zip > SHA256SUMS
