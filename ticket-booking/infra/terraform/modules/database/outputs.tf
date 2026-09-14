output "proxy_endpoint" {
  value = aws_db_proxy.main.endpoint
}

output "reader_endpoint" {
  value = aws_rds_cluster.main.reader_endpoint
}

output "cluster_arn" {
  value = aws_rds_cluster.main.arn
}

output "cluster_identifier" {
  value = aws_rds_cluster.main.cluster_identifier
}

output "proxy_name" {
  value = aws_db_proxy.main.name
}
