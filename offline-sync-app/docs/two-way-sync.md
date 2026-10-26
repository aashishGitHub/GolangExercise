# Two-way sync: inbound webhooks + SaaS <-> SaaS

Packages: `internal/webhook` (receive), `internal/twoway` (map, merge, pull, push), `internal/mockjira` (test double), `cmd/webhook-receiver`, `cmd/webhook-receiver-lambda`, `cmd/mockjira`. Terraform: `infra/terraform/modules/webhook`. Migration `000003_two_way_sync`. Internal entity: `work_items` (title, status, notes, `deleted_at`).

## Architecture

Control flow (who calls whom, and when):

```
 external SaaS (mockjira)                 FieldSync
 ------------------------                 ------------------------------------------------------
 user edits issue
   |  version++ , enqueue event
   |
   +--POST /webhooks/{system}-----------> webhook.Handler            (sync, must answer fast)
        X-Webhook-Delivery                  1. MaxBytesReader        -> 413 if too big
        X-Webhook-Timestamp                 2. read RAW bytes
        X-Webhook-Signature                 3. Verify HMAC(ts "." id "." raw), hmac.Equal, +-5 min
                                               -> 401 bad_signature / stale_timestamp
                                            4. INSERT webhook_inbox ON CONFLICT DO NOTHING
                                            5. 202 accepted | 202 duplicate
                                                |
                                                v   (async, ticker; prod: queue-triggered)
                                          webhook.Worker.RunOnce
                                            per row, ONE tx:
                                              ClaimInboxRow FOR UPDATE SKIP LOCKED
                                              twoway.Applier.ProcessWebhook -> Apply
                                              MarkInboxProcessed(outcome)
                                            on error: attempts++ ; at N -> sync_dead_letters

 poll (completeness, repairs lost webhooks)
 twoway.Extractor.Run  (ticker / scheduled, time budget)
   GET /issues?updated_since=&cursor=  --> per page, ONE tx: Apply* + UpsertSyncState

 twoway.Loader  (consume.RunOnce over domain_events 'work_item.updated')
   skip if actor == "system:<sys>"       (origin filter)
   PATCH /issues/{id}  Idempotency-Key = domain event id   (retry: 429 Retry-After, 5xx jittered backoff)
   UpsertSyncPushed(version, hash of response)            (echo memory)
   permanent / exhausted -> sync_dead_letters
```

Data flow (what is written where):

```
 webhook body --raw--> webhook_inbox(system, delivery_id PK, body, attempts, outcome, dead_at)
                           |
 poll page ----------------+--> Apply(issue snapshot)
                                  1. external_id_map: (system, external_id) -> internal_id   (insert, then select)
                                  2. echo?  sync_pushed(version, hash) == incoming          -> outcome echo   (webhook only)
                                  3. version gate: UPDATE external_id_map SET last_version
                                       WHERE last_version < incoming                        -> else ignored_stale
                                  4. work_items FOR UPDATE, Ownership.Merge(current, incoming)
                                       external-owned fields <- incoming, internal-owned kept
                                       internal-owned field differed                        -> outcome conflict
                                  5. UpsertWorkItem (deleted -> deleted_at, never DELETE)
                                  6. domain_events 'work_item.updated|deleted'  actor system:<sys>  (only if content/deleted changed)

 EditNotes (internal) --tx--> work_items.notes + domain_events actor=<user>
                                  --> Loader --> external PATCH --> sync_pushed
```

## Why each choice

