# Aurora's master password: Terraform generates it once and Secrets Manager
# holds the only copy — never in state as a variable, never in a .tfvars file.
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
    username = "fieldsync"
    password = random_password.db_master.result
  })
}

# The external system must hold the same value: an operator copies it from this secret into the vendor's webhook settings.
resource "random_password" "webhook" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "webhook" {
  name = "${var.name_prefix}/webhook-secret"
}

resource "aws_secretsmanager_secret_version" "webhook" {
  secret_id     = aws_secretsmanager_secret.webhook.id
  secret_string = random_password.webhook.result
}
