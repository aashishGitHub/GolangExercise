# Event Ticket Booking Platform — Spoken Answer Script

**Target: 15–20 minutes. ~150 words/minute. Pause at every `---`.**
Timings are cumulative. Say the words in **bold** — they are the terms the interviewer is listening for.

---

## 0:00 — 1:30 | Scope the problem first

"Before I draw anything, let me lock the scope.

I'll assume one venue-based ticketing platform. Concerts and movies. Ten million registered users. The hard case is not steady-state traffic. The hard case is an **on-sale spike** — one popular artist, fifty thousand seats, five hundred thousand people hitting the page in the same sixty seconds.

That single number drives the whole design, so let me state the key asymmetry up front.

**Reads are unbounded. Writes are bounded by inventory.**

Five hundred thousand people can look at the seat map. Only fifty thousand seats exist. So at most fifty thousand successful writes will ever happen. My job is to absorb the read storm cheaply, and to make the small number of writes strictly correct.

Out of scope for this hour: dynamic pricing, resale marketplace, and the recommendation engine.

Functional requirements: browse events, view a live seat map, hold seats, pay, get a QR ticket, get reminders.

Non-functional: seat allocation must be **linearizable** — no double-booking, ever. Seat map read can be **eventually consistent** — a two-second stale view is acceptable. Availability over consistency on the read path, consistency over availability on the write path."

---

## 1:30 — 2:45 | Capacity math

"Quick numbers so the components are justified, not guessed.

Peak read: five hundred thousand concurrent viewers, seat map refresh every two seconds. That is roughly **250,000 requests per second** if I poll. That is why I will not poll — I'll push deltas over **WebSocket** instead.

Seat map payload: fifty thousand seats. If I send a JSON object per seat, that is several megabytes. Unacceptable. Instead I split it.

**Static layout** — seat coordinates, row, section, tier. This never changes for a venue. I version it and serve it from **CloudFront**, immutable, cache forever.

**Dynamic availability** — each seat has three states: free, held, sold. Two bits. Fifty thousand seats is 12.5 kilobytes as a **bitset**, and under two kilobytes after gzip. I send that bitset once on connect, then only deltas after that.

Writes: fifty thousand seats over a ten-minute on-sale is under a hundred writes per second sustained. Tiny. But every one of them is contended."

---

## 2:45 — 4:30 | High-level architecture

"Here is the shape.

Clients hit **CloudFront**, then **API Gateway**. Behind that, a **BFF** — Backend for Frontend. The BFF handles **Cognito** token validation, session, and response shaping. Advantage: the mobile app and web app need different payload shapes, and the BFF keeps that concern out of the domain services.

Behind the BFF, four services:

- **Catalog Service** — events, venues, static seat layout. Read-heavy, cache-friendly.
- **Inventory Service** — seat state. This is the only service that writes seat state. Single writer by design.
- **Order Service** — orchestrates hold, payment, confirmation. This is the **saga orchestrator**.
- **Ticketing Service** — QR generation, ticket delivery, wallet passes.

For the spike, one thing sits in front of everything: a **virtual waiting room**. On-sale opens, users get a signed queue token, and I admit them at a controlled rate — say two thousand per second. This is admission control. The architectural advantage is that it converts an unbounded thundering herd into a bounded, predictable arrival rate. Every downstream capacity decision becomes provable instead of hopeful.

Event backbone is **EventBridge** for domain events, and **SQS** for work queues with **DLQs** on every consumer."

---

## 4:30 — 8:00 | Seat locking — the core of the problem

"This is the heart of the design, so I'll go slow.

A hold is a **conditional write**, not a lock in the mutex sense. There is no lock server. There is a row, and a compare-and-set on that row.

I'll use **DynamoDB** as the source of truth for seat state. Partition key is `event_id`, sort key is `seat_id`. That gives me per-seat item-level isolation and it scales by event.

Each item has: `status`, `held_by`, `hold_expires_at`, and a `version` number.

To place a hold, I do a single `UpdateItem` with a **condition expression**:

> set status to HELD, held_by to this user, hold_expires_at to now plus five minutes —
> **only if** status is FREE, **or** status is HELD and hold_expires_at is less than now.

One atomic operation. If two users race, DynamoDB serializes them at the item level. One gets a 200. The other gets `ConditionalCheckFailedException` and I return 409 Conflict. No double-booking is structurally possible.

