CHROME_PATH ?= /Applications/Google Chrome.app
export CHROME_PATH PLAYWRIGHT_MODULE

.PHONY: verify verify-go verify-web check-go-venue check-web-venue

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
	@case "$$PLAYWRIGHT_MODULE" in /*) ;; *) \
	  echo 'Set PLAYWRIGHT_MODULE to the absolute path of the admitted Playwright module on ENG-UK. See RUN.md.' >&2; exit 2 ;; \
	esac
	@cd web && node -e "if (typeof require(process.env.PLAYWRIGHT_MODULE).chromium?.launch !== 'function') process.exit(2)" || { \
	  echo 'Could not load Playwright from PLAYWRIGHT_MODULE. See RUN.md.' >&2; exit 2; \
	}

verify-go: check-go-venue
	go build ./...
	go test ./...
	go vet ./...
	cd third_party/dostoevsky && go test ./... && go vet ./...
	bash tests/roster-apply.sh
	bash scripts/build-clients.sh dist/ui

verify-web: check-web-venue
	mkdir -p build
	go build -o build/openagentinboxd ./cmd/openagentinboxd
	bash scripts/build-clients.sh dist/ui
	cd web && npm ci && npm run build && npm run typecheck
	cd web && INBOX_DAEMON="$(CURDIR)/build/openagentinboxd" npm test
	cd web && INBOX_DAEMON="$(CURDIR)/build/openagentinboxd" node tests/browser.cjs
