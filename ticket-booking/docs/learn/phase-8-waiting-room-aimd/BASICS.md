# Phase 8 — Virtual Waiting Room and AIMD: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Scope: [MILESTONES.md, Phase 8](../../../MILESTONES.md). Theory: [deep-dive.md](../../deep-dive.md) section 7 (thundering herd, waiting room, closed-loop admission).

> The browser has a thin client for this: [web/src/api/queue.ts](../../../web/src/api/queue.ts) (`joinQueue`, `waitForAdmission`). There is no waiting-room screen with a live position. After 30 seconds of waiting it shows an error instead.

1. **thundering herd** — Many clients hit the same resource at the same instant, for example at on-sale time. The server overloads, requests time out, clients retry, and the load grows. Analogy: a whole stadium crowd rushing one gate. See [deep-dive.md](../../deep-dive.md) section 7. In this project: this is the problem Phase 8 exists to soften.

2. **waiting room** — A line in front of the booking system. People join the line first. They are let in at a controlled speed. Analogy: a bouncer with a clicker at a club door. In this project: [internal/waitingroom/queue.go](../../../internal/waitingroom/queue.go).

3. **arrival order (ZSET)** — A Redis sorted set holds the line. Each person is added with the arrival time in microseconds as their score. Their rank is their place in line. Analogy: numbered tickets sorted by time stamp. In this project: `Queue.Join` (key `{event:E}:q`).

4. **ZADD NX** — "Add only if not already there". If you refresh the page and join again, you keep your original place. You can never jump ahead by retrying. Analogy: you cannot get a better ticket number by re-entering the shop. In this project: `Join` in [internal/waitingroom/queue.go](../../../internal/waitingroom/queue.go), and `TestQueue_RejoinKeepsOriginalPlaceInLine`.

5. **cursor** — A single number per event in Redis (`{event:E}:cursor`). Anyone whose rank is below the cursor is admitted. The cursor only moves forward. Analogy: the "now serving" sign. In this project: `Queue.Join` reads it; `Controller.Tick` advances it.

6. **admission rate** — How many people per second are let in. Here it is stored as a number in Redis (`{event:E}:rate`). Each second the cursor moves forward by that many places. This is a counter-style limiter, not a classic token bucket (a bucket that refills with tokens that requests spend). Analogy: a turnstile that clicks N times per second. In this project: `Controller.Tick` in [internal/waitingroom/controller.go](../../../internal/waitingroom/controller.go).

7. **AIMD** — Additive Increase, Multiplicative Decrease. When things look healthy, raise the rate by a fixed small amount (here +5 per second). When anything looks bad, cut the rate in half. Slow to grow, fast to back off. TCP congestion control uses the same idea. Analogy: easing onto the accelerator, but stamping on the brake. In this project: constants `AdditiveStep` and the `rate*0.5` line in [internal/waitingroom/controller.go](../../../internal/waitingroom/controller.go).

8. **closed loop / backpressure** — The controller measures how stressed the booking system is and feeds that back into the admission rate. The system pushes back on the line. Analogy: a thermostat. In this project: `Controller.Tick` reads three signals, below.

9. **the three signals** — Hold latency p99 above 500 ms, database pool utilisation above 80%, or hold error rate above 1%. Any one "red" signal triggers the halving. All green triggers +5. In this project: constants `LatencyRedThreshold`, `PoolRedThreshold`, `ErrorRateRedThreshold` in [internal/waitingroom/controller.go](../../../internal/waitingroom/controller.go).

10. **p99** — The latency that 99 out of 100 requests beat. It shows the slow tail that averages hide. Analogy: "99 of 100 parcels arrive within this many days". In this project: `HoldMetrics.P99` in [internal/waitingroom/metrics.go](../../../internal/waitingroom/metrics.go).

11. **ring buffer (metrics)** — A fixed array that overwrites its oldest entry when full. `HoldMetrics` keeps the last 200 hold outcomes. It counts calls, not seconds. Old results are forgotten as new ones arrive. Analogy: a notebook with 200 lines that you overwrite from the top. In this project: `NewHoldMetrics(200)` in [internal/wiring/wiring.go](../../../internal/wiring/wiring.go).

12. **pool utilisation** — Acquired connections divided by the maximum connections in the Postgres pool. High means the database is close to running out of connections. Analogy: how many checkout lanes are busy. In this project: `c.pool.Stat()` in `Tick`.

13. **system error vs domain error** — A lost seat race (409) is normal and is NOT counted as an error for AIMD. Only unexpected failures (the 500 case) count. Otherwise a popular seat would slow the whole line. In this project: `isSystemError` in [internal/httpapi/holds.go](../../../internal/httpapi/holds.go).

14. **admission token** — A signed ticket the server gives once you pass the cursor. You must send it in the `X-Admission-Token` header to create a hold. Shape: `v1.<base64 payload>.<base64 signature>`. Analogy: a wristband at the door. In this project: [internal/waitingroom/token.go](../../../internal/waitingroom/token.go).

15. **HMAC** — A signature made from the message plus a secret key. Only someone with the key can make or check it. Any edit breaks it. Analogy: a wax seal only the server owns. In this project: `sign` and `VerifyToken` (comparison uses `hmac.Equal`, constant time).

16. **bound to the user** — The token carries your Cognito `sub` (your user id). If another user sends it, verification fails. So tokens cannot be traded or shared. Analogy: a wristband with your name on it. In this project: `ErrSubMismatch` in [internal/waitingroom/token.go](../../../internal/waitingroom/token.go).

17. **token lifetime** — A token lasts 15 minutes (`tokenTTL`). That is meant to cover a full checkout without going back in line. In this project: [internal/waitingroom/token.go](../../../internal/waitingroom/token.go).

18. **middleware** — Code that runs before the real handler and can stop the request. `RequireAdmission` returns 403 if the token is missing, forged, for the wrong user or event, or expired. It guards `POST .../holds` only. Analogy: a security guard before the stairs. In this project: [internal/waitingroom/middleware.go](../../../internal/waitingroom/middleware.go).

19. **202 vs 200 on join** — `POST /events/{id}/queue` returns 202 with position and ETA while you wait, and 200 with the token once you are admitted. The client polls again after `pollAfterMs` (2000). In this project: [internal/httpapi/waitingroom.go](../../../internal/httpapi/waitingroom.go).

20. **audit row per tick** — Every second, for every event, the controller writes one row to `waiting_room_audit`: rate, cursor, p99, pool utilisation, error rate, and which signals were red. That lets you plot the loop from real data. Analogy: a flight recorder. In this project: [migrations/000009_waiting_room_audit.up.sql](../../../migrations/000009_waiting_room_audit.up.sql).

21. **runs inside the server** — The AIMD loop is started by `wiring.New` as a goroutine in the same process that serves hold requests. The latency and pool numbers are only meaningful from that process. MILESTONES calls this a real gap for several replicas. In this project: `runAIMDLoop` in [internal/wiring/wiring.go](../../../internal/wiring/wiring.go).
