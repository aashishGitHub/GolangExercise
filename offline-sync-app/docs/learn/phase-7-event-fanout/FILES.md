# Phase 7 — Event-driven Fan-out: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Shape of the whole phase:

```
local:  /api/sync -> TxRunner -> domain_events row
                                   |-> cmd/outbox-relay (5s ticker) -> EventBridge (Moto)   [nothing consumes it locally]
                                   |-> consume.RunOnce per consumer (5s ticker), dedup in processed_events
prod:   /api/sync -> TxRunner -> domain_events row
        outbox-relay Lambda (Scheduler, every minute) -> EventBridge bus -> rule -> SQS (+DLQ) -> consumer Lambda
        -> sqsconsume.Handle, dedup in processed_events
```

> Note: MILESTONES verified the push with a real Go WebSocket client. `grep` for "WebSocket" and `/ws` in `web/src` finds nothing, so the React app does not subscribe to these pushes yet.

> Note: the comment in `migrations/000002_add_domain_events.up.sql` above `ws_connections` still says "Real AWS: DynamoDB". MILESTONES, `cmd/ws-connect-lambda/main.go` and the README say DynamoDB was deliberately dropped and Postgres is used in both. The code (Lambdas use `q.InsertWSConnection`) wins: it is Postgres. The migration comment is stale.

## [internal/sync/transactional.go](../../../internal/sync/transactional.go)

- **What it does**: `TxRunner.Sync` runs a whole sync batch in one pgx transaction, and writes a `domain_events` row for each record that was `Applied`.
- **Control flow**:
  1. `NewRouter` builds `sync.NewTxRunner(beginner)` (the pgx pool). `syncHandler` and `confirmHandler` call `svc.Sync(...)` on it.
  2. `r.beginner.Begin(ctx)`, then `defer tx.Rollback(ctx)` (a no-op after commit).
  3. `NewService(db.New(tx)).Sync(...)` applies the upserts (Phase 2 logic, using the transaction as the `Querier`).
  4. Loop over `resp.Locations`, `resp.SiteAssessments`, `resp.Photos`. Skip any outcome that is not `OutcomeApplied`. For the rest call `events.Publish` with type `location.upserted`, `site_assessment.upserted` or `photo.upserted`.
  5. `tx.Commit(ctx)`. Any error earlier returns and the deferred rollback undoes everything.
- **Data flow**: `sync.Request` in, upserted rows plus one event row per applied record, committed together. The event `Payload` is the input struct (for example `PhotoInput`), marshaled to JSON.
- **How it is tested**: Integration test (`-tags=integration`, real Postgres) in [integration_test.go](../../../internal/sync/integration_test.go): `TestIntegration_TxRunnerEventsOnlyOnApplied`. It proves an applied write leaves exactly one `location.upserted` row and a stale re-send leaves still one. It does not test a rollback when `events.Publish` fails. The plain `Service` is unit tested in [sync_test.go](../../../internal/sync/sync_test.go) (for example `TestSync_Location_Applied`).
- **Read next**: [internal/events/events.go](../../../internal/events/events.go)

## [internal/events/events.go](../../../internal/events/events.go)

- **What it does**: `Publish` inserts one `domain_events` row using whatever `db.Querier` the caller holds, so it joins the caller's transaction.
- **Control flow**:
  1. Called from `TxRunner.Sync` (Phase 7). Phase 8 also calls it from `internal/twoway/apply.go` (`Applier.publish`) and `internal/twoway/workitems.go` (`WorkItems.EditNotes`).
  2. `json.Marshal(evt.Payload)`.
  3. `q.InsertDomainEvent(...)` with `SchemaVersion` fixed to the constant `1`.
- **Data flow**: `events.Event{AggregateType, AggregateID, Type, Payload, ActorSub}` in, row out with `published_at` still NULL.
- **How it is tested**: No test file in `internal/events`. It is covered indirectly by `TestIntegration_TxRunnerEventsOnlyOnApplied`.
- **Read next**: [migrations/000002_add_domain_events.up.sql](../../../migrations/000002_add_domain_events.up.sql)

