# Phase 0 — Scaffolding: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

## L1 Beginner

**Q1. What is a health endpoint and why does the project have one?**
It is a route that returns `{"status":"ok"}` without touching any dependency. It proves the process is up and the router works. See `handleHealth` in [internal/httpapi/router.go](../../../internal/httpapi/router.go).

Try it:
```
curl localhost:8080/health
```

**Q2. Why run Postgres, Redis, and fake AWS services in Docker?**
So the whole system runs on a laptop with no cloud account. The stack is defined in [docker-compose.yml](../../../docker-compose.yml). Each service stands in for a real one: cognito-local for Cognito, MinIO for S3, moto for EventBridge/SQS.

Try it:
```
docker compose up -d
```
```
docker compose ps
```

**Q3. What is an environment variable and why does the app use them for settings?**
It is a named string the OS passes to a process. The same binary then runs locally and in prod with different values and no code change. [internal/config/config.go](../../../internal/config/config.go) reads them in `Load()`.

**Q4. What does `make test` do?**
It runs `go test ./...` (see [Makefile](../../../Makefile)). That runs unit tests only, because integration tests need the `integration` build tag.

Try it:
```
make test
```

## L2 Junior

**Q5. Trace what happens from `make dev-server` to the first request being served.**
1. `LOAD_ENV` in the [Makefile](../../../Makefile) sources `.env` and `.env.local`.
2. `go run ./cmd/server` starts `main` in [cmd/server/main.go](../../../cmd/server/main.go).
3. `config.Load()` builds `Config`.
4. `wiring.New` builds the pgx pool, Redis client, services, and calls `httpapi.NewRouter` ([internal/wiring/wiring.go](../../../internal/wiring/wiring.go)).
5. `http.ListenAndServe(cfg.Addr, app.Router)` serves.

**Q6. How does `getenv` treat an empty variable?**
`getenv` only returns the env value when the variable is set and non-empty: `ok && v != ""`. An empty string falls back to the default. See [internal/config/config.go](../../../internal/config/config.go).

**Q7. What does each middleware in `NewRouter` do, and why does order matter?**
`RequestID` tags the request, `RealIP` fixes the client address, `Recoverer` turns a panic into a 500 instead of killing the connection handler, and `cors.Handler` adds CORS headers and answers preflight. They run in `r.Use` order, outermost first, in [internal/httpapi/router.go](../../../internal/httpapi/router.go). I did not find a test that proves a specific ordering requirement.

**Q8. Why is `moto-server` on host port 5001?**
Port 5000 collides with the macOS AirPlay Receiver. The compose file comment says so in [docker-compose.yml](../../../docker-compose.yml).

## L3 Mid

**Q9. Why pin container images to exact versions, and what real problem did it expose?**
`latest` can change under you. MILESTONES records a real finding: Docker Hub denied anonymous pulls of pinned `minio/minio` RELEASE tags, so both `minio` and `minio-init` use `quay.io/minio/...` with the same tags. The cognito-local pin is `4.0.0` because the compose comment says versions at or above 4.0.0 provide the `.well-known` endpoints and the port must stay 9229. See [docker-compose.yml](../../../docker-compose.yml) and [MILESTONES.md](../../../MILESTONES.md).

**Q10. Why does `make test-integration` use `-p 1`?**
The Makefile comment says later phases have tests with global scans (all `domain_events`, all `orders`). Go runs different packages' test binaries concurrently, and two binaries on one shared Postgres/Redis produced real cross-package flakes. `-p 1` runs packages one at a time. See [Makefile](../../../Makefile). The cost is a slower suite.

**Q11. Why run `terraform validate` in CI and never `apply`?**
`validate` needs no AWS access: the providers use fake credentials and `skip_*` flags in [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf). `apply` would create billable resources, so MILESTONES makes it a separate, explicitly user-triggered step. The CI job runs `make build-lambda` first because the compute module reads Lambda zip files at validate time (comment in [ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml)).

**Q12. How is this phase tested, and what does that prove?**
There is no Go test for it. Verification was manual: `go build`, `go vet`, `gofmt -l`, `curl /health`, `docker compose ps`, `terraform validate`, all pasted in [MILESTONES.md](../../../MILESTONES.md). That proves the skeleton compiles and boots. It does not guard against regressions, because nothing automated covers `config.Load` or the router.

## L4 Senior

**Q13. What are the weak spots in the config design?**
`Load()` returns no error and uses silent fallbacks. A typo like `HOLD_TTL_SECONDS=abc` falls back to 600 with no warning (`atoiOr`). Required values such as `COGNITO_CLIENT_ID` default to empty and fail later, at token verification, not at startup. The "local-only defaults to empty" rule is also not applied everywhere (`CognitoEndpoint`, `RedisAddr`, `DATABASE_URL` default to localhost; `QR_SIGNING_SECRET` and `WAITING_ROOM_SECRET` default to dev secrets). See [internal/config/config.go](../../../internal/config/config.go).

**Q14. Why split `internal/wiring` out of `cmd/server`, and what does it not solve?**
`cmd/server` and `cmd/server-lambda` both need the same dependency graph, so one `wiring.New` avoids two copies drifting ([internal/wiring/wiring.go](../../../internal/wiring/wiring.go)). It does not solve per-replica background work: the AIMD loop (`runAIMDLoop`) starts inside `New`, so every replica or warm Lambda runs its own copy. The package comment names this gap.

**Q15. At 100x scale, what in the scaffolding breaks first?**
Not verified by any measurement in this phase. From the code: `wiring.New` creates the pool with `pgxpool.New(ctx, cfg.DatabaseURL)` using defaults, with no pool-size setting. MILESTONES Phase 2 later attributes a latency spread under concurrency to connection-pool queuing, so pool sizing is the first suspect. Treat that as a hypothesis to verify, not a measured result.

**Q16. What does the local stack not reproduce about AWS?**
moto is control-plane only: the compose comment says it never delivers an event or fires a schedule. cognito-local does not implement SRP, so the frontend must use `USER_PASSWORD_AUTH` (see [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh)). The fuller list is in docs/plan.md "Fidelity gaps and risks" ([plan.md](../../plan.md)).
