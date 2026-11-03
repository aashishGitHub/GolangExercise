# Phase 7 — Event-driven Fan-out: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 7 adds an extra layer. When a write really changes data, the server records an event. Small workers then react to that event: check a photo's GPS, update counts, and push a live message to other users. The core sync still works without this layer.

1. **Event-driven design** — Instead of one function doing everything, one part says "this happened" and other parts react. The first part does not know who listens. Think of a shop bell: one ring, and whoever cares comes over.
   In this project: [events.go](../../../internal/events/events.go)

2. **Domain event** — A short record of a business fact, like "photo X was saved". It has an id, a type name, the data, who did it and when. Think of a line in a ship's log.
   In this project: [000002_add_domain_events.up.sql](../../../migrations/000002_add_domain_events.up.sql) (table `domain_events`)

3. **Fan-out** — One event goes to many workers, each doing its own job. Here `photo.upserted` reaches three consumers. Think of one announcement over a loudspeaker heard in three rooms.
   In this project: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf) (`local.consumers`)

4. **Consumer** — A program that receives events and does one job. This project has three: photo-processor, dashboard-aggregator, notifier. Think of three staff members who each read the same memo and act on their part.
   In this project: [photoprocessor.go](../../../internal/photoprocessor/photoprocessor.go), [dashboardaggregator.go](../../../internal/dashboardaggregator/dashboardaggregator.go), [notifier.go](../../../internal/notifier/notifier.go)

5. **Database transaction** — A group of database steps that either all succeed or all fail together. Think of a bank transfer: money leaves one account and arrives in the other, or neither happens.
   In this project: [transactional.go](../../../internal/sync/transactional.go) (`TxRunner.Sync`)

6. **The dual-write problem** — If you save to the database and then call another system, the program can crash between the two. You get data with no event, or an event with no data. Think of paying for a parcel and the post office losing the label.
   In this project: [events.go](../../../internal/events/events.go) (package comment)

7. **Transactional outbox** — The fix for the dual-write problem. In the same transaction as the data, write an "event to send" row into an outbox table. A separate worker sends it later. Think of putting a letter in the outbox tray at the same moment you file the paperwork.
   In this project: [events.go](../../../internal/events/events.go) (`Publish`)

8. **Relay** — The worker that reads unsent outbox rows, sends them, and marks them sent. If it crashes, the unsent rows are still there for next time. Think of a postman who checks the tray every few minutes.
   In this project: [relay.go](../../../internal/outbox/relay.go) (`Relay.RunOnce`)

9. **Only on real change** — The code writes an event only when the record was actually applied. A stale or rejected write makes no event. Think of only logging a goal when the ball crosses the line.
   In this project: [transactional.go](../../../internal/sync/transactional.go) (`if result.Outcome != OutcomeApplied { continue }`)

10. **EventBridge** — An AWS service that takes events and routes them by rules to targets. The bus is the mailbox, a rule is a sorting instruction. Think of a sorting office. Locally, a fake called Moto stands in.
    In this project: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf) (`aws_cloudwatch_event_bus`, `aws_cloudwatch_event_rule`)

11. **Envelope and schema version** — The envelope is the fixed outer shape of every event message. `schemaVersion` says which shape it is, so a newer sender does not break an older reader. Think of a form with a version number in the corner.
    In this project: [relay.go](../../../internal/outbox/relay.go) (`Envelope`), [events.go](../../../internal/events/events.go) (`SchemaVersion`)

12. **SQS queue** — A waiting line for messages. A worker takes a message, does the job, then deletes it. Think of a ticket queue at a deli counter.
    In this project: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf) (`aws_sqs_queue.queue`)

13. **At-least-once delivery** — The system promises a message arrives one time or more, never zero. Duplicates can happen. Think of a courier who rings twice if unsure you heard. So every consumer must cope with seeing the same event twice.
    In this project: [relay.go](../../../internal/outbox/relay.go) (`RunOnce` comment)

