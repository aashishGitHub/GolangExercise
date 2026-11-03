# Phase 9 — QR Ticketing + Reminders: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [Learn index](../README.md)

## L1 Beginner

**Q1. What is a signed QR token and why does a ticket need one?**
The QR holds the text `v1.<payload>.<signature>`. The payload names the ticket. The signature is an HMAC made with a server-only key, so nobody can invent a valid one. See `encodeQR` in [payload.go](../../../internal/ticketing/payload.go).

**Q2. What is a presigned URL and why is it used for the QR image?**
It is a download link with a signature and an expiry. The tickets bucket is private, so this is the only way to fetch a PNG. See `PresignedGetURL` in [internal/storage/s3.go](../../../internal/storage/s3.go) and `PresignTTL` (60 seconds) in [service.go](../../../internal/ticketing/service.go).

**Q3. What does "redeem" mean and why must it work only once?**
Redeem marks the ticket as used at the gate. If it worked twice, one ticket could admit two people. See `Service.Redeem` in [service.go](../../../internal/ticketing/service.go).

**Q4. What is a reminder dedup and why is it needed?**
Dedup means one reminder per ticket per kind. Without it, every 30-second tick would send the same reminder again. The table `reminders_sent` has primary key `(ticket_id, kind)`. See [migrations/000010_tickets.up.sql](../../../migrations/000010_tickets.up.sql).

## L2 Junior

**Q5. Trace what happens when an order becomes TICKETED.**
`RunSaga` confirms seats, sets `CONFIRMED`, then calls `issueTickets` in [order.go](../../../internal/order/order.go). That calls `IssueTicketsForOrder`: list seat ids, then per seat `encodeQR`, `qrcode.Encode`, `PutObject`, `InsertTicket`. Last, status becomes `TICKETED`. The same sequence runs in `compensate` after a reallocation.

**Q6. Trace `POST /gate/redeem` with a valid token.**
`redeemGate` in [httpapi/ticketing.go](../../../internal/httpapi/ticketing.go) decodes `{token}` and calls `Redeem`. `decodeQR` splits on dots, runs `Verify`, parses JSON. `q.RedeemTicket` runs the CAS update. 1 row changed gives 200 `{"status":"redeemed"}`.

**Q7. How does the server tell a tampered token from a malformed one, and what status does each get?**
Wrong shape, wrong version or bad base64 gives `ErrMalformed`. A good shape with a wrong signature gives `ErrBadSignature`. Both map to 403 `bad_signature` in `redeemGate`. Tests: `TestQR_MalformedTokenRejected`, `TestQR_TamperedPayloadRejected` in [payload_test.go](../../../internal/ticketing/payload_test.go).

**Q8. How does the reminder scheduler decide what to send?**
`RunOnce` in [reminder.go](../../../internal/reminder/reminder.go) asks `ListTicketsDueForReminder` for tickets whose event starts between now and now plus the lead time, not revoked, and not yet in `reminders_sent` for that kind. Each result is logged and recorded. Note: a ticket 90 minutes from its event matches both the T-24h and T-2h windows, so both fire in one tick. MILESTONES shows exactly that.
Try it:
```
go run ./cmd/reminder-scheduler
```

## L3 Mid

**Q9. Why is `/gate/redeem` unauthenticated, and what makes that safe enough?**
A gate scanner has no attendee JWT. The signed token is the credential, like a paper barcode. Safety comes from HMAC (cannot forge) plus the single-use CAS (cannot replay). It is mounted outside the auth group in [router.go](../../../internal/httpapi/router.go). The cost: anyone who photographs a QR before the owner uses it can redeem first.

**Q10. Why does `RedeemTicket` use one conditional UPDATE instead of SELECT then UPDATE?**
Two scanners could both read `redeemed_at IS NULL` and both write. One statement with `WHERE redeemed_at IS NULL` lets the database pick a single winner. `Redeem` reads the rows-affected count. See [sqlc/queries.sql](../../../sqlc/queries.sql) and `TestRedeem_HappyPathThenDuplicateRejected` in [integration_test.go](../../../internal/ticketing/integration_test.go). That test proves sequential duplicate rejection. It does not run two redeems in parallel.

**Q11. Why return 404, not 403, when a user asks for someone else's ticket QR?**
403 confirms that the ticket ID exists. `getTicketQR` in [httpapi/ticketing.go](../../../internal/httpapi/ticketing.go) returns the same 404 for "missing" and "not yours". No test file covers this handler; MILESTONES records it as a design choice.

**Q12. Why a `Signer` interface instead of calling HMAC directly?**
It is the seam for KMS in production. `Service` and `encodeQR` only know `Sign` and `Verify` ([signer.go](../../../internal/ticketing/signer.go)). The unit tests swap keys freely. Not done in this repo: a KMS implementation. I have no verified source that one exists here; only `HMACSigner` is in the code.
Try it:
```
go test ./internal/ticketing/...
```

## L4 Senior

**Q13. What happens if ticket issuance fails after the customer has paid?**
`issueTickets` in [order.go](../../../internal/order/order.go) logs and returns. The saga step is marked `DONE` and the order goes to `TICKETED` anyway. The user has paid and has no ticket. The code comment says a retry path is a real gap. `IssueTicketsForOrder` is also not transactional and not idempotent. A rerun after a partial failure would hit `tickets_seat_live_uq` for seats that already have a ticket. This is not solved.

**Q14. What does the QR design NOT protect against?**
Replay of a copied QR before first use: no nonce exists in `qrPayload` ([payload.go](../../../internal/ticketing/payload.go)) and only the first redeem wins. Key rotation: `Verify` has no key id, so changing `QR_SIGNING_SECRET` invalidates every issued QR. Revocation: `revoked_at` exists and is checked by the CAS, but no code in this phase sets it.

**Q15. What breaks at 100× scale for reminders?**
`RunOnce` runs a join over all live tickets every 30 seconds, then does one `MarkReminderSent` per row in a loop, and a send is a log line. At large volume you need batching, a real provider with its own retry and idempotency key, and a per-ticket one-shot schedule instead of polling. Local polling is a stand-in because moto never fires schedules (see [cmd/reminder-scheduler/main.go](../../../cmd/reminder-scheduler/main.go) header comment). The Lambda twin is covered in [Phase 11](../phase-11-iac-lambda/BASICS.md).

**Q16. Why a second overbooking tripwire in `tickets`, and what does it actually prove?**
`tickets_seat_live_uq` ([migration](../../../migrations/000010_tickets.up.sql)) rejects two live tickets for one `(event_id, seat_id)` even if the inventory CAS were wrong. It is defense in depth at the last write. It does not stop the double booking itself; it turns it into an insert error that `issueTickets` would only log. No automated test in this phase inserts a duplicate to prove the index fires.
