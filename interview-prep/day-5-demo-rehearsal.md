# Day 5 — Demo rehearsal and architecture defense

**Goal:** deliver a calm, 10-minute live demo that proves correctness under failure, not a feature tour.
By the end you have a preflight checklist, a timed script, a whiteboard sequence, and a recovery plan
for the three most likely demo failures.

**Rule:** the demo is a story: **problem → invariant → architecture → live proof → failure proof →
tradeoffs.** Every command should answer "what does this prove?"

---

## Timebox (120 min)

| Time | Block |
|---|---|
| 0:00–0:10 | Preflight the stack |
| 0:10–0:25 | Learn the story arc and the 60-second opening |
| 0:25–1:00 | Happy-path rehearsal (create → active → patch → delete) |
| 1:00–1:20 | Failure rehearsal (race, duplicate/stale/poison) |
| 1:20–1:40 | Whiteboard from memory + top-15 Q&A |
| 1:40–1:55 | Record one 10-minute run; review timing and filler |
| 1:55–2:00 | Fix the top three risks; log weak spots |

---

## 1. The story arc

**Description.** The demo is a six-part argument: problem, invariant, architecture, live proof, failure proof, and tradeoffs. Every command should answer "what does this prove?" The opening names the hard problem and the guarantee; the architecture explains the outbox, relay, worker, and consumer; the failure proof shows the invariants surviving concurrency, duplication, staleness, and poison.

### 30-second opening

> "This is a tenant provisioning control plane. The interesting part isn't creating a tenant — it's
> staying correct when the HTTP request commits, the worker crashes, the broker redelivers, and
> progress arrives out of order. The design is at-least-once messaging plus idempotent, order-tolerant
> consumers. I'll show the happy path, then break it on purpose."

Why this works: it names the hard problem, the guarantee, and the demo plan in three sentences.

### 90-second architecture

> "The API writes the tenant and a task row in one Postgres transaction. The task row is the outbox:
> `published_at IS NULL` means not yet published. A relay claims committed rows with
> `FOR UPDATE SKIP LOCKED`, publishes with publisher confirms and `mandatory`, and marks them published
> in the same transaction. The worker has no database connection; it consumes the task and publishes
> progress. The consumer is the authority: it inserts a deterministic message ID into an inbox, locks
> tenant then task, applies the rank guard, updates task and tenant in one transaction, and only then
> acks."

Draw while saying it. Do not read from the README.

### Happy path (3–4 min)

1. `POST /v1/tenants` → 201, `Location`, tenant `provisioning`, task `accepted`.
2. Poll tenant → `active`, version bumps.
3. `PATCH` with current version → 202, status `updating`.
4. Poll → `active`, version bumps again.
5. `DELETE` with current version → 202, status `destroying`.
6. Poll → `destroyed`; `GET` still works (tombstone).
7. Show task history: deploy/update/destroy, all `done`.

Point at the version bump: "The terminal outcome increments the tenant version, so a client holding the
old token gets a conflict instead of a lost update."

### Failure proof (3–4 min)

Pick **two**, not all five, for a 10-minute demo:

- 50 concurrent `POST`s → exactly one 201, 49 conflicts. "The unique index is the arbiter, not a Go
  mutex."
- 50 concurrent `PATCH`es at the same version → exactly one 202, 49 rejected. "The conditional UPDATE
  is atomic; zero rows is disambiguated into 404/409/409."
- Duplicate `done` → tenant version unchanged; inbox no-op.
- Stale `in_progress` after `done` → no regression; rank guard.
- Poison JSON → DLQ count increments; consumer keeps processing.

### Close (60 sec)

> "The guarantee is at-least-once, not exactly-once. The outbox makes publish-after-commit structural;
> the inbox and rank guard make redelivery and reordering harmless. The costs are relay latency,
> duplicate work, and an outbox that needs retention. This build has no reaper, no auth, and no
> metrics/tracing — those are the next production steps, not hidden gaps."

Naming the gaps is part of the demo. Do not wait to be caught.

---

## 2. Preflight checklist

Run this **before** the interview, not during it.

### Stack

