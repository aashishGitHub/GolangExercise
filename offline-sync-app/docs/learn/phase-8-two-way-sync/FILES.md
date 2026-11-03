# Phase 8 — Two-way sync: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md) · full design doc: [two-way-sync.md](../../two-way-sync.md)

Test layers used below: **unit** = plain `go test ./...` (fakes or gomock, no database). **integration** = `go test -tags=integration ./internal/twoway/...`, real Postgres (`make compose-up` then `make migrate`), mockjira over `httptest`.

## [internal/webhook/handler.go](../../../internal/webhook/handler.go)

- **What it does**: the HTTP endpoint for inbound webhooks. It authenticates, stores the raw body in the inbox, and answers 202. It does no business work.
- **Control flow**:
  1. [cmd/webhook-receiver/main.go](../../../cmd/webhook-receiver/main.go) (or the Lambda `main`) builds `NewRouter`, which mounts `NewHandler` at `POST /webhooks/{system}`.
  2. The handler wraps the body in `http.MaxBytesReader`, then `io.ReadAll` (too big: 413 `payload_too_large`).
  3. `Verifier.Verify` runs on the raw bytes (fail: 401 with `rejectionCode`: `stale_timestamp`, `malformed_headers` or `bad_signature`).
  4. Empty delivery id: 400 `missing_delivery_id`.
  5. `Inbox.Insert` (`DBInbox.Insert`) calls sqlc `InsertWebhookInbox`. Insert error: 500 `inbox_unavailable`, so the sender retries.
  6. 1 row inserted: 202 `accepted`. 0 rows: 202 `duplicate`.
- **Data flow**: headers `X-Webhook-Delivery`, `X-Webhook-Timestamp`, `X-Webhook-Signature` + raw body -> `webhook_inbox(system, delivery_id, body)` via `INSERT ... ON CONFLICT DO NOTHING`. Output is a small JSON `{"status":...}` or `{"error":...}`.
- **How it is tested**: [handler_test.go](../../../internal/webhook/handler_test.go), unit, with a `fakeInbox`: `TestHandler_ValidDeliveryIs202AndStoredRaw`, `TestHandler_BadSignatureIs401AndNothingStored`, `TestHandler_TamperedBodyIs401`, `TestHandler_DeliveryIDSwappedAfterSigningIs401SoTheRealDeliveryIsNotSwallowed`, `TestHandler_StaleTimestampIs401EvenWithValidSignature`, `TestHandler_DuplicateDeliveryIs202AndStoredOnce`, `TestHandler_OversizedBodyIs413`, `TestHandler_MissingDeliveryIDIs400`, `TestHandler_InboxFailureIs500SoSenderRetries`. [inbox_test.go](../../../internal/webhook/inbox_test.go) has `TestDBInbox_Insert` (gomock Querier, 1 row vs 0 rows). End to end with real SQL: `TestIntegration_BadSignatureIs401AndNothingIsStored` and `TestIntegration_DuplicateDeliveryIsProcessedOnce` in [integration_test.go](../../../internal/twoway/integration_test.go).
- **Read next**: [internal/webhook/verify.go](../../../internal/webhook/verify.go).

> Note: the design doc's flow diagram lists "verify" straight after "read body" and does not show the `missing_delivery_id` 400. In the code that check comes after `Verify` and before the insert.

## [internal/webhook/verify.go](../../../internal/webhook/verify.go)

- **What it does**: checks the HMAC-SHA256 signature and the timestamp window of one delivery.
- **Control flow**:
  1. `Handler` calls `Verifier.Verify(timestamp, deliveryID, signature, body)`.
  2. `strings.CutPrefix` removes `sha256=`; a missing prefix, empty timestamp, or a `.` in the delivery id returns `ErrMalformedHeaders`.
  3. `hex.DecodeString` and `strconv.ParseInt` on the inputs (failure: `ErrMalformedHeaders`).
  4. `hmac.New(sha256.New, secret)` is fed `timestamp + "." + deliveryID + "." ` then the body.
  5. `hmac.Equal` against the decoded signature (mismatch: `ErrBadSignature`).
  6. Age = `now - timestamp`; outside +-tolerance returns `ErrStaleTimestamp`. The MAC check runs first, the time check second.
- **Data flow**: strings + bytes in, a sentinel error (or nil) out. Nothing is persisted. The clock is injected (`now func() time.Time`).
- **How it is tested**: [verify_test.go](../../../internal/webhook/verify_test.go), unit, one table test `TestVerify`: an OpenSSL-computed vector, window edges (exactly 300 s valid, 301 s stale, future too), wrong secret, tampered body, reformatted body, swapped timestamp, swapped delivery id, `.` in id, missing/odd headers. The mock's own signer is checked against the same vector by `TestSign_MatchesOpenSSLVector` in [mockjira_test.go](../../../internal/mockjira/mockjira_test.go).
- **Read next**: [internal/webhook/worker.go](../../../internal/webhook/worker.go).

