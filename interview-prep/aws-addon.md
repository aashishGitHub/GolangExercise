# AWS add-on — optional, 3 × 45 minutes

Use this **only after** the Go core for the day is done. It is not a separate study track. The goal is to
map work you already have onto AWS vocabulary, not to become an AWS solutions architect in three days.

If the interview has no AWS component, skip this file. The Go/ControlPlane material is the offer.

---

## How to use

| Block | When | Output |
|---|---|---|
| 1 | After Day 2 or 3 | Draw ControlPlane on AWS from memory |
| 2 | After Day 4 | Explain SQS vs RabbitMQ vs EventBridge with failure modes |
| 3 | After Day 5 | Security/ops/cost answers + 3-minute walkthrough |

Each block is 45 minutes: 10 minutes recall, 20 minutes mechanism, 10 minutes Q&A aloud, 5 minutes log.
Do not let this push the core past two hours.

---

## 1. The mapping table

**Description.** This table maps each ControlPlane piece to an AWS building block and names the tradeoff. The goal is not a full AWS redesign; it is to show that the correctness mechanisms stay in Postgres while AWS changes the compute, transport, and operations. The one-sentence version is the answer to "how would you deploy this?"

| ControlPlane piece | AWS option | Why / tradeoff |
|---|---|---|
| Go API process | ECS Fargate service behind ALB | Long-lived HTTP, simple container, no server management |
| Worker process | Separate ECS Fargate service | Long-running tasks; Lambda's 15-minute limit and cold starts fit poorly |
| Postgres | RDS for PostgreSQL, Multi-AZ | Managed backups/PITR/failover; same SQL and constraints |
| Connection pooling | RDS Proxy (optional) | Useful with many tasks/Lambda; adds hop and cost |
| RabbitMQ | Amazon MQ for RabbitMQ | Closest lift-and-shift; managed brokers, but check engine/feature support and cost |
| RabbitMQ alternative | SQS (+ SNS for fan-out) | Cheaper/simpler queue; loses exchanges/routing/per-message requeue semantics |
| Outbox relay | Same code in the API task or a separate ECS service | RDS + SQS/MQ are still not one transaction; outbox remains necessary |
| Update consumer | Same API task or separate ECS service | Keep the inbox and rank guard; transport changes, correctness does not |
| Secrets | Secrets Manager / SSM Parameter Store | No `guest/guest`, no secrets in env files |
| IAM | ECS task role (least privilege) + execution role | Task role for SQS/S3/Secrets; execution role for ECR/logs |
| Logs/metrics | CloudWatch Logs, metrics, alarms; ADOT/X-Ray for traces | JSON logs already structured; add correlation IDs |
| Container registry | ECR | Scan on push, immutable tags |
| TLS/DNS | ACM + Route 53 + ALB | TLS terminates at ALB; re-encrypt to app if required |
| WAF/rate limiting | AWS WAF + API Gateway/ALB rules | Edge protection; app still enforces per-tenant limits |
| Network | VPC: public subnets for ALB/NAT, private for tasks/RDS | No database in a public subnet |
| IaC | Terraform (S3 remote state + locking) | Reproducible; review plan; avoid console drift |
| CI/CD | GitHub Actions → ECR → ECS deploy | Build/test/scan, then rolling deploy with health checks |
| Backups/DR | RDS automated backups + PITR; cross-region snapshots | Define RPO/RTO; test restore, not just backup |

### The architecture sentence

> "Lift the containers to ECS Fargate, put Postgres on RDS Multi-AZ, keep the outbox and inbox unchanged,
> and replace RabbitMQ with Amazon MQ first — or SQS if the routing model can be simplified. The
> correctness mechanisms stay in Postgres; AWS changes the transport and operations, not the invariants."

That sentence is the whole add-on. Everything else is detail.

---

## 2. ControlPlane on AWS — draw it

