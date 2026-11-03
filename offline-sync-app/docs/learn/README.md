# FieldSync: learn it from beginner to senior

Each phase of this project has three files:

| File | Job |
|------|-----|
| `BASICS.md` | Small points in very simple English, with an everyday analogy each |
| `FILES.md` | The key source files of the phase: control flow, data flow, how each is tested |
| `QUESTIONS.md` | Interview Q&A in four tiers |

## The four tiers

| Tier | You can answer | Typical question |
|------|----------------|------------------|
| L1 Beginner | What is it, why does the app need it | "What is a JWT?" |
| L2 Junior | How this code does it, step by step | "Trace a `POST /api/sync` from router to SQL." |
| L3 Mid | Why this design, how it is tested, what the test proves | "Why one atomic upsert instead of lock-then-check?" |
| L4 Senior | Trade-offs, failure modes, what breaks at 100x, what is not solved | "What does the outbox not guarantee?" |

## Suggested path

1. Read `BASICS.md` of every phase in order. No code needed.
2. Read `FILES.md` of each phase with the source files open next to it.
3. Answer `QUESTIONS.md` tier by tier. Cover the answer, say yours aloud, then compare.
4. Run the "Try it" commands yourself. Each is one plain command.

Prerequisite Go background lives in the repo root docs: [concurrency](../../../docs/concurrency.md), [synchronization](../../../docs/synchronization.md), [type conversions](../../../docs/type-conversions.md).

Build order and what was actually verified: [MILESTONES.md](../../MILESTONES.md).

## Phase index

| # | Phase | Folder |
|---|-------|--------|
| 0 | Scaffolding | [phase-0-scaffolding](phase-0-scaffolding/) |
| 1 | Auth | [phase-1-auth](phase-1-auth/) |
| 2 | Sync API + DB | [phase-2-sync-api-db](phase-2-sync-api-db/) |
| 3 | Photos / S3 | [phase-3-photos-s3](phase-3-photos-s3/) |
| 4 | Dashboard | [phase-4-dashboard](phase-4-dashboard/) |
| 5 | IaC / local deploy loop | [phase-5-iac-local-deploy](phase-5-iac-local-deploy/) |
| 6 | Hardening | [phase-6-hardening](phase-6-hardening/) |
| 7 | Event-driven fan-out | [phase-7-event-fanout](phase-7-event-fanout/) |
| 8 | Two-way sync (webhooks) | [phase-8-two-way-sync](phase-8-two-way-sync/) |
| 9 | GraphQL read API | [phase-9-graphql](phase-9-graphql/) |
| 10 | Analytics SQL | [phase-10-analytics](phase-10-analytics/) |

Phases 8 to 10 were added after the numbered milestones. Their senior Q&A already exists in [two-way-sync.md](../two-way-sync.md), [graphql.md](../graphql.md) and [analytics/QUESTIONS.md](../../analytics/QUESTIONS.md); those phases link to it instead of copying it.

## Where the numbers come from

Any figure in these files is copied from MILESTONES.md or the project docs with its caveat. Load-test numbers are from one local machine, not a capacity claim. Where code and milestone text disagree, the code is right and the file says so in a `> Note:`.
