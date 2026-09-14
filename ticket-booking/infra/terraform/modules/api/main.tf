# docs/plan.md "api" module: HTTP API v2 + JWT authorizer + throttling, in
# front of the single cmd/server-lambda function (chi does the routing
# internally, matching cmd/server's local lith -- one Lambda function
# behind a catch-all $default route, not one function per REST resource).
# The CloudFront distribution here exists PURELY as a WAF attachment
# point (docs/plan.md: WAFv2's REGIONAL scope can't attach directly to an
# HTTP API, only to an ALB/API Gateway REGIONAL resource or a CloudFront
# distribution -- CloudFront is the one that composes cleanly with
# modules/waf's rate-based rule).

resource "aws_apigatewayv2_api" "http" {
  name          = "${var.name_prefix}-api"
  protocol_type = "HTTP"

  cors_configuration {
    allow_origins = ["*"] # tightened per real frontend domain once one exists; local dev's chi CORS
    # middleware (internal/httpapi/router.go) is the actual enforcement today
    allow_methods = ["GET", "POST", "DELETE", "OPTIONS"]
    allow_headers = ["Authorization", "Content-Type", "X-Admission-Token"]
  }
}

resource "aws_apigatewayv2_integration" "server" {
  api_id                 = aws_apigatewayv2_api.http.id
  integration_type       = "AWS_PROXY"
  integration_uri        = var.server_function_arn
  payload_format_version = "2.0"
}

resource "aws_apigatewayv2_authorizer" "jwt" {
  api_id           = aws_apigatewayv2_api.http.id
  authorizer_type  = "JWT"
  identity_sources = ["$request.header.Authorization"]
  name             = "${var.name_prefix}-jwt"

  jwt_configuration {
    audience = [var.cognito_client_id]
    issuer   = "https://cognito-idp.us-east-1.amazonaws.com/${var.cognito_user_pool_id}"
  }
}

# The catch-all route is UNauthenticated at the API Gateway layer -- auth
# is enforced per-route inside the chi router (internal/httpapi/router.go's
# own auth-gated Group), matching exactly which routes are open (catalog,
# availability) vs identity-bearing (holds, orders) locally. Duplicating
# that split here as two separate API Gateway routes would require
# reimplementing chi's routing table in Terraform and would drift the
# moment router.go changes; the JWT authorizer resource above exists and
# validates real tokens, but attaching it per-route is real future work
# once the route list is considered stable enough not to fight this file
# on every change.
resource "aws_apigatewayv2_route" "default" {
  api_id    = aws_apigatewayv2_api.http.id
  route_key = "$default"
  target    = "integrations/${aws_apigatewayv2_integration.server.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http.id
  name        = "$default"
  auto_deploy = true

  default_route_settings {
    throttling_burst_limit = 100
    throttling_rate_limit  = 50
  }

  access_log_settings {
    destination_arn = aws_cloudwatch_log_group.access_logs.arn
    format = jsonencode({
      requestId       = "$context.requestId", status = "$context.status",
      path            = "$context.path", integrationErrorMessage = "$context.integrationErrorMessage",
      responseLatency = "$context.responseLatency",
    })
  }
}

resource "aws_cloudwatch_log_group" "access_logs" {
  name              = "/aws/apigateway/${var.name_prefix}-api"
  retention_in_days = 14
}

resource "aws_lambda_permission" "apigw" {
  statement_id  = "AllowAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = var.server_function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.http.execution_arn}/*/*"
}

# The WAF attachment point (docs/plan.md "api" module comment, verbatim) --
# an HTTP API has no native origin CloudFront can point at directly for a
# Lambda-proxy backend the way an S3 bucket works, so this uses the API's
# own regional invoke domain as a custom origin. modules/waf associates
# its WebACL with THIS distribution, not with the HTTP API resource
# directly.
resource "aws_cloudfront_distribution" "api" {
  enabled    = true
  comment    = "${var.name_prefix} API (WAF attachment point)"
  web_acl_id = var.waf_web_acl_arn

  origin {
    domain_name = replace(aws_apigatewayv2_api.http.api_endpoint, "https://", "")
    origin_id   = "api-gateway"
    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  default_cache_behavior {
    target_origin_id         = "api-gateway"
    viewer_protocol_policy   = "redirect-to-https"
    allowed_methods          = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods           = ["GET", "HEAD"]
    cache_policy_id          = data.aws_cloudfront_cache_policy.disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_viewer.id
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}

data "aws_cloudfront_cache_policy" "disabled" {
  name = "Managed-CachingDisabled" # an API, not static assets -- never cache at the edge
}

data "aws_cloudfront_origin_request_policy" "all_viewer" {
  name = "Managed-AllViewer" # forward Authorization/X-Admission-Token through untouched
}