```text
Route 53 → ACM → ALB (public subnets)
                    │
                    ▼
          ECS Fargate: controlplane (private subnets)
             │                         │
             ▼                         ▼
       RDS Postgres              Amazon MQ / SQS
       (Multi-AZ)                      │
             ▲                         ▼
             │                 ECS Fargate: worker
             │                         │
             └──── update consumer ◄───┘
                    (same API task or separate service)

CloudWatch Logs/Metrics/Alarms ← all tasks
Secrets Manager ← DB/MQ credentials
ECR ← images; IAM task roles ← least privilege
```

Key points to say:

- API and worker are separate services, so they scale and fail independently.
- The relay can live in the API task (fewer moving parts) or as its own service (independent scaling).
- RDS is private; only the task security group can reach 5432.
- The ALB health check hits `/healthz` (readiness), not a shallow liveness endpoint.
- The worker has no DB credentials; it only publishes progress.
- CloudWatch alarms: oldest unpublished outbox age, DLQ depth, 5xx rate, task age, RDS connections.

**If they ask "why not Lambda for everything?"**

> "The API could be Lambda behind API Gateway, but provisioning is long-running and the worker benefits
> from a warm process and controlled concurrency. Lambda's 15-minute limit, cold starts, and per-invocation
> DB connections add failure modes without removing the need for the outbox. Fargate is the lower-risk
> lift. I would use Lambda for short, spiky handlers, not the long worker."

---

## 3. SQS vs RabbitMQ vs EventBridge

### SQS

**Description.** SQS is a managed queue with at-least-once delivery. Standard queues prioritize throughput and best-effort ordering; FIFO queues add per-`MessageGroupId` ordering and deduplication within a five-minute window. A received message is hidden for the visibility timeout rather than deleted, so a crashed consumer causes redelivery after the timeout. There are no exchanges or routing keys; fan-out is SNS to multiple queues.

- Standard: at-least-once, best-effort ordering, nearly unlimited throughput.
- FIFO: ordering per `MessageGroupId`, dedup ID within a 5-minute window, lower throughput.
- Visibility timeout: a received message is hidden, not deleted; if the consumer crashes, it reappears
  after the timeout. Set it longer than the handler's worst case; heartbeat/extend for long work.
- DLQ: `maxReceiveCount`; redrive to the source queue after a fix.
- Long polling reduces empty receives.
- No exchanges/routing keys. Fan-out is SNS → multiple SQS queues.
- Lambda integration: partial batch failure response (`batchItemFailures`) so only failed messages retry.

Mapping:

| ControlPlane | SQS |
|---|---|
| `worker.tasks` | Work queue; one consumer service |
| `cp.task-updates` | Update queue; DLQ + `maxReceiveCount` |
| routing keys | Separate queues or message attributes/filter policies |
| per-message ack | Delete after success; visibility timeout is the redelivery window |
| `x-delivery-limit` | `maxReceiveCount` on the DLQ redrive policy |
| priority | Separate queues + separate consumers (SQS has no priorities) |

**Does SQS remove the outbox?** No. RDS and SQS are still two systems. The relay publishes after commit;
a crash between `SendMessage` and marking `published_at` causes a duplicate, which the inbox absorbs. The
same crash matrix applies.

**FIFO and per-tenant ordering:** `MessageGroupId = tenant_id` gives ordering within a tenant. It does not
give global ordering, and it can hot-spot a busy tenant. The rank guard is still useful for out-of-order
delivery and duplicate terminal updates.

### RabbitMQ (Amazon MQ)

**Description.** Amazon MQ for RabbitMQ is the closest lift-and-shift for ControlPlane because it preserves exchanges, routing keys, per-message ack/nack, requeue, DLX, and priorities. It has more operational surface than SQS: Amazon manages the broker, but you still own topology, queue type, and tuning. It fits when routing and poison-message semantics are core rather than incidental.

