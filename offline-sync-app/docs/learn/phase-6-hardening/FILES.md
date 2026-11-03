# Phase 6 — Hardening: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Testing note for the whole phase: the Terraform files are checked by `terraform validate` only (`make tf-validate`, CI job `terraform-validate`). Nothing tests that the WAF blocks anything or that an IAM policy denies what it should. The load tool is a standalone program with no tests.

## [infra/terraform/modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf)

- **What it does**: One shared WAFv2 web ACL (`aws_wafv2_web_acl.main`, scope `CLOUDFRONT`) with three AWS managed rule groups.
- **Control flow**:
  1. `envs/local/main.tf` calls `module "waf"` and passes `providers = { aws.us_east_1 = aws.us_east_1 }`.
  2. `versions.tf` declares `configuration_aliases = [aws.us_east_1]`, and the resource sets `provider = aws.us_east_1`.
  3. `default_action { allow {} }`. A `dynamic "rule"` loops over `AWSManagedRulesCommonRuleSet`, `AWSManagedRulesKnownBadInputsRuleSet`, `AWSManagedRulesSQLiRuleSet`. Priority is the list index. `override_action { none {} }` keeps each group's own block actions.
  4. `outputs.tf` exposes `web_acl_arn`, which `module.storage` and `module.api` take as `web_acl_arn`.
- **Data flow**: Name prefix in, ACL ARN out. At runtime each CloudFront request is evaluated against the rules before reaching the origin.
- **How it is tested**: `terraform validate` only.
- **Read next**: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf)

## [infra/terraform/modules/api/main.tf](../../../infra/terraform/modules/api/main.tf)

- **What it does**: Phase 6 additions: the `cors_configuration` on the HTTP API and `aws_cloudfront_distribution.api`, which fronts the API only to attach the WAF.
- **Control flow**:
  1. `aws_apigatewayv2_api.main` has `cors_configuration` with `allow_origins = ["https://${var.allowed_origin}"]`, methods `GET POST PUT OPTIONS`, headers `authorization`, `content-type`, `max_age = 300`.
  2. In `envs/local/main.tf`, `allowed_origin = module.storage.web_cloudfront_domain`.
  3. `aws_cloudfront_distribution.api` has origin = the API endpoint host (HTTPS only, TLS 1.2), `web_acl_id = var.web_acl_arn`, `cache_policy_id` of AWS managed "CachingDisabled" and `origin_request_policy_id` of AWS managed "AllViewerExceptHostHeader". All methods allowed, only GET and HEAD are cacheable.
- **Data flow**: Browser, then CloudFront (WAF check), then API Gateway (CORS handling, JWT authorizer), then Lambda. The `Authorization` header and query strings reach the origin.
- **How it is tested**: `terraform validate` only. The policy UUIDs are hard-coded with comments naming the managed policy; I did not re-verify them against AWS docs.
- **Read next**: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

> Note: I found no `disable_execute_api_endpoint` anywhere in `infra/`. With the default `execute-api` URL still enabled, a caller could reach API Gateway directly and skip CloudFront and the WAF. I am not certain of the default behaviour for HTTP APIs; verify in the AWS provider docs.
>
> Note: I found no throttling settings (`throttle`) or WAF rate-based rule in `infra/`.

## [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

- **What it does**: The Lambda's IAM role and the inline `lambda_app` policy that is the least-privilege part of Phase 6.
- **Control flow**:
  1. `aws_iam_role_policy.lambda_app` has two statements.
  2. Statement 1: action `secretsmanager:GetSecretValue`, resource `var.db_secret_arn` (one secret).
  3. Statement 2: actions `s3:PutObject`, `s3:GetObject`, resource `"${var.photos_bucket_arn}/*"`. The comment says `GetObject` also covers the `HeadObject` that confirm uses.
  4. The managed `AWSLambdaVPCAccessExecutionRole` is attached separately for logs and network interfaces.
- **Data flow**: Policy JSON built by `jsonencode`, attached to the role the Lambda assumes at run time.
- **How it is tested**: `terraform validate` only. No IAM policy simulation.
- **Read next**: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

> Note: MILESTONES says "least-priv IAM per Lambda (`modules/compute`)". Only `modules/compute` is covered by Phase 6. The Phase 7 Lambdas (`eventing`, `websocket`) have their own roles with the same pattern; those are in the Phase 7 docs.

## [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

