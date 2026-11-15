# Day 6 — Full mock, behavioral, and final checklist

**Goal:** simulate both rounds, find the three things that would actually cost you the offer, and stop.
No new topics after 18:00.

**You should leave today with:** a mock score, six STAR outlines, a 90-second "tell me about yourself,"
and a logistics checklist for both interview days.

---

## Timebox (120 min)

| Time | Block |
|---|---|
| 0:00–0:10 | Review the weak-spot bank |
| 0:10–0:55 | Full mock (45 min, no pauses) |
| 0:55–1:15 | Score and fix the top three |
| 1:15–1:35 | STAR stories + tell-me-about-yourself |
| 1:35–1:50 | Questions to ask + logistics |
| 1:50–2:00 | Final checklist; stop |

---

## 1. Full mock — 45 minutes

Do this out loud, timed, with no notes. Record it if you can stand it.

### Part A — rapid fire (10 min)

Pick 10 random questions from [`qna-grill.md`](qna-grill.md). Answer each in 45–60 seconds. If you
exceed 90 seconds, mark it red.

### Part B — coding (12 min)

Choose one:

1. Implement a bounded worker pool with `context`, `errgroup`, cancellation, and a test.
2. Implement `TaskUpdateAllowed` + `TenantOutcome` from scratch, with table tests.
3. Implement the outbox claim query + relay skeleton (claim, publish, mark, commit) in pseudocode/Go.
4. Implement a keyset pagination handler + SQL, including cursor validation.

Talk while you code. The interviewer is evaluating reasoning, not typing speed.

### Part C — demo (10 min)

Run the 5-minute version, then spend 5 minutes on deep-dive questions:

- Why outbox?
- Why `SKIP LOCKED`?
- What if the relay crashes after confirm?
- Why both inbox and rank guard?
- What is the biggest gap?

### Part D — system design mini (8 min)

"Design a tenant provisioning control plane. Start with the invariant, then the mechanism." Use the
8-step framework from [`README.md`](README.md). Stop at tradeoffs; do not try to finish the whole design.

### Part E — behavioral (5 min)

Answer two:

- "Tell me about a production issue you owned."
- "Tell me about a technical disagreement."

---

## 2. Scorecard

**Description.** The scorecard turns a mock interview into a prioritized fix list. It scores correctness, depth, tradeoffs, structure, concurrency, database, messaging, demo, communication, and behavioral answers from 1 to 5, with 4+ required in the core technical dimensions. For every 1 or 2, write one fix and retest it; do not try to fix ten things at once.

Score 1–5. Target: **4+ in correctness, depth, and tradeoffs; 3+ everywhere else.**

| Dimension | 1 | 3 | 5 | Score |
|---|---|---|---|---|
| Correctness | Wrong facts | Mostly right | Precise, names edge cases | |
| Depth | Surface names | Explains how | Explains why + failure mode | |
| Tradeoffs | One option | Names alternative | Names cost + when to switch | |
| Structure | Rambles | Answers the question | Framework, then answer | |
| Concurrency | Confuses races | Knows primitives | Picks the right boundary | |
| DB | Basic SQL | Constraints/transactions | Isolation/locks/zero-rows | |
| Messaging | Names queue | Outbox/inbox | Crash matrix + guarantees | |
| Demo | Feature tour | Happy path | Failure proof + backup | |
| Communication | Filler/too long | Clear | Concise, signposts, pauses | |
| Behavioral | Vague | STAR-ish | Specific, measured, reflective | |

Log scores in [`progress.md`](progress.md). For every 1–2, write **one** fix and retest tomorrow. Do not
try to fix ten things.

---

## 3. Behavioral — six stories, not twenty

**Description.** Prepare six reusable stories instead of memorizing answers to every possible prompt. Each story uses STAR (Situation, Task, Action, Result) and ends with a one-line reflection about what you would do differently. The same story can answer several prompts by changing the emphasis, so the set should cover ownership, debugging, disagreement, failure, influence, and delivery under constraint.

You need six reusable stories. Each should be 90–120 seconds. Use **STAR**, then add a one-line
reflection: what you would do differently.

### Story 1 — Ownership / ambiguity

Prompt: "Tell me about a time you owned something ambiguous."

Outline:

- Situation: a vague request or a system with no clear owner.
- Task: what you decided needed to happen; what you explicitly did not do.
- Action: how you scoped it, who you consulted, what you shipped first.
- Result: measurable outcome + what changed in the team.
- Reflection: what you would clarify earlier.

**ControlPlane angle:** you turned a broad "build a control plane" exercise into named invariants,
state machines, an outbox, and a test that proves the lifecycle. You wrote the design doc before the
code and kept a decisions table.