- Exchanges, routing keys, per-message ack/nack, requeue, DLX, priorities.
- Quorum queues for durability/replication; check the Amazon MQ engine version and feature support.
- More operational surface than SQS; Amazon MQ manages brokers but you still own topology and tuning.
- Best when the routing model and poison/requeue semantics are core, as in ControlPlane.

### EventBridge

**Description.** EventBridge is an event bus with rules, filtering, targets, archive/replay, and a schema registry. It is built for domain-event fan-out and integration, with at-least-once delivery and per-target retry/DLQ behavior. It is not a per-message work queue with ack and visibility semantics, so long provisioning work belongs on SQS or RabbitMQ.

- Event bus with rules, filtering, targets, archive/replay, schema registry.
- At-least-once delivery; targets have their own retry/DLQ behavior.
- Great for domain-event fan-out and integration; not a per-message work queue with ack/visibility.
- Do not use it as the only work queue for long provisioning tasks; use SQS for work, EventBridge for
  notifications/fan-out.

**Answer skeleton:**

> "I would keep RabbitMQ if the exchange/routing/DLX model is valuable and Amazon MQ's cost is acceptable.
> If the model can be simplified, SQS standard plus a DLQ is cheaper and operationally simpler; FIFO only
> if per-tenant ordering is a hard requirement. EventBridge is for fan-out/domain events, not for the work
> queue. In all three, the outbox and inbox stay because the database and the broker are separate."

---

## 4. Serverless mapping — offline-sync-app

Use this to answer "have you built serverless?" without turning the Go notebook into an AWS course.

| Concern | AWS building block | Design note |
|---|---|---|
| API | API Gateway HTTP API + Lambda | Return 202 for long work; API Gateway timeout is not a provisioning budget |
| Auth | Cognito user pool + JWT authorizer | Derive tenant/user from claims; never trust a body tenant ID |
| Sync write | Lambda → RDS/DynamoDB | Idempotency key + conditional write |
| Queue | SQS + DLQ | Visibility timeout > handler; partial batch failures |
| Domain events | EventBridge | Fan-out, archive/replay, separate consumers |
| Client outbox | Offline client outbox (already built) | Drains to an idempotent endpoint; server dedups |
| Server inbox | `processed_events` / conditional put | Same idempotency model as ControlPlane |
| Files | S3 presigned URLs | Keep large payloads out of Lambda/API |
| Secrets | Secrets Manager / SSM | No secrets in client or repo |
| Observability | CloudWatch + X-Ray/ADOT | Correlation ID across API → queue → consumer |
| IaC | Terraform | Remote state + locking; modules per environment |

### DynamoDB in one paragraph

**Description.** DynamoDB is a key-value/document store where partition-key design is the schema. Conditional writes provide idempotency, `TransactWriteItems` covers small multi-item invariants, TTL expires items, streams emit changes, and GSIs serve alternate access patterns. It scales well by key, but it is not a general relational constraint engine; unique-slug or one-open-task invariants are simpler in Postgres.

Partition key design is the schema. Use conditional writes for idempotency (`attribute_not_exists(pk)`),
`TransactWriteItems` for small multi-item invariants, TTL for expiry, streams for change events, and GSIs
for access patterns. It is excellent at key-based scale; it is not a general relational constraint engine.
If the invariant is "one open task per tenant" or "unique slug forever", Postgres is the simpler arbiter.

### Lambda + RDS

**Description.** Each concurrent Lambda invocation may open its own database connection, so a relational database needs RDS Proxy or a small pool to avoid connection exhaustion. Cold starts affect latency, not correctness. VPC-attached Lambdas need NAT or VPC endpoints for AWS APIs, and long-running work should move to ECS or Step Functions rather than fight the 15-minute limit.

- Each concurrent invocation can open a connection; use RDS Proxy or a small pool.
- Cold starts matter for latency, not correctness.
- VPC-attached Lambdas need NAT or VPC endpoints for AWS APIs.
- Long work → ECS/Step Functions, not a 15-minute Lambda.
- Async invocation failures go to a DLQ/destination; configure it.