- [ ] Docker Desktop is running; enough memory (Postgres + RabbitMQ + two Go images).
- [ ] Ports 8080, 5432, 5672, 15672 are free.
- [ ] From `C:\PROJECT\aashishGithub\Avid\ControlPlane`:
  - [ ] `docker compose up -d --build --wait` succeeds.
  - [ ] `curl.exe -sS http://localhost:8080/healthz` returns `{"status":"ok"}`.
  - [ ] `docker compose ps` shows all services up/healthy (worker has no healthcheck).
- [ ] RabbitMQ UI opens at `http://localhost:15672` (`guest` / `guest`).
- [ ] `go test ./... -count=1` passes (or at least the lifecycle test).
- [ ] `make race-demo` works in Git Bash, or `bash scripts/race-demo.sh` works.
- [ ] `scripts/publish-update.sh` works via Git Bash.

### Screen and terminal

- [ ] Font size large enough for a shared screen (16–18 pt).
- [ ] Terminal history clean; no secrets, tokens, or personal paths.
- [ ] Notifications off; Slack/Teams closed.
- [ ] Three tabs only: terminal, RabbitMQ UI, editor with `DESIGN.md`/sequence diagram (optional).
- [ ] A second terminal for logs (`docker compose logs -f controlplane worker`).
- [ ] Clock/timezone visible; timestamps in logs are UTC.

### Backup

- [ ] A recorded 5-minute demo or screenshots of each step.
- [ ] A terminal buffer with the successful outputs (create, poll, race, tests).
- [ ] A one-page diagram open locally.
- [ ] A fallback plan: if live Docker fails, walk the code + recorded output, then run tests.

**Do not** run `make down` right before the demo: it removes the Postgres volume and forces a cold
build. If you need a clean slate, do it 20+ minutes early and re-run `make up`.

---

## 3. Happy-path rehearsal

Use the exact commands in [`controlplane-demo.md`](controlplane-demo.md). For rehearsal, narrate every
step and keep a timer visible.

### What to say at each step

| Step | Say | Prove |
|---|---|---|
| Health | "Both dependencies are up; health checks DB + broker." | Operational readiness |
| Create | "This commits tenant + deploy task, then returns 201. The task is unpublished." | Atomic intent, outbox |
| Poll | "The worker published in_progress then done; the consumer applied it." | Async flow |
| Version | "Version went 1 → 2 on create, then 2 → 3 on terminal outcome." | Optimistic token |
| PATCH | "202 because it only starts work; the body already says updating." | Sync vs async semantics |
| Delete | "Destroy is async; the row remains as a tombstone so the slug stays unique." | Tombstoning |
| Task list | "Every transition has an audit row." | Observability/history |
| Logs | "Every line carries tenant_id/task_id/message_id." | Debuggability |

### Timing targets

- Opening: 30 s.
- Architecture: 90 s.
- Create + poll: 60 s.
- Patch + poll: 45 s.
- Delete + poll: 45 s.
- Failure proof: 2–3 min.
- Tradeoffs/close: 60 s.

If you run over, cut the delete flow, not the failure proof. The failure proof is the differentiator.

---

## 4. Failure rehearsal

**Description.** The failure rehearsal proves that correctness does not depend on the happy path. The race demo shows the unique index and conditional `UPDATE` arbitrating concurrent writes; the duplicate, stale, and poison demos show the inbox, rank guard, and DLQ handling redelivery and bad messages. Predict the result before running it, then show the prediction was right.

### Race demo

**Description.** The race demo fires 50 concurrent creates with the same slug and 50 concurrent patches at the same version. Exactly one create and one patch should win; the rest receive conflicts. The point is that no Go mutex is doing the arbitration: the Postgres unique index and the conditional `UPDATE` are the concurrency control.

```bash
make race-demo
# or:
bash scripts/race-demo.sh
```

Expected:

```text
race 1: 50 concurrent POSTs for slug 'race-...'
  created=1 already_exists=49
race 2: 50 concurrent PATCHes at version 3
  accepted=1 rejected=49
OK: exactly one winner per race
```

Say: "No application mutex. The unique index and the conditional UPDATE are the arbiters. The losers
get a domain error, not a silent lost update."

### Duplicate, stale, poison

**Description.** These three injections exercise different defenses. A duplicate terminal update is deduplicated by the inbox `message_id`; a stale lower-rank update is rejected by the rank guard; malformed JSON is dead-lettered because retrying it can never succeed. In each case, the tenant version should remain unchanged unless a valid terminal transition actually applied.

