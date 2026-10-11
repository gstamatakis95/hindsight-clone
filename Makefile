# memd developer targets. See AGENTS.md ("Commands") and docs/SPEC.md.
#
#   make gen      buf generate, sqlc, mockery
#   make lint     golangci-lint, interface-doc check, buf lint + breaking, ruff
#   make test     go test -race ./...
#   make e2e      opt-in end-to-end tests (docker compose stack + emulated gateway)
#   make up       docker compose: postgres, minio
#   make migrate  alembic upgrade head, then dump schema/schema.sql
#   make run      memd all

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

ROOT    := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
VENV    := $(ROOT)/.venv
PYTHON  ?= python3.12
ALEMBIC := $(VENV)/bin/alembic
RUFF    := $(VENV)/bin/ruff
MOCKERY := $(ROOT)/.bin/mockery
COMPOSE := docker compose -f $(ROOT)/docker-compose.yml
BREAKING_AGAINST ?= main

export MEMD_PG_PORT ?= 55432
export MEMD_DSN ?= postgresql+psycopg://memd:memd@localhost:$(MEMD_PG_PORT)/memd

.PHONY: help gen gen-proto gen-sqlc gen-mocks lint lint-go lint-ifacedoc lint-proto lint-py ruff fmt test e2e \
	up down migrate migrate-check downgrade run tools venv

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

# ---------- code generation ----------

gen: gen-proto gen-sqlc gen-mocks ## buf generate, sqlc, mockery

gen-proto:
	buf generate

gen-sqlc:
	cd internal/adapter/postgres && sqlc generate

gen-mocks: $(MOCKERY)
	rm -rf internal/mocks
	$(MOCKERY) --config .mockery.yaml --log-level warn

$(MOCKERY): tools/mockery/go.mod tools/mockery/go.sum
	go -C tools/mockery build -o $(MOCKERY) github.com/vektra/mockery/v3

tools: $(MOCKERY) venv ## build mockery and the alembic venv

# ---------- lint ----------

lint: lint-go lint-ifacedoc lint-proto lint-py ## golangci-lint, ifacedoc, buf lint + breaking, ruff

lint-go:
	golangci-lint run ./...

lint-ifacedoc:
	go run ./tools/ifacedoc .

# buf breaking runs against BREAKING_AGAINST (default main; after the contracts merge, use
# BREAKING_AGAINST=contracts-v1 to check against the frozen tag). An unresolvable ref fails the target; the
# check is skipped only when the ref exists but has no protos yet (the WP-00 branch against an empty main).
lint-proto:
	buf lint
	@git rev-parse --verify --quiet '$(BREAKING_AGAINST)^{commit}' >/dev/null || \
		{ echo "buf breaking: ref '$(BREAKING_AGAINST)' does not resolve" >&2; exit 1; }
	@if git cat-file -e '$(BREAKING_AGAINST):api/memory/v1' 2>/dev/null; then \
		buf breaking --against '.git#ref=$(BREAKING_AGAINST)'; \
	else \
		echo "buf breaking: skipped ($(BREAKING_AGAINST) has no api/memory/v1 yet)"; \
	fi

ruff: lint-py ## ruff check + format --check over migrations

lint-py: venv
	$(RUFF) check migrations
	$(RUFF) format --check migrations

fmt: ## apply gofumpt + goimports and ruff format
	golangci-lint fmt ./...
	$(RUFF) format migrations

# ---------- tests ----------

test: ## go test -race ./...
	go test -race ./...

# Each test removes its own compose project; the sweep afterwards catches projects of this run left by a
# panic or timeout (names memd-e2e-<run>-*, decision 1412).
e2e: venv ## opt-in end-to-end tests (compose stack + emulated gateway), then sweep this run's projects
	run=$$(date +%s)$$$$; status=0; \
	MEMD_E2E_RUN=$$run go test -race -count=1 -tags e2e -timeout $${E2E_TIMEOUT:-30m} ./test/e2e/... || status=$$?; \
	{ docker compose ls -a -q --filter name=memd-e2e-$$run- 2>/dev/null || true; } | while read -r p; do \
		echo "e2e: removing leftover compose project $$p"; \
		docker compose -p "$$p" down -v --remove-orphans --timeout 5 >/dev/null 2>&1 || true; \
	done; \
	exit $$status

# ---------- local stack & schema ----------

venv: $(ALEMBIC)

$(ALEMBIC): migrations/pyproject.toml
	test -x $(VENV)/bin/python || $(PYTHON) -m venv $(VENV)
	$(VENV)/bin/pip install --quiet --upgrade pip
	$(VENV)/bin/pip install --quiet -e "migrations[dev]"
	touch $(ALEMBIC)

up: ## docker compose up: postgres + minio (healthy)
	$(COMPOSE) up -d --wait postgres minio
	$(COMPOSE) run --rm minio-init

down: ## docker compose down (keeps volumes)
	$(COMPOSE) down

migrate: venv ## alembic upgrade head, then dump schema/schema.sql
	$(ALEMBIC) -c migrations/alembic.ini upgrade head
	scripts/schema-dump.sh

migrate-check: venv ## fail if schema/schema.sql is stale
	ALEMBIC=$(ALEMBIC) scripts/schema-check.sh

downgrade: venv ## alembic downgrade base
	$(ALEMBIC) -c migrations/alembic.ini downgrade base

run: ## memd all
	go run ./cmd/memd all
