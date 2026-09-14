output "domain_endpoint" {
  value = var.enable_search ? aws_opensearch_domain.events[0].endpoint : null
}