| Choice | Reason |
|---|---|
| HMAC over `timestamp + "." + deliveryId + "." + rawBody` | The MAC covers exact bytes. Parsing and re-marshalling JSON changes whitespace/key order and breaks it; a test signs a body with odd spacing to prove raw bytes are used. Signing the timestamp makes a captured request useless outside the window. Signing the delivery id: see below. |
| Delivery id may not contain `.` (401 `malformed_headers`) | The timestamp is digits only and the id has no `.`, so the first two dots split the signed string unambiguously. Without the rule, a captured request whose body contains a `.` could be resent with `id + "." + bodyPrefix` as the id and the rest as the body, under the same MAC. |
| `hmac.Equal` on decoded bytes | Constant-time compare; plain `==` on hex strings leaks prefix length through timing. |
| Verify before touching the DB | Unauthenticated traffic never costs a write or a row. |
| 202 fast + inbox | The sender only needs "stored". Processing time, DB blips and poison bodies never become sender-side retries. |
| Inbox PK = delivery id, one row = dedupe + raw event | A single `INSERT ... ON CONFLICT DO NOTHING` is atomic, so "seen" and "stored" cannot disagree. (Deviation from "two writes in one tx": one row, one statement.) |
| Duplicate -> 202, not 4xx | The sender must stop retrying; the first copy is stored. |
| Worker tx per row, `SKIP LOCKED` | Apply + event + processed-mark commit together (crash = nothing happened); two workers never take the same row. |
| Version gate in SQL (`WHERE last_version < $v`) | Ordering decided by the external system's monotonic `version`, atomically. Client clocks (`updatedAt`) are never used to resolve conflicts. |
| `last_version` on the link row, not on `work_items` | Version is a property of one external system's copy; a second system gets its own link. |
| Per-field ownership | Each field has exactly one writer, so most conflicts cannot exist. Unlisted fields default to internal-owned: a config typo can't let the SaaS overwrite local data. |
| Loader pushes only internal-owned fields | It can't clobber a status someone changed in the SaaS (no lost update on their fields). |
| Echo memory (version + hash of the PATCH *response*) | Hash is over what the server stored, not what we sent, so server-side normalisation still matches. Exact version match, so a later real edit is never swallowed. |
| Echo check is webhook-only | A poll returns full current state; it must be able to repair something a dropped webhook left behind. |
| Origin filter (`actor_sub = system:<sys>`) | An external change must not be reflected back as a PATCH. |
| Event id as `Idempotency-Key` | A push that succeeded but whose response was lost is retried with the same key; the server replays the first answer instead of writing twice. |
| Full jitter, honour `Retry-After` | `sleep = rand(0, min(cap, base * 2^n))` stops synchronised retry herds; if the server says when, we wait exactly that. |
| Page = one tx with its checkpoint | Crash replays at most one page; replay is a no-op through the version gate. |
| Budget check *before* a page, reserving the slowest page so far | Never start work you can't finish inside the cap; exit cleanly, resume from the cursor. |
| Overlap window on `updated_since` | Timestamp granularity and late commits would otherwise skip rows; re-reads are free because of idempotency. |
| Tombstones | A row missing from a poll is not evidence of deletion (an `updated_since` poll only returns changes). Delete = flag at a higher version; the row stays. A delete that arrives before its create still wins by version. |
| `sync_dead_letters` after N attempts | No infinite retry; payload kept as raw bytes (a poison body may not be valid JSON), with reason and attempt count. |

## Why the delivery id must be signed

The inbox dedupes on `(system, delivery_id)` with `ON CONFLICT DO NOTHING`, so the id decides whether a body is stored. When only `timestamp.body` was signed, anyone holding one captured request could resend it inside the 5 minute window under any id they chose:

- **Squatting a real delivery (the serious one).** mockjira's sequential ids (`dlv-42`) are guessable; random ids make this harder, not impossible to try. The attacker posts the captured body as `dlv-43` before the real `dlv-43` arrives. The real one then hits the conflict, gets `202 duplicate`, and the sender stops retrying. The genuine event is silently lost until the next poll repairs it.
- **Bypassing dedupe.** The same body under N fresh ids makes N inbox rows. The version gate makes the re-applies no-ops, but each costs a row and a worker pass.

With the id inside the MAC, a different id is a different MAC: `401 bad_signature`, nothing stored, and the real delivery is accepted with its own body (`TestHandler_DeliveryIDSwappedAfterSigningIs401SoTheRealDeliveryIsNotSwallowed`). An exact replay of the same (timestamp, id, body) inside the window is still accepted and is a harmless `202 duplicate`.

