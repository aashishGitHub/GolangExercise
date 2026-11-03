# Phase 11 — IaC and Lambda Twins: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 11](../../../MILESTONES.md)

> Note: nothing in this phase was deployed. MILESTONES says `terraform plan` was never run. Only `terraform validate` (checks the files are well-formed) ran.

1. **Infrastructure as code (IaC)** — you describe servers, databases and queues in text files instead of clicking in a web console. You can review, version and repeat the files. It is like a recipe instead of a cook's memory.
   In this project: [infra/terraform/](../../../infra/terraform/)

2. **Terraform** — a tool that reads those files and makes the cloud match them. The language is HCL (HashiCorp Configuration Language). It compares "what I wrote" with "what exists" and creates the difference. It is like a builder working from a blueprint.
   In this project: [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

3. **Module** — a reusable folder of Terraform files with inputs (variables) and outputs. Fourteen modules exist here: `network`, `secrets`, `database`, `cache`, `auth`, `storage`, `compute`, `api`, `websocket`, `eventing`, `waf`, `observability`, `search`, `waitingroom`. It is like a Lego set where each box builds one part.
   In this project: [infra/terraform/modules/](../../../infra/terraform/modules/)

4. **Validate vs plan vs apply** — `validate` checks syntax and types only. `plan` shows what would change in the real cloud. `apply` changes it. This project only ran `validate`. It is like proofreading a letter before deciding to post it.
   In this project: `tf-validate` in [Makefile](../../../Makefile)

5. **Provider with fake credentials** — Terraform talks to AWS through a provider. Here the provider uses placeholder keys and `skip_*` flags so `validate` runs with no AWS account. It is like practising a phone call with a toy phone.
   In this project: [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

6. **AWS Lambda** — a function the cloud runs for you only when something calls it. You do not manage a server. You pay per run. It is like a taxi, not a car you own.
   In this project: [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

7. **Lambda twin** — a second `main` package that runs the same business code as the local program, but with the Lambda entry point. Seven twins exist: `server-lambda`, `outbox-relay-lambda`, `hold-reaper-lambda`, `projector-lambda`, `reconciler-lambda`, `saga-worker-lambda`, `reminder-scheduler-lambda`. It is like a left-hand and right-hand glove cut from one pattern.
   In this project: [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go)

8. **Local ticker vs scheduled invocation** — locally, a program loops forever on a timer (a ticker). On AWS, a scheduler calls the Lambda once per tick and the function exits. The work per pass is the same. It is like a night guard who walks rounds all night versus an alarm that wakes a guard once an hour.
   In this project: [cmd/reminder-scheduler/main.go](../../../cmd/reminder-scheduler/main.go) vs [cmd/reminder-scheduler-lambda/main.go](../../../cmd/reminder-scheduler-lambda/main.go)

9. **EventBridge Scheduler** — an AWS service that calls a target on a schedule like `rate(1 minute)`. Here it calls five of the Lambdas. It is like an alarm clock service.
   In this project: `aws_scheduler_schedule.periodic` in [infra/terraform/modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf)

10. **Extracting shared code (`RunOnce`)** — the loop body was pulled into a package function so the ticker program and the Lambda call the same code. No copy to drift. It is like one recipe card shared by two kitchens.
    In this project: [internal/reaper/reaper.go](../../../internal/reaper/reaper.go) and [internal/reminder/reminder.go](../../../internal/reminder/reminder.go)

11. **Composition root / wiring** — the one place that creates every object (database pool, Redis, S3 client, services, router) and connects them. Both `cmd/server` and `cmd/server-lambda` call it. It is like the fuse box where every appliance is plugged in once.
    In this project: [internal/wiring/wiring.go](../../../internal/wiring/wiring.go)

12. **Cross-compile** — build a program for a different computer type than the one you type on. `GOOS=linux GOARCH=arm64` on a Mac makes a Linux ARM program. Go does this without extra tools. It is like writing a book in French while sitting in an English office.
    In this project: `build-lambda` in [Makefile](../../../Makefile)

13. **arm64** — a CPU family (also called aarch64, used in Apple Silicon and AWS Graviton). The Lambdas are set to `architectures = ["arm64"]`. The code comment says arm64 is "cheaper". It is like choosing a smaller engine.
    In this project: [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

14. **`provided.al2023` runtime and `bootstrap`** — for compiled languages like Go, you give Lambda a plain executable named `bootstrap` on the "provided" Amazon Linux 2023 base. Lambda just runs it. It is like handing a chef a finished dish instead of a recipe.
    In this project: `runtime = "provided.al2023"` and `handler = "bootstrap"` in [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

15. **Zip artifact and hash** — Lambda code is uploaded as a zip. Terraform stores a hash of the zip with `filebase64sha256`, so it notices when the zip changes. The zips are built into a folder that git ignores. It is like a sealed parcel with a weight label.
    In this project: `build-lambda` in [Makefile](../../../Makefile)

16. **Cold start and warm reuse** — the first call to a Lambda starts a fresh environment, which is slow. Later calls reuse it. Code in `init()` runs once per environment, so the server Lambda builds its dependency graph there and keeps its connection pool open. It is like a shop opening once in the morning, not before every customer.
    In this project: `init` in [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go)

17. **API Gateway HTTP API + proxy adapter** — API Gateway is the public front door. The adapter (`httpadapter.NewV2`) turns an API Gateway event into a normal `http.Request` for the chi router. It is like a translator at a border post.
    In this project: [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go) and [infra/terraform/modules/api/main.tf](../../../infra/terraform/modules/api/main.tf)

18. **Reserved concurrency** — a cap on how many copies of one Lambda run at once. The server function is capped at 50. That also caps how many database connections it can open. It is like a limit on how many tills are open at a shop.
    In this project: `reserved_concurrent_executions` in [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

19. **IAM role (least privilege)** — a set of permissions the Lambda holds. Least privilege means giving only what is needed. Here all seven Lambdas share one role, scoped to this app's secrets, buckets and KMS key. A stricter design would use one role per function. It is like a key ring: better to carry only the keys you use.
    In this project: `aws_iam_role.lambda_exec` in [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

20. **KMS MAC key** — a key kept inside AWS that can create and check message signatures (MAC means message authentication code). It is the production home for the QR signing key from Phase 9. The IAM policy grants `kms:GenerateMac` and `kms:VerifyMac`. It is like a bank vault that stamps documents for you.
    In this project: `aws_kms_key.qr_signing` in [infra/terraform/modules/secrets/main.tf](../../../infra/terraform/modules/secrets/main.tf)

21. **Dead-letter queue (DLQ)** — a side queue where a message goes after too many failed tries (here 5). It keeps bad messages from blocking good ones. It is like the "returned to sender" bin at a post office.
    In this project: `aws_sqs_queue.consumer_dlq` in [infra/terraform/modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf)

22. **CloudFront Function** — a tiny script that runs at the network edge, before the request reaches your servers. Its code rejects hold requests with no admission token, saving a Lambda call. In the current Terraform the function exists but is not attached to a distribution. It is like a bouncer who has been hired but has no door yet.
    In this project: [infra/terraform/modules/waitingroom/main.tf](../../../infra/terraform/modules/waitingroom/main.tf)

23. **Honest gaps** — parts that are declared but do not work end to end yet. Two are named in MILESTONES: the WebSocket module and the unattached JWT authorizer. Writing gaps down is better than pretending. It is like a map that marks "unexplored".
    In this project: [infra/terraform/modules/websocket/main.tf](../../../infra/terraform/modules/websocket/main.tf) (top comment)

24. **CI check** — a pipeline that runs on each push. The `terraform-validate` job builds every Lambda zip first, then validates. The zips must exist because the Terraform reads their hash. It is like a spell-checker that runs on every save.
    In this project: [.github/workflows/ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml)
