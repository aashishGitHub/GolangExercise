# Go Interview Sprint — 6 days, 2 hours a day

Two rounds, six study days:

| Date | Round | What to assume |
|---|---|---|
| Thu 19 Nov 2026 | Go / backend technical | Concepts, concurrency, coding, HTTP/DB, "why did you build it that way?" |
| Fri 20 Nov 2026 | ControlPlane live demo | Architecture walkthrough, failure injection, deep-dive tradeoffs |

If the rounds are reversed, swap Day 5 and Day 6. The material does not change.

**The promise:** by Wednesday night you can

1. Draw the ControlPlane architecture from memory in 3 minutes.
2. Explain 15 design decisions with the alternative and the failure mode.
3. Deliver a 10-minute demo, with a 5-minute and a 20-minute version.
4. Answer 100+ Go/backend questions in 60 seconds each.
5. Run the race demo, fault injection, and lifecycle test — and explain what each proves.
6. Tell six STAR stories without rambling.

This is a **sprint**, not a Go textbook. You already have the deep notes in
[`../docs/`](../docs/) and the source of truth in
[`../../Avid/ControlPlane/docs/DESIGN.md`](../../Avid/ControlPlane/docs/DESIGN.md).
Use them as reference; the daily files tell you what to study, in what order, and what to prove.

---

## The schedule

| Day | Date | Focus | 2-hour output | Interview value |
|---|---|---|---|---|
| 1 | Fri 13 Nov | Go language core, errors, interfaces, testing | One-page mental model + 15 grill answers | 19th concepts |
| 2 | Sat 14 Nov | Concurrency, context, worker pools, leaks | Working bounded worker pool + race test | 19th coding/concurrency |
| 3 | Sun 15 Nov | HTTP, API design, Postgres, transactions | Whiteboard request lifecycle + SQL drills | 19th backend depth |
| 4 | Mon 16 Nov | Messaging + ControlPlane end-to-end | Architecture from memory + 12 decisions | Both rounds |
| 5 | Tue 17 Nov | Demo rehearsal + architecture defense | Three timed run-throughs (5/10/20 min) | 20th demo |
| 6 | Wed 18 Nov | Full mock, behavioral, weak spots | Mock scorecard + six STAR bullets | Both rounds |
| — | Thu 19 Nov | Interview 1 | 30-minute warm-up only | — |
| — | Fri 20 Nov | Interview 2 / demo | Run the demo runbook | — |

Weekdays: **2 hours core**. Weekends: 2 hours core, optional 45-minute lab if you feel strong.
Do not let the optional AWS block eat the Go core. The core is the interview.

> **Starting late?** Day 1 and Day 2 each have a beginner companion
> ([`day-1a-go-core.md`](day-1a-go-core.md), [`day-2a-concurrency.md`](day-2a-concurrency.md)). If a
> day needs the on-ramp, use the one-day-shifted sequence: Day 2 Sun 15 Nov, Day 3 Mon 16, Day 4 Tue
> 17. Then use Wed 18 for a compressed Day 5 rehearsal plus the Day 6 mock. The 19/20 Nov interview
> dates do not move.

---

## The daily 2-hour loop

| Time | Block | What you actually do |
|---|---|---|
| 0:00–0:10 | Cold recall | Close everything. Write yesterday's 5 key ideas from memory. |
| 0:10–0:50 | Mechanism study | Read the day file section. Stop at each table; say the "why" aloud. |
| 0:50–1:25 | Active practice | Code, whiteboard, run a demo, or write SQL — not more reading. |
| 1:25–1:45 | Grill | Answer the day's must-answer questions aloud before opening the answer. |
| 1:45–1:55 | Explain-back | Record a 3-minute voice note or write 5 bullets with no notes. |
| 1:55–2:00 | Log | Add weak spots to [`progress.md`](progress.md). Pick tomorrow's first question. |

**Minimum effective day (45 min):** 10 min recall + 20 min one mechanism + 10 min grill + 5 min log.
**Emergency day (20 min):** read the day's "If you only remember five things" and answer three questions.

---

## What to do when

### Before Day 1 (30 minutes)

1. Read this page.
2. Open [`controlplane-demo.md`](controlplane-demo.md) and check the preflight list.
3. Run `docker compose up -d --build --wait` in `../../Avid/ControlPlane` once. Confirm `/healthz`.
4. Open [`progress.md`](progress.md) and fill the Day 1 row.
5. Put the interview dates and times in your calendar with a 30-minute buffer.

### Each day

- Read the day file top to bottom once. Do not highlight.
- Do the active practice with the file closed.
- Answer the grill questions **out loud**. If you cannot say it in 60 seconds, it is not learned.
- Log red/amber/green in [`progress.md`](progress.md).

### The last 48 hours

- **Wed:** full mock, then only weak spots. No new topics after 18:00.
- **Thu morning:** 30 minutes on the cheat sheet + one demo dry run. Then stop.
- **Thu evening:** 20 minutes on the demo story, not code.
- **Fri morning:** preflight the demo stack, run the 5-minute version once, then stop.

---

## The 8-step backend answer framework

Use this for every system-design or "walk me through" question.

1. **Requirements** — users, happy path, SLOs, read/write ratio, consistency needs, out of scope.
2. **API contract** — resources, verbs, status codes, errors, idempotency, pagination.
3. **Data model and invariants** — tables, keys, constraints, state machines, what must never be true.
4. **Concurrency and transaction boundaries** — what races exist, which mechanism wins each race, lock order.
5. **Messaging and delivery semantics** — sync vs async, outbox, at-least-once, idempotency, ordering.
6. **Failure modes** — crash points, retries, poison messages, broker/DB outage, stuck work, recovery.
7. **Operations and security** — logs, metrics, traces, health, auth, secrets, blast radius, SLOs.
8. **Tradeoffs** — alternatives rejected, cost, latency, complexity, what you would change at 10x.

