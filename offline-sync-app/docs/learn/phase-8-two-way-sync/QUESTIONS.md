# Phase 8 — Two-way sync: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md) · full design doc: [two-way-sync.md](../../two-way-sync.md)

Run every "Try it" command from `offline-sync-app/`. Commands marked integration need `make compose-up` and `make migrate` first.

## L1 Beginner

**Q1. What is a webhook, and why not just poll the other system?**
A webhook is a request the other system sends to your URL when something changes. It is fast: you hear about the change in seconds. But it can be dropped. So this project uses both: webhooks for speed ([internal/webhook/handler.go](../../../internal/webhook/handler.go)) and a poll for completeness ([internal/twoway/extractor.go](../../../internal/twoway/extractor.go)).

Try it:
```
go test ./internal/webhook
```

**Q2. Why must a webhook be signed?**
Anyone on the internet can POST to your URL. The HMAC signature (a fingerprint made with a shared secret) proves the sender knows the secret and that the body was not changed. See `Verifier.Verify` in [internal/webhook/verify.go](../../../internal/webhook/verify.go).

Try it:
```
go test -v -run TestVerify ./internal/webhook
```

**Q3. What is an echo loop?**
FieldSync pushes a change to the SaaS, the SaaS sends a webhook about that same change, FieldSync treats it as new and pushes again, forever. The project stops it with three guards: single-writer fields ([ownership.go](../../../internal/twoway/ownership.go)), an origin filter ([loader.go](../../../internal/twoway/loader.go), `Loader.Handle`), and echo memory (`Applier.isEcho` in [apply.go](../../../internal/twoway/apply.go)).

**Q4. What is a dead letter, and why keep one?**
A message that keeps failing is parked after N attempts with its raw payload and reason, so it cannot block the queue or retry forever. Table `sync_dead_letters` in [migrations/000003_two_way_sync.up.sql](../../../migrations/000003_two_way_sync.up.sql). Writers: `Worker.recordFailure` ([worker.go](../../../internal/webhook/worker.go)) and `Loader.deadLetter` ([loader.go](../../../internal/twoway/loader.go)).

## L2 Junior

**Q1. Trace one webhook from the HTTP request to a stored row.**
`NewRouter` routes `POST /webhooks/{system}` to `NewHandler` ([handler.go](../../../internal/webhook/handler.go)). It: (1) caps the body with `http.MaxBytesReader`, (2) reads raw bytes, (3) calls `Verifier.Verify` ([verify.go](../../../internal/webhook/verify.go)), (4) rejects an empty delivery id, (5) calls `DBInbox.Insert`, which runs `InsertWebhookInbox` (`ON CONFLICT DO NOTHING`), (6) replies 202 `accepted`, or 202 `duplicate` when 0 rows were inserted. Nothing else runs in the request.

Try it:
```
go test -v -run 'TestHandler_ValidDeliveryIs202AndStoredRaw' ./internal/webhook
```

**Q2. How does a stored webhook become a work item update?**
A ticker in [cmd/webhook-receiver/main.go](../../../cmd/webhook-receiver/main.go) calls `Worker.RunOnce` ([worker.go](../../../internal/webhook/worker.go)). For each pending id, `processOne` opens a transaction, claims the row (`FOR UPDATE SKIP LOCKED`), and calls `Applier.ProcessWebhook` ([inbound.go](../../../internal/twoway/inbound.go)), which decodes the body and calls `Applier.Apply` ([apply.go](../../../internal/twoway/apply.go)). `Apply` runs the version gate (`AdvanceLinkVersion`), the ownership merge, `UpsertWorkItem`, and an event insert. Then `MarkInboxProcessed(outcome)` and commit.

