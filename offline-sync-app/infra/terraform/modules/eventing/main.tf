# EventBridge bus + SQS(+DLQ) + Lambda per consumer (photo-processor,
# dashboard-aggregator, notifier), plus the scheduled outbox-relay Lambda.
# Local dev substitutes a domain_events-polling loop for the SQS trigger
# (see internal/consume's package comment) — this module is what actually
# runs in prod.

resource "aws_cloudwatch_event_bus" "main" {
  name = "${var.name_prefix}-events"
}

locals {
  # One entry per SQS-triggered consumer. notifier gets an extra IAM
  # statement below (execute-api:ManageConnections) the other two don't need.
  consumers = {
    photo-processor = {
      event_types = ["photo.upserted"]
      zip_path    = "${path.module}/build/photo-processor/bootstrap.zip"
    }
    dashboard-aggregator = {
      event_types = ["photo.upserted"]
      zip_path    = "${path.module}/build/dashboard-aggregator/bootstrap.zip"
    }
    notifier = {
      event_types = ["photo.upserted"]
      zip_path    = "${path.module}/build/notifier/bootstrap.zip"
    }
  }
}

resource "aws_sqs_queue" "dlq" {
  for_each                  = local.consumers
  name                      = "${var.name_prefix}-${each.key}-dlq"
  message_retention_seconds = 1209600 # 14 days — max, so a stuck message isn't silently lost
}

resource "aws_sqs_queue" "queue" {
  for_each                   = local.consumers
  name                       = "${var.name_prefix}-${each.key}-queue"
  visibility_timeout_seconds = 30
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq[each.key].arn
    maxReceiveCount     = 5
  })
}

resource "aws_sqs_queue_policy" "queue" {
  for_each  = local.consumers
  queue_url = aws_sqs_queue.queue[each.key].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "events.amazonaws.com" }
      Action    = "sqs:SendMessage"
      Resource  = aws_sqs_queue.queue[each.key].arn
      Condition = {
        ArnEquals = { "aws:SourceArn" = aws_cloudwatch_event_rule.consumer[each.key].arn }
      }
    }]
  })
}

resource "aws_cloudwatch_event_rule" "consumer" {
  for_each       = local.consumers
  name           = "${var.name_prefix}-${each.key}-rule"
  event_bus_name = aws_cloudwatch_event_bus.main.name
  event_pattern = jsonencode({
    "detail-type" = each.value.event_types
  })
}

resource "aws_cloudwatch_event_target" "consumer" {
  for_each       = local.consumers
  rule           = aws_cloudwatch_event_rule.consumer[each.key].name
  event_bus_name = aws_cloudwatch_event_bus.main.name
  arn            = aws_sqs_queue.queue[each.key].arn
}

resource "aws_iam_role" "consumer" {
  for_each = local.consumers
  name     = "${var.name_prefix}-${each.key}-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

# Covers CloudWatch Logs + ENI (VPC access, needed to reach RDS Proxy) + SQS
# consume permissions for the specific queue below.
resource "aws_iam_role_policy_attachment" "consumer_vpc_access" {
  for_each   = local.consumers
  role       = aws_iam_role.consumer[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

resource "aws_iam_role_policy" "consumer_app" {
  for_each = local.consumers
  name     = "${var.name_prefix}-${each.key}-app-policy"
  role     = aws_iam_role.consumer[each.key].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat([
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = var.db_secret_arn
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
        Resource = aws_sqs_queue.queue[each.key].arn
      },
      ], each.key == "notifier" ? [{
        Effect   = "Allow"
        Action   = ["execute-api:ManageConnections"]
        Resource = "${var.websocket_api_arn}/*"
    }] : [])
  })
}

resource "aws_lambda_function" "consumer" {
  for_each      = local.consumers
  function_name = "${var.name_prefix}-${each.key}"
  role          = aws_iam_role.consumer[each.key].arn

  filename         = each.value.zip_path
  source_code_hash = filebase64sha256(each.value.zip_path)

  runtime       = "provided.al2023"
  architectures = ["arm64"]
  handler       = "bootstrap"
  timeout       = 30
  memory_size   = 256

  vpc_config {
    subnet_ids         = var.private_subnet_ids
    security_group_ids = [var.lambda_security_group_id]
  }

  environment {
    variables = each.key == "notifier" ? merge(var.base_environment, {
      WS_MANAGEMENT_API_ENDPOINT = var.websocket_management_api_endpoint
    }) : var.base_environment
  }
}

resource "aws_lambda_event_source_mapping" "consumer" {
  for_each         = local.consumers
  event_source_arn = aws_sqs_queue.queue[each.key].arn
  function_name    = aws_lambda_function.consumer[each.key].arn
  batch_size       = 10
  # Only the messages this handler actually reports as failed get
  # redelivered — see internal/sqsconsume's partial-batch-response contract.
  function_response_types = ["ReportBatchItemFailures"]
}

# ---- outbox-relay: scheduled, not SQS-triggered ----

resource "aws_iam_role" "outbox_relay" {
  name = "${var.name_prefix}-outbox-relay-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "outbox_relay_vpc_access" {
  role       = aws_iam_role.outbox_relay.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

resource "aws_iam_role_policy" "outbox_relay_app" {
  name = "${var.name_prefix}-outbox-relay-app-policy"
  role = aws_iam_role.outbox_relay.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = var.db_secret_arn
      },
      {
        Effect   = "Allow"
        Action   = ["events:PutEvents"]
        Resource = aws_cloudwatch_event_bus.main.arn
      },
    ]
  })
}

resource "aws_lambda_function" "outbox_relay" {
  function_name = "${var.name_prefix}-outbox-relay"
  role          = aws_iam_role.outbox_relay.arn

  filename         = "${path.module}/build/outbox-relay/bootstrap.zip"
  source_code_hash = filebase64sha256("${path.module}/build/outbox-relay/bootstrap.zip")

  runtime       = "provided.al2023"
  architectures = ["arm64"]
  handler       = "bootstrap"
  timeout       = 30
  memory_size   = 256

  vpc_config {
    subnet_ids         = var.private_subnet_ids
    security_group_ids = [var.lambda_security_group_id]
  }

  environment {
    # EVENT_BUS_NAME is set here, not in var.base_environment, specifically
    # to avoid a circular reference — the bus is created inside this module.
    variables = merge(var.base_environment, {
      EVENT_BUS_NAME = aws_cloudwatch_event_bus.main.name
    })
  }
}

resource "aws_scheduler_schedule" "outbox_relay" {
  name                         = "${var.name_prefix}-outbox-relay-schedule"
  schedule_expression          = "rate(1 minute)"
  schedule_expression_timezone = "UTC"

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.outbox_relay.arn
    role_arn = aws_iam_role.scheduler.arn
  }
}

resource "aws_iam_role" "scheduler" {
  name = "${var.name_prefix}-outbox-relay-scheduler-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "scheduler.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "scheduler_invoke" {
  name = "${var.name_prefix}-outbox-relay-scheduler-policy"
  role = aws_iam_role.scheduler.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "lambda:InvokeFunction"
      Resource = aws_lambda_function.outbox_relay.arn
    }]
  })
}
