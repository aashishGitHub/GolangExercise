# Phase 5 — IaC and Local Deploy Loop: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

How the Terraform here is "tested": `make tf-validate` (runs `build-lambda`, then `terraform init -upgrade` and `terraform validate` in `infra/terraform/envs/local`) and the CI job `terraform-validate`. Both are `terraform validate` only: they check syntax and types, not behaviour. Nothing runs `plan` or `apply`.

> Note: the `envs/local` and the modules directory now include Phase 7 modules (`eventing`, `websocket`, `webhook`) and Phase 6's `waf`. Those are covered in the Phase 6 and 7 docs. This file covers the Phase 5 shape.

## [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

- **What it does**: The root that wires every module together and configures the AWS provider with placeholder credentials.
- **Control flow**:
  1. `terraform init` reads `required_providers` (`aws ~> 5.0`, `random ~> 3.6`).
  2. Two `provider "aws"` blocks (default and alias `us_east_1`) use fake keys plus `skip_credentials_validation`, `skip_requesting_account_id`, `skip_metadata_api_check`.
  3. `module "network"` and `module "secrets"` have no inputs from other modules.
  4. `module "database"` takes network ids and secret outputs. `module "auth"` stands alone. `module "waf"` gets the aliased provider. `module "storage"` takes `web_acl_arn`.
  5. `module "compute"` takes subnets, the Lambda security group, the photos bucket ARN, the secret ARN and `local.base_environment`.
  6. `module "api"` takes the compute `invoke_arn` and `function_name` plus Cognito values.
- **Data flow**: Module outputs flow into other modules as inputs. `local.base_environment` builds the Lambda env map: `APP_ENV=prod`, `DATABASE_URL` (user and password from `module.secrets`, host from `module.database.proxy_endpoint`), `COGNITO_ISSUER`, `COGNITO_CLIENT_ID`, `S3_PHOTOS_BUCKET`, `AWS_REGION`. `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` are left out on purpose.
- **How it is tested**: `terraform validate` via `make tf-validate` and the CI job. No `plan`.
- **Read next**: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

> Note: `DATABASE_URL` is built with the plaintext password and set as a Lambda environment variable. `random_password.result` is also stored in Terraform state. The comment in `modules/secrets/main.tf` says "never in state as a variable", which is narrower than "never in state". Verify your threat model.

## [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

- **What it does**: Creates the IAM role, its two policies and the `aws_lambda_function.api` that runs `cmd/lambda`.
- **Control flow**:
  1. `aws_iam_role.lambda` can be assumed by `lambda.amazonaws.com`.
  2. `aws_iam_role_policy_attachment.vpc_access` attaches the AWS managed `AWSLambdaVPCAccessExecutionRole` (logs plus network interfaces).
  3. `aws_iam_role_policy.lambda_app` allows only `secretsmanager:GetSecretValue` on `var.db_secret_arn`, and `s3:PutObject`, `s3:GetObject` on `"${var.photos_bucket_arn}/*"`.
  4. `aws_lambda_function.api` reads `build/bootstrap.zip` and hashes it with `filebase64sha256`. Runtime `provided.al2023`, `arm64`, `timeout = 10`, `memory_size = 256`, VPC config, `environment { variables = var.environment }`.
- **Data flow**: Zip file in, so a changed binary changes `source_code_hash` and triggers a redeploy. The env map is passed through unchanged.
- **How it is tested**: `terraform validate` only. The zip must exist on disk: `make build-lambda` locally, a zero-byte placeholder in CI.
- **Read next**: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf)

> Note: I grepped the Go code for `secretsmanager` and found no match. The app gets DB credentials from the env var, so the Lambda's `GetSecretValue` permission looks unused by app code. RDS Proxy reads the secret through its own role in `modules/database`. Verify before removing it.

## [infra/terraform/modules/storage/main.tf](../../../infra/terraform/modules/storage/main.tf)

- **What it does**: Two private S3 buckets (photos, web) each behind a CloudFront distribution with Origin Access Control.
- **Control flow**:
  1. `aws_s3_bucket.*` plus `aws_s3_bucket_public_access_block.*` block all public access.
  2. `aws_cloudfront_origin_access_control.*` (sigv4, `signing_behavior = "always"`).
  3. `aws_cloudfront_distribution.*` with `viewer_protocol_policy = "redirect-to-https"`, cache policy id for AWS "CachingOptimized", `web_acl_id = var.web_acl_arn`.
  4. `aws_s3_bucket_policy.*` allows `s3:GetObject` to `cloudfront.amazonaws.com` only when `AWS:SourceArn` equals that distribution.
  5. The web distribution has `default_root_object = "index.html"` and maps 403 and 404 to `/index.html` with 200.
- **Data flow**: Browser to CloudFront to S3 for reads only. Photo uploads skip CloudFront: the client PUTs to a presigned S3 URL (see [s3.go](../../../internal/storage/s3.go)).
- **How it is tested**: `terraform validate` only.
- **Read next**: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf)

> Note: I found no CORS configuration on the photos bucket in this module (`grep` for "cors" in `modules/` matches only `modules/api`). A browser PUT to a presigned URL on real S3 normally needs bucket CORS. Local MinIO worked in the E2E. Verify for real AWS.

## [infra/terraform/modules/api/main.tf](../../../infra/terraform/modules/api/main.tf)

