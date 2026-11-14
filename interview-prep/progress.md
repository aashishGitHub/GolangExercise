# Progress log — 6-day Go sprint

Fill this in at the end of each session. Two minutes of honest logging beats another hour of reading.

**Rules**

- One row per day. If you miss a day, do not “double up”; do the next day and move the missed weak spots
  into the bank.
- A topic is **green** only if you explained it aloud in under 60 seconds without notes.
- A **red** is a gift: it tells you exactly what to study tomorrow. Do not fix ten reds; pick one.
- No new topics after **Wed 18 Nov, 18:00**.

---

## Snapshot

| Field | Value |
|---|---|
| Technical round | Thu 19 Nov 2026 |
| Demo round | Fri 20 Nov 2026 |
| Core budget | 2 hours/day |
| Optional AWS | 30–45 min, only after the core |
| Primary repo | `C:\PROJECT\aashishGithub\Avid\ControlPlane` |
| Notebook | `C:\PROJECT\aashishGithub\GolangExercise\interview-prep` |

---

## Daily log

| Day | Date | Focus | Min | Cold recall (1–5) | Practice done | Grill G/A/R | Explain-back (1–5) | One weak spot | Tomorrow's first question |
|---|---|---|---:|---:|---|---|---|---|---|
| 1 | Fri 13 Nov | Go core, errors, interfaces, testing | | | | | | | |
| 2 | Sat 14 Nov | Concurrency, context, pools, leaks | | | | | | | |
| 3 | Sun 15 Nov | HTTP, API design, Postgres, transactions | | | | | | | |
| 4 | Mon 16 Nov | Messaging, outbox/inbox, ControlPlane | | | | | | | |
| 5 | Tue 17 Nov | Demo rehearsal (5/10/20 min) | | | | | | | |
| 6 | Wed 18 Nov | Full mock, behavioral, weak spots | | | | | | | |

**G/A/R** = green / amber / red count from that day's grill. A day with zero reds is suspicious; you
probably did not test the hard parts.

---

## Day 1 — Go core

> **Remediation note (Sat 14 Nov):** If Day 1 felt too dense, read
> [`day-1a-go-core.md`](day-1a-go-core.md) as the beginner on-ramp, then retry the drills. Do not
> double up: shift Day 2 to Sun 15 Nov, Day 3 to Mon 16, and Day 4 to Tue 17. Use Wed 18 for a compressed
> Day 5 rehearsal plus the Day 6 mock.

- [ ] Read [`day-1a-go-core.md`](day-1a-go-core.md); said the 19 Remember lines aloud.
- [ ] Wrote the one-page type/interface/error model from memory.
- [ ] Explained slice aliasing and `append` with a concrete example.
- [ ] Explained typed-nil-in-interface and why it matters.
- [ ] Wrote `errors.Is` vs `errors.As` with a wrapped chain.
- [ ] Wrote a table-driven test with `t.Run` and `t.Cleanup`.
- [ ] Answered 15 grill questions aloud.
- [ ] Logged reds in the weak-spot bank.

Score: ___ / 5. One thing to re-explain tomorrow: ______________________________.

---

## Day 2 — Concurrency

> **Remediation note (Sat 14 Nov):** If the scheduler, channel, or memory-model sections felt too
> dense, read [`day-2a-concurrency.md`](day-2a-concurrency.md) before the drills. It covers the same
> concepts in the same order. Do not double up: keep the shifted schedule and move on to Day 3 on
> Mon 16 Nov.

- [ ] Read [`day-2a-concurrency.md`](day-2a-concurrency.md); said the 21 Remember lines aloud.
- [ ] Drew G-M-P and explained `GOMAXPROCS` in a container.
- [ ] Wrote `RunPool` from memory, including producer close and cancellation.
- [ ] Wrote a pipeline stage and named the channel owner.
- [ ] Explained fan-in ordering loss.
- [ ] Wrote retry with classification, bound, jitter, context.
- [ ] Explained SSE cleanup and the `WriteTimeout` trap.
- [ ] Listed five leak patterns.
- [ ] Ran a race test with `-race -count=100`.
- [ ] Explained why Postgres, not a Go mutex, wins the ControlPlane races.