Now the detail most people miss.

I am **not** relying on DynamoDB **TTL** for correctness. DynamoDB TTL is a background reaper. It deletes lazily, sometimes hours late. If I trusted TTL to free a seat, a seat could stay locked long after expiry.

Instead, expiry is **read-time**. The condition expression treats any hold with `hold_expires_at < now` as already free. The seat self-heals on the next contender. TTL is only a janitor for storage cleanup, never a correctness mechanism.

For multi-seat holds — a user wants four seats together — I use **TransactWriteItems**. All four succeed or all four fail. That gives me **atomicity across seats**, which a per-seat loop would not. The tradeoff is that transactions cost double the write units and fail hard under contention, so I retry with **jittered exponential backoff**.

One more layer. For read speed only, I keep a **Redis** copy of the availability bitset. Redis is a **cache**, never the arbiter. If Redis and DynamoDB disagree, DynamoDB wins. I say this explicitly because Redis-as-a-lock via Redlock has known correctness issues under partition and clock skew, and I don't want seat correctness resting on it."

---

## 8:00 — 9:30 | Releasing unconfirmed holds — the event-driven part

"The question asks specifically about releasing holds after timeout. Two mechanisms, layered.

**Mechanism one — passive expiry.** Already covered. The condition expression makes an expired hold invisible. This is the correctness guarantee. It costs nothing and it cannot fail.

**Mechanism two — active release.** Correctness is not enough. If a seat frees up silently, nobody watching the seat map knows. So I need an event.

When a hold is created, the Order Service schedules a release check using **EventBridge Scheduler** — a one-shot schedule at hold expiry plus a few seconds. When it fires, it invokes a **Lambda** that attempts the release, again as a conditional write: free this seat only if it is still held by that same hold ID. That condition makes the operation **idempotent**. If the user already paid, the hold ID no longer matches, and the release is a no-op. No risk of releasing a paid seat.

On successful release, Inventory publishes a `SeatReleased` event to EventBridge. A fan-out **Lambda** pushes the delta to every WebSocket connection subscribed to that event, via **API Gateway WebSocket** and a connection registry in DynamoDB.

Advantage of this split: passive expiry guarantees correctness even if the entire scheduler is down. Active release guarantees freshness. Correctness never depends on a timer firing."

---

## 9:30 — 11:00 | Payment succeeded but seat allocation failed

"This is the ugly case, and I want to name the pattern before I solve it.

This is a **distributed transaction across two systems I cannot two-phase-commit** — my inventory and the payment provider. So I use a **Saga** with **compensating transactions**, orchestrated by the Order Service.

The happy path, in order:

1. Hold seats — conditional write.
2. Create an order in PENDING with an **idempotency key**.
3. Charge the payment provider, passing that same idempotency key so a retry never double-charges.
4. Confirm the seats — conditional write, only if still held by this order.
5. Issue the ticket.

Step four is where it can break. If the hold expired between charge and confirm, the conditional write fails and I have money but no seat.

Compensation, in priority order:

First, attempt **automatic re-allocation** — find equivalent seats in the same tier and price. Most users prefer a seat two rows back over a refund. If that succeeds, notify and continue.

If not, **auto-refund** through the payment provider, mark the order COMPENSATED, and notify the user with an apology and a credit.

Every step writes to an **outbox table** in the same transaction as the state change, and a relay publishes from the outbox. That is the **Transactional Outbox pattern**. Its advantage: I never have the failure mode of 'state changed but event lost,' which is the classic source of stuck sagas.

And I want to prevent this case, not just handle it. So I extend the hold at the moment payment is initiated — bump `hold_expires_at` to payment timeout plus a buffer. The race narrows to almost nothing.

Note what I am **not** using here. A **circuit breaker** is the wrong tool. Circuit breakers protect against a failing downstream dependency. This is not a failing dependency — this is a lost race with a correct outcome. Different problem, different pattern."

---

## 11:00 — 12:30 | Data model and CQRS

"Let me be precise about **CQRS** — Command Query Responsibility Segregation — because it is genuinely the right fit here, for the reason I gave at the start.

**Command side.** DynamoDB, strongly consistent, conditional writes. Small volume, absolute correctness. Orders and payments go to **Aurora PostgreSQL**, because they need relational integrity, joins for reporting, and a financial audit trail.

