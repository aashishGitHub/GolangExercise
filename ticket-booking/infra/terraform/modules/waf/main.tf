# docs/plan.md "waf" module: managed rules + a rate-based rule scoped to
# POST /api/v1/events/*/holds specifically -- the ONE endpoint an on-sale
# stampede actually hits hard (docs/plan.md's own status-code semantics:
# "409 SEAT_TAKEN is the dominant non-2xx during an on-sale and must be
# fast and cheap"; WAF's job is capping request VOLUME before it ever
# reaches that cheap-but-not-free path, not the seat-contention logic
# itself). Bot Control is gated off -- it's billed per request, and this
# project's actual bot defense is unguessable hold IDs + admission tokens
# (docs/plan.md fidelity gap list), not a paid managed rule group.
#
# CLOUDFRONT scope, so this MUST run in us-east-1 regardless of the
# primary region -- the us_east_1 provider alias exists in envs/local
# specifically for this module (see envs/local/main.tf's own comment,
# written back in Phase 0 anticipating this).
resource "aws_wafv2_web_acl" "main" {
  provider = aws.us_east_1

  name  = "${var.name_prefix}-waf"
  scope = "CLOUDFRONT"

  default_action {
    allow {}
  }

  rule {
    name     = "aws-managed-common"
    priority = 0
    override_action {
      none {} # apply the managed group's own block/count actions as-is
    }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesCommonRuleSet"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.name_prefix}-common"
      sampled_requests_enabled   = true
    }
  }

  rule {
    name     = "aws-managed-known-bad-inputs"
    priority = 1
    override_action {
      none {}
    }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesKnownBadInputsRuleSet"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.name_prefix}-known-bad-inputs"
      sampled_requests_enabled   = true
    }
  }

  # The rule this module exists for: rate-limit by source IP, scoped to
  # the hold-creation path via a byte-match on the URI, not the whole API.
  rule {
    name     = "rate-limit-holds"
    priority = 2
    action {
      block {}
    }
    statement {
      rate_based_statement {
        limit = 300 # requests / 5-min window / IP -- generous enough for one real
        # attendee retrying a losing 409, tight enough to blunt a
        # single-source script
        aggregate_key_type = "IP"

        scope_down_statement {
          and_statement {
            statement {
              byte_match_statement {
                search_string = "/holds"
                field_to_match {
                  uri_path {}
                }
                positional_constraint = "ENDS_WITH"
                text_transformation {
                  priority = 0
                  type     = "NONE"
                }
              }
            }
            statement {
              byte_match_statement {
                search_string = "POST"
                field_to_match {
                  method {}
                }
                positional_constraint = "EXACTLY"
                text_transformation {
                  priority = 0
                  type     = "NONE"
                }
              }
            }
          }
        }
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.name_prefix}-rate-limit-holds"
      sampled_requests_enabled   = true
    }
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "${var.name_prefix}-waf"
    sampled_requests_enabled   = true
  }
}
