# Mirrors what cognito-local emulates for dev: email-as-username, no client
# secret (public SPA client), USER_PASSWORD_AUTH + refresh allowed. Real
# Cognito supports SRP too — Amplify can use it here even though local dev
# can't (cognito-local has no SRP support), so USER_SRP_AUTH is also allowed.

resource "aws_cognito_user_pool" "main" {
  name = "${var.name_prefix}-users"

  username_attributes      = ["email"]
  auto_verified_attributes = ["email"]

  password_policy {
    minimum_length    = 8
    require_lowercase = true
    require_uppercase = true
    require_numbers   = true
    require_symbols   = false
  }
}

resource "aws_cognito_user_pool_client" "web" {
  name         = "${var.name_prefix}-web"
  user_pool_id = aws_cognito_user_pool.main.id

  generate_secret = false
  explicit_auth_flows = [
    "ALLOW_USER_PASSWORD_AUTH",
    "ALLOW_USER_SRP_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]
}