### Partial batch failures

**Description.** An SQS-triggered Lambda receives a batch, but only some records may fail. Returning `batchItemFailures` tells SQS to retry just those message IDs instead of the whole batch, which is the serverless analogue of acking each message after its transaction commits. Without it, one poison record causes the entire batch to be redelivered.

```json
{ "batchItemFailures": [ { "itemIdentifier": "message-id-1" } ] }
```

Return only the failed messages so SQS does not redeliver the whole batch. This is the serverless analogue
of ack-after-commit per message.

---

## 5. Security and operations — the senior answers

### Identity

**Description.** An ECS task role grants the running application its AWS permissions; the execution role is for the agent that pulls images and writes logs at launch. Give each service least privilege: the API can read/write RDS and publish, the worker can consume and publish, and neither can delete the database. Per-tenant authorization is an application concern, and the tenant ID must come from the token, not the request body.

- ECS **task role** for the app's AWS permissions; **execution role** for ECR/logs/secrets at launch.
- Least privilege per service: API can read/write RDS + publish; worker can consume + publish; neither
  can delete the database.
- No long-lived access keys in containers. Use IAM roles.
- Per-tenant authz is an application concern, not just IAM. Tenant ID comes from the token, not the body.

### Network

**Description.** Put the ALB in public subnets and the tasks and RDS in private subnets. Security groups should allow only the edges you need: ALB to API on 8080, API to RDS on 5432, and API/worker to the broker on 5671/5672. VPC endpoints for ECR, S3, CloudWatch, and Secrets Manager avoid NAT data charges, and TLS should terminate at the ALB with optional re-encryption inside.

- ALB in public subnets; tasks and RDS in private subnets.
- Security groups: ALB → API only on 8080; API → RDS only on 5432; API/worker → MQ only on 5671/5672.
- VPC endpoints for ECR, S3, CloudWatch, Secrets Manager to avoid NAT data charges.
- TLS everywhere; certificate on ALB; optionally mTLS/re-encrypt inside.

### Secrets

**Description.** Store database and broker credentials in Secrets Manager or SSM Parameter Store, with rotation for database credentials. Do not bake secrets into images, commit them to the repository, or leave them in plaintext Terraform state. Use separate secrets per environment so a staging credential cannot open production.

- Secrets Manager with rotation for DB credentials; no `guest/guest`.
- Do not bake secrets into images or Terraform state in plaintext.
- Separate secrets per environment.

### Observability

**Description.** Emit structured JSON logs with request, tenant, task, and message IDs so one trace crosses HTTP, database, broker, and consumer. Track request rate/latency/errors, oldest unpublished outbox age, relay failures, DLQ depth, consumer lag, task age, and database connections/lock waits. Alarm on user-visible symptoms, not on dashboards, and propagate correlation IDs through traces.

- Structured JSON logs with request ID, tenant ID, task ID, message ID.
- Metrics: request rate/latency/error by code; outbox oldest unpublished age; relay publish failures; DLQ
  depth; consumer lag/unacked; task age by status; DB connections/lock waits.
- Alarms on symptoms: stuck tenants, oldest outbox age, DLQ growth, 5xx rate.
- Traces across HTTP → DB → broker → consumer; propagate correlation IDs.
- Dashboards are not alerts; page on user-visible symptoms.

### Deployment and DR

**Description.** Deploy immutable image tags with health checks and rolling or blue-green rollout. Run migrations as a one-off task using an expand → migrate → contract sequence; `CREATE IF NOT EXISTS` is not a production migration strategy. Define RPO/RTO, use RDS automated backups plus PITR and Multi-AZ, keep cross-region snapshots, and test the restore rather than assuming the backup works.

