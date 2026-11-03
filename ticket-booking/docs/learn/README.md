# ticket-booking: learn it from beginner to senior

Each phase of this project has three files:

| File | Job |
|------|-----|
| `BASICS.md` | Small points in very simple English, with an everyday analogy each |
| `FILES.md` | The key source files of the phase: control flow, data flow, how each is tested |
| `QUESTIONS.md` | Interview Q&A in four tiers |

## The four tiers

| Tier | You can answer | Typical question |
|------|----------------|------------------|
| L1 Beginner | What is it, why does the app need it | "What is a seat hold?" |
| L2 Junior | How this code does it, step by step | "Trace a hold request from handler to the CAS update." |
| L3 Mid | Why this design, how it is tested, what the test proves | "Why compare-and-swap in Postgres and not a Redis lock?" |
| L4 Senior | Trade-offs, failure modes, what breaks at 100x, what is not solved | "What happens to a hold if Redis is down?" |

## Suggested path

1. Read `BASICS.md` of every phase in order. No code needed.
2. Read `FILES.md` of each phase with the source files open next to it.
3. Answer `QUESTIONS.md` tier by tier. Cover the answer, say yours aloud, then compare.
4. Run the "Try it" commands yourself. Each is one plain command.

Theory behind the design is in [deep-dive.md](../deep-dive.md) (topic-ordered: concurrency, saga, waiting room, sharding). These phase files point into it where it helps.

Prerequisite Go background lives in the repo root docs: [concurrency](../../../docs/concurrency.md), [synchronization](../../../docs/synchronization.md), [type conversions](../../../docs/type-conversions.md).

Build order and what was actually verified: [MILESTONES.md](../../MILESTONES.md).

## Phase index

| # | Phase | Folder |
|---|-------|--------|
| 0 | Scaffolding | [phase-0-scaffolding](phase-0-scaffolding/) |
| 1 | Auth | [phase-1-auth](phase-1-auth/) |
| 2 | Catalog + seat-map read path | [phase-2-catalog-seatmap](phase-2-catalog-seatmap/) |
| 3 | Hold / release / confirm CAS | [phase-3-hold-cas](phase-3-hold-cas/) |
| 4 | Canvas seat map + a11y tree | [phase-4-canvas-seatmap-a11y](phase-4-canvas-seatmap-a11y/) |
| 5 | Expiry side effects: outbox + reaper | [phase-5-expiry-outbox-reaper](phase-5-expiry-outbox-reaper/) |
| 6 | Payment saga, idempotency, reconciler | [phase-6-payment-saga](phase-6-payment-saga/) |
| 7 | Realtime: projector + WS deltas | [phase-7-realtime-ws](phase-7-realtime-ws/) |
| 8 | Virtual waiting room + AIMD | [phase-8-waiting-room-aimd](phase-8-waiting-room-aimd/) |
| 9 | QR ticketing + reminders | [phase-9-qr-reminders](phase-9-qr-reminders/) |
| 10 | Load harness + measured numbers | [phase-10-load-harness](phase-10-load-harness/) |
| 11 | IaC completion + Lambda twins | [phase-11-iac-lambda](phase-11-iac-lambda/) |
| 12 | DynamoDB comparison spike | [phase-12-dynamodb-spike](phase-12-dynamodb-spike/) |

## Where the numbers come from

Any figure in these files is copied from MILESTONES.md or the project docs with its caveat. Load-test numbers are from one local machine, not a capacity claim. Where code and milestone text disagree, the code is right and the file says so in a `> Note:`.
