# Phase 9 — QR Ticketing + Reminders: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 9](../../../MILESTONES.md)

1. **Ticket** — a row in the `tickets` table that proves a person paid for one seat. One seat means one ticket. Think of a paper cinema ticket stub.
   In this project: [migrations/000010_tickets.up.sql](../../../migrations/000010_tickets.up.sql)

2. **QR code** — a square picture of black and white dots that stores a short piece of text. A phone camera reads the dots back into text. It is like a barcode on a parcel, but it holds more text.
   In this project: [internal/ticketing/service.go](../../../internal/ticketing/service.go) (`IssueTicketsForOrder` calls `qrcode.Encode`)

3. **Token** — a short string a client sends to prove something. Here the token says "this is ticket X". It is the text that goes inside the QR code. Think of a coat-check number.
   In this project: [internal/ticketing/payload.go](../../../internal/ticketing/payload.go)

4. **Signed token** — a token with a second part, the signature, added at the end. Only the server can make a correct signature because only the server knows a secret key. A wax seal on a letter works the same way.
   In this project: `encodeQR` in [internal/ticketing/payload.go](../../../internal/ticketing/payload.go)

5. **HMAC** — a way to make a signature from the message plus a secret key. Anyone with the same key can recompute it and compare. A bank stamp that only your bank can press is a good picture.
   In this project: `HMACSigner` in [internal/ticketing/signer.go](../../../internal/ticketing/signer.go) (uses SHA-256)

6. **Tamper detection** — noticing that someone changed a message. If you edit the token, the old signature no longer matches the new text. The gate then rejects it. It is like a tamper seal on a medicine bottle.
   In this project: `decodeQR` returns `ErrBadSignature`; test `TestQR_TamperedPayloadRejected` in [internal/ticketing/payload_test.go](../../../internal/ticketing/payload_test.go)

7. **Token format `v1.<payload>.<signature>`** — three parts joined by dots. `v1` is the format version. The payload is the data, base64-encoded (turned into plain letters and digits that are safe to copy). The signature is also base64. The waiting room uses the same shape, so the codebase has one signed-token style.
   In this project: [internal/ticketing/payload.go](../../../internal/ticketing/payload.go)

8. **Signer interface** — a small Go contract with `Sign` and `Verify`. Code only talks to the contract, not to one concrete key type. Locally the key comes from an env var. In production a key service (KMS) is meant to sit behind the same contract. It is like a power socket: the plug does not care which power plant is behind it.
   In this project: [internal/ticketing/signer.go](../../../internal/ticketing/signer.go)

9. **Object storage (S3 / MinIO)** — a place to store files by name, such as images. S3 is Amazon's version. MinIO is a free local copy with the same API. Think of a locker room where each locker has a name.
   In this project: [internal/storage/s3.go](../../../internal/storage/s3.go)

10. **Bucket** — a top-level folder in object storage. This project has a private bucket just for tickets (`ticketing-tickets` by default). Private means nobody can download without a permit.
    In this project: `S3TicketsBucket` in [internal/config/config.go](../../../internal/config/config.go)

11. **Presigned URL** — a download link with a signature and an end time built into it. Whoever holds the link can download that one file until it expires. A hotel key card that stops working at checkout is the same idea.
    In this project: `PresignedGetURL` in [internal/storage/s3.go](../../../internal/storage/s3.go)

12. **URL expiry (TTL)** — TTL means time to live. Here it is 60 seconds (`PresignTTL`). After that, the storage server answers 403 Forbidden. The app must ask for a fresh link. It is like a parking ticket valid for one hour.
    In this project: `PresignTTL` in [internal/ticketing/service.go](../../../internal/ticketing/service.go)

13. **Ownership check** — the server checks that the logged-in user owns the thing they ask for. If not, it answers "404 not found" instead of "403 forbidden". A 403 would tell a stranger that the ID is real. It is like a hotel desk that says "no such guest" instead of "that guest is not available to you".
    In this project: `getTicketQR` in [internal/httpapi/ticketing.go](../../../internal/httpapi/ticketing.go)