- Immutable image tags; scan on push; deploy with health checks and rolling/blue-green.
- Migrations: run as a one-off task with a backward-compatible sequence (expand → migrate → contract).
  `CREATE IF NOT EXISTS` is not enough for production.
- RDS automated backups + PITR; test restore. Define RPO/RTO.
- Multi-AZ for RDS and tasks; SQS/MQ durability; cross-region snapshots for DR.
- Runbooks for broker down, DB failover, DLQ replay, stuck tasks.

### Cost

**Description.** The main cost drivers are NAT Gateway data processing, Amazon MQ broker hours, RDS Multi-AZ, and idle Fargate tasks. Right-size tasks, autoscale on queue depth or CPU, and use Savings Plans for steady load. SQS and EventBridge are cheap per request but not free at volume, and CloudWatch log ingestion adds up; set budgets and alarms instead of discovering the bill.

- NAT Gateway data processing; Amazon MQ broker hours; RDS Multi-AZ; idle Fargate tasks.
- Right-size tasks; autoscale on queue depth/CPU; use Savings Plans for steady load.
- SQS is cheap at low volume; EventBridge charges per event; CloudWatch logs ingestion adds up.
- Budgets/alarms, not surprise bills.

---

## 6. Twelve Q&A — say each in 45 seconds

<details>
<summary><strong>1. How would you deploy ControlPlane on AWS?</strong></summary>

ECR images for API and worker; ECS Fargate services in private subnets; ALB for the API; RDS Postgres
Multi-AZ; Amazon MQ for RabbitMQ (or SQS if simplifying); Secrets Manager; IAM task roles; CloudWatch;
Terraform. The relay/consumer stay in the API task initially; split them when scaling or failure isolation
demands it. The outbox/inbox are unchanged because RDS and the broker are still separate systems.

</details>

<details>
<summary><strong>2. Why not Lambda for the worker?</strong></summary>

Provisioning is long-running, and Lambda has a 15-minute limit, cold starts, and per-invocation DB
connections. A warm ECS service gives controlled concurrency and a simpler long-task model. Lambda is fine
for short, spiky handlers; it does not remove the need for the outbox or idempotency.

</details>

<details>
<summary><strong>3. SQS or RabbitMQ?</strong></summary>

RabbitMQ if the exchange/routing/DLX model is valuable and Amazon MQ cost is acceptable. SQS standard plus
DLQ if the model can be simplified: cheaper, managed, visibility timeout instead of per-message ack. FIFO
only if per-tenant ordering is a hard requirement. EventBridge is for fan-out, not the work queue. The
outbox and inbox survive either choice.

</details>

<details>
<summary><strong>4. Does SQS replace the outbox?</strong></summary>

No. RDS and SQS are two systems; there is no shared transaction. The service commits the task row, the
relay sends to SQS, then marks `published_at`. A crash between send and mark causes a duplicate, which the
inbox absorbs. Without the outbox, a crash after commit can lose the event.

</details>

<details>
<summary><strong>5. How do you handle poison messages in SQS?</strong></summary>

A redrive policy with `maxReceiveCount` sends repeatedly failing messages to a DLQ. The consumer classifies
permanent errors and stops retrying immediately; transient errors retry and then return to the queue. Alarm
on DLQ depth; redrive after a fix. For Lambda, return `batchItemFailures` so only failed messages retry.

</details>

<details>
<summary><strong>6. How do you get ordering with SQS?</strong></summary>

FIFO queue with `MessageGroupId = tenant_id` gives per-tenant ordering and dedup within the 5-minute window.
There is no global ordering, and a hot tenant can limit throughput. The rank guard still handles
out-of-order/duplicate terminal updates, and the inbox still dedups.

</details>

<details>
<summary><strong>7. How do you secure multi-tenant access?</strong></summary>

Cognito/JWT at the edge, tenant ID from claims (never the body), application-level tenant scoping on every
query, per-tenant rate limits/quotas, least-privilege IAM task roles, private subnets, TLS, Secrets Manager,
audit logs. IAM is not a substitute for row-level authorization.

