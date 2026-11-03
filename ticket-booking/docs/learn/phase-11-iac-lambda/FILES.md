# Phase 11 — IaC and Lambda Twins: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 11](../../../MILESTONES.md)

> Note: MILESTONES says "Nine new Terraform modules" and then lists ten names (`auth`, `storage`, `compute`, `api`, `websocket`, `eventing`, `waf`, `observability`, `search`, `waitingroom`). With the four earlier ones that is 14, which matches the 14 module directories and the `terraform init` output in MILESTONES. "Nine" is a typo.

> Note: [internal/config/config.go](../../../internal/config/config.go) says local settings "default to EMPTY, not to a localhost address, so a Lambda in prod can never silently point at a dev emulator". The code does not do that for `DATABASE_URL`, `REDIS_ADDR`, `COGNITO_ENDPOINT`, `QR_SIGNING_SECRET` or `WAITING_ROOM_SECRET`: they default to localhost or dev secrets. [modules/compute](../../../infra/terraform/modules/compute/main.tf) sets only `APP_ENV = "prod"` as a Lambda environment variable. So as written, a deployed Lambda would use localhost defaults. This is not mentioned in MILESTONES.

> Note: the Go code has only `HMACSigner`. The Terraform grants `kms:GenerateMac` and `kms:VerifyMac` and creates the key, but no Go code calls KMS (`grep` for "kms" in `*.go` finds only comments).

> Note: `modules/eventing` says scheduled functions run on cadences "matching their local ticker intervals". They do not: local tickers are 2 s (outbox-relay), 3 s (hold-reaper), 5 s (saga-worker), 30 s (reminder-scheduler), 60 s (reconciler); the schedules are `rate(1 minute)` for four and `rate(30 minutes)` for the reminder scheduler.

> Note: `modules/waitingroom` creates the CloudFront Function, and its ARN is an output. I found no `function_association` in any `.tf` file, so nothing attaches it to a distribution. MILESTONES calls it "a real CloudFront Function, not a stub", which is true of the function and not of the wiring.

## [Makefile](../../../Makefile) (`build-lambda`, `tf-validate`)

