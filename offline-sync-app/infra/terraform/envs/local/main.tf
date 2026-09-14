terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

# Placeholder provider config so `terraform init`/`validate`/`plan` succeed
# with no credentials. `terraform apply` against this env targets real AWS
# and is never run automatically — it needs explicit, separate user approval,
# and real credentials this config does not provide.
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "local"
  secret_key                  = "local"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}

# CLOUDFRONT-scope WAFv2 resources must be created via a provider pinned to
# us-east-1 regardless of the stack's primary region (an AWS requirement, not
# a choice) — see modules/waf's comment. Identical to the default provider
# today since this stack's primary region already is us-east-1, but named
# explicitly so it stays correct if the primary region ever changes.
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
  name_prefix = "fieldsync-local"

  # Shared by every Lambda that talks to Postgres/Cognito/S3 — the compute,
  # eventing, and websocket modules all start from this and layer on their
  # own extras (e.g. eventing's consumers don't need COGNITO_* at all, but
  # passing it costs nothing and keeps one source of truth).
  base_environment = {
    APP_ENV           = "prod"
    DATABASE_URL      = "postgres://${module.secrets.username}:${module.secrets.password}@${module.database.proxy_endpoint}:5432/fieldsync"
    COGNITO_ISSUER    = module.auth.user_pool_endpoint
    COGNITO_CLIENT_ID = module.auth.client_id
    S3_PHOTOS_BUCKET  = module.storage.photos_bucket_name
    AWS_REGION        = "us-east-1"
    # S3_ENDPOINT/S3_ACCESS_KEY/S3_SECRET_KEY, EVENT_BUS_ENDPOINT, and
    # EVENT_BUS_NAME are intentionally omitted here — EVENT_BUS_NAME is set
    # directly inside modules/eventing (it already has the bus resource in
    # scope, and taking it as an input here would be a circular reference:
    # this local feeds module.eventing, which creates the bus). The others
    # are omitted so prod uses real S3/EventBridge endpoints + the Lambda
    # execution role's own credentials (see internal/config.Load's comments).
  }
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
  source                      = "../../modules/database"
  name_prefix                 = local.name_prefix
  vpc_id                      = module.network.vpc_id
  private_subnet_ids          = module.network.private_subnet_ids
  aurora_security_group_id    = module.network.aurora_security_group_id
  rds_proxy_security_group_id = module.network.rds_proxy_security_group_id
  db_secret_arn               = module.secrets.secret_arn
  db_username                 = module.secrets.username
  db_password                 = module.secrets.password
}

module "auth" {
  source      = "../../modules/auth"
  name_prefix = local.name_prefix
}

module "waf" {
  source      = "../../modules/waf"
  name_prefix = local.name_prefix
  providers = {
    aws.us_east_1 = aws.us_east_1
  }
}

module "storage" {
  source      = "../../modules/storage"
  name_prefix = local.name_prefix
  web_acl_arn = module.waf.web_acl_arn
}

module "compute" {
  source                   = "../../modules/compute"
  name_prefix              = local.name_prefix
  private_subnet_ids       = module.network.private_subnet_ids
  lambda_security_group_id = module.network.lambda_security_group_id
  photos_bucket_arn        = module.storage.photos_bucket_arn
  db_secret_arn            = module.secrets.secret_arn
  environment              = local.base_environment
}

module "api" {
  source                     = "../../modules/api"
  name_prefix                = local.name_prefix
  lambda_invoke_arn          = module.compute.invoke_arn
  lambda_function_name       = module.compute.function_name
  cognito_user_pool_endpoint = module.auth.user_pool_endpoint
  cognito_client_id          = module.auth.client_id
  web_acl_arn                = module.waf.web_acl_arn
  allowed_origin             = module.storage.web_cloudfront_domain
}

module "websocket" {
  source                   = "../../modules/websocket"
  name_prefix              = local.name_prefix
  private_subnet_ids       = module.network.private_subnet_ids
  lambda_security_group_id = module.network.lambda_security_group_id
  db_secret_arn            = module.secrets.secret_arn
  environment              = local.base_environment
}

module "eventing" {
  source                            = "../../modules/eventing"
  name_prefix                       = local.name_prefix
  private_subnet_ids                = module.network.private_subnet_ids
  lambda_security_group_id          = module.network.lambda_security_group_id
  db_secret_arn                     = module.secrets.secret_arn
  base_environment                  = local.base_environment
  websocket_api_arn                 = module.websocket.api_arn
  websocket_management_api_endpoint = module.websocket.management_api_endpoint
}