## [migrations/000002_add_domain_events.up.sql](../../../migrations/000002_add_domain_events.up.sql)

- **What it does**: Creates `domain_events`, `processed_events`, `location_stats`, `ws_connections`.
- **Control flow**: `make migrate` (the `migrate` CLI) applies it. The down file drops the four tables.
- **Data flow**: Key shapes:
  - `domain_events`: `id`, `aggregate_type`, `aggregate_id`, `event_type`, `schema_version` (default 1), `payload JSONB`, `actor_sub`, `occurred_at`, `published_at` (NULL means not yet sent). A partial index `domain_events_unpublished_idx ON (occurred_at) WHERE published_at IS NULL` serves the relay's scan.
  - `processed_events`: `PRIMARY KEY (event_id, consumer_name)`.
  - `location_stats`: counts per `location_id`.
  - `ws_connections`: `connection_id` primary key, `user_sub`, `location_id`.
- **How it is tested**: Exercised by the integration tests and by the manual live run in MILESTONES. There is no migration test.
- **Read next**: [internal/outbox/relay.go](../../../internal/outbox/relay.go)

> Note: I found no index on `ws_connections.location_id` in this migration, and no cleanup of `domain_events` or `processed_events` rows.

## [internal/outbox/relay.go](../../../internal/outbox/relay.go)

- **What it does**: `Relay.RunOnce` sends up to one batch (25) of unpublished events to EventBridge and marks each published only after a confirmed success.
- **Control flow**:
  1. Local: `cmd/outbox-relay/main.go` calls it from a 5-second ticker (`make dev-outbox-relay`). Prod: `cmd/outbox-relay-lambda/main.go` `handler` calls it once per invocation, triggered by `aws_scheduler_schedule.outbox_relay` (`rate(1 minute)`).
  2. `q.ListUnpublishedEvents(ctx, 25)`.
  3. For each row, marshal an `Envelope` and call `eb.PutEvents` with `Source: "fieldsync.sync"`, `DetailType` set to the event type, and `EventBusName`.
  4. If `PutEvents` returns an error, log and `continue`. If `out.FailedEntryCount > 0`, log and `continue`. Both leave the row unpublished.
  5. Otherwise `q.MarkEventPublished`.
- **Data flow**: `domain_events` rows in, EventBridge entries out, `published_at` set. Delivery is at-least-once: a crash after `PutEvents` and before `MarkEventPublished` re-sends.
- **How it is tested**: Unit tests in [relay_test.go](../../../internal/outbox/relay_test.go) with a hand-written `fakePublisher` and a generated mock `Querier`: `TestRelay_RunOnce_PublishesAndMarks`, `TestRelay_RunOnce_LeavesUnmarkedOnPublishFailure`, `TestRelay_RunOnce_LeavesUnmarkedOnRejectedEntry`, `TestRelay_RunOnce_MultipleEventsIndependent`. They prove the mark-only-on-success rule. They do not test two relays running at once.
- **Read next**: [internal/consume/consume.go](../../../internal/consume/consume.go)

> Note: `ListUnpublishedEvents` is a plain `SELECT ... ORDER BY occurred_at LIMIT $1` with no row locking (`sqlc/queries.sql`). Two relay instances running at once (for example two local processes) can send the same event twice. Consumers' dedup absorbs that.

## [internal/consume/consume.go](../../../internal/consume/consume.go)

- **What it does**: The local-dev consumer loop body: `RunOnce` lists events this consumer has not processed, runs a handler, marks each processed.
- **Control flow**:
  1. Called from `cmd/photo-processor`, `cmd/dashboard-aggregator` (5-second tickers) and `runNotifierLoop` inside `cmd/server`.
  2. `q.ListUnprocessedEventsForConsumer` (a `LEFT JOIN processed_events` that excludes already-handled ids, filtered by `event_type = ANY(...)`, limit 25).
  3. `handle(ctx, evt)`. On error: log, `continue` (retried next tick).
  4. `q.MarkEventProcessed` (`INSERT ... ON CONFLICT DO NOTHING`).