- **What it does**: API Gateway HTTP API in front of the Lambda, with a Cognito JWT authorizer on `/api/*`. (Phase 6 adds a CloudFront front and CORS here.)
- **Control flow**:
  1. `aws_apigatewayv2_api.main` (`protocol_type = "HTTP"`).
  2. `aws_apigatewayv2_integration.lambda`: `AWS_PROXY`, `payload_format_version = "2.0"`, which matches `httpadapter.NewV2` in `cmd/lambda`.
  3. `aws_apigatewayv2_authorizer.cognito`: type `JWT`, reads `$request.header.Authorization`, `audience = [client_id]`, `issuer = user pool endpoint`.
  4. Routes: `GET /health` (no authorizer) and `ANY /api/{proxy+}` (JWT).
  5. `aws_lambda_permission.apigw` lets API Gateway invoke the function.
- **Data flow**: Request with `Authorization: Bearer <JWT>` goes to the authorizer. If valid, the event goes to Lambda. The Go router then verifies the token again in `verifier.Middleware`.
- **How it is tested**: `terraform validate` only. The in-Lambda check is unit tested in [auth_test.go](../../../internal/auth/auth_test.go): `TestMiddleware_ValidToken`, `TestMiddleware_MissingToken`, `TestMiddleware_ExpiredToken`, `TestMiddleware_WrongAudience`, `TestMiddleware_WrongTokenUse`.
- **Read next**: [cmd/lambda/main.go](../../../cmd/lambda/main.go)

## [cmd/lambda/main.go](../../../cmd/lambda/main.go)

- **What it does**: Wraps the same `httpapi.NewRouter` that `cmd/server` uses so it runs on Lambda.
- **Control flow**:
  1. Go runs `init()` once per cold start: `config.Load()`, `pgxpool.New`, `storage.New`, `auth.NewVerifier`.
  2. `httpapi.NewRouter(verifier, db.New(pool), pool, store, wsstub.NewHub())` builds the router.
  3. `httpadapter.NewV2(router)` becomes the package-level `adapter`.
  4. `main()` calls `lambda.Start(handler)`. Each invocation runs `handler`, which calls `adapter.ProxyWithContext`.
- **Data flow**: `events.APIGatewayV2HTTPRequest` in, converted to `http.Request`, chi handles it, converted to `events.APIGatewayV2HTTPResponse` out.
- **How it is tested**: Not unit tested. Checked by cross-compiling: `make build-lambda`, and in CI `GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/lambda`. That proves it compiles for the target, not that it runs.
- **Read next**: [internal/config/config.go](../../../internal/config/config.go)

## [internal/config/config.go](../../../internal/config/config.go)

- **What it does**: `Load()` reads settings from environment variables with a `getenv(key, fallback)` helper.
- **Control flow**:
  1. Called by `cmd/server/main.go` and `cmd/lambda/main.go` (and the other entrypoints).
  2. `getenv` returns the fallback if the variable is unset or empty.
- **Data flow**: Env vars in, `Config` struct out. Key rules from the Phase 5 fix:
  - `CognitoIssuer` is `COGNITO_ISSUER` if set, else `COGNITO_ENDPOINT` (default `http://localhost:9229`) + `/` + `COGNITO_USER_POOL_ID`.
  - `S3Endpoint`, `S3AccessKey`, `S3SecretKey` default to empty, so prod falls through to real S3 and the role's credentials.
  - `DatabaseURL` still defaults to the local Postgres URL, and `Env` defaults to `local`.
- **How it is tested**: Untested. There is no `config_test.go`.
- **Read next**: [internal/storage/s3.go](../../../internal/storage/s3.go)

## [internal/storage/s3.go](../../../internal/storage/s3.go)

- **What it does**: Presigned PUT URLs, an existence check, and bucket creation for local dev.
- **Control flow**:
  1. `New(ctx, endpoint, region, bucket, accessKey, secretKey)` builds AWS config. Static credentials are used only if both keys are non-empty. Otherwise the SDK default chain is used.
  2. If `endpoint != ""`, set `BaseEndpoint` and `UsePathStyle = true` (MinIO).
  3. `PresignPut` returns a URL for a direct client upload. `Exists` does `HeadObject`. `EnsureBucket` is called by `cmd/server` only, not by `cmd/lambda`.
- **Data flow**: key and content type in, signed URL out. Photo bytes never pass through the Lambda.
- **How it is tested**: No `s3_test.go`. Verified manually against MinIO in Phase 3 (see MILESTONES).
- **Read next**: [.github/workflows/fieldsync-ci.yml](../../../../.github/workflows/fieldsync-ci.yml)

## [.github/workflows/fieldsync-ci.yml](../../../../.github/workflows/fieldsync-ci.yml)

- **What it does**: GitHub Actions workflow with three jobs: `go`, `web`, `terraform-validate`. No deploy step.
- **Control flow**:
  1. Triggers on push and pull_request when `offline-sync-app/**` changed. `defaults.run.working-directory` is `offline-sync-app`.
  2. `go`: `go build ./...`, `go vet ./...`, `go test ./...`, then the arm64 cross-compile of `./cmd/lambda`.
  3. `web`: `npm ci`, `npm run build` (which runs `tsc -b && vite build`).
  4. `terraform-validate`: `setup-terraform` 1.9.0, creates a zero-byte `modules/compute/build/bootstrap.zip`, then `terraform init -backend=false` and `terraform validate`.
- **Data flow**: Source in, pass or fail out. No artifacts, no secrets.
- **How it is tested**: It is the test. Runs only on GitHub.
- **Read next**: [../phase-6-hardening/BASICS.md](../phase-6-hardening/BASICS.md)

> Note: MILESTONES describes the job as using "a placeholder zero-byte zip so the lambda-artifact reference resolves". Today the code references more zips: `modules/websocket`, `modules/webhook` and `modules/eventing` (several) also call `filebase64sha256` on `build/...zip` files. The CI job creates only the compute one. I am not certain how `terraform validate` treats a missing file there. You may want to check a CI run.
>
> Also: the `go` job runs `go test ./...` only. It does not run `-tags=integration`, so no real-Postgres tests run in CI.