**Q3. Trace a local edit of notes to the external PATCH.**
`WorkItems.EditNotes` ([workitems.go](../../../internal/twoway/workitems.go)) updates the row and publishes `work_item.updated` in one transaction. `consume.RunOnce` passes the event to `Loader.Handle` ([loader.go](../../../internal/twoway/loader.go)). It skips events whose actor is `system:<system>`, finds the external id, builds a patch with `Ownership.PatchFor` (internal-owned fields only), and calls `HTTPClient.Patch` ([client.go](../../../internal/twoway/client.go)) with the event id as `Idempotency-Key`. On success it stores version and content hash with `UpsertSyncPushed`.

Try it:
```
go test -v -run TestHandle_PushesOnlyInternalOwnedFieldsWithTheEventIDAsIdempotencyKey ./internal/twoway
```

**Q4. The extractor crashes after page 2. What happens on the next run?**
Each page is applied and its checkpoint written in one transaction in `Extractor.Run` ([extractor.go](../../../internal/twoway/extractor.go)). The crashed page rolled back, so `sync_state` still holds page 1's `next_cursor`, `run_since` and `run_max`. The next `Run` sees a non-empty cursor and resumes from it. Re-applying anything already applied is a no-op through the version gate.

Try it:
```
go test -v -run TestRun_CrashOnSecondPageKeepsTheFirstCheckpoint ./internal/twoway
```

## L3 Mid

**Q1. Why is the delivery id inside the signed text, and what does the test prove?**
The inbox dedupes on `(system, delivery_id)`. If the id were unsigned, an attacker with one captured request could post its body under the id of a real upcoming delivery; the real one would hit the conflict, get `202 duplicate`, and the sender would stop retrying. With the id in the MAC, a swapped id gives `401 bad_signature`. `TestHandler_DeliveryIDSwappedAfterSigningIs401SoTheRealDeliveryIsNotSwallowed` ([handler_test.go](../../../internal/webhook/handler_test.go)) proves the 401 and that the real delivery is still accepted afterwards. Full reasoning: [two-way-sync.md](../../two-way-sync.md), section "Why the delivery id must be signed". Companion rule: an id containing `.` is rejected as `ErrMalformedHeaders` in [verify.go](../../../internal/webhook/verify.go), so the signed string splits unambiguously.

Try it:
```
go test -v -run TestHandler_DeliveryIDSwappedAfterSigningIs401SoTheRealDeliveryIsNotSwallowed ./internal/webhook
```

**Q2. Why is the version gate a single SQL `UPDATE ... WHERE last_version < $v` instead of read-then-compare in Go?**
Read-then-compare has a window where two workers both read the old version and both apply. The single statement is atomic and also row-locks the link until the transaction ends. See `AdvanceLinkVersion` in [sqlc/queries.sql](../../../sqlc/queries.sql). The unit test `TestApply_OlderVersionIsIgnoredStaleAndWritesNothing` ([apply_test.go](../../../internal/twoway/apply_test.go)) only proves `Apply` returns `ignored_stale` when the mock returns no rows; it cannot prove the SQL itself. The integration tests `TestIntegration_OutOfOrderDeliveryIgnoresTheOlderVersion` and `TestIntegration_ConcurrentFirstSightCreatesOneLinkAndOneItem` ([integration_test.go](../../../internal/twoway/integration_test.go)) run the real SQL against Postgres.

Try it (integration):
```
go test -tags=integration -run TestIntegration_OutOfOrderDeliveryIgnoresTheOlderVersion ./internal/twoway/...
```

**Q3. Why is echo detection only on the webhook path, not on the poll path?**
A poll returns full current state and must be able to repair something a dropped webhook left behind. If the poll also dropped "echoes", it could skip a real repair. `Apply` checks `src == SourceWebhook` before `isEcho` ([apply.go](../../../internal/twoway/apply.go)). `TestApply_PollDoesNotConsultEchoMemory` pins this. The cost, noted in the design doc: while a push is in flight, a poll can report a label-only `conflict`.

Try it:
```
go test -v -run TestApply_PollDoesNotConsultEchoMemory ./internal/twoway
```

