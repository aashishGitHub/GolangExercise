# Phase 7 — Event-driven Fan-out: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [../README.md](../README.md)

## L1 Beginner

**Q1. What is a domain event, and why does this app need an event layer at all?**
A domain event is a stored record of a business fact, like "photo X was saved". The sync API stays simple and fast, and other jobs hang off the event: validate the photo's GPS, refresh per-location counts, push a live message to other workers. MILESTONES labels the phase "additive, not a launch blocker": sync works without it. Source: [events.go](../../../internal/events/events.go), [MILESTONES.md](../../../MILESTONES.md).

**Q2. What is the transactional outbox, and what problem does it solve?**
It solves the dual-write problem: saving data and then calling EventBridge can crash in between and lose the event. Instead, `TxRunner` writes the event row into `domain_events` in the same transaction as the data. A relay sends unsent rows later. If the process dies, the row is still there. Source: [transactional.go](../../../internal/sync/transactional.go), [relay.go](../../../internal/outbox/relay.go).

**Q3. What does "at-least-once delivery" mean for this code?**
An event may arrive more than once, never zero times. The relay can re-send after a crash, and SQS can redeliver. So every consumer checks `processed_events` first and must be safe to repeat. Source: [sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go).

**Q4. What is a DLQ, and where is it here?**
A dead-letter queue holds messages that failed too many times so they are not lost. Each of the three consumer queues has one: `maxReceiveCount = 5`, retention 1209600 seconds (14 days). Source: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf).

Try it:
```
make tf-validate
```

## L2 Junior

**Q5. Trace a photo confirm to a consumer in local dev.**
`confirmHandler` calls `svc.Sync` on the `TxRunner`. In one transaction it upserts the photo; if the outcome is `Applied` it calls `events.Publish`, which inserts a `photo.upserted` row; then commit. Separately, `cmd/photo-processor` (5-second ticker) calls `consume.RunOnce`, which lists events not in `processed_events` for its name, runs `photoprocessor.Handle`, then `MarkEventProcessed`. Source: [photos.go](../../../internal/httpapi/photos.go), [transactional.go](../../../internal/sync/transactional.go), [consume.go](../../../internal/consume/consume.go).

Try it:
```
make compose-up
make migrate
make dev-server
make dev-photo-processor
```

**Q6. Trace the same event in prod.**
Same transaction writes the row. `aws_scheduler_schedule.outbox_relay` fires every minute and invokes `cmd/outbox-relay-lambda`, whose `handler` calls `Relay.RunOnce`: list unpublished, `PutEvents` to the bus, `MarkEventPublished`. An EventBridge rule on `detail-type = photo.upserted` sends it to each consumer's SQS queue. The event source mapping calls the consumer Lambda with up to 10 messages, which calls `sqsconsume.Handle`. Source: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf), [cmd/outbox-relay-lambda/main.go](../../../cmd/outbox-relay-lambda/main.go), [sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go).

**Q7. How does a consumer avoid doing an event twice?**
Locally, `ListUnprocessedEventsForConsumer` does a `LEFT JOIN processed_events` and keeps only rows with no match for this `consumer_name`. In prod, `sqsconsume.Handle` calls `IsEventProcessed` before the handler and `MarkEventProcessed` after (`INSERT ... ON CONFLICT DO NOTHING`). The primary key is `(event_id, consumer_name)`. Source: [sqlc/queries.sql](../../../sqlc/queries.sql), [sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go).

**Q8. How does the notifier know whom to push to, locally and in prod?**
It loads the photo's site to get the `LocationID`, then `ListWSConnectionsByLocation` from `ws_connections`. Locally the `Pusher` is `wsstub.Hub`, an in-memory map in the same process as `cmd/server`. In prod it is `ManagementAPIPusher`, calling `PostToConnection`. Connections are registered by `wsHandler` (local) or `cmd/ws-connect-lambda` (prod `$connect`). Source: [notifier.go](../../../internal/notifier/notifier.go), [hub.go](../../../internal/wsstub/hub.go), [managementapi.go](../../../internal/notifier/managementapi.go).

## L3 Mid

**Q9. Why outbox plus relay, rather than calling EventBridge right after commit?**
A direct call after commit can fail or the process can die, and the event is gone. A call before commit can announce a write that then rolls back. The outbox makes the durable record part of the transaction; delivery becomes a retryable background job. The price is latency (a minute in prod, 5 seconds locally) and duplicates. What proves it: `TestIntegration_TxRunnerEventsOnlyOnApplied` (needs `make compose-up` and `make migrate`) shows the event row exists for an applied write and does not for a stale one; `TestRelay_RunOnce_LeavesUnmarkedOnPublishFailure` and `TestRelay_RunOnce_LeavesUnmarkedOnRejectedEntry` show a failed send stays unpublished. Neither tests a crash mid-transaction. Source: [integration_test.go](../../../internal/sync/integration_test.go), [relay_test.go](../../../internal/outbox/relay_test.go).

Try it:
```
make test-integration
make test
```

