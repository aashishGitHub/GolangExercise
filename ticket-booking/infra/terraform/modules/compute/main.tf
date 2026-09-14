# docs/plan.md "compute" module: provided.al2023/arm64 Lambdas, one per
# cmd/*-lambda twin, built by `make build-lambda` into build/<name>.zip.
# One shared execution role across all seven functions here — least-
# privilege SCOPED TO THE RESOURCES this app's Lambdas collectively touch
# (never account-wide "*"), not fully separated per function. A stricter
# design would split this into one role per function (a saga-worker
# Lambda has no business holding S3 permissions it never calls); that's
# real future work, not done here, and is noted rather than silently
# skipped.
locals {
  # handler: the binary name make build-lambda produces at
  # build/<handler>/bootstrap, zipped to build/<handler>.zip.
  functions = {
    server = {
      handler         = "server-lambda"
      memory_mb       = 512
      timeout_seconds = 15
      # The write path (docs/plan.md "compute": "reserved_concurrent_executions
      # on the write path") -- caps how many concurrent executions can ever
      # run, which caps how many concurrent pgx connections this function
      # alone can open against RDS Proxy, regardless of how hard API Gateway
      # is hit.
      reserved_concurrent_executions = 50
    }
    outbox-relay = {
      handler                        = "outbox-relay-lambda"
      memory_mb                      = 128
      timeout_seconds                = 30
      reserved_concurrent_executions = -1 # unreserved
    }
    hold-reaper = {
      handler                        = "hold-reaper-lambda"
      memory_mb                      = 128
      timeout_seconds                = 30
      reserved_concurrent_executions = -1
    }
    projector = {
      handler                        = "projector-lambda"
      memory_mb                      = 256
      timeout_seconds                = 15
      reserved_concurrent_executions = -1
    }
    reconciler = {
      handler                        = "reconciler-lambda"
      memory_mb                      = 128
      timeout_seconds                = 60
      reserved_concurrent_executions = -1
    }
    saga-worker = {
      handler                        = "saga-worker-lambda"
      memory_mb                      = 256
      timeout_seconds                = 30
      reserved_concurrent_executions = -1
    }
    reminder-scheduler = {
      handler                        = "reminder-scheduler-lambda"
      memory_mb                      = 128
      timeout_seconds                = 30
      reserved_concurrent_executions = -1
    }
  }
}

data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "lambda_exec" {
  name               = "${var.name_prefix}-lambda-exec"
  assume_role_policy = data.aws_iam_policy_document.assume.json
}

resource "aws_iam_role_policy_attachment" "vpc_access" {
  role       = aws_iam_role.lambda_exec.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

data "aws_iam_policy_document" "lambda_scoped" {
  statement {
    sid       = "ReadDBAndRedisSecrets"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [var.db_secret_arn, var.redis_auth_secret_arn]
  }
  statement {
    sid       = "SignAndVerifyQR"
    actions   = ["kms:GenerateMac", "kms:VerifyMac"]
    resources = [var.qr_signing_key_arn]
  }
  statement {
    sid       = "TicketQRObjects"
    actions   = ["s3:PutObject", "s3:GetObject"]
    resources = ["${var.tickets_bucket_arn}/*"]
  }
  statement {
    sid       = "LayoutObjects"
    actions   = ["s3:PutObject"] # only event-publisher-shaped writes; reads are public via CloudFront
    resources = ["${var.layouts_bucket_arn}/*"]
  }
  statement {
    sid       = "PublishDomainEvents"
    actions   = ["events:PutEvents"]
    resources = ["*"] # EventBridge PutEvents doesn't support resource-level scoping to one bus by ARN pre-condition; scoped via a condition instead
    condition {
      test     = "StringEquals"
      variable = "events:source"
      values   = ["ticketing.inventory"]
    }
  }
}

resource "aws_iam_role_policy" "lambda_scoped" {
  name   = "${var.name_prefix}-lambda-scoped"
  role   = aws_iam_role.lambda_exec.id
  policy = data.aws_iam_policy_document.lambda_scoped.json
}

resource "aws_lambda_function" "fn" {
  for_each = local.functions

  function_name = "${var.name_prefix}-${each.key}"
  role          = aws_iam_role.lambda_exec.arn

  filename         = "${path.module}/build/${each.value.handler}.zip"
  source_code_hash = filebase64sha256("${path.module}/build/${each.value.handler}.zip")

  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"] # cheaper + build/loadtest already targets arm64 locally on Apple Silicon

  memory_size = each.value.memory_mb
  timeout     = each.value.timeout_seconds

  reserved_concurrent_executions = each.value.reserved_concurrent_executions

  vpc_config {
    subnet_ids         = var.private_subnet_ids
    security_group_ids = [var.lambda_security_group_id]
  }

  environment {
    variables = {
      APP_ENV = "prod"
    }
  }
}

resource "aws_cloudwatch_log_group" "fn" {
  for_each = local.functions

  name              = "/aws/lambda/${var.name_prefix}-${each.key}"
  retention_in_days = 14 # modules/observability owns the REST of the log-group story; this one's
  # explicit here since aws_lambda_function auto-creates an unmanaged,
  # never-expiring one otherwise
}
