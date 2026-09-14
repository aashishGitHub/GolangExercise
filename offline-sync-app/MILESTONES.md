# FieldSync — Milestone Tracker

Source of truth for build order. Mirrors the phased roadmap in
`docs/study-the-basic-requirements-md-and-sleepy-lovelace.md` (§16), expanded into checkable tasks.
Update checkboxes as work lands; do not reorder phases without noting why below the table.

**Local-AWS emulator decision (locked 2026-09-07, amended 2026-09-07 — see Phase 3 note below):** fully
free patchwork — real `postgres:16` (no RDS API layer), `cognito-local` for auth, **MinIO** for S3 (not
LocalStack — see below), Moto server as an EventBridge supplement, a hand-rolled local WS stub standing in
for API Gateway WebSocket (documented fidelity gap — real WebSocket API GW only exercised against real
AWS later). Terraform is written and `plan`-validated throughout; `apply` against real AWS is a separate,
explicitly user-triggered step, never run automatically.

Status legend: `[ ]` not started · `[~]` in progress · `[x]` done · `[!]` blocked (see note)

---

## Phase 0 — Scaffolding ✅ 2026-09-07
- [x] Go module + repo layout (`cmd/server`, `internal/*`, `migrations/`, `sqlc/`)
- [x] chi router lith with `GET /health`, runs as local HTTP server (verified: `curl localhost:8080/health` → `{"status":"ok"}`)
- [x] React + Vite + TypeScript PWA skeleton (`web/`) — Dexie, dexie-react-hooks, Zustand, vite-plugin-pwa
      installed; `npm run build` produces `sw.js`/`workbox` + manifest