- **What it does**: Declares the `us_east_1` provider alias and wires WAF into storage and api.
- **Control flow**:
  1. `provider "aws" { alias = "us_east_1" ... }` with the same placeholder credentials as the default provider.
  2. `module "waf"` receives it. `module.storage` and `module.api` both get `web_acl_arn = module.waf.web_acl_arn`.
  3. So all three CloudFront distributions (web, photos, api front) share one ACL.
- **Data flow**: ACL ARN output, distributed as an input.
- **How it is tested**: `terraform validate` only.
- **Read next**: [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf) if you skipped it, otherwise [router.go](../../../internal/httpapi/router.go)

## [internal/httpapi/router.go](../../../internal/httpapi/router.go)

- **What it does**: Builds the chi router, including the `cors.Handler` used for local dev.
- **Control flow**:
  1. `NewRouter` adds `RequestID`, `RealIP`, `Logger`, `Recoverer`, then `cors.Handler` with `AllowedOrigins: []string{"http://localhost:5173"}`.
  2. The same `NewRouter` is used by `cmd/lambda`, so this middleware also runs in Lambda.
- **Data flow**: Request `Origin` header compared against the allow list; matching preflights get CORS headers back.
- **How it is tested**: No test for CORS behaviour.
- **Read next**: [cmd/server/main.go](../../../cmd/server/main.go)

> Note: the comment in the file says "CloudFront origin replaces this in prod". In the code, the allow list is a fixed `localhost:5173` for every environment. In prod, CORS answers must come from API Gateway's `cors_configuration`. I am not certain how API Gateway merges its CORS headers with the Lambda's response headers; test it in a real deployment.

## [cmd/server/main.go](../../../cmd/server/main.go)

- **What it does**: Local entrypoint. Relevant here for the connection pool the load test exercised.
- **Control flow**:
  1. `config.Load()`, then `pgxpool.New(ctx, cfg.DatabaseURL)` with no custom pool settings in code.
  2. `store.EnsureBucket`, `auth.NewVerifier`, `db.New(pool)`, a notifier goroutine, then `httpapi.NewRouter(...)`.
  3. `http.ListenAndServe(cfg.Addr, router)`: a plain server with no read or write timeouts set.
- **Data flow**: Each `POST /api/sync` takes a connection from the pool for its database work. If all connections are busy, the request waits.
- **How it is tested**: Not tested. It is the program the load test hit.
- **Read next**: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

## [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

- **What it does**: `package main` tool that fires concurrent `POST /api/sync` requests and prints throughput and latency percentiles.
- **Control flow**:
  1. Flags: `-c` workers (default 10), `-n` total (default 500), `-url` (default `http://localhost:8080/api/sync`), `-token` or env `LOADTEST_TOKEN`. Missing token exits 1.
  2. A buffered channel `sem` of size `concurrency` limits in-flight goroutines.
  3. Each goroutine builds a body with one brand-new location (`uuid.New()`), sets the bearer token, calls `client.Do`, records the elapsed time, and counts a non-200 or error as `errCount`.
  4. After `wg.Wait()`, sort the latencies and print `requests=... throughput=... req/s`, `ok=... err=...`, `p50 p95 p99 max`.
- **Data flow**: Flags in, N HTTP requests out, latency slice, sorted, printed. Nothing persisted by the tool; the server inserts N location rows.
- **How it is tested**: Not tested. It is a measurement tool.
- **Read next**: [../phase-7-event-fanout/BASICS.md](../phase-7-event-fanout/BASICS.md)

> Note: the tool sends only new locations, so every request takes the insert path of the upsert. It never exercises the stale or conflict branches, photos, or the site assessment path.
>
> Note: the numbers in MILESTONES (about 4,800 req/s plateau; p50 2.6 ms and p95 5.5 ms at c=10; p50 38.8 ms and p95 60.8 ms at c=200; zero errors at 10, 50, 100, 200) were measured on 2026-09-07. Phase 7 (2026-09-08) wrapped `/api/sync` in a transaction with `domain_events` inserts (`internal/sync/transactional.go`). The current code path is not the one that was measured.
>
> Note: the HTTP client is `&http.Client{Timeout: 10 * time.Second}` with the default transport. I believe Go's default transport keeps only 2 idle connections per host, so at high concurrency the client may open many new connections. This could add client-side cost to the numbers. Verify in the `net/http` docs.