If you get stuck, say: "Let me start with the invariant, then the mechanism that protects it."
That sentence alone signals senior judgment.

---

## Files

| File | Use |
|---|---|
| [`day-1-go-core.md`](day-1-go-core.md) | Types, slices, maps, interfaces, errors, testing |
| [`day-1a-go-core.md`](day-1a-go-core.md) | Beginner on-ramp to Day 1: same concepts, slower pace |
| [`day-2-concurrency.md`](day-2-concurrency.md) | Goroutines, channels, context, patterns, leaks |
| [`day-2a-concurrency.md`](day-2a-concurrency.md) | Beginner on-ramp to Day 2: same concepts, slower pace |
| [`day-3-http-db.md`](day-3-http-db.md) | HTTP, API design, pgx, transactions, Postgres concurrency |
| [`day-4-messaging-controlplane.md`](day-4-messaging-controlplane.md) | Outbox, inbox, RabbitMQ, failure matrix, decisions |
| [`day-5-demo-rehearsal.md`](day-5-demo-rehearsal.md) | Demo runbook and three timed versions |
| [`day-6-mock-and-behavioral.md`](day-6-mock-and-behavioral.md) | Full mock, STAR stories, final checklist |
| [`controlplane-demo.md`](controlplane-demo.md) | Commands, scripts, whiteboard, demo Q&A, backup plan |
| [`qna-grill.md`](qna-grill.md) | 138 rapid-fire questions with hidden answers |
| [`examples/go-concurrency.md`](examples/go-concurrency.md) | Worker pool, pipeline, semaphore, retry, SSE, graceful shutdown |
| [`examples/go-service-patterns.md`](examples/go-service-patterns.md) | HTTP, errors, pgx tx, SQL, outbox, consumer, tests |
| [`examples/controlplane-walkthrough.md`](examples/controlplane-walkthrough.md) | File-by-file map and "show me the line" questions |
| [`cheatsheets.md`](cheatsheets.md) | One-pagers for the final 48 hours |
| [`progress.md`](progress.md) | Daily log, mock scorecard, weak-spot bank |
| [`aws-addon.md`](aws-addon.md) | Optional 30–45 min AWS mapping after the core |

---

## Source map

Primary source of truth:

- [`ControlPlane`](https://github.com/aashishGitHub/ControlPlane) — the demo repo (Avid client exercise).
- [`DESIGN.md`](../../Avid/ControlPlane/docs/DESIGN.md) — schema, state machines, API, messaging, decisions.
- [`PRD.md`](../../Avid/ControlPlane/docs/PRD.md) — requirements and non-goals.
- [`M3-RELEVANT-QNA.md`](../../Avid/ControlPlane/docs/M3-RELEVANT-QNA.md) — outbox, inbox, DLQ, reconnect.
- [`M4-RELEVANT-QNA.md`](../../Avid/ControlPlane/docs/M4-RELEVANT-QNA.md) — worker, fault injection, tests, compose.
- [`ControlPlane README`](../../Avid/ControlPlane/README.md) — quick start, API, manual verification.

Your existing Go notes, for optional depth:

- [`concurrency-cheatsheet.md`](../docs/concurrency-cheatsheet.md)
- [`concurrency-patterns.md`](../docs/concurrency-patterns.md)
- [`goroutine-problems.md`](../docs/goroutine-problems.md)
- [`synchronization.md`](../docs/synchronization.md)
- [`ticket-booking`](../ticket-booking/README.md) — seat stampede, Postgres as arbiter, Redis as filter.
- [`offline-sync-app`](../offline-sync-app/README.md) — outbox, idempotent consumers, AWS serverless.
- [`DSnAlgoUsingJS/Go`](https://github.com/aashishGitHub/DSnAlgoUsingJS/tree/master/Go) — Go DSA, if you want a warm-up.

---

## Non-negotiables

1. **Draw before you read.** Every day starts with a blank page. If you cannot draw it, you do not know it.
2. **Every claim gets a "why" and a "what breaks".** "I used an outbox" is junior. "I used an outbox because publishing inside the request can commit the DB and lose the event; the cost is relay latency and duplicate delivery, which the consumer already tolerates" is senior.
3. **The demo is a story, not a feature tour.** Problem → invariant → architecture → live proof → failure proof → tradeoffs.
4. **Do not claim production readiness.** The README lists the gaps. Naming them before the interviewer does is a strength.
5. **Stop at 2 hours.** Retention beats volume. The optional AWS block is optional.

---

## Success check — Wednesday night

You are ready if you can do all of these without notes:

- [ ] Draw the architecture and the create/update sequence in under 5 minutes.
- [ ] Explain `published_at`, `FOR UPDATE SKIP LOCKED`, publisher confirms, and the crash window.
- [ ] Explain deterministic `message_id`, the inbox, and the rank guard with one example each.
- [ ] Explain why the unique index beats a pre-check `SELECT`, and why the conditional `UPDATE` beats read-then-write.
- [ ] Run the 10-minute demo and the duplicate/stale/poison injection without reading a script.
- [ ] Answer 20 random questions from [`qna-grill.md`](qna-grill.md) at 4/5 or better.
- [ ] Tell the six STAR stories in under 2 minutes each.
