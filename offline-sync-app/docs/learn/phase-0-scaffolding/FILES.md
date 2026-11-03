# Phase 0 — Scaffolding: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

> Note: MILESTONES describes Phase 0 as "a chi lith with `GET /health`". The code today is bigger: later phases added S3, the WebSocket hub, and the notifier loop to the same files. This doc describes the files as they are now and marks what belongs to Phase 0.

> Note: there is no committed test for anything in this phase. MILESTONES records a manual check (`curl localhost:8080/health` returns `{"status":"ok"}`). `make test` runs `go test ./...` but nothing in `cmd/server`, `internal/httpapi`, or `internal/config` has a `_test.go` file.

## [cmd/server/main.go](../../../cmd/server/main.go)

- **What it does**: Starts the API as a plain local HTTP server. It builds every dependency and passes them to the router.
- **Control flow**:
  1. You run `make dev-server` (the [Makefile](../../../Makefile) loads `.env` and `.env.local`, then `go run ./cmd/server`).
  2. `main` calls `config.Load()`.
  3. `pgxpool.New(ctx, cfg.DatabaseURL)` makes the Postgres pool.
  4. `storage.New(...)` then `store.EnsureBucket(ctx)` prepare S3/MinIO (Phase 3 addition).
  5. `auth.NewVerifier(...)` (Phase 1 addition), `db.New(pool)`, `wsstub.NewHub()`.
  6. `go runNotifierLoop(ctx, q, hub)` starts a 5-second ticker goroutine (Phase 7 addition).
  7. `httpapi.NewRouter(verifier, q, pool, store, hub)` builds the handler.
  8. `http.ListenAndServe(cfg.Addr, router)` blocks and serves.
- **Data flow**: environment variables → `config.Config` → constructors → one `http.Handler`. Nothing is persisted here.
- **How it is tested**: untested. Verified by hand with `curl` per MILESTONES.
- **Read next**: [internal/httpapi/router.go](../../../internal/httpapi/router.go).

## [internal/httpapi/router.go](../../../internal/httpapi/router.go)

- **What it does**: Builds the chi router: global middleware, `/health`, `/ws`, and a `/api` group behind JWT verification.
- **Control flow**:
  1. `NewRouter` is called by [cmd/server/main.go](../../../cmd/server/main.go) and by [cmd/lambda/main.go](../../../cmd/lambda/main.go).
  2. `r.Use(...)` adds `middleware.RequestID`, `RealIP`, `Logger`, `Recoverer`, then `cors.Handler`.
  3. `r.Get("/health", healthHandler)` is mounted outside the auth group.
  4. `r.Group(...)` applies `verifier.Middleware` to `/api/whoami` (Phase 1), `/api/sync`, locations, sites, and photo routes (Phases 2 and 3).
- **Data flow**: HTTP request → middleware chain (adds request ID, logs, recovers panics, sets CORS headers) → matched handler → HTTP response.
- **How it is tested**: no test in `internal/httpapi`. Handlers that need no database (`healthHandler`) are not covered either.
- **Read next**: [internal/httpapi/health.go](../../../internal/httpapi/health.go).

## [internal/httpapi/health.go](../../../internal/httpapi/health.go)

- **What it does**: Replies `{"status":"ok"}` with `Content-Type: application/json`. It does not check the database.
- **Control flow**:
  1. chi matches `GET /health`.
  2. `healthHandler` sets the header and calls `json.NewEncoder(w).Encode(...)`.
- **Data flow**: no input → a fixed map → JSON body.
- **How it is tested**: untested (no `_test.go`). Manual `curl` only.
- **Read next**: [internal/config/config.go](../../../internal/config/config.go).

## [internal/config/config.go](../../../internal/config/config.go)

- **What it does**: Reads settings from environment variables into a `Config` struct, with local-dev defaults.
- **Control flow**:
  1. Every entrypoint calls `config.Load()`.
  2. `Load` calls `getenv(key, fallback)` per field.
  3. `getenv` uses `os.LookupEnv` and treats an empty value as unset.
