# Plan — Deliver `Offline-Sync-Go-AWS-Plan.md` (PRD + Architecture for a Go + AWS port)

## Context
The user wants to rebuild the FieldSync offline-first flood-damage app on a **Go + AWS serverless** stack with a
**React (Vite) PWA**, in a **new project**. Deliverable this round = **one self-contained Markdown document**
(PRD + full architecture + implementation plan + starter snippets) they can drop into a fresh repo and start.
No code is built here; the doc is the artifact. It ports the existing app's proven model (offline outbox,
idempotent GUID upsert, LWW, geotagged photos, ≤10 rule, per-location dashboard, auth-gates-sync-not-capture).

## Locked decisions
- **Compute:** AWS Lambda (Go, `provided.al2023`, arm64) behind **API Gateway HTTP API**. Single "Lambda-lith"
  running a **chi** router via `aws-lambda-go-api-proxy` → same handler runs locally as a plain HTTP server
  (dev/prod parity). Per-route Lambdas noted as alternative.
- **Database:** **Aurora Serverless v2 (PostgreSQL)**; access via **pgx + sqlc** (type-safe). Migrations via
  **golang-migrate**. **RDS Proxy** for Lambda connection pooling; Aurora in private subnets.
- **Auth (industry standard):** **Amazon Cognito user pool** + **API Gateway JWT authorizer**. Client uses
  **AWS Amplify Auth** (register/login/refresh, hosted or custom UI); Lambda reads `sub`/`email` from the
  authorizer claims — no password code owned. Offline-tolerant (cached tokens, refresh on reconnect).
- **Photos:** **S3** via **presigned PUT URLs** (client uploads directly, dodging Lambda payload limits);
  private bucket + **CloudFront (OAC)** for serving; geotag metadata + object key in Postgres.
- **Frontend:** **React + Vite + TypeScript PWA** — Dexie + `dexie-react-hooks` (≈ Angular liveQuery), a sync
  store (Zustand), `vite-plugin-pwa`/Workbox service worker, canvas watermarking, Amplify Auth. Hosted on
  **S3 + CloudFront**. (Next.js explicitly rejected — no SSR/SEO value for an auth-gated offline field tool.)
- **IaC:** **Terraform** (VPC, Aurora, RDS Proxy, Cognito, API Gateway, Lambda, S3×2, CloudFront, IAM, Secrets).
- **Testing:** Go `testing` + `testify` + **gomock** (mirrors the Moq suite: sync applied/ignored-stale/
  conflict, idempotency, ≤10 rule). Optional `dockertest` for repo integration. Frontend: manual/e2e.
- **Events:** **Amazon EventBridge** custom bus (`ceres-events`) as the async backbone. Server-side
  **transactional outbox**: the sync-service writes domain rows + a `domain_events` row in the *same*
  Postgres transaction, then dual-writes to EventBridge; a scheduled **outbox-relay** Lambda re-scans
  `domain_events` for `published_at IS NULL` as the safety net (no CDC/Debezium at this scale). This table
  doubles as the audit log. The client-facing `POST /api/sync` response stays **synchronous** — events
  describe what happened *after* commit, they never gate the offline-outbox ack.
- **Async consumers:** EventBridge rules → **SQS (+DLQ)** → single-purpose Lambdas: `photo-processor`
  (S3 upload event → thumbnail/geotag validation/moderation), `dashboard-aggregator` (recompute
  per-location pie stats), `notifier` (push to connected clients). SQS buffers bulk-sync bursts and gives
  retry/backoff without hammering Aurora. Every consumer is **idempotent**, checked against a
  `processed_events` dedup table (EventBridge/SQS is at-least-once).
