# Admin panel build. Usage: make -f web/web.mk web  (or `include web/web.mk` from the root Makefile).
WEB_DIR ?= web

.PHONY: web web-check web-dev web-mock web-clean

web: ## build the admin panel into web/dist (embedded by package web)
	cd $(WEB_DIR) && npm ci && npm run build

web-check: ## typecheck, lint, unit tests and build of the admin panel
	cd $(WEB_DIR) && npm ci && npm run check

web-dev: ## panel dev server against a running core (MF_CORE_URL, default http://127.0.0.1:8080)
	cd $(WEB_DIR) && npm run dev

web-mock: ## panel dev server with the in-browser simulator (no core needed)
	cd $(WEB_DIR) && npm run dev:mock

web-clean:
	cd $(WEB_DIR) && find dist -mindepth 1 ! -name .keep -delete && rm -rf dist-mock