- **Data flow**: env vars → `Config` (for example `ADDR` defaults to `:8080`, `DATABASE_URL` to the local Postgres, `APP_ENV` to `local`). S3, EventBridge, and WebSocket endpoint fields deliberately have no local default, so prod falls through to real AWS.
- **How it is tested**: untested (no `config_test.go`).
- **Read next**: [docker-compose.yml](../../../docker-compose.yml).

## [docker-compose.yml](../../../docker-compose.yml)

- **What it does**: Starts the local stand-ins: `postgres:16`, `cognito-local` (pinned `4.0.0`), `minio`, `moto-server`.
- **Control flow**:
  1. `make compose-up` runs `docker compose up -d`.
  2. Docker starts each service. Only `postgres` has a `healthcheck` (`pg_isready -U fieldsync`).
  3. Host ports: Postgres 5432, cognito-local 9229, MinIO 9000 (API) and 9001 (console), Moto 5001 (container port 5000; 5000 clashes with macOS AirPlay Receiver).
- **Data flow**: named volumes `postgres-data`, `cognito-local-data`, `minio-data` keep data between restarts.
- **How it is tested**: untested. Verified by running it.

> Note: MILESTONES Phase 0 lists a `localstack` service. The file has no LocalStack; Phase 3 replaced it with MinIO (see the comment above the `minio` service).

- **Read next**: [Makefile](../../../Makefile).

## [Makefile](../../../Makefile)

- **What it does**: Short names for common commands.
- **Control flow**:
  1. `dev-server`: loads `.env` and `.env.local` with `set -a` and `source`, then `go run ./cmd/server`.
  2. `dev-web`: `cd web && npm run dev`.
  3. `test` runs `go test ./...`; `test-integration` runs `go test -tags=integration ./...`.
  4. `compose-up` / `compose-down` wrap `docker compose`.
  5. `migrate` runs the `migrate` CLI against `$DATABASE_URL` (default local Postgres) with `-path migrations up`. The CLI must already be installed; the Makefile does not install it.
  6. `sqlc-generate`, `build`, `build-lambda`, `tf-validate` (never plans or applies).
- **Data flow**: shell env → Go or Node process.
- **How it is tested**: untested; each target is exercised by use.
- **Read next**: [web/vite.config.ts](../../../web/vite.config.ts).

## [web/vite.config.ts](../../../web/vite.config.ts)

- **What it does**: Configures Vite, the React plugin, and the PWA plugin.
- **Control flow**:
  1. `npm run dev` (or `build`) loads this config.
  2. `envDir` points at the repo root, so Vite reads the same `.env.local` the Go server reads (`VITE_*` variables are the ones exposed to the browser).
  3. `VitePWA` registers `registerType: 'autoUpdate'`, a manifest named `FieldSync` with `display: 'standalone'`, and `navigateFallbackDenylist: [/^\/api/]` so API paths never get the app shell.
- **Data flow**: source in `web/src` → bundle in `web/dist` plus generated service worker and manifest.
- **How it is tested**: untested. MILESTONES says `npm run build` produced `sw.js`, `workbox`, and a manifest.
- **Read next**: [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf).

## [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

- **What it does**: The Terraform root for the whole stack. It sets a placeholder AWS provider (fake `local` keys, credential checks skipped) so `init`, `validate`, and `plan` work without an account.
- **Control flow**:
  1. `make tf-validate` first runs `build-lambda`, then `terraform init -upgrade` and `terraform validate` in this folder.
  2. Terraform wires `module "network"`, `secrets`, `database`, `auth`, `waf`, `storage`, `compute`, `api`, `websocket`, `eventing`, `webhook`.
- **Data flow**: module outputs feed other modules' inputs (for example `module.network.vpc_id` into `module.database`). `local.base_environment` is shared by the Lambda modules.
- **How it is tested**: only by `terraform validate` via `make tf-validate`. `apply` against real AWS is never run automatically.

> Note: MILESTONES says Phase 0 had "no resources yet". The file now has all the modules above; they came in later phases.

- **Read next**: [Phase 1 FILES](../phase-1-auth/FILES.md).
