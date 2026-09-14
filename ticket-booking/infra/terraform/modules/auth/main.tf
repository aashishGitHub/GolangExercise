# docs/plan.md "auth" module: a Cognito pool whose app client allows
# ALLOW_USER_PASSWORD_AUTH so the REAL pool's auth flow matches
# cognito-local's (jagregory/cognito-local doesn't implement SRP — see
# scripts/seed-cognito-local.sh's own comment). internal/auth.Verifier
# validates RS256/iss/aud/token_use identically against either.
resource "aws_cognito_user_pool" "main" {
  name = "${var.name_prefix}-users"

  username_attributes      = ["email"]
  auto_verified_attributes = ["email"]

  password_policy {
    minimum_length    = 8
    require_lowercase = true
    require_numbers   = true
    require_symbols   = false
    require_uppercase = true
  }

  # Local dev (cognito-local) has no MFA/advanced-security support to
  # mirror, so prod doesn't turn it on either -- keeping the two pools'
  # actual auth surface identical is the point (docs/plan.md fidelity
  # gap #7: "cognito-local's claims are a subset of real Cognito's; no
  # SRP, no MFA").
  mfa_configuration = "OFF"

  admin_create_user_config {
    allow_admin_create_user_only = false
  }
}

resource "aws_cognito_user_pool_client" "web" {
  name         = "${var.name_prefix}-web"
  user_pool_id = aws_cognito_user_pool.main.id

  # USER_PASSWORD_AUTH, not the SRP flow Amplify defaults to -- required
  # so the exact same frontend auth code path works against cognito-local
  # AND real Cognito (scripts/seed-cognito-local.sh's own finding).
  explicit_auth_flows = [
    "ALLOW_USER_PASSWORD_AUTH",
    "ALLOW_ADMIN_USER_PASSWORD_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]

  generate_secret        = false # a browser SPA client, never confidential
  id_token_validity      = 1
  access_token_validity  = 1
  refresh_token_validity = 30
  token_validity_units {
    id_token      = "hours"
    access_token  = "hours"
    refresh_token = "days"
  }
}