**Q10. Why does the relay check `FailedEntryCount`, not only the Go error?**
`PutEvents` can return success at the call level while rejecting individual entries. Without the check the row would be marked published and the event lost. Source: [relay.go](../../../internal/outbox/relay.go). Proof: `TestRelay_RunOnce_LeavesUnmarkedOnRejectedEntry`.

**Q11. Why is dedup keyed per consumer, and why Postgres for `ws_connections`?**
Each consumer must see every event, so a global "processed" flag would let the first consumer starve the others. MILESTONES records that all three consumers stored the same `event_id` under their own names in the live run. For `ws_connections`: the plan's DynamoDB was dropped because the app's connection volume (a handful of field workers per location) does not justify it; one database serves local and prod. Source: [000002_add_domain_events.up.sql](../../../migrations/000002_add_domain_events.up.sql), [cmd/ws-connect-lambda/main.go](../../../cmd/ws-connect-lambda/main.go).

> Note: the comment in that migration still says "Real AWS: DynamoDB". The code uses Postgres; the comment is stale.

**Q12. What do the unit tests prove, and what do they leave open?**
Proved: `sqsconsume` skips already-processed ids and reports only the failing message (`TestHandle_SkipsAlreadyProcessed`, `TestHandle_ReportsFailedMessageOnly`, `TestHandle_MalformedBodyReportsFailure`); consumers reject bad payloads (`TestHandle_MalformedPayloadErrors` in each package); the notifier pushes to all connections for a location (`TestHandle_PushesToAllConnectionsForLocation`); a bad geotag does not return an error (`TestHandle_BadGeotagDoesNotError`).
Open: no tests for `consume.RunOnce`, `events.Publish` directly, `wsstub`, `ws.go`, `ManagementAPIPusher`, the `*-lambda` entrypoints. The Terraform is `terraform validate` only, so nothing proves `ReportBatchItemFailures` or the DLQ redrive work. The live runs in MILESTONES were manual. Source: test files listed in [FILES.md](FILES.md).

## L4 Senior

**Q13. What failure modes does this design have?**
- Duplicates: the relay can re-send after a crash, and `ListUnpublishedEvents` takes no row lock, so two relay instances running at once (for example two local processes) can send the same event. Dedup absorbs it.
- Dedup is not atomic with the side effect: check, handler, mark are three steps. A crash between handler and mark repeats the handler. The aggregator is safe (it recounts). The notifier is not: it pushes again.
- Notifier retry: `Handle` returns on the first failed `Push`, so connections already pushed to receive it again on retry.
- No ordering guarantee: a failed row is skipped and retried later while newer rows go first.
- Poison messages: a message that always fails goes to the DLQ after 5 receives, and nothing alerts or replays it (MILESTONES says the CloudWatch alarm was not built).
Source: [relay.go](../../../internal/outbox/relay.go), [sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go), [notifier.go](../../../internal/notifier/notifier.go).

**Q14. What breaks at 100x scale?**
Reading from the code constants (nothing here was measured):
- Relay throughput: one `RunOnce` per invocation, batch size 25, one `PutEvents` call per event, scheduled `rate(1 minute)`. That drains at most 25 events per minute. If writes exceed that, the unpublished backlog grows without bound. Locally the same batch runs every 5 seconds.
- `domain_events` and `processed_events` are never purged, and I found no index on `ws_connections.location_id`.
- `consume.RunOnce` joins two growing tables on every 5-second tick per consumer.
- The notifier pushes sequentially to each connection of a location.
- Every consumer Lambda opens a pgx pool at init, adding connection pressure to the RDS Proxy.
Source: [relay.go](../../../internal/outbox/relay.go), [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf), [000002_add_domain_events.up.sql](../../../migrations/000002_add_domain_events.up.sql).

**Q15. What is NOT solved?**
- Web client: no WebSocket code in `web/src`, so nobody in the UI receives the pushes. No code in `web/src` reads `location_stats` either; only the Phase 9 GraphQL API does.
- Events for locations and sites reach the bus but no rule routes them.
- `schema_version` is stamped on every event but no consumer reads it, so there is no version handling yet.
- `wsHandler` takes `locationId` from the query string and does not check the caller may see that location.
- Stale `ws_connections` rows: `ManagementAPIPusher.Push` ignores `GoneException`; I found no cleanup job beyond `$disconnect`.
- DLQ alarm and replay tooling.
- Local fidelity gap: `wsstub` only works inside `cmd/server`, and local consumers poll the table, so SQS behaviour is never exercised locally.
- MILESTONES says the relay and "every consumer/Lambda" decode via `outbox.Envelope`. In code only `sqsconsume` does; local `consume` uses `db.DomainEvent` directly.

**Q16. What does the photo-processor actually achieve today?**
Very little: on a bad geotag it logs a line and returns nil (deliberately, so it does not retry a permanently bad record). The finding is not stored, no flag is set on the photo, nothing alerts. It proves the pipeline wiring end to end, which is the stated goal, but as a validation feature it has no durable effect. Thumbnail generation was explicitly left out. Source: [photoprocessor.go](../../../internal/photoprocessor/photoprocessor.go).