14. **Redeem** — mark a ticket as used at the gate. It can work only once. Think of a conductor punching your train ticket.
    In this project: `Service.Redeem` in [internal/ticketing/service.go](../../../internal/ticketing/service.go)

15. **Compare-and-swap (CAS) in SQL** — one `UPDATE ... WHERE redeemed_at IS NULL` statement. If it changes 0 rows, someone else already redeemed. No separate "read, then write" step, so two scanners cannot both win. It is like two people grabbing the last cookie: the jar lets only one hand succeed.
    In this project: `RedeemTicket` in [sqlc/queries.sql](../../../sqlc/queries.sql)

16. **Unauthenticated endpoint on purpose** — `POST /gate/redeem` needs no login. The signed QR token is the credential. A gate scanner has no user account. A paper ticket barcode works the same way.
    In this project: [internal/httpapi/router.go](../../../internal/httpapi/router.go) (`/gate/redeem` is mounted outside the authed group)

17. **Second safety net (unique index)** — the `tickets` table has a rule: only one live ticket per (event, seat). Even if the booking logic had a bug, the database would refuse a second live ticket. A seat-number sticker plus a guard at the door is the picture.
    In this project: `tickets_seat_live_uq` in [migrations/000010_tickets.up.sql](../../../migrations/000010_tickets.up.sql)

18. **Saga step `issue_ticket`** — the last step of the order saga. It runs after payment and seat confirmation. A saga is a chain of steps where each can fail on its own. If ticket creation fails, the order stays `TICKETED` and the error is only logged.
    In this project: `issueTickets` in [internal/order/order.go](../../../internal/order/order.go)

19. **Nil-safe setter (`WithTicketing`)** — a method that plugs in an optional helper after construction. If you never plug it in, the helper is `nil` and the code skips it. Old tests keep compiling. It is like a phone with an optional case.
    In this project: `WithTicketing` in [internal/order/order.go](../../../internal/order/order.go)

20. **Composition root / wiring** — the one place that builds all the objects and connects them. Here `ticketing.New(...)` is built and handed to the order service. It is like the electrician who plugs every appliance in once, at the fuse box.
    In this project: [internal/wiring/wiring.go](../../../internal/wiring/wiring.go)

21. **Scheduler** — a program that does something at a set time or every N seconds. Here it checks for events that start soon. It is like an alarm clock.
    In this project: [cmd/reminder-scheduler/main.go](../../../cmd/reminder-scheduler/main.go) (polls every 30 seconds)

22. **Reminder (T-24h, T-2h)** — a message sent 24 hours and 2 hours before an event. "T minus" is a countdown to start. The "send" here is fake: it writes a log line and a database row.
    In this project: `RunOnce` in [internal/reminder/reminder.go](../../../internal/reminder/reminder.go)

23. **Dedup (de-duplication)** — making sure the same reminder is sent only once. The `reminders_sent` table has a primary key of (ticket, kind). Restarting the scheduler does not send again. It is like a "done" tick on a to-do list that survives a power cut.
    In this project: `MarkReminderSent` in [sqlc/queries.sql](../../../sqlc/queries.sql)

24. **Fake provider** — a stand-in for a real email or SMS service. It does the bookkeeping but sends nothing. It keeps the project honest about what is real. A flight simulator is the picture.
    In this project: [internal/reminder/reminder.go](../../../internal/reminder/reminder.go)

25. **Integration test vs unit test** — a unit test checks one function with no outside systems. An integration test needs the real Postgres and MinIO running. Here, integration tests have the Go build tag `integration`. Unit tests check the logic in a lab. Integration tests drive the car on a road.
    In this project: [internal/ticketing/integration_test.go](../../../internal/ticketing/integration_test.go) and [internal/ticketing/payload_test.go](../../../internal/ticketing/payload_test.go)
