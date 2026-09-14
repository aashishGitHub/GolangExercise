# Aurora Serverless v2 (Postgres) behind RDS Proxy. Proxy exists specifically
# for Lambda: without it, each concurrent Lambda invocation opens its own
# Postgres connection and Aurora's connection limit gets exhausted under
# bursty concurrency — the proxy pools connections down to what Aurora can
# actually hold. max_capacity is higher than a typical CRUD app's default:
# event_seats is the contended write path, capacity-planned for the
# waiting room's admitted rate, not the arrival rate (docs/plan.md
# "database" module).

resource "aws_db_subnet_group" "main" {
  name       = "${var.name_prefix}-db-subnets"
  subnet_ids = var.private_subnet_ids
}

# idle_in_transaction_session_timeout: an idle transaction holding a lock on
# an event_seats row is the failure mode that takes down an on-sale — kill
# it after 5s rather than let it sit. log_min_duration_statement surfaces
# any hold/confirm CAS that's slow enough to matter.
resource "aws_rds_cluster_parameter_group" "main" {
  name   = "${var.name_prefix}-aurora-pg"
  family = "aurora-postgresql16"

  parameter {
    name  = "idle_in_transaction_session_timeout"
    value = "5000"
  }
  parameter {
    name  = "log_min_duration_statement"
    value = "200"
  }
}

resource "aws_rds_cluster" "main" {
  cluster_identifier              = "${var.name_prefix}-aurora"
  engine                          = "aurora-postgresql"
  engine_mode                     = "provisioned" # required value for Aurora Serverless v2
  engine_version                  = "16.4"
  database_name                   = "ticketing"
  master_username                 = var.db_username
  master_password                 = var.db_password
  db_subnet_group_name            = aws_db_subnet_group.main.name
  db_cluster_parameter_group_name = aws_rds_cluster_parameter_group.main.name
  vpc_security_group_ids          = [var.aurora_security_group_id]
  storage_encrypted               = true
  skip_final_snapshot             = true # local/dev-shaped default; flip for a real prod env

  serverlessv2_scaling_configuration {
    min_capacity = var.min_capacity
    max_capacity = var.max_capacity
  }
}

# Writer + a reader instance — the reader takes catalog/reporting reads once
# Phase 11 routes to it; both provisioned now since Aurora Serverless v2
# instances scale independently of this split.
resource "aws_rds_cluster_instance" "writer" {
  cluster_identifier = aws_rds_cluster.main.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.main.engine
  engine_version     = aws_rds_cluster.main.engine_version
}

resource "aws_rds_cluster_instance" "reader" {
  cluster_identifier = aws_rds_cluster.main.id
  instance_class     = "db.serverless"
  engine             = aws_rds_cluster.main.engine
  engine_version     = aws_rds_cluster.main.engine_version
}

resource "aws_iam_role" "rds_proxy" {
  name = "${var.name_prefix}-rds-proxy-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "rds.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "rds_proxy_secrets" {
  name = "${var.name_prefix}-rds-proxy-secrets"
  role = aws_iam_role.rds_proxy.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = var.db_secret_arn
    }]
  })
}

resource "aws_db_proxy" "main" {
  name                   = "${var.name_prefix}-proxy"
  engine_family          = "POSTGRESQL"
  role_arn               = aws_iam_role.rds_proxy.arn
  vpc_subnet_ids         = var.private_subnet_ids
  vpc_security_group_ids = [var.rds_proxy_security_group_id]
  require_tls            = true
  idle_client_timeout    = 120 # seconds; RDS Proxy's session-pinning risk (docs/plan.md fidelity gap) argues for reclaiming idle connections aggressively

  auth {
    auth_scheme = "SECRETS"
    secret_arn  = var.db_secret_arn
  }
}

resource "aws_db_proxy_default_target_group" "main" {
  db_proxy_name = aws_db_proxy.main.name
  connection_pool_config {
    max_connections_percent = 100
  }
}

resource "aws_db_proxy_target" "main" {
  db_proxy_name         = aws_db_proxy.main.name
  target_group_name     = aws_db_proxy_default_target_group.main.name
  db_cluster_identifier = aws_rds_cluster.main.cluster_identifier
}
