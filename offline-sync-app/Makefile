.PHONY: dev-server dev-web dev-outbox-relay dev-photo-processor dev-dashboard-aggregator test test-integration compose-up compose-down migrate sqlc-generate build build-lambda tf-validate

dev-server:
	bash -c 'set -a; [ -f .env ] && source .env; [ -f .env.local ] && source .env.local; set +a; go run ./cmd/server'

dev-web:
	cd web && npm run dev

# Phase 7 consumers — local dev's domain_events-polling substitutes for the
# SQS trigger the *-lambda entrypoints use in prod (see internal/consume's
# package comment). notifier isn't here: it only works as a goroutine inside
# cmd/server, sharing its in-memory WebSocket hub (see internal/wsstub).
dev-outbox-relay:
	bash -c 'set -a; [ -f .env ] && source .env; [ -f .env.local ] && source .env.local; set +a; go run ./cmd/outbox-relay'

dev-photo-processor:
	bash -c 'set -a; [ -f .env ] && source .env; [ -f .env.local ] && source .env.local; set +a; go run ./cmd/photo-processor'

dev-dashboard-aggregator:
	bash -c 'set -a; [ -f .env ] && source .env; [ -f .env.local ] && source .env.local; set +a; go run ./cmd/dashboard-aggregator'

test:
	go test ./...

# Requires `make compose-up` + `make migrate` first (real Postgres, no mocks).
test-integration:
	go test -tags=integration ./...

compose-up:
	docker compose up -d

compose-down:
	docker compose down

migrate:
	migrate -database "$${DATABASE_URL:-postgres://fieldsync:fieldsync@localhost:5432/fieldsync?sslmode=disable}" -path migrations up

sqlc-generate:
	cd sqlc && sqlc generate

build:
	go build ./...
	cd web && npm run build

# Terraform's compute/eventing modules read these zips directly via
# filebase64sha256 — run this before `terraform apply` (or `tf-validate`,
# which only needs the files to exist).
build-lambda:
	GOOS=linux GOARCH=arm64 go build -o infra/terraform/modules/compute/build/bootstrap ./cmd/lambda
	cd infra/terraform/modules/compute/build && zip -q -X bootstrap.zip bootstrap
	GOOS=linux GOARCH=arm64 go build -o infra/terraform/modules/eventing/build/photo-processor/bootstrap ./cmd/photo-processor-lambda
	cd infra/terraform/modules/eventing/build/photo-processor && zip -q -X bootstrap.zip bootstrap
	GOOS=linux GOARCH=arm64 go build -o infra/terraform/modules/eventing/build/dashboard-aggregator/bootstrap ./cmd/dashboard-aggregator-lambda
	cd infra/terraform/modules/eventing/build/dashboard-aggregator && zip -q -X bootstrap.zip bootstrap
	GOOS=linux GOARCH=arm64 go build -o infra/terraform/modules/eventing/build/notifier/bootstrap ./cmd/notifier-lambda
	cd infra/terraform/modules/eventing/build/notifier && zip -q -X bootstrap.zip bootstrap
	GOOS=linux GOARCH=arm64 go build -o infra/terraform/modules/eventing/build/outbox-relay/bootstrap ./cmd/outbox-relay-lambda
	cd infra/terraform/modules/eventing/build/outbox-relay && zip -q -X bootstrap.zip bootstrap
	GOOS=linux GOARCH=arm64 go build -o infra/terraform/modules/websocket/build/bootstrap ./cmd/ws-connect-lambda
	cd infra/terraform/modules/websocket/build && zip -q -X bootstrap.zip bootstrap

# Never runs plan/apply — real AWS credentials are a separate, explicit step.
tf-validate: build-lambda
	cd infra/terraform/envs/local && terraform init -upgrade && terraform validate
