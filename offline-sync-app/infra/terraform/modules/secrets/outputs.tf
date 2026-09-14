output "secret_arn" {
  value = aws_secretsmanager_secret.db_credentials.arn
}

output "username" {
  value = "fieldsync"
}

output "password" {
  value     = random_password.db_master.result
  sensitive = true
}