### Story 2 — Hardest bug / debugging

Prompt: "Tell me about the hardest bug you debugged."

Outline:

- Symptom: what users/systems saw.
- Investigation: hypotheses, instrumentation, what you ruled out.
- Root cause: the actual mechanism, not "a race."
- Fix: the smallest correct fix + a regression test.
- Reflection: what systemic change prevents the class of bug.

**ControlPlane angle:** the lifecycle-test flake was not just a broker collision; the test database was
shared with the running compose relay, so the relay could claim test outbox rows. The fix separated the
test database/vhost and serialized packages with an advisory lock. The lesson: isolate the test
environment, not just the queue name.

### Story 3 — Disagreement / tradeoff

Prompt: "Tell me about a disagreement about a technical decision."

Outline:

- The two options and the stakes.
- Why the other person disagreed (steelman it).
- The evidence you used (latency, correctness, cost, operational burden).
- The decision and how you committed even if it was not your first choice.
- Reflection: how you would decide faster.

**ControlPlane angle:** outbox vs publish-in-request; RabbitMQ vs Kafka; rank guard vs a from→to table.
The pattern: name the failure mode, choose the simplest mechanism that protects the invariant, write
down the cost.

### Story 4 — Failure / incident

Prompt: "Tell me about a time you failed or caused an incident."

Outline:

- What happened, in concrete terms.
- Your contribution (own it; no passive voice).
- Immediate mitigation.
- Root cause and durable fix.
- What you changed in your process.

Do not use "we" to hide your role. Do not pick a failure that suggests you cannot do the job. Pick one
where the fix changed your behavior.

### Story 5 — Influence / leadership without authority

Prompt: "Tell me about a time you influenced a team or raised the bar."

Outline:

- The gap (quality, performance, architecture, on-call).
- Who needed convincing and why.
- The artifact: RFC, demo, checklist, migration plan, mentorship.
- Adoption metric.
- Reflection: what made it stick.

**Angles from your work:** a design doc/decisions table, a test isolation fix, a CI quality gate, a
performance budget, a migration playbook, mentoring a junior. Use the one with a real metric.

### Story 6 — Delivery under constraint

Prompt: "Tell me about a tight deadline."

Outline:

- The constraint (time, headcount, dependency).
- How you cut scope without cutting the invariant.
- What you deferred and how you communicated it.
- Result and debt.
- Reflection: what you would sequence differently.

**ControlPlane angle:** a take-home with a fixed brief; you prioritized the correctness core, documented
non-goals, and named the hardening gaps instead of pretending they did not exist.

---

## 4. Tell me about yourself — 90 seconds

**Description.** The intro is a 90-second argument in four beats: what you do now, the proof that you can do this role, why this role, and a handoff inviting the interviewer to go deeper. It is not a chronological resume and not a list of technologies. The proof beat should name one system and the invariants it enforces.

Use four beats:

1. **Now:** "I'm a senior engineer with 13 years across product engineering, currently building
   production-shaped Go services with Postgres and RabbitMQ."
2. **Proof:** "My most relevant recent work is a tenant provisioning control plane: transactional
   outbox, idempotent consumer, optimistic locking, and a lifecycle test against real Postgres and
   RabbitMQ."
3. **Why this role:** "I'm looking for a role where correctness and system design matter, not just
   shipping endpoints."
4. **Close:** "I can go deeper on the control plane, the concurrency model, or the database design —
   whichever is most useful."

Do not recite your resume chronologically. Do not say "I'm a passionate developer." Do not list every
technology.

### Why Go?

**Description.** The answer should connect Go's properties to the workload: I/O-bound services with explicit concurrency, cancellation, and a small deployment footprint. It should also avoid overclaiming: the database enforces the cross-process invariants, and Go's job is bounded concurrency and clean shutdown, not replacing database constraints.

> "The service is I/O-bound and concurrency-heavy: HTTP, Postgres, RabbitMQ, reconnect loops, workers.
> Go's explicit concurrency and small deployment footprint fit that. But the correctness mechanism is
> Postgres, not goroutines — I use Go to issue the right statements with bounded concurrency and to stop
> cleanly."

### Why this role/company?

Fill three specifics: the problem domain, the scale/constraints, and the team's engineering bar. If you
do not know enough, say what you want to learn and ask a question. Do not generic-flatter.

---

## 5. Questions to ask

**Description.** The questions you ask are part of the interview: they signal whether you think about correctness, operations, and engineering process. Pick three or four that reveal the system's hardest invariant, its delivery guarantees, its on-call reality, and how decisions are made. Avoid perks and questions answered on the website.

