# Phase 5 — IaC and Local Deploy Loop: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [../README.md](../README.md)

## L1 Beginner

**Q1. What is a Terraform module, and why split the infra into modules?**
A module is a folder of `.tf` files that builds one logical piece (network, database, auth, storage, compute, api, secrets). Each has `variables.tf` for inputs and `outputs.tf` for results. Splitting keeps each piece small and replaceable. `envs/local/main.tf` is the only file that wires them. Source: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf).

**Q2. Why does the app run on Lambda behind API Gateway, and what is `cmd/lambda`?**
Lambda runs code only when a request arrives, so there is no server to manage. API Gateway is the public door. `cmd/lambda` wraps the same `httpapi.NewRouter` that `cmd/server` uses, through `httpadapter.NewV2`, so local and prod run the same handlers. Source: [cmd/lambda/main.go](../../../cmd/lambda/main.go).

**Q3. What is RDS Proxy and why is it there?**
A connection pool that sits in front of Aurora. Each concurrent Lambda copy would open its own Postgres connection and could exhaust the database limit. The proxy shares a smaller set of real connections. Source: comment at the top of [modules/database/main.tf](../../../infra/terraform/modules/database/main.tf).

Try it:
```
make tf-validate
```

## L2 Junior

**Q4. Trace one `POST /api/sync` in the Phase 5 prod shape.**
Client sends `Authorization: Bearer <JWT>` to API Gateway. The route `ANY /api/{proxy+}` has the Cognito JWT authorizer, which checks the token first. API Gateway invokes the Lambda with a v2 payload. `handler` calls `adapter.ProxyWithContext`, chi runs `verifier.Middleware` (checks the JWT again), then `syncHandler`. The pgx pool connects to the RDS Proxy endpoint from `DATABASE_URL`, and the proxy talks to Aurora. Source: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf), [cmd/lambda/main.go](../../../cmd/lambda/main.go), [router.go](../../../internal/httpapi/router.go).

> Note: Phase 6 later puts a CloudFront distribution in front of API Gateway for WAF. See the Phase 6 docs.

**Q5. How does the same binary get different settings locally and in prod?**
Through `config.Load()` and env vars. Prod: Terraform sets `COGNITO_ISSUER` from the user pool endpoint and omits `S3_ENDPOINT` and the S3 keys, so `storage.New` uses real S3 and the role's credentials. Local: `.env` and `.env.local` supply MinIO and cognito-local settings, and the issuer is assembled from `COGNITO_ENDPOINT` + `COGNITO_USER_POOL_ID`. Source: [config.go](../../../internal/config/config.go), [s3.go](../../../internal/storage/s3.go).

**Q6. What exactly does `make tf-validate` do?**
It depends on `build-lambda`, which cross-compiles each Lambda entrypoint to `bootstrap` for linux/arm64 and zips it under `infra/terraform/modules/*/build/`. Then `cd infra/terraform/envs/local && terraform init -upgrade && terraform validate`. It never runs `plan` or `apply`. Source: [Makefile](../../../Makefile).

Try it:
```
make build-lambda
make tf-validate
```

**Q7. How do photos get uploaded and read in the storage design?**
Uploads: the client PUTs bytes directly to S3 using a URL from `PresignPut`. The Lambda is not in the path. Reads: CloudFront reads the private bucket through Origin Access Control, and the bucket policy allows only that distribution's ARN. Source: [modules/storage/main.tf](../../../infra/terraform/modules/storage/main.tf), [s3.go](../../../internal/storage/s3.go).

## L3 Mid

**Q8. Why one Lambda running the whole router instead of one Lambda per route?**
One artifact, one cold start path, and local dev runs the identical router, so local matches prod. The cost: every route shares one IAM role (so the role must cover all routes' needs) and one memory and timeout setting (`256` MB, `10` s in `modules/compute`). It is a deliberate "chi-lith" choice, noted in the router's comments. Source: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf), [router.go](../../../internal/httpapi/router.go).

