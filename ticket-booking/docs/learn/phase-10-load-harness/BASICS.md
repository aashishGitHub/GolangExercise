# Phase 10 — Load Harness: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 10](../../../MILESTONES.md)

> All numbers below come from [MILESTONES.md](../../../MILESTONES.md). They were measured on one developer laptop. The harness prints them as "a relative regression baseline, not a production capacity claim". Do not quote them as capacity.

1. **Load test** — a program that acts like many users at the same time against the real running app. You learn how the app behaves when it is busy, not just when one person uses it. Think of a stress test where 20 people walk onto a bridge at once.
   In this project: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

2. **Virtual user (worker)** — one goroutine (a lightweight Go thread) that plays one customer. It runs the full journey, then starts the next one. Think of an actor who plays the same scene again and again.
   In this project: the `-workers` flag and the worker loop in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

3. **Journey** — the whole path of one customer: join the queue, get a seat hold, create an order, wait until the order is `TICKETED`. It goes over real HTTP, not function calls. It is like testing a shop by walking in, picking, paying and leaving.
   In this project: `journeyRunner.run` in [scripts/loadtest/journey.go](../../../scripts/loadtest/journey.go)

4. **Ramp-up** — workers start one after another over a time window, not all in the same instant. This avoids a sudden spike at second zero. Think of a crowd entering a stadium through the gate, not through the roof.
   In this project: the `-ramp-up` flag, `startDelay` in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

5. **Latency** — how long one request takes from send to answer. Measured in milliseconds (ms, one thousandth of a second). It is like the time between ordering coffee and getting it.
   In this project: `HoldMs`, `OrderMs`, `TotalMs` in `Result`, [scripts/loadtest/journey.go](../../../scripts/loadtest/journey.go)

6. **Percentiles (p50, p95, p99)** — sort all latencies from fastest to slowest. p50 is the middle one. p95 is the value that 95 out of 100 requests beat. p99 shows the slow tail. An average hides the slow ones, like saying the mean wait at a queue is fine while a few people wait an hour.
   In this project: `printReport` in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

7. **Throughput** — how much work finishes per unit of time, for example journeys per second. The harness prints the total number of journeys for the run, not a per-second rate. It is like cars passing a toll booth per minute.
   In this project: `total journeys:` line in `printReport`, [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

8. **Contention** — many users fighting for the same thing. Here the thing is a seat. More contention means more conflicts. It is like ten people reaching for one last doughnut.
   In this project: the `-contention` flag (`uniform` or `hot`) in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

9. **Uniform vs hot scenario** — `uniform` picks a random seat out of 30,000 for each journey. `hot` makes every worker try the same seat (`-hot-ordinal`). Uniform shows everyday traffic. Hot shows the worst-case race. It is like a food court versus one popular stall.
   In this project: `pickSeat` in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

10. **Seat ordinal** — a number that names a seat in the venue, used by the client instead of the internal seat id. It is like a row-and-seat label on a theatre chair.
    In this project: `acquireHold` sends `seatOrdinals` in [scripts/loadtest/journey.go](../../../scripts/loadtest/journey.go)

11. **Conflict rate** — the share of journeys that got "seat taken" (HTTP 409). A conflict is not an error. It is the system correctly saying "someone else got there first". It is like a polite "sorry, taken" at a booking desk.
    In this project: `OutcomeSeatTaken` in [scripts/loadtest/journey.go](../../../scripts/loadtest/journey.go)

12. **Conversion** — the share of journeys that ended with a ticket. "Hold to purchase" means from getting a hold to a `TICKETED` order. It is like the share of shop visitors who buy.
    In this project: `hold->purchase conv` line in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

13. **Invariant check** — a fact that must always be true, checked after the run. Here: no seat may have two live tickets. The harness runs one SQL query and expects zero rows. It is like counting the cash at closing time and expecting it to match the sales.
    In this project: `runDBInvariants` in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

14. **Reused identity pool** — the harness creates only a small set of real login users (default 20) and reuses them round-robin. Creating thousands of users in cognito-local would be too slow and would measure the wrong thing. It is like re-using 20 stunt doubles.
    In this project: [scripts/loadtest/users.go](../../../scripts/loadtest/users.go)

15. **cognito-local** — a free local fake of Amazon's login service (Cognito). It issues real signed tokens that the server checks. It is like a practice driving-licence office.
    In this project: `provisionUsers` in [scripts/loadtest/users.go](../../../scripts/loadtest/users.go)

16. **WebSocket (WS) delta lag** — a WebSocket is a long-lived connection where the server can push messages. A delta is a small "seat X changed" message. Lag is how long the push takes after the hold. The harness runs one WS client and measures it. It is like the delay between a scoreboard change and the screen update.
    In this project: [scripts/loadtest/wslag.go](../../../scripts/loadtest/wslag.go)

17. **CSV output** — a plain text table, one row per journey. You can open it in a spreadsheet and chart it. It is like a receipt roll.
    In this project: the CSV writer in [scripts/loadtest/main.go](../../../scripts/loadtest/main.go) (`-csv` flag)

18. **Polling** — asking again and again "is it done yet?". The harness polls `GET /orders/{id}` every 150 ms for up to 5 seconds. It is like a child asking "are we there yet?".
    In this project: `pollOrder` in [scripts/loadtest/journey.go](../../../scripts/loadtest/journey.go)

19. **Outcome classes** — each journey ends as `TICKETED`, `SEAT_TAKEN`, `QUEUE_TIMEOUT`, `ORDER_FAILED` or `ERROR`. Counting by class tells you what went wrong, not just that something did. It is like a report card with named grades.
    In this project: `Outcome` constants in [scripts/loadtest/journey.go](../../../scripts/loadtest/journey.go)

20. **Race condition** — a bug that only appears when two things happen at the very same moment. Normal tests rarely hit the window. Load makes it likely. It is like two people reaching a door at once and both getting stuck.
    In this project: the WebSocket fix in `writeLoop`, [internal/wshub/wshub.go](../../../internal/wshub/wshub.go)

21. **Send on a closed channel** — a Go channel is a pipe between goroutines. Sending into a pipe that another goroutine already closed makes the whole program panic (crash). The first load run crashed the server this way. It is like posting a letter into a mailbox that was just welded shut.
    In this project: the comment above `writeLoop` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go)

22. **Chaos disconnect** — the WS hub randomly cuts about 1% of live connections on purpose. It forces clients to prove they can reconnect and resync. It is like a fire drill.
    In this project: `chaosDisconnect` and `ChaosDisconnectRate` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go)

23. **Relative regression baseline** — a saved number from today's run, to compare with a future run. A big jump means something got worse. It does not tell you what production can handle. It is like marking your child's height on a door frame.
    In this project: the report banner in `printReport`, [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

24. **Dev hardware caveat** — a laptop running Docker is not a real server fleet. Numbers from it only compare to other runs on the same laptop. It is like timing a lap on a bicycle and using it to predict a race car.
    In this project: top comment of [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)