Pick 3–4. The questions should signal senior judgment, not perks.

### About the system

- "What is the hardest correctness invariant in the system today, and how is it enforced?"
- "Where does the team use async messaging, and what delivery guarantee do you actually rely on?"
- "What does your on-call look like for this service? Which alerts fire most?"
- "What is the biggest source of technical debt, and is it funded?"

### About the role

- "What would success look like in the first 90 days?"
- "How do decisions get made here — RFC, design review, or owner decides?"
- "What is the split between new build and maintenance for this team?"
- "How do you handle cross-team API contracts and breaking changes?"

### About growth

- "Who are the strongest engineers here, and what do they do differently?"
- "How does the team review design docs and production readiness?"
- "What is one thing you wish the team did more of?"

Avoid: salary/remote in the first round, "what does the company do?", questions answered on the website.

---

## 6. Interview-day routines

**Description.** The day-of routine is deliberately narrow: review, rehearse one architecture drawing, test the equipment, and stop. Cramming new topics the morning of the interview adds noise without adding recall. During the interview, clarify the question, state assumptions, think aloud in the requirements → mechanism → failure → tradeoff order, and ask for a hint before going silent.

### Thu 19 Nov — technical round

**Morning (30 min):**

- Read [`cheatsheets.md`](cheatsheets.md) once.
- Answer 5 random Q&A.
- Draw the ControlPlane architecture once.
- Stop. Do not cram new topics.

**Before the call (10 min):**

- Water, bathroom, quiet room, notifications off.
- Terminal + editor open; no Docker needed unless asked.
- One-page cheat sheet visible but not readable on camera.
- Test mic/camera/screen share.

**During:**

- Repeat/clarify the question; state assumptions.
- Think aloud in structure: requirements → mechanism → failure → tradeoff.
- If stuck, say what you know and what you would look up. Do not bluff.
- Ask for a hint before going silent for 30 seconds.

**After:** write three things you did well and one fix. Do not spiral.

### Fri 20 Nov — demo round

**Morning (30 min):**

- Preflight the stack; run the 5-minute version once.
- Check health, RabbitMQ UI, backup recording.
- Stop. Do not refactor code.

**During:**

- Ask how much time and whether they want the short or full version.
- State the plan: "I'll cover the problem and architecture, then show the happy path and break it on
  purpose."
- Keep the timer visible; leave 2 minutes for questions.
- If something breaks, use the recovery line and move on.

**After:** send a short thank-you with one relevant link (repo/design doc) if appropriate.

---

## 7. Final checklist

### Knowledge

- [ ] Draw architecture + create/update sequences from memory.
- [ ] Explain 12 decisions with alternative + failure mode.
- [ ] Answer 20 random Q&A at 4/5.
- [ ] Explain the crash matrix without notes.
- [ ] Explain why the DB, not Go, is the concurrency arbiter.
- [ ] Name five known limitations.

### Demo

- [ ] Stack preflights in under 5 minutes.
- [ ] 5-minute and 10-minute versions timed.
- [ ] Duplicate/stale/poison run and explained.
- [ ] Backup recording/screenshots ready.
- [ ] Recovery lines for Docker/API/broker/worker.

### Behavioral

- [ ] 90-second intro.
- [ ] Six STAR outlines with real metrics/placeholders filled.
- [ ] Three questions to ask.
- [ ] One failure story that is honest and job-relevant.

### Logistics

- [ ] Interview links/calendar invites confirmed.
- [ ] Mic/camera/screen share tested.
- [ ] Quiet room, water, charger, backup internet (hotspot).
- [ ] Notifications off; Slack/Teams closed.
- [ ] Resume/project links open; GitHub repo public or access confirmed.
- [ ] Sleep. No late-night cramming.

---

## 8. If you only have two hours left

Priority order:

1. Draw the architecture and both sequences (20 min).
2. Run the 5-minute demo twice (30 min).
3. Answer 20 Q&A (30 min).
4. Six STAR outlines, one minute each (15 min).
5. Cheat sheet + logistics (15 min).
6. Stop (10 min buffer).

Do not start a new book, a new project, or a refactor. The marginal value is negative.

---

## 9. End-of-day checkpoint

- [ ] Mock score logged; top three fixes chosen.
- [ ] Six STAR outlines written, not memorized word-for-word.
- [ ] 90-second intro recorded and timed.
- [ ] Three questions to ask chosen.
- [ ] Demo preflight passed.
- [ ] Tomorrow's clothes/room/links ready.
- [ ] You stop by 18:00.

**If you only remember five things:** structure, invariant, failure mode, tradeoff, honesty.

**Next:** interview day. You are prepared. Do not add scope.