- **What it does**: `build-lambda` cross-compiles every `cmd/*-lambda` to a Linux arm64 `bootstrap` binary and zips it. `tf-validate` runs `build-lambda`, then `terraform init -upgrade` and `terraform validate` in `infra/terraform/envs/local`.
- **Control flow**:
  1. `make tf-validate` depends on `build-lambda`.
  2. `LAMBDA_ENTRYPOINTS = $(notdir $(wildcard cmd/*-lambda))` finds the seven directories.
  3. For each: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o .../<name>/bootstrap ./cmd/<name>`, then `zip -q -X ../<name>.zip bootstrap` inside `infra/terraform/modules/compute/build`.
  4. Then `cd infra/terraform/envs/local && terraform init -upgrade && terraform validate`.
- **Data flow**: Go source → static ARM64 ELF `bootstrap` → `build/<name>.zip` (git-ignored). Terraform reads each zip with `filebase64sha256`.
- **How it is tested**: live verification recorded in MILESTONES (`file` shows ARM aarch64 ELF; `terraform validate` green). In CI, the `terraform-validate` job runs `make build-lambda` first. No Go test covers it.
- **Read next**: [internal/wiring/wiring.go](../../../internal/wiring/wiring.go).

## [internal/wiring/wiring.go](../../../internal/wiring/wiring.go)

- **What it does**: `wiring.New(ctx, cfg)` builds the whole dependency graph and returns `*App{Router, Pool, Redis}`. It also starts the AIMD admission loop goroutine (`runAIMDLoop`).
- **Control flow**:
  1. [cmd/server/main.go](../../../cmd/server/main.go) `main` and [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go) `init` both call `wiring.New`.
  2. `New` creates: pgx pool and `db.New`; `storage.New` (S3); `holdlock.New` and `inventory.New`; `payment.NewFakeProvider`; `order.New` then `WithTicketing(ticketing.New(...))`; Redis client; `projector.New`; waiting-room `Queue`, `Controller` and `HoldMetrics`; `auth.NewVerifier`; `wshub.New`.
  3. `httpapi.NewRouter(...)` assembles the chi router. `go runAIMDLoop(ctx, q, controller)` starts the ticker.
- **Data flow**: `config.Config` → constructed services → `App`. The loop reads event ids and calls `controller.Tick` per event.
- **How it is tested**: no test file in `internal/wiring`. MILESTONES records a live check that `cmd/server` still answered `/health` and `/api/v1/events` after the refactor. The AIMD loop in a Lambda does not generalize across concurrent environments; the package comment says so.
- **Read next**: [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go).

## [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go)

- **What it does**: wraps the same chi router with `httpadapter.NewV2` so API Gateway HTTP API v2 events reach it.
- **Control flow**:
  1. Lambda runtime starts the `bootstrap` binary: package `init()` runs once per environment.
  2. `init` calls `config.Load()`, `wiring.New`, then `httpadapter.NewV2(app.Router)`.
  3. `main` calls `lambda.Start(handler)`.
  4. Each invocation: `handler(ctx, req)` → `adapter.ProxyWithContext` → chi router → handlers.
- **Data flow**: `events.APIGatewayV2HTTPRequest` → `http.Request` → router → `events.APIGatewayV2HTTPResponse`. The pool is deliberately never closed, so warm invocations reuse it. `init` calls `log.Fatalf` if wiring fails.
- **How it is tested**: no test file. MILESTONES only checks that it compiles to an ARM64 binary.
- **Read next**: [cmd/reminder-scheduler-lambda/main.go](../../../cmd/reminder-scheduler-lambda/main.go).

## [cmd/reminder-scheduler-lambda/main.go](../../../cmd/reminder-scheduler-lambda/main.go) (and [cmd/hold-reaper-lambda/main.go](../../../cmd/hold-reaper-lambda/main.go))

- **What it does**: one-shot twins of the local tickers. Each invocation opens a pool, runs one pass, closes the pool.
- **Control flow**:
  1. EventBridge Scheduler (`aws_scheduler_schedule.periodic` in [modules/eventing](../../../infra/terraform/modules/eventing/main.tf)) invokes the function.
  2. `handler` → `config.Load()` → `pgxpool.New` → `db.New`.
  3. Reminder: `reminder.RunOnce(ctx, q, "T-24h", reminder.T24h)` then `RunOnce(..., "T-2h", reminder.T2h)` from [internal/reminder/reminder.go](../../../internal/reminder/reminder.go).
  4. Reaper: `holdlock.New`, `inventory.New`, then `reaper.RunOnce` from [internal/reaper/reaper.go](../../../internal/reaper/reaper.go).
- **Data flow**: schedule tick → DB reads/writes through the shared `RunOnce` → log line with count. Unlike the server Lambda, the pool is closed on return (`defer pool.Close()`).
- **How it is tested**: no test for the Lambda mains. `internal/reminder` and `internal/reaper` also contain no test files.
- **Read next**: [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf).

## [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

- **What it does**: declares the seven Lambda functions (`for_each` over `local.functions`), one shared IAM execution role, and log groups.
- **Control flow**: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf) `module "compute"` passes subnets, security group and ARNs from `network`, `secrets` and `storage`. `module "api"`, `module "websocket"` and `module "eventing"` take `function_arns` / `server_function_arn` from its outputs.
- **Data flow**: `build/<handler>.zip` + sizing map → `aws_lambda_function.fn`. `runtime = "provided.al2023"`, `architectures = ["arm64"]`, `handler = "bootstrap"`, VPC config on private subnets. Server: 512 MB, 15 s timeout, `reserved_concurrent_executions = 50`; the other six are unreserved (`-1`). The scoped IAM policy covers secrets read, KMS MAC, tickets bucket Put/Get, layouts bucket Put, and `events:PutEvents` constrained by `events:source`.
- **How it is tested**: `terraform validate` only (via `make tf-validate` and CI). `plan` and `apply` were never run, so nothing proves the IAM policy or settings work in a real account.
- **Read next**: [infra/terraform/modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf).

## [infra/terraform/modules/eventing/main.tf](../../../infra/terraform/modules/eventing/main.tf)

- **What it does**: the custom EventBridge bus, an SQS queue plus DLQ plus rule for the event-driven consumer (`projector`), and Scheduler entries for the five periodic Lambdas.
- **Control flow**:
  1. The outbox relay publishes to the bus. The rule `aws_cloudwatch_event_rule.consumer` matches `source = ticketing.inventory` and detail types `seat.held`, `seat.released`, `seat.booked`.
  2. The rule targets the SQS queue. `aws_lambda_event_source_mapping.consumer` (batch size 10) feeds `projector`.
  3. The DLQ redrive policy uses `maxReceiveCount = 5`.
  4. `aws_scheduler_schedule.periodic` calls outbox-relay, hold-reaper, reconciler, saga-worker (all `rate(1 minute)`) and reminder-scheduler (`rate(30 minutes)`). IAM role `scheduler_invoke` allows `lambda:InvokeFunction`.
- **Data flow**: domain event → bus → rule → SQS → Lambda; timer → Lambda.
- **How it is tested**: `terraform validate` only.
- **Read next**: [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf).

## [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

- **What it does**: the root Terraform configuration. It sets two AWS providers with fake keys and `skip_*` flags (the second, `us_east_1`, for the CloudFront-scope WAF) and wires all 14 modules.
- **Control flow**: `terraform init` → `terraform validate` reads `main.tf` and `versions.tf` (required Terraform `>= 1.5`, AWS provider `~> 5.0`, no backend, local state).
- **Data flow**: module outputs feed other modules' inputs, for example `module.network.private_subnet_ids` into `database` and `compute`.
- **How it is tested**: `make tf-validate` and the CI job below.
- **Read next**: [.github/workflows/ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml).

## [.github/workflows/ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml)

- **What it does**: CI. The `terraform-validate` job builds all Lambda zips, installs Terraform 1.9.0 and runs `terraform init -backend=false` and `terraform validate`. There is no `plan` or `apply`.
- **Control flow**: push → `actions/setup-go` (version from `go.mod`) → `make build-lambda` → `hashicorp/setup-terraform@v3` → init + validate in `ticket-booking/infra/terraform/envs/local`.
- **Data flow**: repo → zips in `build/` → validated Terraform. No credentials.
- **How it is tested**: it is the test. It proves the HCL parses and type-checks, not that it deploys.
- **Read next**: [Phase 12 files](../phase-12-dynamodb-spike/FILES.md).
