# Phase 9 — QR Ticketing + Reminders: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 9](../../../MILESTONES.md)

> Note: MILESTONES says the `nonce` in the QR payload "is documented in `token.go`". `internal/ticketing` has no `token.go`, and `qrPayload` has no `nonce` field. The MILESTONES reference most likely means `internal/waitingroom/token.go`, whose nonce is also not checked against a single-use store. The code is in `payload.go` and carries only `tid`, `oid`, `eid`, `sid`. Replay is stopped only by the redeem CAS, not by a nonce.

> Note: MILESTONES lists the reminder loop under `cmd/reminder-scheduler`. The send logic now lives in `internal/reminder` (moved in Phase 11 so the Lambda twin can share it). The cmd file is only a ticker.

## [internal/ticketing/signer.go](../../../internal/ticketing/signer.go)

- **What it does**: defines the `Signer` interface (`Sign`, `Verify`) and `HMACSigner`, an HMAC-SHA256 implementation with a secret key.
- **Control flow**:
  1. [internal/wiring/wiring.go](../../../internal/wiring/wiring.go) `New` calls `ticketing.NewHMACSigner(cfg.QRSigningSecret)`.
  2. The signer is passed into `ticketing.New`, which stores it on `Service`.
  3. `encodeQR` calls `Sign`; `decodeQR` calls `Verify`.
  4. `Verify` calls `Sign` again and compares with `hmac.Equal` (constant-time compare).
- **Data flow**: payload bytes + key → HMAC-SHA256 → 32-byte signature. Nothing is persisted.
- **How it is tested**: unit, no build tag. `TestHMACSigner_VerifyRejectsWrongSignature` in [payload_test.go](../../../internal/ticketing/payload_test.go) checks that a signature fails for different data.
- **Read next**: [payload.go](../../../internal/ticketing/payload.go).

## [internal/ticketing/payload.go](../../../internal/ticketing/payload.go)

- **What it does**: builds and parses the signed token `v1.<b64 payload>.<b64 sig>` that goes inside the QR image.
- **Control flow**:
  1. `Service.IssueTicketsForOrder` calls `encodeQR(signer, qrPayload{...})`.
  2. `encodeQR` does `json.Marshal`, base64url-encodes the body, calls `signer.Sign` on the base64 text, joins with dots.
  3. At the gate, `Service.Redeem` calls `decodeQR`.
  4. `decodeQR` splits on `.`, checks 3 parts and `v1`, decodes the signature, calls `signer.Verify`, then decodes and unmarshals the body.
- **Data flow**: `qrPayload{TicketID, OrderID, EventID, SeatID}` → JSON → base64url → token string. Reverse on decode. The signature covers the base64 text, not the raw JSON. The signature is checked before the JSON is parsed. Errors: `ErrMalformed`, `ErrBadSignature`.
- **How it is tested**: unit, no build tag, in [payload_test.go](../../../internal/ticketing/payload_test.go): `TestQR_EncodeDecodeRoundTrip`, `TestQR_TamperedPayloadRejected`, `TestQR_WrongSignerRejected`, `TestQR_MalformedTokenRejected` (cases: empty, no dots, one part, `v2`).
- **Read next**: [service.go](../../../internal/ticketing/service.go).

## [internal/ticketing/service.go](../../../internal/ticketing/service.go)

- **What it does**: the ticketing use cases. `IssueTicketsForOrder` mints one ticket per seat, renders a QR PNG, uploads it, inserts a row. `PresignQR` returns a 60-second download link. `Redeem` verifies a token and flips the ticket to redeemed.
- **Control flow**:
  1. Issue: [internal/order/order.go](../../../internal/order/order.go) `issueTickets` → `IssueTicketsForOrder` → `q.ListSeatIDsForOrder` → per seat: `encodeQR` → `qrcode.Encode(token, qrcode.Medium, 256)` → `s3.PutObject` (content type `image/png`, cache-control `no-store`) → `q.InsertTicket`.
  2. Presign: [internal/httpapi/ticketing.go](../../../internal/httpapi/ticketing.go) `getTicketQR` → `PresignQR` → `q.GetTicket` → `s3.PresignedGetURL(..., PresignTTL)`.
  3. Redeem: `redeemGate` → `Redeem` → `decodeQR` → `q.RedeemTicket` (returns rows changed) → 0 rows means `ErrAlreadyRedeemed`.
- **Data flow**: order id → seat ids (from `event_seats.booking_id`) → token → PNG bytes in the tickets bucket (key `tickets/<ticketID>.png`) + a `tickets` row holding `qr_s3_key`. Redeem sets `redeemed_at`. There is no transaction around the loop: if seat 2 fails, ticket 1 is already stored.
- **How it is tested**: integration, build tag `integration`, in [integration_test.go](../../../internal/ticketing/integration_test.go): `TestIssueTicketsForOrder_RendersRealQRImages` (downloads each PNG from MinIO, `png.Decode`, checks non-zero size), `TestRedeem_HappyPathThenDuplicateRejected`, `TestRedeem_TamperedTokenRejected`. The 60-second expiry (403 from MinIO after a 65-second wait) was checked by live curl in MILESTONES only. No automated test covers `PresignQR` expiry.
- **Read next**: [internal/httpapi/ticketing.go](../../../internal/httpapi/ticketing.go).

