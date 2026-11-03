# Phase 11 — IaC and Lambda Twins: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [Learn index](../README.md)

## L1 Beginner

**Q1. What is Terraform and why describe infrastructure in files?**
Terraform reads text files and makes the cloud match them. Files can be reviewed, versioned and repeated. Root config: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf).

**Q2. What is a Lambda twin?**
A second `main` package that calls the same business code with a Lambda entry point. Example: [cmd/server-lambda/main.go](../../../cmd/server-lambda/main.go) beside [cmd/server/main.go](../../../cmd/server/main.go). Seven exist.

**Q3. What does `terraform validate` prove, and what does it not?**
It proves the files parse and the types line up. It does not contact AWS, so it does not prove the infrastructure can be created or the permissions are right. MILESTONES says `terraform plan` was never run.

**Q4. What is cross-compiling, and why does `make build-lambda` need it?**
Building for another OS or CPU than your own machine. Lambda here runs Linux on arm64, and the dev machine is a Mac. The target uses `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`. See `build-lambda` in the [Makefile](../../../Makefile).
Try it:
```
make build-lambda
```

## L2 Junior

**Q5. Trace `make tf-validate`.**
`tf-validate` depends on `build-lambda`: for each `cmd/*-lambda` it builds `bootstrap` and zips it into `infra/terraform/modules/compute/build/<name>.zip`. Then it runs `terraform init -upgrade` and `terraform validate` in `envs/local`. The zips must exist because [modules/compute](../../../infra/terraform/modules/compute/main.tf) calls `filebase64sha256` on them.
Try it:
```
make tf-validate
```

**Q6. How do `cmd/server` and `cmd/server-lambda` share code?**
Both call `wiring.New(ctx, cfg)` in [internal/wiring/wiring.go](../../../internal/wiring/wiring.go), which returns `App{Router, Pool, Redis}`. `cmd/server` passes the router to `http.ListenAndServe`. `cmd/server-lambda` wraps it with `httpadapter.NewV2` in `init()`.

**Q7. How does the reminder scheduler run locally versus on AWS?**
Locally, [cmd/reminder-scheduler/main.go](../../../cmd/reminder-scheduler/main.go) loops on a 30-second ticker. On AWS, EventBridge Scheduler (`aws_scheduler_schedule.periodic`, `rate(30 minutes)`) invokes [cmd/reminder-scheduler-lambda/main.go](../../../cmd/reminder-scheduler-lambda/main.go) once. Both call `reminder.RunOnce` for `T-24h` and `T-2h`.

**Q8. How does the projector get its events on AWS?**
Event bus → rule matching `seat.held`, `seat.released`, `seat.booked` → SQS queue → event source mapping (batch size 10) → `projector` Lambda. After 5 failed receives a message goes to the DLQ. All in [modules/eventing](../../../infra/terraform/modules/eventing/main.tf).

## L3 Mid

**Q9. Why extract `internal/wiring`, `internal/reaper` and `internal/reminder` instead of copying code into each Lambda?**
Two copies drift. A shared function means the ticker and the Lambda run identical logic, so a fix lands once. The cost: the shared packages have no tests of their own (no test files in `internal/wiring`, `internal/reaper`, `internal/reminder`). The only check recorded is a live run of `cmd/server` after the refactor (MILESTONES).

**Q10. Why does `server-lambda` never close its pool, while the scheduled Lambdas do?**
`server-lambda` keeps a warm environment and reuses the pool across requests; the comment says closing it would defeat pooling. The scheduled twins open a pool, run one pass, and close it with `defer pool.Close()`. Per-invocation pools are not reused across calls in these twins. The module comments say RDS Proxy exists to protect Aurora from this connection churn ([modules/database](../../../infra/terraform/modules/database/main.tf)).

**Q11. Why one IAM role for all seven Lambdas, and is it right?**
[modules/compute](../../../infra/terraform/modules/compute/main.tf) uses one role scoped to the app's own secrets, buckets and KMS key (never `*` for those). Its own comment admits a stricter design is one role per function, since saga-worker does not need S3. It is a deliberate shortcut, not best practice.

**Q12. Why does CI build the Lambda zips before `terraform validate`?**
`aws_lambda_function` uses `filebase64sha256` on `build/<name>.zip`, and Terraform needs the file to exist at validate time. See `terraform-validate` in [ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml). What this proves: the HCL type-checks and the Go builds for arm64. It does not prove deployability.

## L4 Senior

**Q13. What would break if you ran `terraform apply` today?**
Several known gaps, from the code and MILESTONES. (1) `modules/compute` sets only `APP_ENV = "prod"`; [config.Load](../../../internal/config/config.go) defaults `DATABASE_URL`, `REDIS_ADDR` and the HMAC secrets to local values, so the functions would not know the real database or secrets. (2) No Go code calls KMS, although the IAM and key exist, so QR signing would still use the env HMAC secret. (3) The WebSocket module's `$connect` points at `server-lambda`, which does not speak API Gateway's WebSocket contract (MILESTONES gap 1). (4) The JWT authorizer is declared but attached to no route (gap 2). (5) The CloudFront Function is not associated with a distribution (my finding: no `function_association` in any `.tf`). I have not run `plan` or `apply`, so there may be more.

**Q14. Why is `internal/wshub` a poor fit for API Gateway WebSockets?**
`wshub` holds every connection in one long-lived process and writes deltas straight to each socket. API Gateway WebSocket invokes a Lambda per message and needs a separate `PostToConnection` call to push. The fix would fan out via `ws_connections` rows. The gap is written at the top of [modules/websocket/main.tf](../../../infra/terraform/modules/websocket/main.tf).

**Q15. What happens to the AIMD admission loop and in-process state when `server-lambda` scales out?**
`wiring.New` starts `go runAIMDLoop(...)` in every execution environment. N environments run N loops. I believe Lambda also freezes background goroutines between invocations, but verify that in the AWS docs. The package comment calls this a gap shared with multi-replica `cmd/server` and says MILESTONES documents it. Not solved here: a single owner for the loop (for example a scheduled Lambda).

**Q16. What are the failure modes of replacing a 2 to 5 second ticker with a one-minute scheduler?**
Local tickers run at 2 s (outbox-relay), 3 s (hold-reaper), 5 s (saga-worker), 60 s (reconciler), 30 s (reminders). Terraform sets `rate(1 minute)` for four and `rate(30 minutes)` for reminders, despite the module comment saying it matches the local intervals. Outbox events and saga catch-up would be delayed up to about a minute. I believe EventBridge Scheduler rate schedules have a one-minute minimum, but verify in the AWS docs. At 100× scale you would also want in-process loops or SQS-triggered Lambdas for latency-sensitive steps, which this phase does not do.