- **Real-time push:** API Gateway **WebSocket API** (Cognito JWT via query param on `$connect`, since the
  WS handshake can't carry an Authorization header from a browser) + a DynamoDB `ws_connections` table
  (connectionId ↔ userId/locationId). `notifier` looks up subscribed connections and posts via the API
  Gateway Management API — other field workers/dashboard viewers see updates without waiting for
  pull-on-login.

## Target architecture (serverless)
```
React PWA (S3+CloudFront) ──HTTPS──> API Gateway (HTTP API + Cognito JWT authorizer)
   IndexedDB outbox                         │ invoke
   sync engine ─ presign ─> S3 (photos) <───┤  Go Lambda-lith (chi via lambda-go-api-proxy)
   Amplify Auth ─> Cognito                  │  → RDS Proxy → Aurora Serverless v2 (Postgres)
   WebSocket client <──push─── notifier <── EventBridge (ceres-events) <── outbox-relay ← domain_events
        ▲                         ▲              │  rules → SQS(+DLQ) →
        │ API GW WebSocket        │              ├─> photo-processor ← S3 ObjectCreated event
        └── ws_connections (DDB) ─┘              └─> dashboard-aggregator
```
Design invariants carried over: client-GUID PK → idempotent upsert; `updated_at` → Last-Write-Wins;
outbox + retry/backoff; auth gates **sync**, not **capture**; server stamps `created_by` from JWT `sub`.
New invariant: the sync ack path is **never** blocked on the event bus — events are a side effect of a
committed write, not a dependency of the write itself.

## Deliverable: `FieldSync-Go-AWS-Plan.md` (repo root) — section outline
1. **Overview & goals** — what we're porting and why serverless.
2. **PRD** — problem, users/roles, user stories, functional + non-functional requirements, offline constraint,
   acceptance criteria, out-of-scope.
3. **Domain model** — DisasterLocation → Farm(SiteAssessment) → Photo (+ condition Good/Moderate/Bad),
   Postgres DDL, GUID PKs, `updated_at`, `created_by`; plus `domain_events` (outbox/audit),
   `processed_events` (consumer dedup), and DynamoDB `ws_connections`.
4. **Architecture** — the diagram above + component responsibilities; VPC/networking; RDS Proxy rationale;
   presigned-upload flow; CloudFront/OAC; EventBridge bus + SQS consumers + WebSocket push; sequence
   diagrams (sync, photo upload, login, **async photo processing**, **sync fan-out**, **real-time push**).
5. **API contract** — routes (`POST /api/sync`, GET locations/{id}, GET sites/{id},
   `POST /api/sites/{id}/photos/presign` + confirm, `GET /health`), request/response JSON, status codes;
   WebSocket routes (`$connect` w/ JWT query param, `$disconnect`, `subscribe`). Note: the sync response
   contract is unchanged by the event layer — events are a side effect, not part of the response.
6. **Auth** — Cognito user pool + app client, API Gateway JWT authorizer, Amplify Auth on the client, the
   offline token lifecycle, "auth gates sync not capture", attribution via `sub`; WebSocket `$connect`
   authorizer (JWT-in-query-string) and reconnect-on-expiry behavior.
7. **Offline-first frontend** — Dexie schema, sync engine (drain/retry/LWW/idempotent, pull-on-login),
   service worker, photo watermark, per-location dashboard (pie), guard (offline allows capture).
8. **Go backend** — repo layout, chi-lith + lambda adapter, sync service + interfaces, sqlc/pgx, migrations,
   the ≤10 rule, error→HTTP mapping; `internal/events` (typed events, `schema_version`, `Publish` helper
   called from every write path — DRY across the lith and all consumers); thin `main.go` entrypoints for
   `photo-processor`, `dashboard-aggregator`, `notifier`, `outbox-relay`.
9. **Infrastructure (Terraform)** — module list, key resources, secrets, environments; EventBridge bus +
   rules, SQS queues + DLQs (visibility timeout, maxReceiveCount), 4 new Lambdas w/ **per-function
   least-priv IAM** (e.g. `photo-processor` gets S3:GetObject + narrow RDS write, nothing else), WebSocket
   API + routes, DynamoDB `ws_connections` (on-demand, TTL), CloudWatch alarm on DLQ depth > 0.
10. **Local development** — docker-compose (Postgres + LocalStack S3 + cognito-local), run chi server, Makefile.
11. **CI/CD** — GitHub Actions (test Go, build web, `terraform apply`, deploy Lambda, S3 sync + CF invalidate).
12. **Testing strategy** — gomock suites mirroring the Moq tests; what to assert; plus **idempotency
     tests** (replay same event twice → single side effect via dedup table), an **outbox-relay test**
     (simulate a failed publish, assert the relay retries the unpublished `domain_events` row), and a
     WebSocket connect→subscribe→push integration/manual test.
13. **Security** — least-priv IAM, private S3+OAC, RDS Proxy + Secrets Manager, WAF, CORS; per-Lambda
     least-priv for the event consumers (no shared broad execution role), event payloads carry only what
     each consumer needs (e.g. `photo.uploaded` carries the S3 key, never a presigned URL), WebSocket
     token-expiry-mid-session handling.
14. **Cost notes** — Aurora Serverless v2 min-ACU floor (flag it), Lambda/API GW/S3/CloudFront, Cognito free
     tier; a cheaper variant (RDS t4g.micro or DynamoDB) called out. EventBridge/SQS are cheap at this
     volume; **flag WebSocket API Gateway's per-connection-minute + per-message pricing** — idle
     all-day field-worker connections add up, so the PWA should disconnect on background/idle.
15. **.NET/Angular → Go/AWS mapping table** — each existing piece → its new equivalent (e.g., `SyncService.cs`
     → `internal/sync`, EF Core → sqlc, `[Authorize]` → Cognito authorizer, `wwwroot/uploads` → S3 presigned).
16. **Phased roadmap** — Phase 0 scaffolding → 1 auth → 2 sync/API+DB → 3 photos/S3 → 4 dashboard →
     5 IaC/deploy → 6 hardening → **7 event-driven fan-out** (EventBridge bus, outbox+relay,
     `photo-processor`, `dashboard-aggregator`, `notifier`, WebSocket API); each concept-ready with a
     milestone. Phase 7 is explicitly **additive, not a launch blocker** — ship the synchronous MVP first.
17. **Starter snippets** — chi-lith `main.go` + lambda entry, a sqlc query + Go sync upsert, a Terraform
     skeleton (API GW + Lambda + Cognito authorizer), the React sync store + Dexie schema, `POST /sync` JSON.

## Verification (of the deliverable)
- The `.md` is self-contained: a reader can scaffold the repo layout, run `docker-compose` for local dev, and
  follow the phased roadmap without referring back to the .NET repo.
- Every functional requirement in the PRD maps to an API route + a frontend behavior + a Terraform resource.
- The mapping table covers every major .NET/Angular component.
- Offline invariants (idempotency, LWW, auth-gates-sync) are stated with the exact mechanism in Go/AWS terms.
- Every domain event has a producer (a write path via `internal/events.Publish`) and at least one
  consumer; every consumer is idempotent and has a DLQ; the sync ack path is confirmed to stay
  synchronous despite the async layer (no route awaits an EventBridge round-trip).

## Post-approval
On approval I'll write `FieldSync-Go-AWS-Plan.md` at the repo root with all sections above, including the starter
code snippets and mermaid/ascii diagrams, ready to copy into a new project.
