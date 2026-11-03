# Phase 6 — Hardening: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [../README.md](../README.md)

## L1 Beginner

**Q1. What is least-privilege IAM, and what may the API Lambda do?**
Give a program only the actions it needs on only the resources it needs. The API Lambda's `lambda_app` policy allows `secretsmanager:GetSecretValue` on one secret and `s3:PutObject` plus `s3:GetObject` on objects in the photos bucket. Plus the AWS managed VPC-access policy for logs and network interfaces. Source: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf).

**Q2. What is a WAF and which rules does the app use?**
A web application firewall that inspects each request and blocks attack-shaped ones. One web ACL with three AWS managed rule groups: `AWSManagedRulesCommonRuleSet`, `AWSManagedRulesKnownBadInputsRuleSet`, `AWSManagedRulesSQLiRuleSet`. Default action is allow. Source: [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf).

**Q3. What is CORS, and what is a preflight?**
CORS is the browser rule that a page from one origin may read answers from another origin only if that server allows it. A preflight is the `OPTIONS` check the browser sends first when the request carries something like an `Authorization` header. Source: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf).

Try it:
```
make tf-validate
```

## L2 Junior

**Q4. Why is there a CloudFront distribution in front of the API, and what does a request look like?**
The API is an API Gateway HTTP API (v2), which cannot take a WAF directly (per the comment in the WAF module and MILESTONES). CloudFront can, so a distribution fronts the API only as a WAF attachment point. Path: browser, CloudFront (WAF rules, caching off, headers forwarded), API Gateway (CORS, JWT authorizer), Lambda. Source: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf), [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf).

**Q5. Where is CORS answered locally and where in prod?**
Locally, chi's `cors.Handler` in `NewRouter` answers, with the allow list fixed to `http://localhost:5173`. In prod, API Gateway's `cors_configuration` answers the preflight without invoking Lambda (per MILESTONES), with the web CloudFront domain as the allowed origin. Note that the chi middleware also runs inside the Lambda, because `cmd/lambda` uses the same `NewRouter`. Source: [router.go](../../../internal/httpapi/router.go), [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf).

**Q6. Trace how the load test runs.**
`main` parses `-c`, `-n`, `-url`, `-token`. A channel `sem` caps in-flight goroutines at `-c`. Each goroutine posts a body with one new location (random UUID) and the bearer token, times the call, and counts non-200 as errors. After all finish it sorts the latencies and prints throughput and p50, p95, p99, max. Source: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go).

Try it:
```
make compose-up
make migrate
make dev-server
LOADTEST_TOKEN=<your-id-token> go run ./scripts/loadtest -n 500 -c 20
```
The token must be a valid Cognito ID token for the local pool.

**Q7. What did the recorded load test show?**
From MILESTONES: a plateau of about 4,800 req/s; p50 2.6 ms and p95 5.5 ms at concurrency 10, rising to p50 38.8 ms and p95 60.8 ms at 200; zero errors at 10, 50, 100 and 200. Caveat from the same text: single local Postgres, single Go process, dev hardware. It is useful to spot regressions, not as a production capacity claim. Source: [MILESTONES.md](../../../MILESTONES.md).

## L3 Mid

**Q8. Why CloudFront in front of the HTTP API, and not switch to a REST API?**
REST API (v1) can attach a WAF directly, but moving to it would drop the plan's locked HTTP API choice (the WAF module's comment says it was a cost and latency decision). CloudFront-in-front was judged the smaller change. The price: an extra hop, an extra distribution to manage, and a possible bypass if the API's own endpoint stays reachable (see Q14). Source: [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf).

**Q9. Why disable caching and forward headers on that distribution?**
API answers are per user and dynamic, so a cached answer would leak or go stale. The distribution uses the managed CachingDisabled policy and the managed AllViewerExceptHostHeader origin request policy, which passes the `Authorization` header and query strings. Source: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf). I did not re-check the policy UUIDs against AWS docs.

**Q10. Why an aliased `us_east_1` provider if the stack already runs in us-east-1?**
CloudFront-scope WAF resources must be created in us-east-1 regardless of the stack's region. Naming the provider explicitly keeps the WAF correct if the primary region ever changes. The cost is a `configuration_aliases` declaration and a `providers = {...}` block on the module. Source: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf), [modules/waf/versions.tf](../../../infra/terraform/modules/waf/versions.tf).

**Q11. How is Phase 6 tested, and what does that prove?**
Terraform: `terraform validate` via `make tf-validate` and CI. It proves the HCL parses and references resolve. It does not prove the WAF blocks attacks, that the IAM policy is sufficient (a missing permission only shows at run time), or that CORS works in a browser. The load tool is itself untested. The only automated proof of hardening behaviour in Go is the JWT middleware in `auth_test.go`, which belongs to Phase 1. MILESTONES presents the load test as the verification, and it ran by hand.

## L4 Senior

**Q12. MILESTONES says the plateau and the linear latency growth point at connection-pool queuing. Is that proven?**
No, it is an inference. A flat throughput and growing latency fit a pool queue, but also a single-CPU limit on the Go process, Postgres CPU, JWT verification, or the load client sharing the machine. `cmd/server` calls `pgxpool.New` with no explicit pool size, so the default applies (I believe the greater of 4 and the CPU count; verify in the pgxpool docs). To test the claim: change the pool size and re-run, watch Postgres `pg_stat_activity`, and run the client on a separate machine. Source: [cmd/server/main.go](../../../cmd/server/main.go), [scripts/loadtest/main.go](../../../scripts/loadtest/main.go).

**Q13. What would change at 100x, and why are the local numbers weak evidence?**
- Prod has Lambda cold starts (each runs `init()` and makes its own pgx pool), RDS Proxy pooling and Aurora Serverless v2 scaling. None existed in the local test.
- The code path changed after the test: Phase 7's `TxRunner` wraps every sync in a transaction with `domain_events` inserts, so each request now does more work and holds a connection longer.
- The test sends only new locations, so it never hits the stale, conflict, photo, or site paths.
- The WAF is now in the path. I believe the common rule set limits request body size, which could block large sync batches, but I am not certain; check the AWS rule group docs.
Source: [MILESTONES.md](../../../MILESTONES.md), [transactional.go](../../../internal/sync/transactional.go).

**Q14. What is NOT solved by Phase 6?**
From the code, none of these are present:
- The API Gateway default endpoint is not disabled (no `disable_execute_api_endpoint` in `infra/`), so the WAF may be bypassable.
- No rate limiting: no throttle settings or WAF rate rule in `infra/`.
- No request body size cap on `/api/sync`: `syncHandler` decodes `r.Body` directly (only the webhook handler uses `http.MaxBytesReader`).
- `cmd/server` uses `http.ListenAndServe` with no server timeouts.
- `GET /api/locations/{id}` and `GET /api/sites/{id}` do not check ownership.
- No test that the WAF or IAM behaves as intended.
Source: [router.go](../../../internal/httpapi/router.go), [sync.go](../../../internal/httpapi/sync.go).

**Q15. Is the Lambda role really least-privilege?**
Close but not minimal. The `GetSecretValue` permission looks unused by the app (no Go code calls Secrets Manager; `DATABASE_URL` arrives as an env var). `s3:PutObject` and `s3:GetObject` cover the whole photos bucket, while the app only needs keys under `photos/`. And `confirmHandler` calls `store.Exists` on the client-supplied `s3Key` without checking it matches `photos/{siteId}/{photoId}`, so a signed-in user can confirm any existing object key in that bucket. These are observations from reading the code; I have not exploited or tested them. Source: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf), [photos.go](../../../internal/httpapi/photos.go).
