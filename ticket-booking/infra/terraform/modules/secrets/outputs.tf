output "db_secret_arn" {
  value = aws_secretsmanager_secret.db_credentials.arn
}

output "db_username" {
  value = "ticketing"
}

output "db_password" {
  value     = random_password.db_master.result
  sensitive = true
}

output "redis_auth_secret_arn" {
  value = aws_secretsmanager_secret.redis_auth.arn
}

output "redis_auth_token" {
  value     = random_password.redis_auth.result
  sensitive = true
}

output "qr_signing_key_arn" {
  value = aws_kms_key.qr_signing.arn
}
