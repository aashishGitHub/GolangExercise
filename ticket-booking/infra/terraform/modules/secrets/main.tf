# Aurora's master password: Terraform generates it once and Secrets Manager
# holds the only copy — never in state as a variable, never in a .tfvars
# file. Also mints the ElastiCache auth token (docs/plan.md "secrets"
# module) — the QR-signing KMS key is added when Phase 9/11 needs it.
resource "random_password" "db_master" {
  length  = 32
  special = false # Aurora's password char restrictions; simplest safe choice
}

resource "aws_secretsmanager_secret" "db_credentials" {
  name = "${var.name_prefix}/db-credentials"
}

resource "aws_secretsmanager_secret_version" "db_credentials" {
  secret_id = aws_secretsmanager_secret.db_credentials.id
  secret_string = jsonencode({
    username = "ticketing"
    password = random_password.db_master.result
  })
}

resource "random_password" "redis_auth" {
  length  = 32
  special = false # ElastiCache AUTH token char restrictions
}

resource "aws_secretsmanager_secret" "redis_auth" {
  name = "${var.name_prefix}/redis-auth-token"
}

resource "aws_secretsmanager_secret_version" "redis_auth" {
  secret_id     = aws_secretsmanager_secret.redis_auth.id
  secret_string = random_password.redis_auth.result
}
