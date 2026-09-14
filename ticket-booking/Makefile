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

# build-lambda: cross-compiled per cmd/*-lambda entrypoint once they exist
# (Phase 11). Left as a documented no-op target until then so `make
# tf-validate` has something to depend on without failing.
build-lambda:
	@echo "build-lambda: no lambda entrypoints yet (Phase 11)"

tf-validate: build-lambda
	cd infra/terraform/envs/local && terraform init -upgrade && terraform validate