Other schemes do the same (from memory, verify before quoting): the Standard Webhooks spec (used by Svix) signs `id.timestamp.body`.

## Deploy as a Lambda (deploy-ready, not deployed)

```
 vendor --POST /webhooks/mockjira--> HTTP API (modules/api, same API as /api/*)
                                       route has no JWT authorizer: the vendor has no Cognito token
                                       |
                                       v  AWS_PROXY, payload 2.0
                                     Lambda fieldsync-local-webhook-receiver (VPC, private subnets)
                                       httpadapter.NewV2 -> webhook.NewRouter -> webhook.NewHandler
                                       |
                                       v
                                     RDS Proxy -> Aurora: webhook_inbox
```

- Same handler in both runtimes: `webhook.NewRouter` is mounted by `cmd/webhook-receiver` and `cmd/webhook-receiver-lambda`. The Lambda is `cmd/lambda`'s pattern (aws-lambda-go-api-proxy `httpadapter.NewV2`), no new dependency.
- Setup runs in `main`, not `init` as in the other `*-lambda` entrypoints. `init` also runs under `go test` and would die without `WEBHOOK_SECRET` and a DB; `main` runs once per execution environment either way.
- `system` is one Terraform variable that sets both the route path and `EXTERNAL_SYSTEM`, so they cannot drift (a mismatch would be a 404 from the router).
- IAM: only `AWSLambdaVPCAccessExecutionRole` (logs + ENIs). No inline policy: the receiver calls no AWS API. The invoke permission is scoped to `.../*/*/webhooks/mockjira`, not `/*/*`.
- Secret: `random_password` in `modules/secrets`, stored in Secrets Manager `<prefix>/webhook-secret` (where an operator copies it into the vendor's webhook settings) and passed as `WEBHOOK_SECRET`, the same path the DB password already takes into `DATABASE_URL`. Trade-off: anyone with `lambda:GetFunctionConfiguration` can read env vars. The stronger option is `GetSecretValue` at cold start plus that one IAM grant; not built, to keep one pattern.

Verified: `go test ./cmd/webhook-receiver-lambda` (synthetic proxy events: valid -> 202, wrong secret -> 401, base64 body -> 202, other path -> 404), `make build-lambda`, `terraform fmt -check -recursive`, `terraform validate`. Not verified: no `plan`/`apply` against an account; the source ARN shape and API Gateway's real header/body encoding are taken from the payload format, not observed.

## Outcomes

Reuses `internal/sync` vocabulary plus one new value.

| Outcome | Meaning |
|---|---|
| `applied` | Newer version merged in. |
| `ignored_stale` | Version not newer than the last applied one (older, duplicate, or overlap re-read). |
| `echo` | Webhook matches what the Loader last pushed (same version and content hash); dropped. |
| `conflict` | Newer version, but it edited an internal-owned field. External-owned fields are still applied; the internal value is kept. |

## Tests (what proves what)

Integration (`go test -tags=integration ./internal/twoway/...`, real Postgres, mockjira over httptest):
bad signature -> 401 and nothing stored; delivery id swapped after signing -> 401 and the real delivery is still accepted (unit, `internal/webhook`); stale timestamp -> 401 even with a valid MAC; duplicate delivery -> one inbox row, one event; out-of-order -> v2 applied, late v1 `ignored_stale`; echo loop terminates (one PATCH, webhook = `echo`, no new event, no pending webhook); echo of an older push after a newer local edit is `echo`, not a false `conflict`; conflict keeps internal notes and applies external status; 429 `Retry-After: 2` -> one 2s wait then the write lands; poison inbox event -> dead letter after exactly 3 attempts, attempts stop growing; loader retries exhausted -> dead letter, event consumed once; crash after applying page 2 but before commit -> checkpoint at page 1, resume starts at the stored cursor, 7 links / 7 events / zero duplicate events; overlap re-read adds no events; tombstone keeps the row and an empty poll deletes nothing; 8 concurrent first sights -> one link, one item, one winner.

Mutation check (done by hand, then restored): removing the signature check, dropping the delivery id from the MAC on both sides (only the swapped-id test and the vector fail), timestamp window, echo detection, version gate, origin filter, `Retry-After`, dead-lettering, checkpoint write, ownership merge, or duplicate detection each makes at least one test fail.

## Layers against an echo loop, honestly

Three independent guards: (1) single-writer field ownership, (2) origin filter on the Loader, (3) echo memory. Any one of them ends the loop in the default config, so a test that only asserts "it terminates" would pass with echo memory removed. What echo memory uniquely buys: an accurate label (`echo`, countable; a rising echo rate means a hot loop upstream) and no false `conflict` when a webhook for an older push of ours lands after a newer local edit. That second case is the test where removing it fails. It matters most if you ever make one field bidirectional.

Known race: if a webhook is processed before the Loader records its push, it is not labelled `echo`; the merge is a no-op, no event is published, and the loop still ends.

## Observability you can query today

```sql
SELECT outcome, count(*) FROM webhook_inbox WHERE system = 'mockjira' GROUP BY outcome;
SELECT count(*), min(received_at) FROM webhook_inbox WHERE processed_at IS NULL AND dead_at IS NULL;   -- backlog and lag
SELECT source, attempts, reason, created_at FROM sync_dead_letters ORDER BY created_at DESC;
SELECT * FROM sync_state;                                                                              -- checkpoint, in-flight cursor
```

Logs carry the delivery id on every rejection and failure.

## What is NOT solved

- **No secret rotation** (single secret, no dual-verify window).
- **Only the receiver has a Lambda.** The inbox worker, extractor and loader still run only inside `cmd/webhook-receiver`; deployed alone, the Lambda would fill `webhook_inbox` and nothing would drain it. No reserved concurrency on it either.
- **Outbound deletes and internal-origin creates.** A work item with no external link is skipped by the Loader (logged). Only inbound deletes exist.
- **No auto-repair after `conflict`.** Internal value is kept; the SaaS still shows theirs until someone pushes.
- **Poll path has no echo memory by design**, so while a push is in flight it can report a label-only `conflict`.
- **Extractor assumes one runner.** Two runners duplicate work and last checkpoint wins. Fix: advisory lock or a single scheduled invocation.
- **Late commits older than the overlap are missed.** Fix: periodic full reconcile (not built).
- **`Retry-After` is unbounded** (the context deadline is the only cap), only the seconds form is parsed, and a sleep inside a page can overrun the extractor budget.
- **Inbox retries have no backoff** (tick interval only), and a poison body burns the same N attempts as a transient DB error.
- **No DLQ replay tooling** (rows are queryable; replay is manual) and no retention job for `webhook_inbox`.
- **No metrics or tracing**, no auth/token refresh to the SaaS, no multi-tenant config, no relationships between entities.
- **`external_id_map` has no `entity_type`** and no FK to `work_items` (entity-agnostic on purpose); add `entity_type` before a second entity.
- **The mock is not Jira.** Header names, payload shape and pagination are invented. Stripe and Slack sign with a similar timestamp-plus-body idea (from memory; verify before quoting).

## AirSync vocabulary

The terms below come from the brief I was given. I have **not verified** them, nor the ~13 minute invocation cap and the state-object details, against developer.devrev.ai. Confirm before quoting them in an interview.

| AirSync term | Here |
|---|---|
| Extractor | `twoway.Extractor`: cursor + `updated_since` pull, time-boxed, resumable |
| State object / checkpoint | `sync_state` row (watermark, in-flight cursor, `run_since`, `run_max`) |
| Loader | `twoway.Loader`: domain events -> idempotent PATCH |
| Object mappers | `external_id_map` (ids) + `ContentOf` / `Ownership` (fields) |
| Event-driven path | `webhook.Handler` -> `webhook_inbox` -> `webhook.Worker` |

## Run it

```
go run ./cmd/mockjira
WEBHOOK_SECRET=dev-webhook-secret go run ./cmd/webhook-receiver
curl -X PATCH localhost:8090/issues/MJ-1 -d '{"status":"Done"}'
curl -X POST 'localhost:8090/_admin/fail?n=2&status=429&retry_after=1'
curl -X POST 'localhost:8090/_admin/flush?mode=reverse'
```

`mode` is `in_order`, `duplicate`, `reverse` or `drop`; delay = leave deliveries queued (run mockjira with `-flush-interval 0`) and flush later.

## Interview Q&A

**1. How do you verify a webhook, and why those details?**
HMAC-SHA256 over `timestamp + "." + deliveryId + "." + raw body`, compared with `hmac.Equal` (constant time), with a +-5 minute window on the timestamp. Raw bytes because the MAC is over exact bytes. The timestamp is inside the MAC so a captured request can't be replayed later. The delivery id is inside it because it is the dedupe key: unsigned, a captured body could be posted under the id of a real upcoming delivery and the real one would be dropped as a duplicate. Body is capped with `MaxBytesReader` before hashing. Verification happens before any DB write. Gap I'd name: no secret rotation.

**2. Why answer 202 and process later?**
The sender needs one fact: "stored". Doing the work inline ties the sender's retry policy to my DB health and my slowest handler, and a poison body would be retried forever by them. The inbox row is dedupe key and raw event at once, so a worker crash loses nothing and I can re-run or dead-letter at my own pace.

**3. Duplicates and out-of-order: how are they handled, and why two mechanisms?**
Duplicate *deliveries* are removed by the inbox primary key (delivery id). Duplicate or reordered *facts* (overlap re-reads, a webhook plus a poll, a late v1 after v2) are removed by the version gate: `UPDATE ... WHERE last_version < incoming` in one statement. The first handles transport retries; the second handles semantics and is the one that makes replay safe.

**4. How do you stop an echo loop, and which guard actually matters?**
Per-field single writer, an origin filter on the Loader, and echo memory (version + hash of the PATCH response). In the default config any one ends the loop. Echo memory is what gives an accurate `echo` label and avoids a false `conflict` when the echo of an older push arrives after a newer local edit. I'd say plainly that a "loop terminates" test alone doesn't prove the memory is needed.

**5. Why per-field ownership and external version instead of last-write-wins on timestamps?**
Client clocks skew and collide, so LWW silently loses edits. Ownership removes most conflicts by construction; for the rest, the external system's monotonic `version` orders its own events. A newer event that edits a field we own is reported as `conflict`: external-owned fields still apply, ours is kept, nothing is silently lost. Cost: no automatic repair of the SaaS copy.

**6. Backfill under a hard invocation cap: how is it crash-safe and resumable?**
Each page and its checkpoint (cursor, `run_since`, running max) commit in one transaction, so a crash replays at most one page, and replay is a no-op via the version gate. Before each page the extractor checks remaining budget against the slowest page so far and exits cleanly if it won't fit; the next run resumes from the cursor. On completion the watermark advances to the max `updatedAt` seen, and the next run starts `overlap` earlier. The ~13 minute figure is unverified.

**7. What do you do on 429, 5xx, or a lost response?**
429: wait exactly `Retry-After`. 5xx or network: full-jitter exponential backoff, capped. Other 4xx: permanent, dead-letter immediately. After N attempts: dead-letter with the reason. Lost response: the PATCH carries the domain event id as `Idempotency-Key`, so the retry replays the original answer (same version) instead of writing twice, and the echo memory then records it.

**8. Deletes, and what happens to a message you can't process?**
Delete is a tombstone (`deleted_at`) applied at a higher version; a missing poll row never deletes anything, and a delete that arrives before its create still wins by version. A message that keeps failing is attempt-counted (inbox row or in-call retries) and, at N, moved to `sync_dead_letters` with raw payload, reason and attempts, so the queue keeps moving. Not built: replay tooling and alerting on dead-letter growth.
