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

# QR-signing key (docs/plan.md "secrets" module + internal/ticketing.Signer
# interface, Phase 9's local implementation is HMAC/env; this is the real
# KMS boundary that interface exists for). A KMS MAC key, not a generic
# encrypt/decrypt CMK — Sign/Verify via GenerateMac/VerifyMac is the
# closest KMS primitive to the HMAC-SHA256 internal/ticketing.HMACSigner
# already implements locally, so swapping the Signer implementation is a
# constructor change, not an algorithm change.
resource "aws_kms_key" "qr_signing" {
  description              = "${var.name_prefix} QR ticket HMAC signing key"
  key_usage                = "GENERATE_VERIFY_MAC"
  customer_master_key_spec = "HMAC_256" # implies HMAC-SHA256 at GenerateMac/VerifyMac call time —
  # matches internal/ticketing.HMACSigner's algorithm exactly
  enable_key_rotation = false # AWS does not support automatic rotation for MAC keys
}

resource "aws_kms_alias" "qr_signing" {
  name          = "alias/${var.name_prefix}-qr-signing"
  target_key_id = aws_kms_key.qr_signing.key_id
}
