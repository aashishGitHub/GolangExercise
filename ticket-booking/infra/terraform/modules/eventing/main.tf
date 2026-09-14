# docs/plan.md "eventing" module: the custom bus internal/outbox's Relay
# publishes to, per-consumer {SQS, DLQ maxReceiveCount=5, rule, target,
# ESM} via for_each for EVENT-DRIVEN consumers, and a Scheduler group for
# consumers that run on a fixed cadence instead of reacting to events.

resource "aws_cloudwatch_event_bus" "domain_events" {
  name = "${var.name_prefix}-events" # matches config.EventBusName's default "ticketing-events" pattern
}

# --- event-driven consumers ------------------------------------------------
# Currently just internal/projector: it's the one consumer whose whole job
# IS reacting to seat.* events (docs/plan.md: "the prod twin is invoked by
# an EventBridge rule subscribed to seat.* events"). for_each over a map
# keeps this real for a second event-driven consumer later without
# reshaping the module.
locals {
  event_driven_consumers = {
    projector = {
      detail_types = ["seat.held", "seat.released", "seat.booked"]
      function_arn = var.function_arns["projector"]
    }
  }
}

resource "aws_sqs_queue" "consumer_dlq" {
  for_each = local.event_driven_consumers
  name     = "${var.name_prefix}-${each.key}-dlq"
}

resource "aws_sqs_queue" "consumer" {
  for_each = local.event_driven_consumers
  name     = "${var.name_prefix}-${each.key}"

  visibility_timeout_seconds = 30 # >= the consumer Lambda's timeout, standard SQS+Lambda requirement

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.consumer_dlq[each.key].arn
    maxReceiveCount     = 5 # docs/plan.md's own number for this module
  })
}

resource "aws_cloudwatch_event_rule" "consumer" {
  for_each       = local.event_driven_consumers
  name           = "${var.name_prefix}-${each.key}"
  event_bus_name = aws_cloudwatch_event_bus.domain_events.name

  event_pattern = jsonencode({
    source        = ["ticketing.inventory"]
    "detail-type" = each.value.detail_types
  })
}

resource "aws_cloudwatch_event_target" "consumer" {
  for_each       = local.event_driven_consumers
  rule           = aws_cloudwatch_event_rule.consumer[each.key].name
  event_bus_name = aws_cloudwatch_event_bus.domain_events.name
  arn            = aws_sqs_queue.consumer[each.key].arn
}

data "aws_iam_policy_document" "sqs_from_eventbridge" {
  for_each = local.event_driven_consumers
  statement {
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.consumer[each.key].arn]
    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com"]
    }
    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [aws_cloudwatch_event_rule.consumer[each.key].arn]
    }
  }
}

resource "aws_sqs_queue_policy" "consumer" {
  for_each  = local.event_driven_consumers
  queue_url = aws_sqs_queue.consumer[each.key].id
  policy    = data.aws_iam_policy_document.sqs_from_eventbridge[each.key].json
}

resource "aws_lambda_event_source_mapping" "consumer" {
  for_each         = local.event_driven_consumers
  event_source_arn = aws_sqs_queue.consumer[each.key].arn
  function_name    = each.value.function_arn
  batch_size       = 10
}

# --- scheduled (non-event-driven) consumers --------------------------------
# outbox-relay, hold-reaper, reconciler, saga-worker, reminder-scheduler all
# run a full pass on a fixed cadence, matching their local ticker intervals
# (cmd/*/main.go's own comments name these exact intervals).
locals {
  scheduled_functions = {
    outbox-relay       = { rate = "rate(1 minute)", function_arn = var.function_arns["outbox-relay"] }
    hold-reaper        = { rate = "rate(1 minute)", function_arn = var.function_arns["hold-reaper"] }
    reconciler         = { rate = "rate(1 minute)", function_arn = var.function_arns["reconciler"] }
    saga-worker        = { rate = "rate(1 minute)", function_arn = var.function_arns["saga-worker"] }
    reminder-scheduler = { rate = "rate(30 minutes)", function_arn = var.function_arns["reminder-scheduler"] }
  }
}

resource "aws_scheduler_schedule_group" "periodic" {
  name = "${var.name_prefix}-periodic"
}

data "aws_iam_policy_document" "scheduler_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["scheduler.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "scheduler_invoke" {
  name               = "${var.name_prefix}-scheduler-invoke"
  assume_role_policy = data.aws_iam_policy_document.scheduler_assume.json
}

data "aws_iam_policy_document" "scheduler_invoke_lambda" {
  statement {
    actions   = ["lambda:InvokeFunction"]
    resources = [for f in local.scheduled_functions : f.function_arn]
  }
}

resource "aws_iam_role_policy" "scheduler_invoke_lambda" {
  name   = "${var.name_prefix}-scheduler-invoke-lambda"
  role   = aws_iam_role.scheduler_invoke.id
  policy = data.aws_iam_policy_document.scheduler_invoke_lambda.json
}

resource "aws_scheduler_schedule" "periodic" {
  for_each   = local.scheduled_functions
  name       = "${var.name_prefix}-${each.key}"
  group_name = aws_scheduler_schedule_group.periodic.name

  schedule_expression = each.value.rate
  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = each.value.function_arn
    role_arn = aws_iam_role.scheduler_invoke.arn
  }
}

# Individual one-shot hold-expiry schedules are NOT created here -- they're
# created at RUNTIME by internal/order's saga (docs/plan.md
# "Terraform/runtime boundary": "the individual one-shot hold-expiry
# schedules are created at runtime by the Order Service -- or someone will
# try to model 50,000 schedules in HCL"). This module only creates the
# schedule GROUP they'd land in.
resource "aws_scheduler_schedule_group" "hold_expiry" {
  name = "${var.name_prefix}-hold-expiry"
}
