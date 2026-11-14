# ControlPlane demo runbook

Canonical commands and narrative for the 20 Nov live demo. Use this with
[`day-5-demo-rehearsal.md`](day-5-demo-rehearsal.md).

**One-line pitch:** "A tenant provisioning control plane that stays correct when the request commits,
the worker crashes, the broker redelivers, and progress arrives out of order."

**What the demo proves:**

1. The API commits durable intent, then returns — no long-held HTTP request.
2. The task row is the transactional outbox; publish happens only after commit.
3. Multiple relay replicas claim disjoint work with `FOR UPDATE SKIP LOCKED`.
4. The consumer is idempotent (inbox) and order-tolerant (rank guard).
5. Poison messages go to a DLQ and do not block valid work.
6. The guarantees are at-least-once + effectively-once business effect, not fairy-tale exactly-once.

---

## 0. Preflight (do this 30 minutes before)

```powershell
cd C:\PROJECT\aashishGithub\Avid\ControlPlane

docker compose ps
docker compose up -d --build --wait
curl.exe -sS http://localhost:8080/healthz
```

Expected: `{"status":"ok"}`.

```powershell
docker compose logs --tail=40 controlplane worker
```

Check:

- [ ] Postgres and RabbitMQ healthy; controlplane and worker running.
- [ ] `http://localhost:15672` opens (`guest` / `guest`).
- [ ] Queues `worker.tasks`, `cp.task-updates`, `cp.task-updates.dlq` exist.
- [ ] `go test ./... -count=1` passes.
- [ ] Git Bash path works: `& "C:\Program Files\Git\bin\bash.exe" --version`.
- [ ] Backup recording/screenshots open in a second window.
- [ ] Notifications off; terminal font 16–18 pt; history clean.

If you need a clean slate:

```powershell
docker compose down -v
docker compose up -d --build --wait
```

Do this **early**. `down -v` removes the Postgres volume and forces a cold start.

---

## 1. Start, logs, stop

### PowerShell (no `make` dependency)

```powershell
cd C:\PROJECT\aashishGithub\Avid\ControlPlane
docker compose up -d --build --wait
docker compose ps
docker compose logs -f controlplane worker
```

Stop following logs with `Ctrl+C`; the stack keeps running.

```powershell
docker compose down       # stop, keep volume
docker compose down -v    # stop and wipe Postgres data
```

### Git Bash

```bash
cd /c/PROJECT/aashishGithub/Avid/ControlPlane
make up
make logs
make down
```

`make down` wipes the volume. Do not run it during the demo.

---

## 2. Happy path — PowerShell

Copy/paste one block at a time. Keep the same PowerShell session: later blocks reuse `$base`,
`$tenantId`, and `$taskId`.

### Create a tenant

```powershell
$base = "http://localhost:8080"
$slug = "demo-" + (Get-Date -Format "HHmmss")
$body = @{ slug = $slug; name = "Demo Tenant" } | ConvertTo-Json

$resp = Invoke-RestMethod -Method Post -Uri "$base/v1/tenants" `
  -ContentType "application/json" -Body $body

$tenantId = $resp.tenant.id
$taskId   = $resp.task.id

"slug=$slug"
"tenant=$tenantId"
"task=$taskId"
"status=$($resp.tenant.status) version=$($resp.tenant.version)"
$resp | ConvertTo-Json -Depth 6
```

Expect `201`, tenant `provisioning`, version `1`, task `accepted`.

Say: "The tenant and task committed in one transaction. The task has `published_at = NULL`; it is the
outbox row. The response is 201 because the tenant exists now."

### Poll until active

```powershell
for ($i = 1; $i -le 40; $i++) {
    $t = Invoke-RestMethod "$base/v1/tenants/$tenantId"
    "{0:D2} status={1} version={2}" -f $i, $t.status, $t.version
    if ($t.status -eq "active") { break }
    Start-Sleep -Milliseconds 250
}
```

Expect `provisioning → active`; version `1 → 2` because the terminal deploy outcome increments it.

Say: "The worker published `in_progress` and `done`; the consumer applied them in one transaction with
the tenant update. The version bump is the async completion signal."

### PATCH

```powershell
$t = Invoke-RestMethod "$base/v1/tenants/$tenantId"
$patch = @{ name = "Demo Tenant Updated"; version = $t.version } | ConvertTo-Json

