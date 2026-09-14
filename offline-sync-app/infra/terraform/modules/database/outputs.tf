output "proxy_endpoint" {
  value = aws_db_proxy.main.endpoint
}

output "cluster_endpoint" {
  value = aws_rds_cluster.main.endpoint
}
