# AWS-managed rule groups covering the OWASP-top-10-shaped attack classes
# (generic exploits, known bad inputs, SQLi) — not a custom rule set, which
# would need its own ongoing maintenance this project has no capacity for.
#
# Scope is CLOUDFRONT, which AWS requires to be created via a provider
# pinned to us-east-1 regardless of the stack's primary region — hence the
# aliased provider input, even though this stack's primary region already
# is us-east-1 today. Also: this is *why* CloudFront fronts the API Gateway
# HTTP API in the api module — HTTP API (v2) has no direct WAF association;
# only CloudFront, ALB, AppSync, Cognito, and REST API (v1) do. Switching to
# REST API would have meant giving up the plan doc's locked HTTP API
# decision (cost/latency), so CloudFront-in-front is the smaller change.
resource "aws_wafv2_web_acl" "main" {
  provider = aws.us_east_1
  name     = "${var.name_prefix}-waf"
  scope    = "CLOUDFRONT"

  default_action {
    allow {}
  }

  dynamic "rule" {
    for_each = toset([
      "AWSManagedRulesCommonRuleSet",
      "AWSManagedRulesKnownBadInputsRuleSet",
      "AWSManagedRulesSQLiRuleSet",
    ])
    content {
      name     = rule.value
      priority = index(["AWSManagedRulesCommonRuleSet", "AWSManagedRulesKnownBadInputsRuleSet", "AWSManagedRulesSQLiRuleSet"], rule.value)

      override_action {
        none {}
      }

      statement {
        managed_rule_group_statement {
          vendor_name = "AWS"
          name        = rule.value
        }
      }

      visibility_config {
        sampled_requests_enabled   = true
        cloudwatch_metrics_enabled = true
        metric_name                = "${var.name_prefix}-${rule.value}"
      }
    }
  }

  visibility_config {
    sampled_requests_enabled   = true
    cloudwatch_metrics_enabled = true
    metric_name                = "${var.name_prefix}-waf"
  }
}