$resp = Invoke-RestMethod -Method Patch -Uri "$base/v1/tenants/$tenantId" `
  -ContentType "application/json" -Body $patch

$resp | ConvertTo-Json -Depth 6
$resp.tenant.status
```

Expect `202`, status `updating`, version incremented. Poll again until `active`; version increments
again on the terminal update outcome.

Say: "202 because this only starts async work. The body already says `updating`, so the client is not
guessing. The conditional UPDATE matched the version and status atomically; a stale version gets 409."

### DELETE

```powershell
$t = Invoke-RestMethod "$base/v1/tenants/$tenantId"
$del = @{ version = $t.version } | ConvertTo-Json

$resp = Invoke-RestMethod -Method Delete -Uri "$base/v1/tenants/$tenantId" `
  -ContentType "application/json" -Body $del

$resp | ConvertTo-Json -Depth 6
```

Poll until `destroyed`:

```powershell
for ($i = 1; $i -le 40; $i++) {
    $t = Invoke-RestMethod "$base/v1/tenants/$tenantId"
    "{0:D2} status={1} version={2}" -f $i, $t.status, $t.version
    if ($t.status -eq "destroyed") { break }
    Start-Sleep -Milliseconds 250
}
```

Then:

```powershell
Invoke-RestMethod "$base/v1/tenants/$tenantId" | ConvertTo-Json -Depth 5
Invoke-RestMethod "$base/v1/tenants/$tenantId/tasks" | ConvertTo-Json -Depth 5
```

Say: "The row is not deleted; `destroyed` is a terminal status. That keeps the slug unique forever, and
the task list is an audit trail."

### Optional: conflict demo

Open a second shell with the same `$tenantId` and `$t.version` (before the successful PATCH) and run the
same PATCH twice. The second should get 409 `tenant_version_conflict` or
`tenant_update_not_allowed` depending on whether the first moved the status. Say: "The zero-row UPDATE
is re-read to disambiguate the reason."

---

## 3. Happy path — Git Bash

```bash
BASE=http://localhost:8080
SLUG="demo-$(date +%s)"

RESP=$(curl -sS -X POST "$BASE/v1/tenants" \
  -H 'content-type: application/json' \
  -d "{\"slug\":\"$SLUG\",\"name\":\"Demo Tenant\"}")