Get a task and tenant ID from a completed tenant:

```bash
curl -sS http://localhost:8080/v1/tenants/<tenant-id>/tasks
```

Run from the ControlPlane repo in Git Bash:

```bash
bash scripts/publish-update.sh duplicate <task-id> <tenant-id>
bash scripts/publish-update.sh stale     <task-id> <tenant-id>
bash scripts/publish-update.sh poison    <task-id> <tenant-id>
```

Watch:

- Tenant version before/after duplicate/stale: unchanged.
- `cp.task-updates.dlq` count: +1 after poison.
- Consumer logs: duplicate/stale applied as no-op; poison dead-lettered; next valid message still
  processed.

**Rehearse the prediction first.** Say "I expect the version to stay the same because the inbox/rank
guard makes it a no-op" before running. Then show it. That is the senior move.

### Optional: failure rate

**Description.** Setting `FAIL_RATE=1` makes the worker report a terminal failure, which should move the tenant to `failed` through the same outbox, rank guard, and tenant mapping. It is optional because it takes time and leaves the stack in a failed state; use it only in the longer demo to show the failure path end to end.

```bash
make worker-restart FAIL_RATE=1
# create a tenant -> deploy fails -> tenant failed
make worker-restart FAIL_RATE=0
# delete the failed tenant -> destroy succeeds -> destroyed
```

Only include this in the 20-minute version. It takes time and can leave the stack in a failed state.

---

## 5. Whiteboard from memory

**Description.** The whiteboard is the compact version of the architecture: client to API to Postgres, the outbox row, the relay to RabbitMQ to the worker, progress back through the consumer, and poison to the DLQ. Label the guarantee on each arrow, then mark the crash points and say how each one recovers.

### Boxes and arrows

**Description.** Draw left to right so the request path and the asynchronous return path are visually separate. The critical labels are the transaction boundary at Postgres, publisher confirm plus `mandatory` at the broker, deterministic `message_id` at the worker, and inbox plus rank guard plus lock order at the consumer.

Draw left to right:

```text
Client → API → Service → Postgres
                 ↓
              outbox row (published_at NULL)
                 ↓
              Relay → RabbitMQ → Worker
                          ↓
                 cp.task-updates → Consumer → Service → Postgres
                          ↓
                        DLQ
```

Label the guarantees on the arrows:

- API → Postgres: one transaction, tenant + task.
- Relay → RabbitMQ: publisher confirm + mandatory.
- Worker → broker: deterministic message_id.
- Consumer → Postgres: inbox + rank guard + lock order.
- Poison → DLQ.

### Create sequence

**Description.** The sequence diagram makes the asynchronous gaps explicit: the API commits tenant and task before returning, the relay claims and publishes after commit, and the consumer applies progress in a separate transaction. The six `X` marks are the crash points; for each one, the recovery is either rollback, republish, redelivery, or inbox deduplication.

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

Practice this until you can draw it in 3 minutes without looking. Mark the crash points with an `X`:
before commit, after commit before publish, after confirm before mark, worker mid-task, consumer before
commit, after commit before ack.

---

## 6. Top-15 demo Q&A (30–60 seconds each)

<details>
<summary><strong>1. Why not publish directly in the request?</strong></summary>

Publish-before-commit can make the worker act on a tenant that does not exist; commit-before-publish
can lose the event on a crash. A DB transaction cannot atomically include RabbitMQ, and RabbitMQ is not
XA. The outbox makes publish-after-commit structural.

</details>

<details>
<summary><strong>2. Why is the task row the outbox?</strong></summary>

A task already needs to be durable for the worker. `published_at IS NULL` adds the delivery state, so
one row serves as work record + event. A separate outbox table would duplicate the task and require
another consistency invariant.

</details>

<details>
<summary><strong>3. Why `SKIP LOCKED`?</strong></summary>

It lets multiple relay replicas claim disjoint batches without blocking on each other. The lock is
transaction-scoped; claim/publish/mark share one transaction so the lock stays held.

</details>

<details>
<summary><strong>4. Why confirms + mandatory?</strong></summary>

A publish returning nil only means bytes reached the socket. A confirm means the broker accepted
responsibility. `mandatory` catches unroutable messages, which a confirm alone can still acknowledge.
The relay marks published only after both.