</details>

<details>
<summary><strong>8. How do you monitor this system?</strong></summary>

Request rate/latency/errors by code; oldest unpublished outbox age; relay publish failures; DLQ depth;
consumer lag/unacked; task age by status; DB connections/lock waits; RDS failover events. Alarm on stuck
tenants and DLQ growth, not just CPU. Trace HTTP → DB → broker → consumer with correlation IDs.

</details>

<details>
<summary><strong>9. How do you migrate the schema?</strong></summary>

Versioned migrations in CI, run as a one-off ECS task before the app rollout. Use expand → migrate →
contract: add nullable columns/new tables, backfill, dual-write/read, then remove old fields. Never rely on
`CREATE IF NOT EXISTS` for an evolving production schema. Take a backup and have a rollback plan.

</details>

<details>
<summary><strong>10. What is your DR plan?</strong></summary>

Define RPO/RTO. RDS automated backups + PITR, Multi-AZ failover, cross-region snapshots; infrastructure in
Terraform to rebuild; SQS/MQ durability; replay unpublished outbox rows; idempotent consumers make replay
safe. Test restore and failover, not just the backup job.

</details>

<details>
<summary><strong>11. How do you handle long-running provisioning?</strong></summary>

API commits intent and returns 202; a worker/service does the work and reports progress; the client polls
or subscribes. Use ECS/Step Functions for long work, not a request-bound Lambda. Add a lease/heartbeat and
a reaper for stuck tasks; reconcile with the provider. The current ControlPlane build has no reaper — name
that gap.

</details>

<details>
<summary><strong>12. What would you change at 10x?</strong></summary>

Split relay/consumer into independently scaled services; batch confirms; partition/shard by tenant; add a
stuck-task reaper and provider reconciliation; archive published outbox rows and expire inbox rows; add
metrics/tracing; per-tenant rate limits; auth/RBAC/TLS; worker DLQ and delivery limit. Keep the invariants;
change the scale knobs.

</details>

### Rapid answer key

| # | Question | Model answer | Section |
|---|---|---|---|
| 1 | How would you deploy ControlPlane on AWS? | ECR images on ECS Fargate in private subnets, ALB for the API, RDS Postgres Multi-AZ, Amazon MQ or SQS, Secrets Manager, task roles, CloudWatch, Terraform; the outbox and inbox stay unchanged. | §2 |
| 2 | Why not Lambda for the worker? | Provisioning is long-running; Lambda has a 15-minute limit, cold starts, and per-invocation DB connections. A warm ECS service gives controlled concurrency. | §2 |
| 3 | SQS or RabbitMQ? | RabbitMQ when exchanges, routing, and DLX semantics matter; SQS plus a DLQ when the model can be simpler; FIFO only for hard per-tenant ordering. | §3 |
| 4 | Does SQS replace the outbox? | No. RDS and SQS have no shared transaction; the outbox still makes publish-after-commit structural and the inbox absorbs duplicates. | §3 |
| 5 | How do you handle poison messages in SQS? | Use a redrive policy with `maxReceiveCount` to a DLQ, classify permanent errors immediately, alarm on DLQ depth, and return `batchItemFailures` from Lambda. | §3 |
| 6 | How do you get ordering with SQS? | Use a FIFO queue with `MessageGroupId = tenant_id`; ordering is per group, not global, and the rank guard still handles reordering. | §3 |
| 7 | How do you secure multi-tenant access? | JWT/Cognito at the edge, tenant ID from claims, tenant scoping on every query, least-privilege task roles, private subnets, TLS, and per-tenant quotas. | §5 |
| 8 | How do you monitor this system? | Track request errors by code, oldest unpublished outbox age, relay failures, DLQ depth, consumer lag, task age, and DB lock waits; alarm on stuck tenants and DLQ growth. | §5 |
| 9 | How do you migrate the schema? | Versioned migrations in CI as a one-off ECS task, using expand → migrate → contract, with a backup and rollback plan; not `CREATE IF NOT EXISTS`. | §5 |
| 10 | What is your DR plan? | Define RPO/RTO, use RDS backups plus PITR and Multi-AZ, keep cross-region snapshots, rebuild with Terraform, replay the outbox, and test restore and failover. | §5 |
| 11 | How do you handle long-running provisioning? | Commit intent, return 202, let an ECS or Step Functions worker report progress, and add a lease/heartbeat plus reaper for stuck tasks. | §3 |
| 12 | What would you change at 10x? | Split relay and consumer services, batch confirms, shard by tenant, add a reaper, archive outbox rows, expire inbox rows, add metrics, auth, rate limits, and a worker DLQ. | §5 |