## [internal/webhook/worker.go](../../../internal/webhook/worker.go)

- **What it does**: drains `webhook_inbox`. Per row, one transaction: claim, apply, mark processed. Failures are counted and finally dead-lettered.
- **Control flow**:
  1. A ticker in [cmd/webhook-receiver/main.go](../../../cmd/webhook-receiver/main.go) (every 2 s, via its `loop` helper) calls `Worker.RunOnce`.
  2. `ListPendingInbox` returns up to `batchSize` (25) delivery ids that are neither processed nor dead.
  3. For each id, `processOne` begins a tx and calls `ClaimInboxRow` (`FOR UPDATE SKIP LOCKED`; no row means another worker has it, skip).
  4. It calls the injected `Processor`; production wiring passes `Applier.ProcessWebhook` (a method value).
  5. `MarkInboxProcessed(outcome)` then `Commit`.
  6. On error: `recordFailure` in a new tx runs `RecordInboxFailure` (attempts + 1). At `maxAttempts` it runs `InsertDeadLetter` (source `inbox`) and `MarkInboxDead`.
- **Data flow**: `webhook_inbox.body` -> `Processor` -> `outcome` string stored on the same row; failures update `attempts`, `last_error`; poison rows are copied raw into `sync_dead_letters`.
- **How it is tested**: there is no `worker_test.go`; it is untested at unit level. It is covered by integration tests in [integration_test.go](../../../internal/twoway/integration_test.go): `TestIntegration_PoisonInboxEventIsDeadLetteredAfterNAttempts` (exactly 3 attempts, raw body kept, attempts stop growing), `TestIntegration_DuplicateDeliveryIsProcessedOnce`, `TestIntegration_OutOfOrderDeliveryIgnoresTheOlderVersion`.
- **Read next**: [internal/twoway/apply.go](../../../internal/twoway/apply.go).

## [internal/twoway/apply.go](../../../internal/twoway/apply.go)

- **What it does**: the one place an external issue snapshot is merged into a local work item. Both the webhook path and the poll path call `Applier.Apply`.
- **Control flow**:
  1. `Worker` (via `ProcessWebhook` in [inbound.go](../../../internal/twoway/inbound.go), which decodes `{"issue": ...}`) or `Extractor` (with `SourcePoll`) calls `Apply` inside a transaction.
  2. `ResolveInternalID`: `InsertExternalLink` then `GetExternalLink` (race-safe mapping of external id to internal id).
  3. Webhook source only: `isEcho` reads `sync_pushed`; same version and `Content.Hash()` means return `OutcomeEcho`.
  4. `AdvanceLinkVersion`: no row back (`pgx.ErrNoRows`) means `OutcomeIgnoredStale`.
  5. `GetWorkItemForUpdate` (row lock), then `Ownership.Merge(current, incoming)` from [ownership.go](../../../internal/twoway/ownership.go).
  6. `UpsertWorkItem` (a delete sets `deleted_at`, never a SQL DELETE).
  7. If content or deleted-state changed, `publish` writes a `work_item.updated` or `work_item.deleted` event with `ActorSub` = `system:<system>` through `events.Publish`.
  8. Returns `OutcomeConflict` if `Merge` rejected any field, else `OutcomeApplied`.
- **Data flow**: `ExternalIssue` (summary, status, description, version, deleted) -> `Content` (title, status, notes) -> merged `Content` -> `work_items` row + `external_id_map.last_version` + a `domain_events` row (outbox) when something changed.
- **How it is tested**: [apply_test.go](../../../internal/twoway/apply_test.go), unit with gomock `Querier`: `TestApply_NewIssueCreatesItemAndEvent`, `TestApply_OlderVersionIsIgnoredStaleAndWritesNothing`, `TestApply_ExternalFieldsApplyInternalFieldsKept`, `TestApply_ExternalEditOfInternalFieldIsConflictAndInternalValueSurvives`, `TestApply_NoContentChangeAdvancesVersionButPublishesNoEvent`, `TestApply_DeleteIsATombstoneNotARemoval`, `TestApply_WebhookMatchingPushedMemoryIsEchoAndTouchesNothing`, `TestApply_PollDoesNotConsultEchoMemory`, `TestProcessWebhook`. Ownership rules: `TestMerge`, `TestPatchFor_OnlyInternalOwnedFields` in [ownership_test.go](../../../internal/twoway/ownership_test.go). Integration: `TestIntegration_ConflictKeepsInternalNotesAndAppliesExternalStatus`, `TestIntegration_DeleteArrivingBeforeCreateStillWinsByVersion`, `TestIntegration_ConcurrentFirstSightCreatesOneLinkAndOneItem`.
- **Read next**: [internal/twoway/extractor.go](../../../internal/twoway/extractor.go).

