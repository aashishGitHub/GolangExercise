# Phase 0 — Scaffolding: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

> Note: the files below kept growing after Phase 0. `cmd/server/main.go` and `internal/httpapi/router.go` shown here are the current versions. In Phase 0 itself the router had only `GET /health`, and `internal/wiring` did not exist (MILESTONES says it arrived in Phase 11).

No Go test covers anything in this phase. There is no `_test.go` for `config`, `httpapi`, or `cmd/server`. Phase 0 was verified by hand (`curl`, `docker compose ps`, `terraform validate`), with the output pasted into [MILESTONES.md](../../../MILESTONES.md).

## [internal/config/config.go](../../../internal/config/config.go)

- **What it does**: defines the flat `Config` struct and `Load()`, which fills it from environment variables with fallbacks.
- **Control flow**:
  1. `cmd/server` `main` calls `config.Load()` once at startup (other `cmd/*` binaries call it too).
  2. `Load` calls `getenv(key, fallback)` for each field.
  3. Numeric and float fields go through `atoiOr` / `atofOr`, which return the fallback if parsing fails.
  4. `Config.CognitoIssuerURL()` returns `CognitoIssuer` if set, else `CognitoEndpoint + "/" + CognitoPoolID`.
- **Data flow**: process environment (strings) → `getenv` (empty string counts as unset) → typed `Config` value → passed by value to `wiring.New`.
- **How it is tested**: untested. No test file exists.
- **Read next**: [cmd/server/main.go](../../../cmd/server/main.go).

> Note: the package comment and MILESTONES say local-only settings default to empty. That is true for `S3Endpoint`, `S3AccessKey`, `EventBusEndpoint`, `WSManagementAPIEndpoint`. It is not true for `CognitoEndpoint` (defaults to `http://localhost:9229`) or `RedisAddr` (defaults to `localhost:6379`). `DATABASE_URL` also defaults to localhost, and `QR_SIGNING_SECRET` and `WAITING_ROOM_SECRET` default to dev secrets. The code wins.

## [cmd/server/main.go](../../../cmd/server/main.go)

- **What it does**: the local-dev entrypoint. Builds the app and serves it with plain `net/http`.
- **Control flow**:
  1. `main` calls `config.Load()`.
  2. `wiring.New(ctx, cfg)` builds the Postgres pool, Redis client, services, and the chi router.
  3. `defer app.Pool.Close()` and `defer app.Redis.Close()`.
  4. `http.ListenAndServe(cfg.Addr, app.Router)` blocks and serves.
  5. Any setup error goes to `log.Fatalf`.
- **Data flow**: `Config` in → `*wiring.App` out → `app.Router` is the `http.Handler` that receives every request.
- **How it is tested**: untested. `main` is not unit-tested, and the sibling [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go) wraps the same router for Lambda.
- **Read next**: [internal/httpapi/router.go](../../../internal/httpapi/router.go).

## [internal/httpapi/router.go](../../../internal/httpapi/router.go)

- **What it does**: builds the chi route tree. For Phase 0 the relevant parts are the middleware stack and `GET /health`.
- **Control flow**:
  1. `wiring.New` calls `NewRouter(...)`.
  2. `NewRouter` adds middleware in order: `middleware.RequestID`, `middleware.RealIP`, `middleware.Recoverer`, `cors.Handler(...)`.
  3. `r.Get("/health", handleHealth)` registers the health route outside `/api/v1`.
  4. `handleHealth` sets `Content-Type: application/json` and encodes `{"status":"ok"}`.
- **Data flow**: HTTP request → middleware chain → matched handler → JSON response. `/health` touches no database and no Redis.
- **How it is tested**: untested. No `httpapi` test file exists. MILESTONES verified `/health` with `curl`.
- **Read next**: [docker-compose.yml](../../../docker-compose.yml).

## [docker-compose.yml](../../../docker-compose.yml)

- **What it does**: starts the local stack named `ticketing`: `postgres`, `redis`, `cognito-local`, `minio`, `minio-init`, `moto-server`. A seventh service, `dynamodb-local`, is behind `profiles: ["spike"]` and belongs to Phase 12.
- **Control flow**:
  1. `make compose-up` runs `docker compose up -d`.
  2. `postgres` and `redis` run their `healthcheck` loops until healthy.
  3. `minio` becomes healthy (`mc ready local`).
  4. `minio-init` waits (`depends_on` with `service_healthy`), runs once, creates three buckets, and exits.
- **Data flow**: host ports 5432, 6379, 9229, 9000/9001, 5001 map to containers. Volumes `postgres-data`, `cognito-local-data`, `minio-data` persist data. `moto-server` is host port 5001 because 5000 collides with macOS AirPlay.
- **How it is tested**: not by a test file. Verified by hand in MILESTONES (`docker compose ps`, `redis-cli ping`, `psql ... select 1`, JWKS `curl`).
- **Read next**: [Makefile](../../../Makefile).

## [Makefile](../../../Makefile)

- **What it does**: short names for common commands.
- **Control flow**:
  1. `make dev-server` runs `LOAD_ENV` (sources `.env` then `.env.local` if present) and then `go run ./cmd/server`.
  2. `make test` runs `go test ./...`.
  3. `make test-integration` runs `go test -tags=integration -p 1 ./...`. The `-p 1` serializes packages because later phases have global-scan tests that collide on a shared database (the Makefile comment says so).
  4. `make migrate`, `make sqlc-generate`, `make build`, `make build-lambda`, `make tf-validate` do what their names say. `tf-validate` depends on `build-lambda`.
- **Data flow**: `.env` / `.env.local` → shell variables → child `go run` process → `config.Load()`.
- **How it is tested**: not tested. CI calls `make build-lambda` directly.
- **Read next**: [../../../../.github/workflows/ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml).

## [.github/workflows/ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml)

- **What it does**: runs checks on push and pull request when files under `ticket-booking/**` change.
- **Control flow**:
  1. Job `go`: `go build ./...`, `go vet ./...`, `go test ./...`.
  2. Job `web`: `npm ci`, `npx tsc -b`, `npm run build`, `npx vitest run`.
  3. Job `terraform-validate`: `make build-lambda`, then `terraform init -backend=false` and `terraform validate` in `infra/terraform/envs/local`.
- **Data flow**: repo checkout → build/test commands → pass/fail. No deploy, no `apply`.
- **How it is tested**: it is the test runner. It runs unit tests only (`go test ./...` without the `integration` tag), so integration tests and Playwright E2E do not run in CI.
- **Read next**: [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf).

> Note: MILESTONES says the `web` and lambda-build jobs were added "once those phases exist". They exist in the file now.

## [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

- **What it does**: root Terraform config for validation. Two AWS providers with fake credentials and `skip_*` flags, then module calls.
- **Control flow**:
  1. `terraform init -backend=false` downloads providers.
  2. `terraform validate` parses the HCL (Terraform's config language) and type-checks it. Nothing is created.
  3. The default provider and the `us_east_1` alias both use placeholder credentials `local`.
- **Data flow**: HCL files → validation result only. [versions.tf](../../../infra/terraform/envs/local/versions.tf) pins `aws ~> 5.0` and `random ~> 3.6` and has no backend block.
- **How it is tested**: by `terraform validate` in CI and `make tf-validate`. No plan or apply test.
- **Read next**: [Phase 1 FILES](../phase-1-auth/FILES.md).

> Note: MILESTONES says Phase 0's `envs/local` had "no resources yet". The file now calls many modules (`network`, `database`, `cache`, `compute`, `api`, and more) added in later phases.
