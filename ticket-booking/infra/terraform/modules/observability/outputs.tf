output "alarm_topic_arn" {
  value = local.alarm_topic_arn
}

output "dashboard_name" {
  value = aws_cloudwatch_dashboard.main.dashboard_name
}