14. **Idempotent** — Doing it twice gives the same result as doing it once. Setting a light switch to "on" is idempotent; flipping it is not. Think of pressing an elevator button twice.
    In this project: [dashboardaggregator.go](../../../internal/dashboardaggregator/dashboardaggregator.go) (recounts, so repeats are harmless)

15. **Dedup table** — A table that remembers "I already handled this event". Before work, check it; after work, write to it. Here it is `processed_events`, keyed by event id and consumer name. The key includes the consumer name, so each consumer handles an event once, not once in total. Think of a guest list ticked off at the door, with one list per door.
    In this project: [000002_add_domain_events.up.sql](../../../migrations/000002_add_domain_events.up.sql) (`PRIMARY KEY (event_id, consumer_name)`), [sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go)

16. **DLQ (dead-letter queue)** — A side queue for messages that failed too many times. They wait there so people can look at them instead of being lost. Think of a "problem parcels" shelf. Here a message moves after `maxReceiveCount = 5` tries, and stays 14 days.
    In this project: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf) (`aws_sqs_queue.dlq`, `redrive_policy`)

17. **Lambda event source mapping** — The wiring that lets AWS read a queue and call your Lambda with a batch of messages. Here the batch size is 10. Think of a conveyor belt feeding a worker 10 items at a time.
    In this project: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf) (`aws_lambda_event_source_mapping.consumer`)

18. **Partial batch failure** — If 1 message in a batch of 10 fails, the Lambda reports just that one. Only that one is retried. Without it, all 10 would come back. Think of a teacher returning only the one failed test. The setting is `ReportBatchItemFailures`.
    In this project: [sqsconsume.go](../../../internal/sqsconsume/sqsconsume.go) (`BatchItemFailures`)

19. **Visibility timeout** — After a worker takes a message, SQS hides it for some seconds. If the worker does not delete it in time, it reappears for another try. Think of a library book on loan: if not returned by the due date, it goes back on the shelf. Set to 30 seconds here.
    In this project: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf) (`visibility_timeout_seconds`)

20. **EventBridge Scheduler** — A cron-like timer from AWS that calls something on a schedule. In prod it calls the relay Lambda every minute. Locally, a loop with a 5-second ticker does the same job. Think of an alarm clock.
    In this project: [modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf) (`aws_scheduler_schedule.outbox_relay`), [cmd/outbox-relay/main.go](../../../cmd/outbox-relay/main.go)

21. **Local substitute** — No free local copy of "SQS triggers Lambda" exists, so locally each consumer polls the `domain_events` table every 5 seconds. The dedup rule stays the same. Think of a rehearsal with a stand-in actor. In local mode the relay and the consumers do not depend on each other.
    In this project: [consume.go](../../../internal/consume/consume.go)

22. **Materialized stats table** — A table that stores a pre-computed answer so readers do not recount. `location_stats` holds good, moderate and bad counts per location. Think of a scoreboard kept up to date instead of recounting the match. Nothing in the web app reads it yet.
    In this project: [dashboardaggregator.go](../../../internal/dashboardaggregator/dashboardaggregator.go)

23. **WebSocket** — A long-lived two-way connection between browser and server. The server can push a message at any time. Think of a phone call, where HTTP is a letter. Used for live "new photo" alerts.
    In this project: [ws.go](../../../internal/httpapi/ws.go)

24. **Connection registry (`ws_connections`)** — A table listing who is connected and for which location. The notifier reads it to know who to message. Think of a seating chart. It is in Postgres in both local and prod.
    In this project: [000002_add_domain_events.up.sql](../../../migrations/000002_add_domain_events.up.sql) (`ws_connections`)

25. **Management API and GoneException** — In prod, a Lambda pushes to a WebSocket client by calling API Gateway's Management API with a connection id. `GoneException` means "that client left". The code treats that as not an error. Think of sending a text to a number that just hung up.
    In this project: [managementapi.go](../../../internal/notifier/managementapi.go) (`Push`, `isGone`)
