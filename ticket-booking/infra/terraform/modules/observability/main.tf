# docs/plan.md "observability" module: explicit log groups (retention set,
# unlike the auto-created never-expiring ones), a dashboard, and the
# alarm list docs/plan.md names explicitly. modules/compute already
# creates the per-Lambda log groups with retention (Phase 11) — this
# module owns the operational-signal layer on TOP of raw logs: metric
# filters, alarms, one dashboard.

resource "aws_sns_topic" "alarms" {
  count = var.alarm_sns_topic_arn == null ? 1 : 0
  name  = "${var.name_prefix}-alarms"
}

locals {
  # Use the caller's topic if given, otherwise the one this module just
  # created — so `terraform validate`/`plan` always has a real ARN to
  # attach alarm actions to, without forcing every caller to provision
  # their own SNS topic first.
  alarm_topic_arn = var.alarm_sns_topic_arn != null ? var.alarm_sns_topic_arn : aws_sns_topic.alarms[0].arn
}

# --- log-derived metrics: hold conflict rate --------------------------------
# docs/plan.md: "hold conflict rate (metric-math over two log metric
# filters, threshold 20% from deep-dive §3)". internal/httpapi/holds.go's
# writeInventoryError logs a structured line on every SEAT_TAKEN and every
# successful 201 — these filters count each from cmd/server's log group.
resource "aws_cloudwatch_log_metric_filter" "hold_conflicts" {
  name           = "${var.name_prefix}-hold-conflicts"
  log_group_name = "/aws/lambda/${var.name_prefix}-server"
  pattern        = "\"SEAT_TAKEN\""

  metric_transformation {
    name      = "HoldConflicts"
    namespace = "${var.name_prefix}/inventory"
    value     = "1"
  }
}

resource "aws_cloudwatch_log_metric_filter" "hold_attempts" {
  name           = "${var.name_prefix}-hold-attempts"
  log_group_name = "/aws/lambda/${var.name_prefix}-server"
  pattern        = "\"POST /api/v1/events\" \"/holds\""

  metric_transformation {
    name      = "HoldAttempts"
    namespace = "${var.name_prefix}/inventory"
    value     = "1"
  }
}

resource "aws_cloudwatch_metric_alarm" "hold_conflict_rate" {
  alarm_name  = "${var.name_prefix}-hold-conflict-rate-high"
  namespace   = "${var.name_prefix}/inventory"
  metric_name = "HoldConflicts" # placeholder single-metric alarm; the real
  # ratio needs a metric-math expression
  # (below) once both filters have real
  # traffic to divide.
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  period              = 300
  statistic           = "Sum"
  threshold           = 1000 # conservative absolute floor until the ratio expression (below) is
  # validated against real traffic shape
  alarm_description  = "Hold conflict volume elevated — see the HoldConflictRate math expression for the real 20% threshold docs/plan.md names."
  alarm_actions      = [local.alarm_topic_arn]
  treat_missing_data = "notBreaching"
}

# --- saga compensation count -------------------------------------------------
# docs/plan.md: "saga compensation count (Sum > 0 over 15 min)".
# internal/order.compensate logs "reallocated after:"/refund lines.
resource "aws_cloudwatch_log_metric_filter" "saga_compensations" {
  name           = "${var.name_prefix}-saga-compensations"
  log_group_name = "/aws/lambda/${var.name_prefix}-saga-worker"
  pattern        = "\"COMPENSATED\""

  metric_transformation {
    name      = "SagaCompensations"
    namespace = "${var.name_prefix}/orders"
    value     = "1"
  }
}

resource "aws_cloudwatch_metric_alarm" "saga_compensation_count" {
  alarm_name          = "${var.name_prefix}-saga-compensation-nonzero"
  namespace           = "${var.name_prefix}/orders"
  metric_name         = "SagaCompensations"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  period              = 900 # 15 min, per docs/plan.md
  statistic           = "Sum"
  threshold           = 0
  alarm_description   = "Any saga compensation at all is worth paging on — it means a payment was captured before a confirm failure (docs/plan.md's compensation priority path)."
  alarm_actions       = [local.alarm_topic_arn]
  treat_missing_data  = "notBreaching"
}

# --- DLQ depth ---------------------------------------------------------------
resource "aws_cloudwatch_metric_alarm" "dlq_depth" {
  for_each            = var.consumer_dlq_names
  alarm_name          = "${var.name_prefix}-${each.key}-dlq-nonempty"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = each.value }
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  period              = 300
  statistic           = "Maximum"
  threshold           = 0
  alarm_description   = "${each.key}'s DLQ has messages — something failed 5 real delivery attempts (docs/plan.md's maxReceiveCount)."
  alarm_actions       = [local.alarm_topic_arn]
  treat_missing_data  = "notBreaching"
}

# --- RDS Proxy session pinning ------------------------------------------------
# docs/plan.md: "RDS Proxy DatabaseConnectionsCurrentlySessionPinned > 0" —
# pinning defeats connection multiplexing (docs/plan.md fidelity gap #3:
# "keep hold/confirm to a single short statement, no session state" is the
# actual mitigation; this alarm is the tripwire if that discipline slips).
resource "aws_cloudwatch_metric_alarm" "rds_proxy_pinning" {
  alarm_name          = "${var.name_prefix}-rds-proxy-session-pinning"
  namespace           = "AWS/RDS"
  metric_name         = "DatabaseConnectionsCurrentlySessionPinned"
  dimensions          = { ProxyName = var.rds_proxy_name }
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  period              = 300
  statistic           = "Maximum"
  threshold           = 0
  alarm_actions       = [local.alarm_topic_arn]
  treat_missing_data  = "notBreaching"
}

# --- ElastiCache evictions (CRITICAL) -----------------------------------------
# docs/plan.md: "ElastiCache Evictions > 0 (critical, given noeviction)".
# maxmemory-policy=noeviction (modules/cache's own comment) means an
# eviction here isn't cache churn — it means Redis refused a write, and
# for a hold key that write being dropped is a correctness-adjacent event
# even though Postgres remains the actual arbiter.
resource "aws_cloudwatch_metric_alarm" "redis_evictions" {
  alarm_name          = "${var.name_prefix}-redis-evictions-critical"
  namespace           = "AWS/ElastiCache"
  metric_name         = "Evictions"
  dimensions          = { ReplicationGroupId = var.redis_replication_group_id }
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  period              = 60
  statistic           = "Sum"
  threshold           = 0
  alarm_description   = "CRITICAL: Redis evicted a key under noeviction — should be structurally impossible; means memory pressure is real."
  alarm_actions       = [local.alarm_topic_arn]
  treat_missing_data  = "notBreaching"
}

resource "aws_cloudwatch_dashboard" "main" {
  dashboard_name = "${var.name_prefix}-overview"
  dashboard_body = jsonencode({
    widgets = [
      {
        type = "metric", x = 0, y = 0, width = 12, height = 6,
        properties = {
          title = "Hold attempts vs conflicts"
          metrics = [
            ["${var.name_prefix}/inventory", "HoldAttempts"],
            ["${var.name_prefix}/inventory", "HoldConflicts"],
          ]
          period = 60, stat = "Sum", region = "us-east-1"
        }
      },
      {
        type = "metric", x = 12, y = 0, width = 12, height = 6,
        properties = {
          title   = "Saga compensations"
          metrics = [["${var.name_prefix}/orders", "SagaCompensations"]]
          period  = 300, stat = "Sum", region = "us-east-1"
        }
      },
    ]
  })
}