**Query side.** DynamoDB Streams feed a projector Lambda that maintains a denormalized availability bitset in **Redis**, plus an event catalog projection in **OpenSearch**.

The advantage of this split is that read scaling and write correctness stop competing. I can add read replicas and cache layers for the 250,000 read RPS without ever weakening the write path. And the read model can be shaped exactly for the seat map, instead of forcing the UI to reassemble it from normalized tables.

The cost, and I'll say it plainly: **eventual consistency on the read path**. A user may see a seat as free for one or two seconds after someone else took it. I handle that in the UX, not the architecture — the seat map is advisory, and the conditional write is the truth. When the click loses the race, the UI says 'just taken' and highlights the nearest equivalent seat. Optimistic UI, server-authoritative."

---

## 12:30 — 13:45 | Search and caching

"Fuzzy search, so a misspelled artist name still returns results. **OpenSearch** — managed Elasticsearch. I use a `multi_match` query with **fuzziness AUTO**, which is edit distance based on term length, plus an **edge n-gram analyzer** for type-ahead. I boost by event date proximity and popularity so the ranking is useful, not just matching.

Caching, on the read path specifically. Three tiers, and I want to be exact about what goes in each:

**CloudFront** — static venue seat layout, event images, the JS bundle. Immutable and versioned, so cache-control is one year.

**Redis / ElastiCache** — the availability bitset per event, event metadata, and price tiers. Short TTL, updated by the projector, invalidated on write.

**Client** — the static layout in IndexedDB, so a returning user renders the map instantly.

I'll flag one thing: user behavior and recommendation data is a legitimate caching concern, but it is not on this read path. That belongs in an analytics pipeline — **Kinesis Data Firehose** to S3, then batch. Keeping it out of the hot path matters, because analytics traffic must never contend with seat availability during an on-sale."

---

## 13:45 — 16:30 | Frontend: the 30,000-seat map

"Now the frontend, which I think is the most underrated part of this problem.

Thirty thousand seats. A DOM node per seat is thirty thousand nodes — layout thrashing, gigabytes of memory, unusable on mobile. So DOM is out for rendering.

I render with **Canvas**, and for the largest venues **WebGL** with instanced rendering, so all thirty thousand seats draw in a single draw call.

Four techniques:

**Viewport culling.** I keep seats in a **quadtree**. On each frame I query only the visible rectangle. At default zoom that's a few hundred seats, not thirty thousand.

**Level of detail.** Zoomed out, I don't draw seats at all — I draw section polygons with an availability heat colour. Zoom in, seats appear. Zoom in further, seat numbers appear. Rendering cost stays flat regardless of zoom.

**Hit testing via the quadtree**, not by iterating seats. Click to seat lookup is O(log n).

**Off the main thread.** The bitset diffing and quadtree updates run in a **Web Worker**. `OffscreenCanvas` lets the worker paint. The main thread stays free, so scroll and zoom stay at sixty frames per second. All rendering is driven by `requestAnimationFrame`, and pan/zoom is coalesced so I never render faster than the display.

Real-time updates arrive as WebSocket deltas — seat ID plus new state, a handful of bytes. I batch them per animation frame and repaint only dirty tiles.

---

Now **accessibility**, and I want to lead with the problem rather than the solution.

**A canvas is invisible to a screen reader.** It is a single opaque element. If I stop at canvas, I have built a system that a blind user cannot buy a ticket from. In many jurisdictions that is also a legal exposure, not just a UX gap.

So the canvas is a visual **enhancement over an accessible core**, not the interface itself.

I render a parallel, visually hidden DOM tree. Structure is `section → row → seat`, as nested lists with `role="grid"`. Each seat is a real focusable button with an accessible name like 'Section A, row 12, seat 4, one hundred and twenty dollars, available.'

But thirty thousand focusable buttons is a terrible experience even if technically conformant. So I make the hierarchy the navigation. Arrow keys move between sections. Enter drills into rows. Arrow keys move seat to seat. Escape moves back up. The user traverses a tree, not a flat list.

I also provide a **'best available' path** — pick a quantity, pick a price range, get seats assigned. For a sighted user that is a convenience. For a screen reader user it is often the fastest route to a ticket, and it makes the whole flow usable without ever touching the map.