- **Data flow**: `domain_events` in, handler side effect, row in `processed_events`.
- **How it is tested**: No test file in `internal/consume`. The consumers' `Handle` functions are unit tested; the loop itself was verified live per MILESTONES.
- **Read next**: [internal/sqsconsume/sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go)

> Note: locally, consumers read `domain_events` directly and do not wait for `published_at`. The relay and the consumers are independent in local dev, so a relay outage does not stop local consumers.

## [internal/sqsconsume/sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go)

- **What it does**: The prod counterpart of `consume`: `Handle` processes one SQS batch for a Lambda and returns only the failed message ids.
- **Control flow**:
  1. `cmd/photo-processor-lambda`, `cmd/dashboard-aggregator-lambda` and `cmd/notifier-lambda` each call `sqsconsume.Handle(ctx, q, ConsumerName, sqsEvent, handler)`.
  2. For each record, unmarshal the body: the EventBridge event, whose `detail` is an `outbox.Envelope`. Bad JSON appends a failure and continues.
  3. `q.IsEventProcessed`. If true, skip (acknowledge).
  4. Build a `db.DomainEvent` and call the handler. Error appends a failure.
  5. `q.MarkEventProcessed`. Error appends a failure.
  6. Return `events.SQSEventResponse{BatchItemFailures: failures}`.
- **Data flow**: SQS batch in, handler side effects, `processed_events` rows, list of failed `MessageId`s out. Failed ids are redelivered; after 5 receives they move to the DLQ (Terraform `maxReceiveCount = 5`).
- **How it is tested**: Unit tests in [sqsconsume_test.go](../../../internal/sqsconsume/sqsconsume_test.go): `TestHandle_ProcessesNewMessage`, `TestHandle_SkipsAlreadyProcessed`, `TestHandle_ReportsFailedMessageOnly`, `TestHandle_MalformedBodyReportsFailure`. They prove dedup skip and partial batch reporting. They do not prove the Terraform setting `ReportBatchItemFailures` is wired, or that a DLQ receives anything.
- **Read next**: [internal/notifier/notifier.go](../../../internal/notifier/notifier.go)

> Note: the check (`IsEventProcessed`), the handler, and the mark (`MarkEventProcessed`) are three separate steps, not one transaction. A crash between handler and mark runs the handler again.

## [internal/notifier/notifier.go](../../../internal/notifier/notifier.go)

- **What it does**: `Handle` turns a `photo.upserted` event into a WebSocket message for everyone connected to that photo's location.
- **Control flow**:
  1. Local: `runNotifierLoop` in `cmd/server/main.go` calls `consume.RunOnce` with `notifier.Handle` and the in-memory `wsstub.Hub` as the `Pusher`. Prod: `cmd/notifier-lambda` calls `sqsconsume.Handle` with `notifier.ManagementAPIPusher`.
  2. Unmarshal payload (`id`, `siteAssessmentId`, `condition`).
  3. `q.GetSiteAssessment` to get the `LocationID`.
  4. `q.ListWSConnectionsByLocation`. No connections: return nil.
  5. Marshal `{"type":"photo.upserted","photoId":...,"condition":...}` and `pusher.Push` to each connection.
- **Data flow**: event in, `ws_connections` rows read, message bytes pushed. A Push error aborts and returns the error, so the event is retried.
- **How it is tested**: Unit tests in [notifier_test.go](../../../internal/notifier/notifier_test.go): `TestHandle_PushesToAllConnectionsForLocation`, `TestHandle_NoConnectionsIsNotAnError`, `TestHandle_MalformedPayloadErrors`, using a `fakePusher`. No tests for `wsstub.Hub`, `ManagementAPIPusher`, `httpapi/ws.go`, or `cmd/ws-connect-lambda`. The live socket run in MILESTONES was manual.
- **Read next**: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf)

> Note: because Push errors return immediately, connections already pushed to in this call get the message again on retry.

## [infra/terraform/modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf)

