.PHONY: dev-server dev-web test test-integration compose-up compose-down migrate sqlc-generate build build-lambda tf-validate

LOAD_ENV = set -a; [ -f .env ] && source .env; [ -f .env.local ] && source .env.local; set +a

dev-server:
	bash -c '$(LOAD_ENV); go run ./cmd/server'

dev-web:
	cd web && npm run dev

test:
	go test ./...

test-integration:
	# -p 1: serialize package test binaries. Phases 7-9 added tests whose
	# invariant/backlog scans are GLOBAL (all domain_events, all orders),
	# not scoped per test — Go runs different packages' tests concurrently
	# by default, so two packages' test binaries hitting the same shared
	# local Postgres/Redis at once produced real, reproducible
	# cross-package flakes (caught in Phase 9's MILESTONES.md verification,
	# not theoretical).
	go test -tags=integration -p 1 ./...

compose-up:
	docker compose up -d

compose-down:
	docker compose down

migrate:
	migrate -database "$${DATABASE_URL:-postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable}" -path migrations up

sqlc-generate:
	cd sqlc && sqlc generate

build:
	go build ./...

# build-lambda: cross-compiles every cmd/*-lambda entrypoint to a
# provided.al2023/arm64 `bootstrap` binary and zips it into
# infra/terraform/modules/compute/build/<name>.zip — the exact path
# modules/compute's aws_lambda_function.fn resources reference via
# filebase64sha256. Requires `zip` (present by default on macOS/Linux).
LAMBDA_BUILD_DIR = infra/terraform/modules/compute/build
LAMBDA_ENTRYPOINTS = $(notdir $(wildcard cmd/*-lambda))

build-lambda:
	@mkdir -p $(LAMBDA_BUILD_DIR)
	@for name in $(LAMBDA_ENTRYPOINTS); do \
		echo "build-lambda: $$name"; \
		mkdir -p $(LAMBDA_BUILD_DIR)/$$name; \
		GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o $(LAMBDA_BUILD_DIR)/$$name/bootstrap ./cmd/$$name || exit 1; \
		(cd $(LAMBDA_BUILD_DIR)/$$name && zip -q -X ../$$name.zip bootstrap); \
	done

tf-validate: build-lambda
	cd infra/terraform/envs/local && terraform init -upgrade && terraform validate