Score: ___ / 5. Leak pattern I missed: ______________________________.

---

## Day 3 — HTTP and Postgres

- [ ] Drew the request lifecycle from socket to SQL and back.
- [ ] Named the server timeouts and what each prevents.
- [ ] Explained 201 vs 202 vs 400 vs 422 vs 409.
- [ ] Wrote the error envelope and the code→status map.
- [ ] Wrote the conditional UPDATE and the three-way disambiguation.
- [ ] Explained why a unique index beats a pre-check SELECT.
- [ ] Wrote the keyset query and named the index.
- [ ] Explained Read Committed + EvalPlanQual in two sentences.
- [ ] Ran one `EXPLAIN (ANALYZE, BUFFERS)` and read it.
- [ ] Named the three lock-order rules.

Score: ___ / 5. SQL fact I was shaky on: ______________________________.

---

## Day 4 — Messaging and ControlPlane

- [ ] Drew the architecture and create/update sequences from memory.
- [ ] Explained the outbox crash matrix without notes.
- [ ] Explained claim + publish + mark in one transaction.
- [ ] Explained publisher confirms vs `mandatory` returns.
- [ ] Explained inbox-first and the rank guard with three examples.
- [ ] Explained terminal outcome mapping and the guarded tenant update.
- [ ] Explained poison vs transient and the ack action.
- [ ] Named the DLQ settings and the `worker.tasks` gap.
- [ ] Ran the 18 “show me the line” prompts.
- [ ] Named 10 known limitations.

Score: ___ / 5. Mechanism I could not draw: ______________________________.

---

## Day 5 — Demo rehearsal

| Run | Target | Actual | What broke | Fix |
|---|---:|---:|---|---|
| 5-minute | 5:00 | | | |
| 10-minute | 10:00 | | | |
| 20-minute | 20:00 | | | |

- [ ] Preflight passes in under 5 minutes.
- [ ] Create → active → patch → active → delete → destroyed runs clean.
- [ ] Race demo: one winner / 49 conflicts; one 202 / 49 rejected.
- [ ] Duplicate and stale do not change the version.
- [ ] Poison increments the DLQ.
- [ ] Logs explain each outcome.
- [ ] Recovery lines rehearsed for stack/API/broker/worker failure.
- [ ] Backup recording/screenshots opened in a second window.
- [ ] I did not run `docker compose down -v` at the end.

Score: ___ / 5. Weakest transition: ______________________________.

---

## Day 6 — Mock and behavioral

- [ ] Full 45-minute mock completed without pauses.
- [ ] Scored below; top three fixes chosen.
- [ ] Six STAR outlines written.
- [ ] 90-second intro recorded and timed.
- [ ] Three questions to ask chosen.
- [ ] Final logistics checklist complete.
- [ ] Stopped by 18:00.

Mock total: ___ / 50. Top three fixes:

1. ______________________________
2. ______________________________
3. ______________________________

---

## Q&A bank scores

Run 20 random questions per day. Update the section counts.

| Section | First pass G/A/R | Final G/A/R | Target |
|---|---|---|---|
| A. Go fundamentals | | | no reds |
| B. Concurrency | | | no reds |
| C. HTTP/API | | | no reds |
| D. Postgres | | | no reds |
| E. Messaging/ControlPlane | | | no reds |
| F. System design | | | ≤ 1 amber |
| G. Testing/behavioral | | | ≤ 2 ambers |

Target before Thursday: **no reds in A–E; at most two ambers overall.**

---

## Mock scorecard

Score 1–5. Target: **4+ in correctness, depth, and tradeoffs; 3+ everywhere else.**