## [internal/twoway/extractor.go](../../../internal/twoway/extractor.go)

- **What it does**: the forward poll. Pulls changed issues page by page inside a time budget, with a resumable checkpoint.
- **Control flow**:
  1. A ticker in `cmd/webhook-receiver` (every minute) calls `Extractor.Run`.
  2. `GetSyncState` loads watermark, `run_since`, `next_cursor`, `run_max`. No in-flight cursor: `since = watermark - Overlap`.
  3. Loop: stop cleanly if the remaining budget is zero or less than the slowest page so far.
  4. `Lister.List(since, cursor, PageSize)` (real implementation: `HTTPClient.List`).
  5. `Transactor.InTx`: `Applier.Apply(..., SourcePoll, issue)` for every issue, then `UpsertSyncState` via `checkpoint`.
  6. Empty `nextCursor` means done: the watermark moves to the maximum `updatedAt` seen. Otherwise store the cursor and continue.
- **Data flow**: external pages -> `work_items` / links / events (through `Apply`) and `sync_state(watermark, run_since, next_cursor, run_max)` in the same transaction.
- **How it is tested**: [extractor_test.go](../../../internal/twoway/extractor_test.go), unit with a `fakeLister`, `fakeClock`, `recordingApplier`: `TestRun_FirstRunBackfillsEverythingAndSetsWatermark`, `TestRun_IncrementalRunStartsOverlapBeforeTheWatermark`, `TestRun_WatermarkNeverMovesBackwardsWhenTheRunSeesOlderRows`, `TestRun_StopsBeforeAPageItCannotFinishAndResumesFromTheCheckpoint`, `TestRun_ZeroBudgetDoesNotStartAPage`, `TestRun_CrashOnSecondPageKeepsTheFirstCheckpoint`, `TestRun_FailedApplyDoesNotAdvanceTheCheckpoint`. Integration: `TestIntegration_CrashMidBackfillResumesFromCheckpointWithZeroDuplicates`, `TestIntegration_IncrementalPullIsIdempotentAcrossTheOverlapWindow`, `TestIntegration_DeletesAreTombstonesAndAMissingPollRowDeletesNothing`.
- **Read next**: [internal/twoway/loader.go](../../../internal/twoway/loader.go).

## [internal/twoway/loader.go](../../../internal/twoway/loader.go)

- **What it does**: the reverse path. Pushes internal edits to the external system as an idempotent PATCH.
- **Control flow**:
  1. `WorkItems.EditNotes` ([workitems.go](../../../internal/twoway/workitems.go)) updates `work_items.notes` and publishes `work_item.updated` in one transaction.
  2. `consume.RunOnce` (ticker, every 5 s in `cmd/webhook-receiver`) hands the event to `Loader.Handle`.
  3. Origin filter: `ActorSub == OriginActor(system)` returns nil (skip).
  4. `GetExternalLinkByInternal`: no link means log and skip.
  5. `Ownership.PatchFor` builds a patch of internal-owned fields only; an empty patch returns nil.
  6. `Pusher.Patch(externalID, patch, evt.ID.String())`, i.e. `HTTPClient.Patch` with `Idempotency-Key` = event id.
  7. Success: `UpsertSyncPushed(version, hash of response)`.
  8. `RetryExhaustedError` or a permanent 4xx: `deadLetter` (source `loader`). Other errors are returned so the event stays unprocessed.
- **Data flow**: `domain_events` row -> `IssuePatch` (summary/status/description pointers) -> HTTP PATCH -> response `ExternalIssue` -> `sync_pushed(external_version, content_hash)`.
- **How it is tested**: [loader_test.go](../../../internal/twoway/loader_test.go), unit with gomock Querier and a fake pusher: `TestHandle_PushesOnlyInternalOwnedFieldsWithTheEventIDAsIdempotencyKey`, `TestHandle_SkipsEventsTheExternalSystemCaused`, `TestHandle_UnlinkedWorkItemIsSkipped`, `TestHandle_NothingInternalOwnedMeansNothingToPush`, `TestHandle_ExhaustedRetriesGoToDeadLettersAndTheEventIsConsumed`, `TestHandle_PermanentClientErrorIsDeadLetteredImmediately`, `TestHandle_NonRetryableInfraErrorLeavesTheEventForTheNextTick`, `TestHandle_MalformedPayloadIsDeadLettered`. Integration: `TestIntegration_EchoLoopTerminates`, `TestIntegration_EchoOfAnOlderPushIsNotReportedAsAConflict`, `TestIntegration_LoaderDeadLettersWhenRetriesAreExhausted`.
- **Read next**: [internal/twoway/client.go](../../../internal/twoway/client.go).

