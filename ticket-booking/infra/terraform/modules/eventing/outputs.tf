output "event_bus_name" {
  value = aws_cloudwatch_event_bus.domain_events.name
}

output "event_bus_arn" {
  value = aws_cloudwatch_event_bus.domain_events.arn
}

output "consumer_dlq_arns" {
  value = { for k, q in aws_sqs_queue.consumer_dlq : k => q.arn }
}
