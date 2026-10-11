# memd developer targets. See AGENTS.md ("Commands") and README.md.
#
#   make gen      buf generate, sqlc, mockery
#   make lint     golangci-lint, interface-doc check, buf lint, ruff
#   make test     go test -race ./... (needs Docker or MEMD_TEST_DATABASE_URL)
#   make e2e      opt-in end-to-end tests (docker compose stack + emulated gateway)
#   make up       docker compose: postgres, minio
#   make migrate  alembic upgrade head, then dump schema/schema.sql
#   make run      memd all
#   make examples build and run every example against a running memd (examples/README.md)
#   make bench    run a benchmark against memd (eval/README.md)

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

ROOT    := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
VENV    := $(ROOT)/.venv
PYTHON  ?= python3.12
ALEMBIC := $(VENV)/bin/alembic
RUFF    := $(VENV)/bin/ruff
MOCKERY := $(ROOT)/.bin/mockery
TXCHECK := $(ROOT)/.bin/txcheck
COMPOSE := docker compose -f $(ROOT)/docker-compose.yml

export MEMD_PG_PORT ?= 55432
export MEMD_DSN ?= postgresql+psycopg://memd:memd@localhost:$(MEMD_PG_PORT)/memd

.PHONY: help gen gen-proto gen-sqlc gen-mocks lint lint-go lint-ifacedoc lint-txcheck lint-proto lint-py ruff fmt test e2e \
	up down migrate migrate-check downgrade run examples tools venv bench bench-resume bench-compare bench-list

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

# ---------- code generation ----------

gen: gen-proto gen-sqlc gen-mocks ## buf generate, sqlc, mockery

gen-proto:
	buf generate

gen-sqlc:
	cd internal/store/postgres && sqlc generate

gen-mocks: $(MOCKERY)
	rm -rf internal/mocks
	$(MOCKERY) --config .mockery.yaml --log-level warn

$(MOCKERY): tools/mockery/go.mod tools/mockery/go.sum
	go -C tools/mockery build -o $(MOCKERY) github.com/vektra/mockery/v3

tools: $(MOCKERY) venv ## build mockery and the alembic venv

# ---------- lint ----------

lint: lint-go lint-ifacedoc lint-txcheck lint-proto lint-py ## golangci-lint, ifacedoc, txcheck, buf lint, ruff

lint-go:
	golangci-lint run ./...

lint-ifacedoc:
	go run ./tools/ifacedoc .

$(TXCHECK): $(wildcard tools/txcheck/*.go tools/txcheck/analyzer/*.go) tools/txcheck/go.mod tools/txcheck/go.sum
	go -C tools/txcheck build -o $(TXCHECK) .

lint-txcheck: $(TXCHECK) ## no model calls or Enqueue-before-Seq inside InTx literals
	$(TXCHECK) ./...

# The API may change freely until the first release, so only buf lint runs here. The first release tag
# becomes the buf breaking baseline: add `buf breaking --against '.git#tag=<that tag>'` to this target then.
lint-proto:
	buf lint

ruff: lint-py ## ruff check + format --check over migrations

lint-py: venv
	$(RUFF) check migrations
	$(RUFF) format --check migrations

fmt: ## apply gofumpt + goimports and ruff format
	golangci-lint fmt ./...
	$(RUFF) format migrations

# ---------- tests ----------

# Store tests run on real Postgres (internal/store/postgres/pgtest): one container for the whole run, or the
# server at MEMD_TEST_DATABASE_URL. The venv provides alembic, which migrates the template database.
test: venv ## go test -race ./... (needs Docker or MEMD_TEST_DATABASE_URL)
	go test -race ./...

# Each test removes its own compose project; the sweep afterwards catches projects of this run left by a
# panic or timeout (the harness names them memd-e2e-<run>-*, so parallel runs never touch each other's).
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

# Runs every scenario of examples/ in turn against a memd that is already up (see examples/README.md); the first
# failing example stops the run. ADDR and TENANT pick the server and the x-tenant-id the examples send.
ADDR   ?= localhost:50051
TENANT ?= examples

examples: ## build and run every example against a running memd (ADDR=host:port TENANT=name)
	go build ./examples/...
	for dir in examples/*/; do \
		case $$dir in examples/internal/) continue;; esac; \
		echo "==> $$dir"; \
		go run ./$$dir -addr $(ADDR) -tenant $(TENANT) || exit 1; \
	done

# ---------- benchmarks (eval/README.md) ----------

BENCH  ?=
CONFIG ?=
ARGS   ?=
EVAL   := go run ./eval/cmd/eval

bench: ## run a benchmark: make bench BENCH=locomo CONFIG=eval/configs/locomo.yaml [ARGS='-limit 1']
	$(EVAL) run $(if $(CONFIG),-config $(CONFIG)) $(if $(BENCH),-benchmark $(BENCH)) $(ARGS)

bench-resume: ## finish an interrupted run: make bench-resume RUN=eval-results/locomo-<time>
	$(EVAL) run -resume $(RUN) $(ARGS)

bench-compare: ## compare two runs: make bench-compare A=eval-results/<run> B=eval-results/<run>
	$(EVAL) compare $(A) $(B)

bench-list: ## list the registered benchmarks
	$(EVAL) list