## [internal/httpapi/ticketing.go](../../../internal/httpapi/ticketing.go)

- **What it does**: the two HTTP handlers: `GET /api/v1/tickets/{ticketID}/qr` (login required) and `POST /gate/redeem` (no login).
- **Control flow**:
  1. [internal/httpapi/router.go](../../../internal/httpapi/router.go) `NewRouter` mounts `/gate/redeem` outside the `/api/v1` auth group and `/tickets/{ticketID}/qr` inside it.
  2. `getTicketQR`: parse UUID → `auth.FromContext` claims → `q.GetTicket` → `q.GetOrder` → compare `order.UserSub` to `claims.Sub` → `svc.PresignQR` → JSON `{url, expiresAt}`.
  3. `redeemGate`: decode `{token}` → `svc.Redeem` → map error: nil → 200, `ErrAlreadyRedeemed` → 409, `ErrBadSignature`/`ErrMalformed` → 403, else 500.
- **Data flow**: HTTP JSON in → service call → status code + JSON out. Nothing is persisted by the handler itself.
- **How it is tested**: no handler-level test file exists for these two handlers. The behavior is covered below the HTTP layer by the ticketing tests, and the status codes (200/409/403) were verified by live curl in MILESTONES.
- **Read next**: [internal/order/order.go](../../../internal/order/order.go).

## [internal/order/order.go](../../../internal/order/order.go) (ticket hooks only)

- **What it does**: calls ticket issuance at both places where an order becomes `TICKETED`.
- **Control flow**:
  1. [internal/wiring/wiring.go](../../../internal/wiring/wiring.go) `New` builds `orders := order.New(...)`, then `orders.WithTicketing(tix)`.
  2. `RunSaga` after `ConfirmSeats` succeeds: `UpdateOrderStatus(CONFIRMED)` → `issueTickets` → `markStep(issue_ticket, DONE)` → `UpdateOrderStatus(TICKETED)`.
  3. `compensate` after a successful reallocation: same three steps, on the new seats.
  4. `issueTickets` returns early if `s.tickets == nil`. On error it only logs; the order still becomes `TICKETED`.
- **Data flow**: order id + event id → `IssueTicketsForOrder` → rows and PNGs. The saga step is marked `DONE` whether or not issuance worked.
- **How it is tested**: integration tests in [internal/order/integration_test.go](../../../internal/order/integration_test.go) assert final status `TICKETED` (they build the service without ticketing, so the nil-safe path). I did not find a test that proves tickets exist after a full saga. That was proven by live curl and a `psql` query in MILESTONES.
- **Read next**: [internal/reminder/reminder.go](../../../internal/reminder/reminder.go).

## [internal/reminder/reminder.go](../../../internal/reminder/reminder.go)

- **What it does**: `RunOnce` finds tickets whose event starts within `lead` from now and that have no reminder of this `kind` yet, "sends" each (a log line), and records it.
- **Control flow**:
  1. [cmd/reminder-scheduler/main.go](../../../cmd/reminder-scheduler/main.go) `main` loads config, opens a pgx pool, starts a 30-second `time.Ticker` (`pollInterval`).
  2. Each tick calls `reminder.RunOnce(ctx, q, "T-24h", reminder.T24h)` then `RunOnce(ctx, q, "T-2h", reminder.T2h)`.
  3. `RunOnce` → `q.ListTicketsDueForReminder` (window `now` to `now+lead`, excludes revoked, excludes rows in `reminders_sent`) → `log.Printf("[FAKE SEND]...")` → `q.MarkReminderSent`.
- **Data flow**: clock + `tickets` JOIN `events` → log line → `reminders_sent(ticket_id, kind)` row. `MarkReminderSent` uses `ON CONFLICT DO NOTHING`. The log happens before the insert, so a failed insert means a double log on the next tick (the code says so in its own log message).
- **How it is tested**: no test file in `internal/reminder`. Verified live in MILESTONES: count of `reminders_sent` stayed at 2 across a scheduler restart.
- **Read next**: [migrations/000010_tickets.up.sql](../../../migrations/000010_tickets.up.sql).

## [migrations/000010_tickets.up.sql](../../../migrations/000010_tickets.up.sql)

- **What it does**: creates `tickets` and `reminders_sent`. `tickets_seat_live_uq` is a partial unique index on `(event_id, seat_id) WHERE revoked_at IS NULL`.
- **Control flow**: applied by `make migrate`. Queries live in [sqlc/queries.sql](../../../sqlc/queries.sql) (`InsertTicket`, `GetTicket`, `RedeemTicket`, `ListTicketsDueForReminder`, `MarkReminderSent`) and are generated into `internal/db` by `make sqlc-generate`.
- **Data flow**: `InsertTicket` writes one row per seat. `RedeemTicket` is `UPDATE tickets SET redeemed_at = now() WHERE ticket_id = $1 AND redeemed_at IS NULL AND revoked_at IS NULL`, returned as rows affected.
- **How it is tested**: indirectly by the integration tests above (double redeem hits the `redeemed_at IS NULL` guard). I did not find a test that tries to insert two live tickets for one seat to trip the unique index.
- **Read next**: [Phase 10 files](../phase-10-load-harness/FILES.md).