</details>

<details>
<summary><strong>5. Why deterministic message IDs?</strong></summary>

Retries and redeliveries of the same logical update must hit the same inbox key. `UUIDv5(task_id +
"/" + status)` is stable; a random ID per attempt would defeat deduplication.

</details>

<details>
<summary><strong>6. Why both inbox and rank guard?</strong></summary>

Inbox deduplicates the same message ID. Rank guard handles different messages arriving out of order
(e.g. late `in_progress` after `done`). They solve different failure modes.

</details>

<details>
<summary><strong>7. Why not exactly-once?</strong></summary>

Exactly-once across Postgres + RabbitMQ needs distributed transactions or a shared log; RabbitMQ is not
XA. The honest guarantee is at-least-once plus idempotent apply and order tolerance, which gives an
effectively-once business effect.

</details>

<details>
<summary><strong>8. What if the broker is down?</strong></summary>

The API still accepts; outbox rows accumulate. The relay retries; when the broker returns, it drains
the backlog. Tenants stay in-flight until published/processed. Monitor oldest unpublished age.

</details>

<details>
<summary><strong>9. What if the worker crashes mid-task?</strong></summary>

The task message is unacked, so the broker requeues it. Another worker picks it up. The worker may
replay `in_progress`/terminal updates; deterministic IDs and the rank guard make the replay harmless.

</details>

<details>
<summary><strong>10. What if the consumer crashes mid-transaction?</strong></summary>

The transaction rolls back, the message is unacked, and the broker redelivers. The inbox insert and
state change commit together, so there is no "marked processed but state lost" window.

</details>

<details>
<summary><strong>11. Why lock tenant before task?</strong></summary>

Consistent lock order prevents ABBA deadlocks. The API path and consumer both touch tenant before task.

</details>

<details>
<summary><strong>12. Why is the worker not allowed to touch the DB?</strong></summary>

The worker is untrusted and stateless. The consumer is the authority on task existence, tenant mapping,
and state transitions. Keeping the worker DB-less limits blast radius and keeps the state machine in
one place.

</details>

<details>
<summary><strong>13. What is the biggest gap?</strong></summary>

No stuck-task reaper/heartbeat: a task stuck in `in_progress` leaves the tenant in-flight forever. Also
no auth/RBAC, no migrations tool, no metrics/tracing, and `worker.tasks` lacks a DLQ/delivery limit.
Name them before the interviewer does.

</details>

<details>
<summary><strong>14. How would you scale it?</strong></summary>

Batch relay confirms and monitor outbox age; shard/partition workers by tenant; add a reaper; archive
published rows; retention for `processed_messages`; metrics/tracing; auth and per-tenant rate limits.
Keep the invariants, change the scale knobs.

</details>

<details>
<summary><strong>15. Why Go?</strong></summary>

The service is I/O-bound and concurrency-heavy: HTTP, DB, broker, reconnect loops, worker pool. Go's
explicit concurrency, small binaries, and straightforward deployment fit it. The correctness mechanism
is still Postgres, not goroutines. If the team were Python-heavy, the same design would work; language
is not the differentiator.

</details>

### Rapid answer key