**Q4. How are the retry rules tested without waiting in real time?**
`HTTPClient` has injected `sleep` and `rand` fields ([client.go](../../../internal/twoway/client.go)). [client_test.go](../../../internal/twoway/client_test.go) replaces them: `TestPatch_5xxBacksOffWithFullJitterAndCap` checks the ceilings, `TestPatch_JitterIsADrawFromZeroToCeiling` checks the draw, `TestPatch_429HonoursRetryAfterThenSucceeds` checks the exact wait, and `TestPatch_GivesUpAfterMaxAttemptsWithoutSleepingAfterTheLast` checks no wasted final sleep. One integration test, `TestIntegration_RateLimitedPushHonoursRetryAfter`, runs the real client against mockjira with an injected 429.

Try it:
```
go test -v -run TestPatch ./internal/twoway
```

## L4 Senior

The eight questions already answered in [two-way-sync.md](../../two-way-sync.md) (section "Interview Q&A": verification, 202 + inbox, duplicates vs out-of-order, echo loops, ownership vs timestamps, resumable backfill, 429/5xx/lost response, deletes and dead letters) are not repeated here. Three more:

**Q1. You deploy only the Lambda receiver. What happens, and what do you build?**
The Lambda ([cmd/webhook-receiver-lambda/main.go](../../../cmd/webhook-receiver-lambda/main.go)) only authenticates and inserts into `webhook_inbox`. The inbox worker, extractor and loader still run only inside [cmd/webhook-receiver/main.go](../../../cmd/webhook-receiver/main.go). So the table fills and nothing drains it; the sender sees healthy 202s, so nobody is alerted by the sender. The design doc lists this as not solved. Options: run a drain Lambda on a schedule or queue trigger around `Worker.RunOnce`; run the extractor as a single scheduled invocation. Watch the backlog with the query in the doc: `SELECT count(*), min(received_at) FROM webhook_inbox WHERE processed_at IS NULL AND dead_at IS NULL`. Not built: any of this, and no reserved concurrency on the receiver.

**Q2. 100x the webhook volume. What breaks first, and what do you change?**
From the code, in order of how the doc and code describe them (I have not load-tested any of this): (1) `Worker.RunOnce` lists up to 25 ids and runs one transaction per row, so throughput is bounded by per-row round trips; `SKIP LOCKED` lets you add workers safely. (2) Inbox retries have no backoff, only the tick interval, and a poison body burns the same `maxAttempts` as a transient DB error; split permanent from transient failures. (3) `webhook_inbox` has no retention job, so it grows; the partial index `webhook_inbox_pending_idx` keeps the pending scan small, but storage and vacuum still grow. (4) The extractor assumes one runner; two runners duplicate work and the last checkpoint wins; fix with an advisory lock or one scheduled invocation. (5) Per-system config only; no multi-tenant config. Metrics and tracing do not exist, so you would add backlog age, outcome counts and dead-letter growth before scaling anything.

**Q3. Which correctness holes remain even when everything is up?**
Three, all in [two-way-sync.md](../../two-way-sync.md) under "What is NOT solved" or "Layers against an echo loop": (a) the echo race: if a webhook is processed before the Loader records its push, it is not labelled `echo`; the merge is a no-op and the loop still ends, so only the label is wrong. (b) Late commits older than the overlap window are missed by the poll; fix is a periodic full reconcile, not built. (c) `Retry-After` is unbounded (only the context deadline caps it), only the seconds form is parsed (`parseRetryAfter` in [client.go](../../../internal/twoway/client.go)), and a sleep inside a page can overrun the extractor budget. Also: after a `conflict`, nothing repairs the SaaS copy; outbound deletes and internal-origin creates do not exist; `external_id_map` has no `entity_type`.

> Note: the design doc's "AirSync vocabulary" section and the ~13 minute invocation cap are marked unverified by the author. Do not quote them in an interview without checking the vendor docs. In code, `EXTRACT_BUDGET` defaults to 10 minutes ([config.go](../../../internal/twoway/config.go)).
