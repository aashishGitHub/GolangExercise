# Terraform is written and `validate`-passing throughout. `plan`/`apply`
# against real AWS is a separate, explicitly user-triggered step — never run
# automatically (see docs/plan.md, decision #9).
#
# Both providers carry placeholder credentials plus the skip_* flags so
# `init`/`validate` run with zero real AWS access. The us-east-1 alias exists
# because WAFv2 CLOUDFRONT-scope WebACLs must be created there regardless of
# the primary region (added when modules/waf lands in Phase 11).

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "local"
  secret_key                  = "local"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

provider "aws" {
  alias                       = "us_east_1"
  region                      = "us-east-1"
  access_key                  = "local"
  secret_key                  = "local"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

locals {
  name_prefix = "ticketing-local"
}

module "network" {
  source      = "../../modules/network"
  name_prefix = local.name_prefix
}

module "secrets" {
  source      = "../../modules/secrets"
  name_prefix = local.name_prefix
}

module "database" {
  source = "../../modules/database"

  name_prefix                 = local.name_prefix
  vpc_id                      = module.network.vpc_id
  private_subnet_ids          = module.network.private_subnet_ids
  aurora_security_group_id    = module.network.aurora_security_group_id
  rds_proxy_security_group_id = module.network.rds_proxy_security_group_id
  db_secret_arn               = module.secrets.db_secret_arn
  db_username                 = module.secrets.db_username
  db_password                 = module.secrets.db_password
}

module "cache" {
  source = "../../modules/cache"

  name_prefix             = local.name_prefix
  private_subnet_ids      = module.network.private_subnet_ids
  redis_security_group_id = module.network.redis_security_group_id
  redis_auth_token        = module.secrets.redis_auth_token
}

module "auth" {
  source      = "../../modules/auth"
  name_prefix = local.name_prefix
}

module "storage" {
  source      = "../../modules/storage"
  name_prefix = local.name_prefix
}

module "compute" {
  source = "../../modules/compute"

  name_prefix              = local.name_prefix
  private_subnet_ids       = module.network.private_subnet_ids
  lambda_security_group_id = module.network.lambda_security_group_id
  db_secret_arn            = module.secrets.db_secret_arn
  redis_auth_secret_arn    = module.secrets.redis_auth_secret_arn
  qr_signing_key_arn       = module.secrets.qr_signing_key_arn
  tickets_bucket_arn       = module.storage.tickets_bucket_arn
  layouts_bucket_arn       = module.storage.layouts_bucket_arn
}

module "waf" {
  source = "../../modules/waf"
  providers = {
    aws.us_east_1 = aws.us_east_1
  }
  name_prefix = local.name_prefix
}

module "api" {
  source = "../../modules/api"

  name_prefix          = local.name_prefix
  server_function_arn  = module.compute.function_arns["server"]
  server_function_name = module.compute.server_function_name
  cognito_user_pool_id = module.auth.user_pool_id
  cognito_client_id    = module.auth.client_id
  waf_web_acl_arn      = module.waf.web_acl_arn
}

module "websocket" {
  source = "../../modules/websocket"

  name_prefix          = local.name_prefix
  server_function_arn  = module.compute.function_arns["server"]
  server_function_name = module.compute.server_function_name
}

module "eventing" {
  source = "../../modules/eventing"

  name_prefix   = local.name_prefix
  function_arns = module.compute.function_arns
}

module "observability" {
  source = "../../modules/observability"

  name_prefix                = local.name_prefix
  consumer_dlq_names         = { for k, v in module.eventing.consumer_dlq_arns : k => "${local.name_prefix}-${k}-dlq" }
  rds_proxy_name             = module.database.proxy_name
  redis_replication_group_id = module.cache.replication_group_id
}

module "search" {
  source = "../../modules/search"

  name_prefix        = local.name_prefix
  enable_search      = false # docs/plan.md decision #8 — see modules/search's own comment
  vpc_id             = module.network.vpc_id
  private_subnet_ids = module.network.private_subnet_ids
}

module "waitingroom" {
  source      = "../../modules/waitingroom"
  name_prefix = local.name_prefix
}
