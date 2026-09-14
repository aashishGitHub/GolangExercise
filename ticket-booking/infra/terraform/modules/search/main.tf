# docs/plan.md "search" module: gated OFF by default (decision #8) — real
# search here is Postgres pg_trgm + GIN, not OpenSearch. This module is
# written and `validate`-passes so the tradeoff is provable, not just
# argued in a doc: count = var.enable_search ? 1 : 0 is the literal gate.

resource "aws_security_group" "opensearch" {
  count       = var.enable_search ? 1 : 0
  name_prefix = "${var.name_prefix}-search-"
  vpc_id      = var.vpc_id

  ingress {
    from_port = 443
    to_port   = 443
    protocol  = "tcp"
    self      = true # Lambda's own SG is added as a separate ingress rule by the caller if needed;
    # kept minimal here since this module is off by default
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_opensearch_domain" "events" {
  count          = var.enable_search ? 1 : 0
  domain_name    = "${var.name_prefix}-events"
  engine_version = "OpenSearch_2.11"

  cluster_config {
    instance_type = "t3.small.search" # smallest real data-node type; still the cost this
    # module's whole existence argues against defaulting to
    instance_count         = 2 # docs/plan.md: "two OpenSearch data nodes"
    zone_awareness_enabled = true
    zone_awareness_config {
      availability_zone_count = 2
    }
  }

  ebs_options {
    ebs_enabled = true
    volume_size = 20
    volume_type = "gp3"
  }

  vpc_options {
    subnet_ids         = slice(var.private_subnet_ids, 0, 2)
    security_group_ids = [aws_security_group.opensearch[0].id]
  }

  encrypt_at_rest {
    enabled = true
  }
  node_to_node_encryption {
    enabled = true
  }
  domain_endpoint_options {
    enforce_https = true
  }
}
