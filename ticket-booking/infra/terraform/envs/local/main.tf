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

# Remaining modules (cache, auth, storage, compute, api, websocket, eventing,
# waf, observability, gated search, waitingroom) are added phase by phase —
# see docs/plan.md "Terraform modules" for the phase-by-phase table.