- **What it does**: The prod event plumbing: event bus, three consumer queues with DLQs, rules, consumer Lambdas, event source mappings, plus the scheduled relay Lambda.
- **Control flow**:
  1. `aws_cloudwatch_event_bus.main`.
  2. `local.consumers` has `photo-processor`, `dashboard-aggregator`, `notifier`, each with `event_types = ["photo.upserted"]`.
  3. `for_each` over it: `aws_sqs_queue.dlq` (`message_retention_seconds = 1209600`), `aws_sqs_queue.queue` (`visibility_timeout_seconds = 30`, `redrive_policy` with `maxReceiveCount = 5`), `aws_cloudwatch_event_rule.consumer` (pattern on `detail-type`), `aws_cloudwatch_event_target.consumer` to the queue, `aws_sqs_queue_policy.queue` (only that rule may send).
  4. Per consumer: an IAM role, `consumer_app` policy (`GetSecretValue`, SQS receive/delete/get-attributes on its queue, plus `execute-api:ManageConnections` for notifier only), `aws_lambda_function.consumer` (timeout 30, memory 256), and `aws_lambda_event_source_mapping.consumer` (`batch_size = 10`, `function_response_types = ["ReportBatchItemFailures"]`).
  5. Relay: `aws_lambda_function.outbox_relay` with `events:PutEvents` on the bus, and `aws_scheduler_schedule.outbox_relay` (`rate(1 minute)`) with its own scheduler role.
- **Data flow**: event in the bus, matched by `detail-type`, delivered to the queue, read by Lambda in batches of up to 10.
- **How it is tested**: `terraform validate` only (`make tf-validate`). MILESTONES says a CloudWatch alarm on DLQ depth was not built and cannot be tested locally.
- **Read next**: [QUESTIONS.md](QUESTIONS.md)

> Note: only `photo.upserted` has a rule. `location.upserted` and `site_assessment.upserted` events are written to `domain_events` and sent to the bus, but no rule matches them.
>
> Note: the queue's visibility timeout and the consumer Lambda timeout are both 30 seconds. I believe AWS guidance is for the visibility timeout to be several times the function timeout for SQS-triggered Lambdas. I am not certain of the exact figure; check the Lambda SQS docs.

## Also in this phase (no separate section)

- [internal/photoprocessor/photoprocessor.go](../../../internal/photoprocessor/photoprocessor.go): `Handle` unmarshals the payload and calls `ValidateGeotag` (lat in [-90, 90], lng in [-180, 180]). A bad geotag is logged, not returned as an error. Tests in [photoprocessor_test.go](../../../internal/photoprocessor/photoprocessor_test.go): `TestValidateGeotag`, `TestHandle_MalformedPayloadErrors`, `TestHandle_BadGeotagDoesNotError`.
- [internal/dashboardaggregator/dashboardaggregator.go](../../../internal/dashboardaggregator/dashboardaggregator.go): `Handle` gets the site's `LocationID`, runs `CountPhotosByConditionForLocation`, and `UpsertLocationStats`. A full recount, so replays are harmless. Tests in [dashboardaggregator_test.go](../../../internal/dashboardaggregator/dashboardaggregator_test.go): `TestHandle_RecomputesAndUpsertsStats`, `TestHandle_MalformedPayloadErrors`.
- [internal/wsstub/hub.go](../../../internal/wsstub/hub.go): in-memory map of connection id to socket. `Hub.Push` returns nil for a missing id. Works only because the notifier loop runs inside `cmd/server`. No tests.
- [internal/httpapi/ws.go](../../../internal/httpapi/ws.go): `GET /ws` (outside the header-auth group): reads `locationId` and `token` from the query string, `verifier.VerifyToken`, `websocket.Accept`, inserts a `ws_connections` row, registers with the hub, blocks on `conn.Read`, cleans up on close. No tests.
- [cmd/ws-connect-lambda/main.go](../../../cmd/ws-connect-lambda/main.go) and [infra/terraform/modules/websocket/main.tf](../../../infra/terraform/modules/websocket/main.tf): the prod `$connect` and `$disconnect` handlers and the API Gateway WebSocket API. No tests; `terraform validate` only.
- [internal/notifier/managementapi.go](../../../internal/notifier/managementapi.go): `ManagementAPIPusher.Push` calls `PostToConnection`; a `GoneException` returns nil. No tests.