- [x] `docker-compose.yml`: postgres:16, cognito-local (jagregory/cognito-local), localstack (hobby,
      SERVICES=s3,lambda,dynamodb,sqs,sns,secretsmanager,iam), moto-server (EventBridge supplement)
      (ws-stub deferred to Phase 7 once there's a notifier to stand behind it)
- [x] Makefile (`make dev-server`, `make dev-web`, `make test`, `make migrate`, `make compose-up/down`, `make build`)
- [x] `.env.example`, root `README.md` (how to run everything locally), `.gitignore`
- [x] Terraform skeleton (`infra/terraform/envs/local`), `terraform init` + `terraform validate` both pass
      clean (AWS provider v5.100.0, placeholder local creds, no resources yet)

## Phase 1 — Auth ✅ 2026-09-07
- [x] Cognito user pool config against `cognito-local` (`scripts/seed-cognito-local.sh`,
      idempotent — verified by running it twice); pinned image `jagregory/cognito-local:4.0.0`
      for the `.well-known/{openid-configuration,jwks.json}` endpoints (added in upstream #348);
      `CODE=123456` fixes the signup confirmation code for local dev/test
- [x] Amplify Auth wired in React (`web/src/amplifyConfig.ts`, `web/src/auth/AuthPanel.tsx`) —
      register/confirm/login/logout; `authFlowType: USER_PASSWORD_AUTH` since cognito-local
      doesn't support SRP; config keys verified against the installed package's own type defs,
      not memory
- [x] chi middleware: JWT verification (`internal/auth`) — fetches JWKS, verifies RS256 sig,
      checks issuer/audience/`token_use=id`; 5 unit tests (valid/missing/expired/wrong-aud/
      wrong-token_use), all passing
- [x] "auth gates sync, not capture" — verifier only wraps the `/api` route group in
      `httpapi.NewRouter`; no capture routes exist yet (Phase 2/3) but the structural gate is in place
- [x] CORS added (`github.com/go-chi/cors`) for the Vite origin — not in the original phase
      list, but required for the browser (curl doesn't enforce it) — verified via preflight +
      cross-origin request
- [x] **Full browser E2E verified** with Playwright (headless Chromium): register → confirm
      (code 123456) → login → `Call /api/whoami` → real 200 response with correct sub/email,
      zero console errors. Screenshots confirmed the UI rendered correctly at each step.
- [ ] `created_by` stamped from JWT `sub` on every write — deferred to Phase 2 (no write routes exist yet)

## Phase 2 — Sync / API + DB ✅ 2026-09-07
- [x] Postgres DDL + `golang-migrate` migrations: `disaster_locations`, `site_assessments`, `photos`
      (client-generated GUID PKs, `created_at`/`updated_at`/`created_by` throughout)
- [x] sqlc queries + generated code (`internal/db`, `Querier` interface for mocking); pgx pool in `cmd/server`
- [x] `internal/sync` service: **atomic single-statement upserts** (`INSERT ... ON CONFLICT ... WHERE
      newer`) instead of app-level lock-then-check — race-free idempotent GUID upsert, `updated_at` LWW,
      and the ≤10-photos-per-site rule all resolved in one round trip per record; manually verified against
      live Postgres via psql before writing any Go code around it
- [x] `POST /api/sync`, `GET /api/locations/{id}`, `GET /api/sites/{id}` routes + error→HTTP mapping
      (400 malformed/invalid id, 404 not found, 500 infra failure); camelCase JSON end-to-end via sqlc's
      `json_tags_case_style: camel`
- [x] Dexie schema (`web/src/db/schema.ts`) + offline outbox + sync engine (`web/src/sync/syncEngine.ts`,
      drain + exponential-backoff auto-retry, resets on `online` event) in the PWA
- [x] gomock test suite (8 tests): applied / ignored-stale / **conflict vs. idempotent-replay**
      (equal timestamp + identical content = applied, not conflict — a real gap I caught before writing
      tests) / ≤10-rule (`limit_exceeded`, distinguished from stale via a not-found `GetPhoto` check) /
      infra-error-aborts-batch
- [x] Real-Postgres integration test (`-tags=integration`) exercising the actual SQL end-to-end, not just
      mocked control flow — full create → stale-resend → fill-to-cap → 11th-rejected → count-stays-10 flow
- [x] **Full browser E2E verified** with Playwright: capture location+site fully offline/unauthenticated,
      confirmed sync is refused pre-login ("auth gates sync, not capture" demonstrated live, not just
      structural), then login → Sync now → both records applied, outbox drains to 0, zero console errors

## Phase 3 — Photos / S3 ✅ 2026-09-07
- [x] Presigned PUT flow: `POST /api/sites/{id}/photos/presign` (`internal/storage`, AWS SDK v2,
      custom endpoint + path-style addressing) + `.../{photoId}/confirm` (HEADs the object to verify
      the PUT actually landed before committing — verified live: confirming without an upload
      correctly 409s) — confirm reuses `sync.Service.applyPhoto`, not a second upsert implementation
- [x] **Emulator swap (real finding, not planned):** LocalStack's image refuses to start at all
      without a `LOCALSTACK_AUTH_TOKEN` — confirmed directly via container logs ("License activation
      failed"), contradicting what its free-tier docs implied. Swapped to **MinIO** for S3 — zero
      Go-code changes needed since the client already used a configurable endpoint. Phase 7's
      LocalStack-dependent services (SQS/DynamoDB/EventBridge-adjacent/Lambda) need a fresh look now.
- [x] Client direct-to-S3 upload + geotag metadata capture + canvas watermark (`web/src/capture/
      watermark.ts`: `createImageBitmap` → draw → `strokeText`/`fillText` geotag+timestamp label →
      `canvas.toBlob`); pending bytes held in a `photoBlobs` Dexie table, separate from photo metadata
- [x] Object key + geotag persisted in Postgres via the existing atomic `UpsertPhoto`
- [x] **Full browser E2E verified**: captured a location+site+photo fully offline (file input →
      watermark → Dexie), then after login, Sync now drove presign → real PUT → confirm for the
      photo alongside the batched location/site sync — all 3 applied, outbox drained to 0. Cross-checked
      independently: `aws s3 ls` against MinIO showed the object, downloaded it, confirmed a valid
      4x4 JPEG (not just "a file exists") — the canvas pipeline genuinely produced real image bytes.

## Phase 4 — Dashboard ✅ 2026-09-07
- [x] Per-location condition breakdown, Good/Moderate/Bad — read path only, no aggregation table yet.
      **Deviation from the plan doc's "(pie)":** the dataviz skill's form-selection table has no pie
      row at all and explicitly routes part-to-whole to a **stacked bar**; implemented as a horizontal
      stacked bar with the reserved status palette (good/warning/critical hexes) + a always-present
      text legend (counts), never color-alone — a pie of an ordered 3-tier severity scale was the
      plan's shorthand for "show the proportions," not a literal chart-type requirement
- [x] `dexie-react-hooks` `useLiveQuery` wiring (`web/src/dashboard/LocationDashboard.tsx`) — reactive
      to both local captures and pulled server data, verified live in-browser with zero manual refresh
- [x] `web/src/sync/pull.ts` — GET `/api/locations/{id}` + per-site GET `/api/sites/{id}`, written into
      local Dexie via `put`/`bulkPut` ("pull-on-login" per the plan doc, so the dashboard reflects other
      devices' synced data, not just this device's own captures)
- [x] **Full browser E2E verified**: captured one photo per condition fully offline, dashboard showed
      correct 1/1/1 breakdown *before* any sync (proving the offline-first read path), then after
      login+sync+pull the same breakdown held (proving pull doesn't double-count or clobber local
      state), zero console errors throughout

## Phase 5 — IaC / local deploy loop ✅ 2026-09-07
- [x] Terraform modules written for every resource named in the plan doc:
      `network` (VPC, 2-AZ public/private subnets, single NAT, security groups), `database` (Aurora
      Serverless v2 Postgres + RDS Proxy via Secrets Manager auth), `auth` (Cognito user pool + app
      client mirroring cognito-local's config), `storage` (S3×2 + CloudFront w/ Origin Access Control,
      SPA 403/404→index.html routing), `compute` (Lambda + least-priv IAM — only the one secret, only
      the photos bucket's objects), `api` (API Gateway HTTP API, JWT authorizer on `/api/*`, `/health`
      open), `secrets` (Secrets Manager, Terraform-generated password, never in state as a variable).
      All wired together in `envs/local`; `make tf-validate` passes clean (`terraform init` + `validate`,
      never `plan`/`apply` — that needs real credentials this config deliberately doesn't provide)
- [x] `cmd/lambda`: `aws-lambda-go-api-proxy`'s `httpadapter.NewV2` wraps the identical
      `httpapi.NewRouter` cmd/server uses — cross-compiles clean for `provided.al2023`/arm64
      (`make build-lambda`), matching the locked architecture decision
- [x] GitHub Actions (`.github/workflows/fieldsync-ci.yml`, repo root — scoped to `offline-sync-app/**`
      via path filters since this is a subdirectory of a larger monorepo): Go build/vet/test +
      Lambda cross-compile check, web build, and a `terraform validate`-only job (placeholder zero-byte
      zip so the lambda-artifact reference resolves) — no `plan`/`apply`/deploy step anywhere
- [x] **Real fix found along the way:** `internal/storage.New` and `internal/config` both hardcoded
      local-dev defaults (MinIO endpoint, static credentials, cognito-local issuer) that would have
      been silently wrong in prod. Fixed to use real S3's default endpoint + the Lambda execution
      role's credentials via the SDK's default chain when unset, and a single `COGNITO_ISSUER` env var
      Terraform sets directly from the real user pool's endpoint (local dev still assembles its own
      from `COGNITO_ENDPOINT`+`COGNITO_USER_POOL_ID`, which don't apply in prod). Also found `make
      dev-server` never actually loaded `.env`/`.env.local` despite the README implying it did — fixed.

## Phase 6 — Hardening ✅ 2026-09-07
- [x] Least-priv IAM per Lambda (`modules/compute`): only `secretsmanager:GetSecretValue` on the one DB
      secret, only `s3:PutObject`/`s3:GetObject` on the photos bucket's objects — no wildcard resources
- [x] **Real architecture gap found and fixed:** API Gateway **HTTP API** (v2) has no direct WAF
      association at all (verified — only REST API v1, ALB, AppSync, Cognito, CloudFront do). Rather
      than give up the plan doc's locked HTTP-API decision, added a CloudFront distribution fronting
      the API purely as a WAF attachment point (`modules/api`), with caching fully disabled and
      Authorization header + query strings forwarded via AWS managed policies (verified their exact
      UUIDs against AWS docs rather than trusting memory). New `modules/waf`: one shared WAFv2 WebACL
      (AWSManagedRulesCommonRuleSet/KnownBadInputs/SQLi), CLOUDFRONT-scope, which AWS requires creating
      via a provider pinned to `us-east-1` regardless of primary region — added that provider alias
      properly rather than relying on primary-region coincidence. Attached to all three CloudFront
      distributions (web, photos, api-front)
- [x] CORS: native `cors_configuration` on the HTTP API (prod) — API Gateway can answer preflight
      OPTIONS without invoking Lambda at all; local dev keeps the chi `cors` middleware since there's
      no API Gateway locally. Secrets Manager wiring already landed in Phase 5 (`modules/secrets` +
      `modules/database`'s proxy auth + `modules/compute`'s IAM policy)
- [x] Load-tested `POST /api/sync` locally (`scripts/loadtest`, real Postgres+Cognito-local, zero mocks):
      **~4,800 req/s plateau, p50 2.6ms/p95 5.5ms at c=10 rising to p50 38.8ms/p95 60.8ms at c=200,
      zero errors at any concurrency tested (10/50/100/200)**. Throughput plateau and linear latency
      growth under rising concurrency both point at connection-pool queuing, not the atomic-upsert SQL
      itself, as the bottleneck. Caveat documented: this is a single local Postgres + single Go process
      on dev hardware — informative for relative regression-testing, not a production capacity claim
      (real Lambda cold starts + RDS Proxy + Aurora Serverless v2 scaling have different characteristics)

## Phase 7 — Event-driven fan-out (additive, not a launch blocker) ✅ 2026-09-08
- [x] `domain_events` table (outbox + audit) — `TxRunner` (`internal/sync/transactional.go`) wraps every
      `/api/sync` and photo-confirm write in one real pgx transaction: the atomic upsert, then (only for
      records that actually `Applied`) an `events.Publish` call, then commit. Verified against real
      Postgres: an ignored-stale write produces **zero** domain_events rows; an applied one produces
      exactly one — not inferred, queried directly
- [x] `internal/events.Publish`; `schema_version` (constant `1`) stamped on every event via the shared
      `outbox.Envelope` — both the relay and every consumer/Lambda decode through the same struct
- [x] `outbox-relay` — polls `published_at IS NULL`, `PutEvents` to EventBridge, marks published only on
      confirmed success (checks `FailedEntryCount`, not just the absence of a Go error). **Verified live
      end-to-end**: real sync write → domain_events row appears unpublished → relay tick → Moto
      confirms `PutEvents` → row flips to published, cross-checked with a direct `SELECT`. Local dev runs
      it as a ticker loop (`cmd/outbox-relay`); prod (`cmd/outbox-relay-lambda`) is invoked every minute
      by an `aws_scheduler_schedule`, matching the plan doc's own "a scheduled outbox-relay Lambda"
- [x] `photo-processor` — geotag range validation (real S3 ObjectCreated wiring would need an S3→EventBridge
      bridge with no local emulator path; substituted "photo.upserted" domain events, documented as a
      pragmatic local/architectural substitution). Thumbnail generation explicitly out of scope (would need
      an image library + a second S3 write path, disproportionate to this phase)
- [x] `dashboard-aggregator` — recomputes `location_stats` (good/moderate/bad counts) per location on
      every `photo.upserted`. **Verified live**: confirmed a real "bad"-condition photo, ran the consumer,
      `location_stats` showed `bad_count=1` — matched, not assumed
- [x] `notifier` + `ws_connections` (Postgres, both local and prod — see the real-architecture-gap note
      below) + WebSocket push. **Verified live, fully end-to-end**: a real Go WebSocket client connected
      to `cmd/server`'s `/ws` route (JWT-in-query-string, matching how a real API GW WebSocket authorizer
      is wired), a photo was confirmed via the real presign/PUT/confirm flow, and the client actually
      received `{"type":"photo.upserted","photoId":"...","condition":"bad"}` within one 5s notifier tick
      — not simulated, an actual socket receiving an actual message
- [x] `processed_events` dedup table — verified after the live run above: all three consumers
      (`photo-processor`, `dashboard-aggregator`, `notifier`) each recorded the *same* `event_id` under
      their own `consumer_name`, confirming per-consumer (not global) idempotency. Unit tests per
      consumer + `internal/sqsconsume` (4 tests: new message, skip-already-processed, one-bad-message-
      doesn't-fail-the-batch, malformed-body) all pass
- [x] DLQ on every consumer queue — `modules/eventing`: 3 SQS queues + 3 DLQs (`maxReceiveCount=5`,
      14-day DLQ retention), EventBridge rules routing `photo.upserted` to each; Lambda event source
      mappings use `function_response_types=["ReportBatchItemFailures"]` so only genuinely failed
      messages redeliver, not the whole batch (unit-tested in `internal/sqsconsume`, standing in for an
      "alarm-equivalent check" — a real CloudWatch alarm on DLQ depth is a Terraform addition, not
      something a local test can exercise)
- [x] **Real architecture gaps found and resolved, not glossed over:**
  - Confirmed `moto-server` genuinely supports EventBridge (`create-event-bus`/`put-events`/
    `list-event-buses` all tested live) before writing any Go around it — unlike LocalStack, this one
    held up
  - Port 5000 (moto-server's default) collides with macOS's AirPlay Receiver — remapped to 5001
  - The plan's original "DynamoDB for `ws_connections`" was **deliberately dropped**: this app's actual
    connection-churn volume (a handful of field workers per disaster location) never approaches what
    would justify DynamoDB's added complexity — Postgres serves both local dev and the real
    `cmd/ws-connect-lambda`/`cmd/notifier-lambda` prod path identically, one less moving part
  - Built the full real-AWS path alongside the local-dev path, not instead of it: `internal/sqsconsume`
    (SQS→Lambda, partial-batch-failure reporting) + 4 `cmd/*-lambda` entrypoints + `cmd/ws-connect-lambda`
    + `modules/websocket` (real API Gateway WebSocket API) + `modules/eventing` (bus, queues, DLQs,
    rules, scheduler) — all cross-compile clean for `provided.al2023`/arm64 and `terraform validate`
    passes across all 10 modules now wired into `envs/local`

---

## Notes / deviations
_(append dated notes here when a phase is skipped, reordered, or a decision changes)_