TENANT_ID=$(printf '%s' "$RESP" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
TASK_ID=$(printf '%s' "$RESP" | grep -o '"id":"[^"]*"' | sed -n '2p' | cut -d'"' -f4)

echo "slug=$SLUG tenant=$TENANT_ID task=$TASK_ID"
echo "$RESP"

# Poll until active
for i in $(seq 1 40); do
  T=$(curl -sS "$BASE/v1/tenants/$TENANT_ID")
  STATUS=$(printf '%s' "$T" | grep -o '"status":"[^"]*"' | head -1 | cut -d'"' -f4)
  VERSION=$(printf '%s' "$T" | grep -o '"version":[0-9]*' | head -1 | cut -d: -f2)
  echo "$i status=$STATUS version=$VERSION"
  [ "$STATUS" = "active" ] && break
  sleep 0.25
done
```

Then PATCH and DELETE:

```bash
T=$(curl -sS "$BASE/v1/tenants/$TENANT_ID")
VERSION=$(printf '%s' "$T" | grep -o '"version":[0-9]*' | head -1 | cut -d: -f2)

curl -sS -i -X PATCH "$BASE/v1/tenants/$TENANT_ID" \
  -H 'content-type: application/json' \
  -d "{\"name\":\"Demo Tenant Updated\",\"version\":$VERSION}"

# Wait for active again, then:
T=$(curl -sS "$BASE/v1/tenants/$TENANT_ID")
VERSION=$(printf '%s' "$T" | grep -o '"version":[0-9]*' | head -1 | cut -d: -f2)

curl -sS -i -X DELETE "$BASE/v1/tenants/$TENANT_ID" \
  -H 'content-type: application/json' \
  -d "{\"version\":$VERSION}"
```

Note: `grep -o '"id":"[^"]*"'` assumes the first two IDs in the create response are tenant then task.
That is the current wire order. If the response shape changes, open it and parse by field name.

---

## 4. Race demo

This is the strongest 60 seconds in the demo: it proves the database is the arbiter, not a Go mutex.

### Git Bash

```bash
cd /c/PROJECT/aashishGithub/Avid/ControlPlane
bash scripts/race-demo.sh
```

### PowerShell

```powershell
cd C:\PROJECT\aashishGithub\Avid\ControlPlane
& "C:\Program Files\Git\bin\bash.exe" scripts/race-demo.sh
```

Expected:

```text
race 1: 50 concurrent POSTs for slug 'race-...'
  created=1 already_exists=49
race 2: 50 concurrent PATCHes at version 3
  accepted=1 rejected=49
OK: exactly one winner per race (slug=race-...)
```

Say: "50 goroutines/requests, one winner. The unique index decides the slug race; the conditional
UPDATE decides the version race. No application lock, no lost update. The losers get a domain error."

If `make` is available: `make race-demo`.

---

## 5. Fault injection

Get a completed tenant and its latest task:

```powershell
$tasks = Invoke-RestMethod "$base/v1/tenants/$tenantId/tasks"
$taskId = $tasks.tasks[0].id
"tenant=$tenantId task=$taskId"
```

Open Git Bash at the repo:

```bash
cd /c/PROJECT/aashishGithub/Avid/ControlPlane
bash scripts/publish-update.sh duplicate "$TASK_ID" "$TENANT_ID"
bash scripts/publish-update.sh stale     "$TASK_ID" "$TENANT_ID"
bash scripts/publish-update.sh poison    "$TASK_ID" "$TENANT_ID"
```

For PowerShell, pass the IDs:

```powershell
& "C:\Program Files\Git\bin\bash.exe" scripts/publish-update.sh duplicate $taskId $tenantId
& "C:\Program Files\Git\bin\bash.exe" scripts/publish-update.sh stale     $taskId $tenantId
& "C:\Program Files\Git\bin\bash.exe" scripts/publish-update.sh poison    $taskId $tenantId
```

### Observe the version

```powershell
(Invoke-RestMethod "$base/v1/tenants/$tenantId").version
```

Run before and after duplicate/stale. Expect unchanged.

### Observe the DLQ

```powershell
(curl.exe -sS -u guest:guest "http://localhost:15672/api/queues/%2F/cp.task-updates.dlq" |
  ConvertFrom-Json).messages
```

Expect +1 after poison.

### Observe the logs

```powershell
docker compose logs --tail=60 controlplane
```

Look for:

- duplicate/stale handled as no-op (applied result not an error),
- poison dead-lettered,
- subsequent valid messages still processed.

Say: "Duplicate is the inbox: same deterministic message ID, `ON CONFLICT DO NOTHING`. Stale is the rank
guard: a different message, but `in_progress` cannot outrank `done`. Poison is a permanent failure, so
it dead-letters instead of requeueing forever."

### Optional: worker failure and recovery

```powershell
$env:FAIL_RATE = "1"
docker compose up -d --build --force-recreate worker
Remove-Item Env:FAIL_RATE
```

Create a tenant; poll until `failed`. Then:

```powershell
$env:FAIL_RATE = "0"
docker compose up -d --build --force-recreate worker
Remove-Item Env:FAIL_RATE
```

Delete the failed tenant with its current version; poll until `destroyed`.

Say: "`FAIL_RATE=1` fails deploy, update, and destroy deliberately: a broken worker can fail cleanup
too. Lowering it and retrying DELETE is the recovery path."

---

## 6. Tests and what they prove

```powershell
cd C:\PROJECT\aashishGithub\Avid\ControlPlane
go test ./... -count=1
```

Targeted:

```powershell
go test ./internal/service -run "TestCreateTenantSlugRace|TestUpdateTenantVersionRace" -count=1 -v
go test ./internal/messaging -run TestFullLifecycleInProcess -count=1 -v
```

Race detector (needs cgo/gcc):

```powershell
go test -race ./... -count=1
```

| Test | Proves |
|---|---|
| `TestCreateTenantSlugRace` | 50 concurrent creates, exactly one 201, 49 conflicts. |
| `TestUpdateTenantVersionRace` | 50 concurrent PATCHes, exactly one accepted, version advances by exactly 1. |
| `TestFullLifecycleInProcess` | Create → active → patch → active → delete → destroyed → recreate 409, with real Postgres + RabbitMQ. |
| Outbox tests | Commit publishes once; rollback publishes nothing; a locked row is skipped, not double-claimed. |
| Consumer tests | Duplicate/stale/poison, DLQ, topology. |

Say: "The concurrency tests use 50 goroutines because the race is the point. The lifecycle test uses
real Postgres and RabbitMQ because the guarantees are in the database and broker, not in a mock."

**Test isolation story (great debugging answer):** the lifecycle test once flaked because the running
compose relay polled the same Postgres database as the test and could claim a test-created outbox row.
A separate RabbitMQ vhost alone was not enough; the test DB and vhost were separated, and a Postgres
advisory lock serialized test packages. This is a concrete "hardest bug" story.

---

## 7. RabbitMQ UI tour (optional, 60 seconds)

Open `http://localhost:15672`, login `guest` / `guest`.

- **Queues and Streams:** `worker.tasks` (work), `cp.task-updates` (progress), `cp.task-updates.dlq`
  (poison).
- Open `cp.task-updates`: show consumers, ready/unacked, delivery limit.
- Open `cp.tasks` exchange: show bindings `task.deploy`, `task.update`, `task.destroy`.
- Open `cp.task-updates` exchange: show bindings `task.update.accepted`, `.in_progress`, `.done`,
  `.failed`.

Say: "Direct exchanges because the routing vocabulary is closed. Every routing key has an explicit
binding; `mandatory` catches a missing one instead of silently dropping."

Do not spend more than a minute here unless asked.

---

## 8. Whiteboard

### Architecture

```text
Client → API → Service → Postgres
                 ↓
              tasks (published_at NULL)
                 ↓
              Relay → RabbitMQ → Worker
                          ↓
                 cp.task-updates → Consumer → Service → Postgres
                          ↓
                        DLQ
```

### Create sequence

```text
Client        API            Postgres        Relay          Broker        Worker        Consumer
  | POST       |                |              |               |             |             |
  |----------->| begin tx       |              |               |             |             |
  |            | insert tenant  |              |               |             |             |
  |            | insert task    |              |               |             |             |
  |            | commit         |              |               |             |             |
  |<-- 201 ----|                |              |               |             |             |
  |            | notify ------->|              |               |             |             |
  |            |                |<-- claim ----|               |             |             |
  |            |                |              |-- publish --->|             |             |
  |            |                |              |<-- confirm ---|             |             |
  |            |                |<-- mark ------|               |             |             |
  |            |                |              |-- commit ---->|             |             |
  |            |                |              |               |-- task ---->|             |
  |            |                |              |               |             |-- in_progress ->|
  |            |                |              |               |             |-- done -------->|
  |            |                |<-- inbox + rank + tenant update ----------|             |
  |            |                |<-- commit + ack ---------------------------|             |
```

### Update sequence

Same as create after the API transaction, except the task is `update`, and the terminal outcome moves
the tenant `updating → active` (or `failed`). The tenant version increments on PATCH and again on the
terminal outcome.

### Crash points to mark

1. Before the API transaction commits → rollback; no event.
2. After commit, before relay publish → relay republishes.
3. After confirm, before `published_at` commit → duplicate publish.
4. Worker mid-task → unacked task requeued.
5. Consumer before commit → rollback + redelivery.
6. Consumer after commit, before ack → redelivery hits inbox no-op.

---

## 9. Demo versions

### 5-minute version (if they are short on time)

1. 30 s: problem + invariant.
2. 90 s: architecture, draw the arrows.
3. 90 s: create + poll active + version bump.
4. 60 s: one failure — race demo **or** duplicate/stale.
5. 30 s: tradeoffs + known gaps.

Skip PATCH/DELETE and the DLQ unless asked. End with: "The guarantee is at-least-once plus idempotent
apply; the cost is relay latency and duplicate work."

### 10-minute version (default)

1. 30 s opening.
2. 90 s architecture.
3. 4 min happy path: create → active → patch → active → delete → destroyed.
4. 2–3 min failure proof: race demo + duplicate/stale (or poison).
5. 60 s tradeoffs/gaps.

### 20-minute version (deep dive)

1. 2 min problem + requirements + non-goals.
2. 3 min architecture + layering.
3. 5 min happy path + task history + logs.
4. 5 min failure proof: race, duplicate, stale, poison/DLQ, optional `FAIL_RATE=1`.
5. 3 min tests + test isolation story.
6. 2 min scale/alternatives/known gaps + questions.

Do not fill 20 minutes with clicking. The extra time is for **why** and **what breaks**.

---

## 10. Deep-dive Q&A

The full bank is [`qna-grill.md`](qna-grill.md). These are the demo-specific ones.

<details>
<summary><strong>Why is the task row the outbox instead of a separate table?</strong></summary>

The task already must be durable for the worker. `published_at IS NULL` adds delivery state, so one row
serves as the work record and the event. A separate outbox table would duplicate the task and create
another invariant to keep in sync. The tradeoff is that task history and outbox retention share a table;
at scale, archive published rows.

</details>

<details>
<summary><strong>Why 201 for create and 202 for patch/delete?</strong></summary>

201 means the tenant exists synchronously at response time. 202 means the request was accepted but the
final state depends on async work. The body already shows `provisioning`/`updating`/`destroying`, so the
client is not guessing.

</details>

<details>
<summary><strong>Why one open task per tenant?</strong></summary>

It prevents concurrent lifecycle operations from fighting over the same tenant. The primary mechanism is
the service's status guards (`active` before PATCH, `active`/`failed` before DELETE); the partial unique
index `tasks_one_open_per_tenant` is a safety net that makes a second open task impossible.

</details>

<details>
<summary><strong>Why tombstones instead of deleting tenant rows?</strong></summary>

The brief requires slug uniqueness including against destroyed tenants. Keeping the row means the
existing `UNIQUE(slug)` constraint covers that case for free. Deleting would require a separate
taken-slugs table or a soft-delete-aware unique index. The cost is table growth, handled by archival.

</details>

<details>
<summary><strong>Why UUIDv7 for row IDs?</strong></summary>

Time-ordered IDs give B-tree locality and make `id` a valid keyset cursor. UUIDv4 is fine for
uniqueness but random, which hurts index locality and range pagination. Message IDs use UUIDv5 because
they must be deterministic, not time-ordered.

</details>

<details>
<summary><strong>Why not Kafka?</strong></summary>

RabbitMQ's per-message ack, requeue, and DLX map directly to this poison/transient model. Kafka is a
partitioned log with offsets; a poison message can block a partition unless you build a retry/DLQ topic,
and consumer-offset management is a different operational skill. Kafka is a reasonable choice if you
need replay or very high throughput; it is not automatically better here.

</details>

<details>
<summary><strong>What is the most subtle bug in the design?</strong></summary>

The outbox relay holding a Postgres transaction open across broker I/O. It is deliberate: the row lock
must stay held through publish + mark, or another relay can double-claim. The risk is lock duration and
throughput under a slow broker. At scale, bound the batch, set timeouts, monitor lock age, and consider
batching confirms. It is a tradeoff, not an accident.

</details>

<details>
<summary><strong>What if the outbox publish succeeds but the mark/commit fails?</strong></summary>

The row stays `published_at IS NULL`; the next relay run republishes. That is a duplicate delivery, not
a lost event. The worker/consumer tolerate duplicates via deterministic IDs and the inbox. The
alternative — mark first, publish later — can lose the event.

</details>

<details>
<summary><strong>What if the worker task message is malformed?</strong></summary>

The worker logs it and `Nack(requeue=false)`. The current `worker.tasks` queue has no DLQ/delivery limit,
so the message is discarded (a known hardening gap). In production, add a DLQ and a delivery limit, and
alert on it.

</details>

<details>
<summary><strong>How did you test the impossible-to-mock parts?</strong></summary>

Real Postgres and RabbitMQ for constraints, locks, confirms, and DLQ behavior. Unit tests for domain
transitions. A dedicated test database + vhost and a Postgres advisory lock to isolate packages. Mocking
the broker would not prove publisher confirms or `SKIP LOCKED`; those are the behaviors under test.

</details>

<details>
<summary><strong>Why does the lifecycle test need a separate database *and* vhost?</strong></summary>

The running compose relay polls the same Postgres database. If tests share it, the relay can claim a
test-created outbox row and publish it to the dev vhost, where the in-process test worker is not
listening. A separate vhost alone does not fix the shared Postgres claim. Separate both, and serialize
test packages with an advisory lock.

</details>

<details>
<summary><strong>What would you do differently with more time?</strong></summary>

Add a stuck-task reaper/heartbeat and provider reconciliation; migrations; auth/RBAC/TLS; metrics and
tracing; a DLQ for `worker.tasks`; retention/archival for outbox and inbox; dedicated HTTP/worker tests;
and a completed `make check` quality gate. Keep the correctness core unchanged.

</details>

---

## 11. If they ask about your other Go work

Keep each to 60–90 seconds and connect it to the same principles.

### ticket-booking

> "An event ticketing platform built to prove a seat is never sold twice under a stampede. Postgres is
> the sole seat-state arbiter; Redis is a throughput filter, never the arbiter. Holds and confirms are
> conditional updates in Postgres, with a waiting room, WebSocket updates, and a load-test harness. The
> same lesson as ControlPlane: the database constraint is the correctness mechanism; caches and queues
> are optimizations."

Likely follow-ups: how do you prevent oversell? Why is Redis not the arbiter? How do you expire holds?
What happens if the reaper dies? Answer with the same invariant-first structure.

### offline-sync-app (FieldSync)

> "An offline-first field assessment PWA with a Go/AWS serverless backend: transactional outbox to
> EventBridge, idempotent consumers with a `processed_events` table, SQS/Lambda entrypoints, Cognito,
> S3, and Terraform. The client keeps an outbox that drains to an idempotent sync endpoint. It is the
> same outbox/idempotency pattern as ControlPlane, applied to a different transport."

Likely follow-ups: how do you resolve conflicts? Last-write-wins vs server authority? How do you handle
partial batch failures? Why Postgres for WebSocket connections instead of DynamoDB? Answer with the
tradeoff, not a feature list.

### How to connect them

> "Across all three, the pattern is the same: make the invariant explicit, put the authoritative check
> in the database, make async delivery at-least-once, and make consumers idempotent. The language and
> transport change; the correctness model does not."

---

## 12. Recovery plan

| Failure | What to do | What to say |
|---|---|---|
| Stack not up | `docker compose up -d --build --wait`; check `ps` | "Let me bring the stack up; I'll walk the architecture while it starts." |
| API 500 | `docker compose logs --tail=80 controlplane` | "The API is up but a dependency is unhealthy; here's the health/log evidence." |
| Broker down | Health check; show outbox rows accumulating | "This is the outbox case: writes still commit, the relay retries, tenants wait." |
| Create 409 | Use a new slug | "That slug is tombstoned; uniqueness includes destroyed tenants." |
| Worker not consuming | `docker compose ps`; restart worker | "The worker reconnects with backoff; let me show the consumer logs." |
| Script permission/`make` issue | Run raw commands or Git Bash path | "I'll use the underlying commands directly." |
| Demo running long | Cut delete flow, keep failure proof | "I'll skip the delete flow; it's the same async pattern." |
| Total failure | Switch to recording/screenshots + tests | "I'll use the recorded run and walk the code; the live stack is being uncooperative." |

**Never debug live for more than 60 seconds.** Keep the narrative moving.

---

## 13. What not to claim

- Do not call it production-ready. It is a take-home with deliberate gaps.
- Do not claim exactly-once delivery. Claim at-least-once + idempotent apply.
- Do not claim the worker is trustworthy. It has no DB; the consumer is the authority.
- Do not claim the outbox is free. It adds relay latency, duplicate delivery, and retention work.
- Do not claim `SKIP LOCKED` is a general read pattern. It is a queue claim.
- Do not claim Go's concurrency prevents the slug/version races. Postgres does.
- Do not hide the missing reaper, auth, metrics, migrations, or worker DLQ. Name them first.