**Q9. What bug did Phase 5 find in config, and what proves it is fixed?**
`internal/storage.New` and `internal/config` had local-dev defaults (MinIO endpoint, static keys, a cognito-local issuer) that would be silently wrong in prod. Now S3 settings default to empty so the SDK default chain applies, and `COGNITO_ISSUER` is one explicit value. Honest answer on proof: there is no test for `config.Load` or `storage.New`. The fix is visible in the code and was checked by running, not by a test.

**Q10. Why `validate` only, and what does it prove?**
`plan` and `apply` need real AWS credentials, which this config deliberately lacks (the provider uses fake keys and skip flags). `validate` proves the HCL parses, references resolve and types match. It does not prove IAM policies are correct, quotas are enough, or that resources can be created. Source: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf).

**Q11. Why verify the JWT at API Gateway and again inside the Lambda?**
The authorizer rejects bad tokens before the Lambda runs, which saves invocations. The in-app `verifier.Middleware` keeps local dev working (no API Gateway locally) and means the handlers still check the token if the route config is wrong. The in-app check is unit tested: `TestMiddleware_ValidToken`, `TestMiddleware_MissingToken`, `TestMiddleware_ExpiredToken`, `TestMiddleware_WrongAudience`, `TestMiddleware_WrongTokenUse`. These prove the middleware rejects those five cases. They do not test the Terraform authorizer. Source: [auth_test.go](../../../internal/auth/auth_test.go).

Try it:
```
go test ./internal/auth/
```

## L4 Senior

**Q12. The secrets module says "never in state as a variable". Is the DB password safe?**
Not fully. `random_password.db_master.result` is stored in Terraform state. It is also passed through module outputs into `aws_rds_cluster.master_password` and into the Lambda's `DATABASE_URL` environment variable, readable by anyone with `lambda:GetFunctionConfiguration`. The Go code never calls Secrets Manager (I found no match), so the secret is a store for RDS Proxy auth, not the app's source. Safer shapes: have the app fetch the secret at start-up, or use IAM database auth. I have not verified how this interacts with rotation. Source: [modules/secrets/main.tf](../../../infra/terraform/modules/secrets/main.tf), [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf).

**Q13. What breaks at 100x traffic in this infra?**
Candidates I can see in the code, none measured:
- Single NAT gateway: one zone's traffic crosses AZs and it is a single point of failure. The module comment calls this a deliberate cost trade-off.
- Lambda concurrency scales out fast, and RDS Proxy has to absorb it. Aurora's `max_capacity` is a variable and caps scale.
- `skip_final_snapshot = true` and no `deletion_protection` set in `modules/database`: a careless `destroy` would drop data. The code comment says it is a local/dev default.
- `init()` in `cmd/lambda` runs on each cold start, creating a pgx pool per Lambda instance.
Source: [modules/network/main.tf](../../../infra/terraform/modules/network/main.tf), [modules/database/main.tf](../../../infra/terraform/modules/database/main.tf).

**Q14. What is NOT solved in Phase 5?**
- No remote state backend: `envs/local/main.tf` has no `backend` block, and CI uses `init -backend=false`. State is local and git-ignored.
- No deploy pipeline and no `plan` in CI. Real AWS access was never granted to this config.
- No S3 CORS on the photos bucket that I could find in `modules/storage` (verify).
- CI runs `go test ./...` only, so the `-tags=integration` tests never run there.
- No test for config or storage.
Source: [fieldsync-ci.yml](../../../../.github/workflows/fieldsync-ci.yml).

**Q15. The CI `terraform-validate` job creates one placeholder zip. Is that enough today?**
I am not certain. Phase 5 had one Lambda zip. Now `modules/websocket`, `modules/webhook` and `modules/eventing` also call `filebase64sha256` on files under their own `build/` folders, and the CI job creates only `modules/compute/build/bootstrap.zip`. Whether `terraform validate` fails on a missing file in that function is something to confirm by reading a CI run. Local `make tf-validate` avoids the question because `build-lambda` creates every zip. Source: [fieldsync-ci.yml](../../../../.github/workflows/fieldsync-ci.yml), [Makefile](../../../Makefile).