## [internal/twoway/client.go](../../../internal/twoway/client.go)

- **What it does**: HTTP client for the external API with retry rules: honour `Retry-After`, full-jitter backoff, fail fast on permanent errors.
- **Control flow**:
  1. `Extractor` calls `HTTPClient.List`; `Loader` calls `HTTPClient.Patch`. Both go through `do`.
  2. `do` loops up to `RetryPolicy.MaxAttempts` (default 5): `once` makes one request.
  3. `retryable`: not if the context is done; an `APIError` is retryable only for 429 or 5xx; transport errors are retryable.
  4. Between attempts `delay` returns `Retry-After` if present, else a uniform draw from `[0, min(MaxDelay, BaseDelay * 2^(attempt-1))]`; `sleepContext` waits.
  5. After the last attempt: `RetryExhaustedError`. A permanent 4xx returns the `APIError` at once.
- **Data flow**: path + body + idempotency key in, decoded JSON (`Page` or `ExternalIssue`) out. Nothing is persisted. Responses are read through `io.LimitReader` (1 MiB).
- **How it is tested**: [client_test.go](../../../internal/twoway/client_test.go), unit, with injected `sleep` and `rand`: `TestPatch_429HonoursRetryAfterThenSucceeds`, `TestPatch_5xxBacksOffWithFullJitterAndCap`, `TestPatch_JitterIsADrawFromZeroToCeiling`, `TestPatch_GivesUpAfterMaxAttemptsWithoutSleepingAfterTheLast`, `TestPatch_PermanentClientErrorIsNotRetried`, `TestPatch_CancelledContextStopsRetrying`, `TestPatch_SameIdempotencyKeyAppliesOnce`, `TestList_PagesThroughCursorAndSendsUpdatedSince`, `TestList_429ThenSuccess`. Integration: `TestIntegration_RateLimitedPushHonoursRetryAfter`.
- **Read next**: [internal/mockjira/mockjira.go](../../../internal/mockjira/mockjira.go).

## [internal/mockjira/mockjira.go](../../../internal/mockjira/mockjira.go)

- **What it does**: an in-memory fake Jira-like SaaS. It is a test double, not real Jira (the package comment says its wire format is not reproduced). It stores issues, bumps `version`, signs and sends webhooks, and injects faults.
- **Control flow**:
  1. [cmd/mockjira/main.go](../../../cmd/mockjira/main.go) builds `mockjira.New(Options)`, seeds issues with `Create`, and serves `Server.Handler()` ([handler.go](../../../internal/mockjira/handler.go)): `GET/POST /issues`, `GET/PATCH/DELETE /issues/{id}`, `POST /_admin/fail`, `POST /_admin/flush`.
  2. Each REST call first goes through `rest`, which consumes one queued fault (`FailNext`) if any.
  3. `Create` / `Update` / `Delete` change state and `enqueue` a delivery (`dlv-N`).
  4. `Flush(mode)` sends the queue as `FlushInOrder`, `FlushDuplicated`, `FlushReversed` or `FlushDropped`, each through `Deliver`, which signs with `sign`.
- **Data flow**: PATCH body (+ optional `Idempotency-Key`) -> issue with `version + 1` and a new `updatedAt` -> pending delivery `{"event","issue"}` -> signed POST to the webhook URL. List is keyset-paginated with an opaque cursor and `updated_since`; deleted issues stay in the list as tombstones.
- **How it is tested**: [mockjira_test.go](../../../internal/mockjira/mockjira_test.go), unit: `TestPatch_IncrementsVersionServerSide`, `TestPatch_SameIdempotencyKeyDoesNotBumpVersionTwice`, `TestList_KeysetPaginationAndUpdatedSince`, `TestList_IncludesTombstones`, `TestFailNext_429CarriesRetryAfterThenRecovers`, `TestFailNext_500DoesNotTouchState`, `TestSign_MatchesOpenSSLVector`, `TestDeliver_HeadersCarryDeliveryIDTimestampAndSignature`, `TestFlush_Modes`, `TestDelivery_HeldUntilFlushed`.
- **Read next**: [cmd/webhook-receiver-lambda/main.go](../../../cmd/webhook-receiver-lambda/main.go) (same router on AWS Lambda; unit-tested by `TestAdapter_ProxyEvent` in [main_test.go](../../../cmd/webhook-receiver-lambda/main_test.go)), then [infra/terraform/modules/webhook/main.tf](../../../infra/terraform/modules/webhook/main.tf) (Terraform: validated, not planned or applied; the design doc says so).
