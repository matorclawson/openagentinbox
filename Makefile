CHROME_PATH ?= /Applications/Google Chrome.app
export CHROME_PATH

PYTHON ?= python3

.PHONY: verify verify-go verify-web verify-harness release release-check check-go-venue check-web-venue

# Full verification requires a receipt from each venue. Dispatch only the
# portion eligible to run on the current machine.
verify:
	@case "$$(uname -s)" in \
	  Linux) $(MAKE) verify-go ;; \
	  Darwin) $(MAKE) verify-web ;; \
	  *) echo 'Use Linux ARM64 production-03/04 or ENG-UK for verification.' >&2; exit 2 ;; \
	esac

check-go-venue:
	@case "$$(uname -s)/$$(uname -m)/$$(hostname -s)" in \
	  Linux/aarch64/zeo-internal-production-03|Linux/aarch64/zeo-internal-production-04) ;; \
	  *) echo 'Run Go verification on admitted Linux ARM64 production-03/04.' >&2; exit 2 ;; \
	esac

check-web-venue:
	@case "$$(uname -s)/$$(hostname -s)" in \
	  Darwin/ENG-UK|Darwin/ENG-UK-*) ;; \
	  *) echo 'Run web verification on ENG-UK.' >&2; exit 2 ;; \
	esac
	@if [ -d "$$CHROME_PATH" ]; then chrome="$$CHROME_PATH/Contents/MacOS/Google Chrome"; else chrome="$$CHROME_PATH"; fi; \
	  test -f "$$chrome" && test -x "$$chrome" || { echo 'Set CHROME_PATH to Google Chrome.app or its executable.' >&2; exit 2; }

verify-go: check-go-venue
	go build ./...
	go test ./...
	go vet ./...
	bash tests/roster-apply.sh
	bash tests/release-outdir.sh
	bash tests/release-package.sh
	bash tests/release-source-time.sh
	bash scripts/build-clients.sh dist/ui

verify-web: check-web-venue
	mkdir -p build
	go build -o build/openagentinboxd ./cmd/openagentinboxd
	bash scripts/build-clients.sh dist/ui
	cd web && npm ci --include=dev && npm run build && npm run typecheck
	cd web && INBOX_DAEMON="$(CURDIR)/build/openagentinboxd" npm test
	cd web && env -i PATH="$$PATH" CHROME_PATH="$$CHROME_PATH" \
	  INBOX_BROWSER_FAILURE_SCREENSHOT="$$INBOX_BROWSER_FAILURE_SCREENSHOT" \
	  node tests/browser.cjs "$(CURDIR)/build/openagentinboxd"

# The harness integration checks need the Python MCP SDK in $(PYTHON)'s environment:
#   python3 -m venv .venv && .venv/bin/pip install 'mcp>=1.29,<2' && make verify-harness PYTHON=.venv/bin/python
# They use disposable accounts and no provider key.
verify-harness: check-go-venue
	mkdir -p build
	go build -o build/openagentinboxd ./cmd/openagentinboxd
	go build -o build/openagentinbox-client ./cmd/openagentinbox-client
	INBOX_DAEMON="$(CURDIR)/build/openagentinboxd" INBOX_CLIENT="$(CURDIR)/build/openagentinbox-client" $(PYTHON) tests/harnesses.py

# Release artifacts, built on ENG-UK from a clean working tree and the exact committed tree.
# Each build claims a NEW directory directly under build/ (see scripts/release-outdir.sh); nothing
# is ever deleted, so remove an old build/release-* directory yourself before reusing a name.
# release-check builds twice and fails unless every checksum, MANIFEST.json included, matches.
COMMIT = $(shell git rev-parse --short=12 HEAD)

release: check-web-venue
	mkdir -p build
	bash scripts/build-release.sh build/release-$(COMMIT)

release-check: check-web-venue
	mkdir -p build
	bash scripts/build-release.sh build/release-check-$(COMMIT)-a
	bash scripts/build-release.sh build/release-check-$(COMMIT)-b
	diff build/release-check-$(COMMIT)-a/MANIFEST.json build/release-check-$(COMMIT)-b/MANIFEST.json
	diff build/release-check-$(COMMIT)-a/SHA256SUMS build/release-check-$(COMMIT)-b/SHA256SUMS
	@echo 'reproducible: both builds have identical checksums'

# Language-independent mailbox behavior. Supply already built binaries in OAI_BIN_DIR.
.PHONY: verify-product-contracts
verify-product-contracts:
	@test -n "$(OAI_BIN_DIR)" || { echo 'Set OAI_BIN_DIR to the candidate binary directory.' >&2; exit 2; }
	OAI_BIN_DIR="$(OAI_BIN_DIR)" OAI_REFERENCE_BIN_DIR="$(or $(OAI_REFERENCE_BIN_DIR),$(OAI_BIN_DIR))" $(PYTHON) tests/product-contracts.py