| # | Question | Model answer | Section |
|---|---|---|---|
| 1 | Why not publish directly in the request? | Publish-before-commit can expose a rolled-back tenant; commit-before-publish can lose the event. The outbox makes publish-after-commit structural. | §1 |
| 2 | Why is the task row the outbox? | The task already needs durability; `published_at IS NULL` adds delivery state so one row is both work record and event. | §1 |
| 3 | Why `SKIP LOCKED`? | It lets relay replicas claim disjoint batches without blocking; the transaction-scoped lock stays held through publish and mark. | §4 |
| 4 | Why confirms plus `mandatory`? | A nil publish only means bytes reached the socket; a confirm means the broker accepted, and `mandatory` catches unroutable messages. | §1 |
| 5 | Why deterministic message IDs? | Retries and redeliveries of the same logical update must produce the same inbox key; UUIDv5 from task and status does that. | §1 |
| 6 | Why both inbox and rank guard? | The inbox deduplicates the same message; the rank guard rejects different messages that arrive out of order. | §1 |
| 7 | Why not exactly-once? | Postgres and RabbitMQ cannot share an atomic transaction; at-least-once plus idempotent apply gives an effectively-once business effect. | §1 |
| 8 | What if the broker is down? | The API keeps accepting and outbox rows accumulate; the relay retries and drains the backlog when the broker returns. | §4 |
| 9 | What if the worker crashes mid-task? | The unacked task is requeued; replay is harmless because of deterministic IDs and the rank guard. | §4 |
| 10 | What if the consumer crashes mid-transaction? | The transaction rolls back and the message is redelivered; inbox and state change commit together. | §4 |
| 11 | Why lock tenant before task? | A consistent lock order prevents ABBA deadlocks across the API and consumer paths. | §4 |
| 12 | Why can the worker not touch the DB? | The worker is untrusted and stateless; the consumer owns task existence, tenant mapping, and state transitions. | §1 |
| 13 | What is the biggest gap? | No stuck-task reaper or heartbeat, plus no auth/RBAC, migrations tool, metrics, or DLQ on `worker.tasks`. Name them first. | §1 |
| 14 | How would you scale it? | Batch confirms, shard workers by tenant, add a reaper, archive published rows, add retention, metrics, auth, and rate limits. | §1 |
| 15 | Why Go? | The workload is I/O-bound and concurrency-heavy; Go fits with explicit concurrency and small deployments, but Postgres still enforces correctness. | §1 |

---

## 7. Record and review

Record one full 10-minute run (screen + voice). Watch it once at 1.5x. Score:

| Dimension | 1 | 3 | 5 |
|---|---|---|---|
| Opening clarity | Meanders | States problem | Problem + invariant + plan in 30 s |
| Architecture | Reads names | Explains arrows | Explains why each arrow exists |
| Happy path | Clicks around | Runs flow | Narrates state/version at each step |
| Failure proof | Skips | Shows one | Predicts, runs, explains guarantee |
| Tradeoffs | Hides gaps | Mentions one | Names costs + alternatives + gaps |
| Recovery | Freezes | Restarts | Has a backup and a calm fallback |

Target: 4+ on architecture, failure proof, tradeoffs. If the demo is over 12 minutes, cut the delete
flow and one failure injection.

### Filler-word audit

Count "um", "basically", "sort of", "just". Replace with a pause. A pause sounds more senior than a
filler.

---

## 8. Demo risks and recovery

| Risk | Prevention | Recovery line |
|---|---|---|
| Docker not running | Preflight 30 min early | "Let me switch to the recorded run while Docker starts." |
| Port conflict | Check `netstat`; stop old stack | Use `docker compose down` then up; or change ports. |
| Build slow | Pre-build; do not cold-build live | Talk architecture while it builds; use cached images. |
| API 500 | Check logs | "The API is up but a dependency is unhealthy; let me show the health output and logs." |
| Broker down | Health check | "This is actually the outbox case: the API still accepts, rows accumulate, relay retries." |
| Create 409 | Slug exists | Use a new slug; explain uniqueness/tombstoning. |
| Worker not consuming | `docker compose ps`, logs | Restart worker; explain prefetch/reconnect. |
| `make` missing | Use Git Bash or raw commands | `docker compose ...`, `bash scripts/...`. |
| RabbitMQ UI login | guest/guest; verify before | Skip UI; use `docker compose logs` and queue declarations. |
| Demo over time | Timer; cut delete | "I'll skip the delete flow; it's the same async pattern." |

**Never debug live for more than 60 seconds.** Say "I'll show the recorded output for this step and
come back to the live stack" and keep the story moving. The interviewer is judging judgment, not Docker
luck.

---

## 9. End-of-day checkpoint

- [ ] 30-second opening memorized.
- [ ] Architecture drawn from memory in under 3 minutes.
- [ ] Happy path run twice, under 5 minutes.
- [ ] Two failure injections run and explained.
- [ ] Top-15 Q&A answered in 60 seconds each.
- [ ] One 10-minute recording reviewed.
- [ ] Backup screenshots/recording ready.
- [ ] Three demo risks have a recovery line.

**If you only remember five things:** problem first, draw the arrows, prove one failure, name the
tradeoffs, have a backup.

**Next:** [`day-6-mock-and-behavioral.md`](day-6-mock-and-behavioral.md).
