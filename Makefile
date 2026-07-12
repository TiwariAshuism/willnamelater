# InfluAudit developer Makefile.
#
# One entry point for the whole stack: bring it up, run migrations, and drive
# the backend, ML service, and web app. Targets shell out to docker compose,
# go, pnpm, and openssl — everything a `make up` needs is a dependency below.
#
# Run `make` (or `make help`) for the annotated target list.

COMPOSE      := docker compose -f deploy/docker-compose.yml
BACKEND      := services/backend
ML           := services/ml
MIGRATIONS   := $(BACKEND)/migrations
JWT_KEY      := deploy/dev-secrets/jwt-dev.pem
WEB          := @influaudit/web
CONTRACTS    := @influaudit/contracts

# Fail loudly inside a recipe rather than limping past a failed command.
SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Stack lifecycle
# ---------------------------------------------------------------------------

.PHONY: up
up: dev-secrets ## Build and start the full stack (postgres, redis, localstack S3, gotenberg, ml, api, worker) detached
	$(COMPOSE) up -d --build
	@echo ""
	@echo "Stack up. API: http://localhost:8080  ·  S3 (LocalStack): http://localhost:4566"
	@echo "Follow logs with: make logs"

.PHONY: up-fg
up-fg: dev-secrets ## Start the full stack in the foreground (Ctrl-C to stop)
	$(COMPOSE) up --build

.PHONY: down
down: ## Stop the stack, keeping data volumes
	$(COMPOSE) down

.PHONY: clean
clean: ## Stop the stack AND drop all data volumes (postgres, redis, localstack) — fresh slate
	$(COMPOSE) down -v

.PHONY: restart
restart: down up ## Restart the whole stack

.PHONY: ps
ps: ## Show stack service status
	$(COMPOSE) ps

.PHONY: logs
logs: ## Tail logs from every service
	$(COMPOSE) logs -f

.PHONY: logs-api
logs-api: ## Tail the api service logs
	$(COMPOSE) logs -f api

.PHONY: logs-worker
logs-worker: ## Tail the worker service logs
	$(COMPOSE) logs -f worker

# ---------------------------------------------------------------------------
# Database migrations
# ---------------------------------------------------------------------------

.PHONY: migrate
migrate: ## Apply all pending migrations (runs the one-shot migrate container against the compose postgres)
	$(COMPOSE) run --rm migrate

.PHONY: migrate-local
migrate-local: ## Apply migrations from the host (needs INFLUAUDIT_POSTGRES__DSN in the environment)
	cd $(BACKEND) && go run ./cmd/migrate

.PHONY: migrate-reset
migrate-reset: clean up ## Drop the data volume and re-apply every migration from scratch
	@echo "Database reset and re-migrated."

.PHONY: migrate-force
migrate-force: ## Clear a dirty migration and pin the version: make migrate-force version=19 (then `make migrate`). If the failed migration left partial objects, drop them first or use migrate-reset.
	@test -n "$(version)" || { echo "usage: make migrate-force version=<n>"; exit 1; }
	$(COMPOSE) exec -T postgres psql -U $${POSTGRES_USER:-influaudit} -d $${POSTGRES_DB:-influaudit} \
		-c "UPDATE schema_migrations SET version=$(version), dirty=false;"

.PHONY: db-psql
db-psql: ## Open a psql shell on the running compose postgres
	$(COMPOSE) exec postgres psql -U $${POSTGRES_USER:-influaudit} -d $${POSTGRES_DB:-influaudit}