Lock and availability changes announce through an `aria-live="polite"` region, debounced — otherwise a busy on-sale would produce constant chatter. Errors, like losing a seat race, go to `aria-live="assertive"`.

Beyond screen readers: seat state never relies on colour alone — held and sold get distinct shape and pattern fills, which also covers colour-vision deficiency. Focus indicators are visible on the canvas, driven by the focus position of the hidden DOM. And the countdown timer respects `prefers-reduced-motion`, plus it is announced at intervals rather than ticking every second."

---

## 16:30 — 18:00 | Checkout, QR, and reminders

"**Checkout.** The hold timer is the dominant UX element — visible, persistent, and it must be **server-authoritative**. The client displays a countdown, but the server owns expiry. Never trust the client clock.

The flow is a single page, not a wizard: seats, offers, payment, confirm. Every step is resumable, because a five-minute hold and a multi-step wizard fight each other. Offers apply optimistically with server revalidation at charge time.

**QR codes.** The critical thing is that the QR must not contain a guessable ticket ID. It carries a **signed payload** — ticket ID, event, seat, issued-at — signed with **HMAC**, keys in **KMS**. The gate scanner verifies the signature offline, then checks a redemption list for double-entry.

For high-value events I'd use a **rotating code** — a TOTP-style value that changes every thirty seconds inside the app. That defeats screenshot resale, which static QR cannot.

Generation is asynchronous: `OrderConfirmed` event fires a **Lambda**, which renders the QR, writes it to **S3**, and delivers via **presigned URL** with a short expiry. The URL is short-lived so a leaked link is not a leaked ticket. Also generate **Apple Wallet** and **Google Wallet** passes here — they handle offline access and lock-screen surfacing better than any app I could build.

**Reminders.** **EventBridge Scheduler**, one-shot schedules at 24 hours and 2 hours before the event. Fan out through **SNS** to email, SMS, and push. Preferences per channel, honoured per user."

---

## 18:00 — 19:30 | Failure modes and operations

"Briefly, what breaks and how I know.

**Redis dies.** Read path degrades to DynamoDB directly. Slower, higher cost, still correct — because Redis was never authoritative. This is the payoff for that earlier decision.

**WebSocket layer dies.** Client falls back to polling at a longer interval with jitter. Degraded freshness, not an outage.

**Payment provider dies.** Circuit breaker — this is where it actually belongs. Trip the breaker, stop taking new checkouts, show a clear message, keep browsing alive.

**Hot partition on one event.** A single mega-event can hit DynamoDB partition limits. Mitigation is the waiting room throttling arrivals, plus write sharding for the availability aggregate.

**Observability.** The metrics I'd actually put on the wall during an on-sale: hold conflict rate, hold-to-purchase conversion, p99 on the conditional write, WebSocket delta lag, and saga compensation count. That last one is the one that should always be near zero — if it climbs, my hold extension window is too tight.

Load testing before every major on-sale, not just at launch."

---

## 19:30 — 20:00 | Close on tradeoffs

"To summarize the three decisions I'd defend hardest.

One — **conditional writes, not locks**. No lock service to fail, no lease to expire wrongly, and correctness holds even if every timer in the system stops.

Two — **CQRS**, because reads are unbounded and writes are bounded by inventory. Those two need different consistency models, and forcing them into one path means either a slow read or an unsafe write.

Three — **accessible DOM under the canvas**. The canvas is the optimization. The DOM is the product.

What I'd revisit with more time: whether **Kinesis** with per-event partitioning gives me a cleaner ordered audit stream for reconciliation than DynamoDB Streams. And whether the waiting room should be a queue or a lottery — a lottery is fairer for a hyped on-sale, and fairness is a product decision that changes the architecture."

---

## Delivery notes

- **Draw as you talk.** Do not talk for three minutes with a blank screen.
- **The interviewer will interrupt.** That is good. Answer the question, then say "returning to the flow" and resume.
- If asked something you don't know: **"I haven't used that directly. Here's what I'd reason from, and here's how I'd verify it."** Never bluff.
- Say the term, then define it in one sentence. "CQRS — Command Query Responsibility Segregation — separate the write model from the read model."
- **Every choice gets a tradeoff.** "I chose X. The cost is Y. I accept it because Z."