---

## 7. Three-minute AWS walkthrough

Use this if asked "how would you run this in production?":

> "I would keep the correctness model and change the operations. The API and worker are separate ECS
> Fargate services in private subnets behind an ALB. Postgres is RDS Multi-AZ. The outbox relay publishes
> to Amazon MQ for RabbitMQ — or SQS if we simplify routing — and the consumer applies updates with the
> same inbox and rank guard. Secrets are in Secrets Manager, IAM task roles are least privilege, and
> CloudWatch alarms cover outbox age, DLQ depth, 5xx rate, and task age. Terraform owns the
> infrastructure; migrations run as a one-off task with expand/contract. The main tradeoff is Amazon MQ
> cost and operational surface versus SQS's simpler model; I would choose based on whether the
> exchange/routing/DLX semantics are worth paying for. The outbox and inbox stay in either case because
> RDS and the broker are separate systems."

Then stop. Do not tour every AWS service.

---

## 8. What not to claim

- Do not claim production-scale AWS operations you have not run.
- Do not claim SQS is exactly-once. Standard is at-least-once; FIFO dedups within a window.
- Do not claim EventBridge is a work queue with per-message ack.
- Do not claim Lambda is a good fit for long provisioning.
- Do not claim the outbox is unnecessary because SQS is managed.
- Do not claim IAM solves tenant authorization.
- Do not claim Multi-AZ is a backup; it is availability, not point-in-time recovery.
- Do not claim a migration tool exists in ControlPlane. It does not.

---

## 9. 45-minute block plans

### Block 1 — mapping

- 10 min: draw the AWS architecture from memory; label public/private subnets, RDS, MQ/SQS, IAM, secrets.
- 20 min: read the mapping table; say the architecture sentence; explain API vs worker split.
- 10 min: answer Q1, Q2, Q7 aloud.
- 5 min: log gaps.

### Block 2 — messaging

- 10 min: recall the outbox/inbox crash matrix.
- 20 min: compare SQS/RabbitMQ/EventBridge; write the decision table from memory.
- 10 min: answer Q3, Q4, Q5, Q6 aloud.
- 5 min: log gaps.

### Block 3 — ops and serverless

- 10 min: recall security/observability/DR bullets.
- 20 min: map offline-sync-app to API Gateway/Lambda/SQS/EventBridge/DynamoDB/S3/Cognito/Terraform.
- 10 min: answer Q8–Q12 aloud; deliver the 3-minute walkthrough.
- 5 min: log gaps; stop.

---

## 10. Checkpoint

- [ ] Draw ControlPlane on AWS in under 3 minutes.
- [ ] Explain why the outbox survives SQS.
- [ ] Explain SQS visibility timeout, DLQ, FIFO group ordering.
- [ ] Explain why Lambda is a poor fit for the long worker.
- [ ] Name the security/observability/DR essentials.
- [ ] Deliver the 3-minute walkthrough without notes.
- [ ] Name five things you would not claim.

**If you only remember one sentence:** AWS changes the transport and operations; Postgres still owns the
invariants.