| Dimension | 1 | 3 | 5 | Score | Evidence / fix |
|---|---|---|---|---|---|
| Correctness | Wrong facts | Mostly right | Precise, names edge cases | | |
| Depth | Surface names | Explains how | Explains why + failure mode | | |
| Tradeoffs | One option | Names alternative | Names cost + when to switch | | |
| Structure | Rambles | Answers the question | Framework, then answer | | |
| Concurrency | Confuses races | Knows primitives | Picks the right boundary | | |
| DB | Basic SQL | Constraints/transactions | Isolation/locks/zero-rows | | |
| Messaging | Names queue | Outbox/inbox | Crash matrix + guarantees | | |
| Demo | Feature tour | Happy path | Failure proof + backup | | |
| Communication | Filler/too long | Clear | Concise, signposts, pauses | | |
| Behavioral | Vague | STAR-ish | Specific, measured, reflective | | |

For every 1–2, write **one** fix and a retest date. Do not fix ten things.

---

## Weak-spot bank

| # | Date | Topic | Question / symptom | Why I missed it | One-line fix | Retest | Status |
|---:|---|---|---|---|---|---|---|
| 1 | Sat 14 Nov | Day 1 Go core | Could not follow the dense file's what/why/how | First pass assumed Go context | Read day-1a L1–L19, say the Remember lines, retry the three drills | Sun 15 Nov | open |
| 2 | | | | | | | |
| 3 | | | | | | | |
| 4 | | | | | | | |
| 5 | | | | | | | |
| 6 | | | | | | | |
| 7 | | | | | | | |
| 8 | | | | | | | |
| 9 | | | | | | | |
| 10 | | | | | | | |

Status: **open / retested-green / still-red**. If a spot stays red after two retests, simplify your
explanation instead of adding detail.

---

## Demo readiness gates

Do not start the interview until all of these are true:

- [ ] Stack preflights in under 5 minutes; `/healthz` returns `{"status":"ok"}`.
- [ ] A create reaches `active` and the version goes 1 → 2.
- [ ] A patch reaches `active`; a stale patch gets 409.
- [ ] A delete reaches `destroyed`; recreate gets 409.
- [ ] Race demo passes (1 winner / 49 conflicts; 1 accepted / 49 rejected).
- [ ] Duplicate and stale leave the version unchanged.
- [ ] Poison lands in the DLQ and valid work continues.
- [ ] I can explain each result in one sentence.
- [ ] Backup recording/screenshots ready.
- [ ] I know the recovery line for each failure.
- [ ] I have not planned to debug live for more than 60 seconds.

---

## Final readiness — Wednesday night

### Knowledge

- [ ] Draw architecture + create/update sequences from memory.
- [ ] Explain 12 decisions with alternative + failure mode.
- [ ] Answer 20 random Q&A at 4/5.
- [ ] Explain the crash matrix without notes.
- [ ] Explain why the DB, not Go, is the concurrency arbiter.
- [ ] Name five known limitations.
- [ ] Explain the test-isolation flake and the fix.

### Demo

- [ ] 5/10/20-minute versions timed.
- [ ] Failure proofs run and explained.
- [ ] Recovery lines ready.

### Behavioral

- [ ] 90-second intro.
- [ ] Six STAR outlines with real metrics/placeholders filled.
- [ ] Three questions to ask.
- [ ] One honest, job-relevant failure story.

### Logistics

- [ ] Interview links/calendar invites confirmed.
- [ ] Mic/camera/screen share tested.
- [ ] Quiet room, water, charger, hotspot.
- [ ] Notifications off; Slack/Teams closed.
- [ ] Resume/project links open; repo access confirmed.
- [ ] Sleep. No late-night cramming.

---

## After each interview — 5-minute reflection

| Field | Notes |
|---|---|
| What went well (three) | |
| What I would change (one) | |
| Question I could not answer | |
| Follow-up to send | |
| Energy / sleep / setup issue | |

Do not spiral. One fix, then rest. The next round is a different conversation.

---

## If you only have two hours left

1. Draw architecture + both sequences (20 min).
2. Run the 5-minute demo twice (30 min).
3. Answer 20 Q&A (30 min).
4. Six STAR outlines, one minute each (15 min).
5. Cheat sheet + logistics (15 min).
6. Stop (10 min buffer).

Do not start a new book, a new project, or a refactor. The marginal value is negative.
