GO      ?= go
BIN     := bin/bitvoice
PKG     := ./cmd/bitvoice
# The released version, written here so a build from a source archive reports the same version as a checkout.
VERSION ?= v0.0.0
# RELEASE_KEY is BioDream's release public key (bvsign -new prints it); a build with it installs only
# upgrades signed with the matching private key.
RELEASE_KEY ?=
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.releaseKey=$(RELEASE_KEY)

.PHONY: all build test vet fmt check clean web e2e manual

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
	cd web && pnpm exec tsc src/manual.ts --outDir /tmp/bitvoice-manual --module es2022 --target es2022 --moduleResolution bundler --skipLibCheck && \
		cp /tmp/bitvoice-manual/manual.js /tmp/bitvoice-manual/manual.mjs && \
		node scripts/manual.mjs /tmp/bitvoice-manual/manual.mjs $(CURDIR)/docs/manual/index.html $(CURDIR)/docs/manual/bitvoice-manual.pdf