.PHONY: migrate-create
migrate-create: ## Scaffold the next-numbered up/down migration pair: make migrate-create name=add_widgets
	@test -n "$(name)" || { echo "usage: make migrate-create name=<snake_case_name>"; exit 1; }
	@last=$$(ls $(MIGRATIONS)/*.up.sql | sed -E 's|.*/([0-9]+)_.*|\1|' | sort -n | tail -1); \
	next=$$(printf "%06d" $$((10#$$last + 1))); \
	up="$(MIGRATIONS)/$${next}_$(name).up.sql"; \
	down="$(MIGRATIONS)/$${next}_$(name).down.sql"; \
	printf -- "-- Owner: <module> (internal/<module>).\n\n" > "$$up"; \
	printf -- "-- Reverse of $${next}_$(name).up.sql\n" > "$$down"; \
	echo "created $$up"; echo "created $$down"

# ---------------------------------------------------------------------------
# Dev secrets
# ---------------------------------------------------------------------------

.PHONY: dev-secrets
dev-secrets: $(JWT_KEY) ## Generate the dev RS256 JWT signing key if absent (gitignored)

$(JWT_KEY):
	@mkdir -p $(dir $(JWT_KEY))
	openssl genrsa -out $(JWT_KEY) 2048
	@echo "generated dev JWT signing key at $(JWT_KEY)"

# ---------------------------------------------------------------------------
# Backend (Go)
# ---------------------------------------------------------------------------

.PHONY: backend-build
backend-build: ## Compile every backend package
	cd $(BACKEND) && go build ./...

.PHONY: backend-test
backend-test: ## Run the backend test suite with the race detector
	cd $(BACKEND) && go test -race ./...

.PHONY: backend-lint
backend-lint: ## gofmt check + go vet + golangci-lint
	cd $(BACKEND) && test -z "$$(gofmt -l .)" || { echo "gofmt: files need formatting"; gofmt -l .; exit 1; }
	cd $(BACKEND) && go vet ./...
	cd $(BACKEND) && golangci-lint run ./...

.PHONY: openapi
openapi: ## Regenerate the OpenAPI spec from the route sources
	cd $(BACKEND) && go run ./cmd/openapigen

.PHONY: openapi-check
openapi-check: ## Fail if the checked-in OpenAPI spec has drifted from the route sources
	cd $(BACKEND) && go run ./cmd/openapigen -check

.PHONY: api
api: ## Run the api server on the host (needs backend env; DB/redis reachable)
	cd $(BACKEND) && go run ./cmd/api

.PHONY: worker
worker: ## Run the background worker on the host (needs backend env; DB/redis reachable)
	cd $(BACKEND) && go run ./cmd/worker

# ---------------------------------------------------------------------------
# ML service (Python)
# ---------------------------------------------------------------------------

.PHONY: ml-install
ml-install: ## Install the ML service with dev extras
	cd $(ML) && pip install -e '.[dev]'

.PHONY: ml-test
ml-test: ## Lint (ruff) and test (pytest) the ML service
	cd $(ML) && ruff check .
	cd $(ML) && pytest

.PHONY: ml-train
ml-train: ## Train the supervised fraud model from the admin label export (needs LABELS_URL, TOKEN, ARTIFACTS). Writes nothing below the data floor.
	cd $(ML) && python -m training.cli \
		--labels-url "$${LABELS_URL:-http://localhost:8080/v1/admin/training/labels}" \
		--token "$${TOKEN:-}" \
		--out "$${INFLUAUDIT_ML_ARTIFACTS:-./artifacts}"

.PHONY: ml-retrain
ml-retrain: ## Champion-challenger retrain (MODEL=fraud|reach): fetch → train → validate → register → shadow → promote. Gated on real data; promotes nothing below the floor or on any failed gate.
	cd $(ML) && python -m training.retrain \
		--model "$${MODEL:-fraud}" \
		--feature-rows-url "$${FEATURE_ROWS_URL:-http://localhost:8080/v1/admin/mlops/feature-rows}" \
		--canaries-url "$${CANARIES_URL:-http://localhost:8080/v1/admin/mlops/canaries}" \
		--models-url "$${MODELS_URL:-http://localhost:8080/v1/admin/mlops/models}" \
		--token "$${TOKEN:-}" \
		--out "$${INFLUAUDIT_ML_ARTIFACTS:-./artifacts}" \
		$${PROMOTE:+--promote}

# ---------------------------------------------------------------------------
# Web (Next.js) + typed contract client
# ---------------------------------------------------------------------------

.PHONY: install
install: ## Install workspace JS dependencies (pnpm)
	pnpm install

.PHONY: contracts
contracts: ## Regenerate the typed TS client from the OpenAPI spec
	pnpm --filter $(CONTRACTS) generate

.PHONY: web
web: ## Run the web dashboard dev server (http://localhost:3000)
	pnpm --filter $(WEB) dev

.PHONY: web-build
web-build: ## Production build of the web app
	pnpm --filter $(WEB) build

.PHONY: web-test
web-test: ## Run the web unit tests
	pnpm --filter $(WEB) test

.PHONY: web-lint
web-lint: ## Typecheck + lint the web app
	pnpm --filter $(WEB) typecheck
	pnpm --filter $(WEB) lint

# ---------------------------------------------------------------------------
# Aggregate / CI
# ---------------------------------------------------------------------------

.PHONY: gen
gen: openapi contracts ## Regenerate the spec AND the TS client from it (run after changing routes)

.PHONY: test
test: backend-test web-test ## Run backend + web test suites

.PHONY: lint
lint: backend-lint web-lint ## Lint backend + web

.PHONY: gate
gate: backend-lint backend-test openapi-check web-lint web-test ## The full local gate (mirrors CI)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
