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

# Modules are added phase by phase (see docs/plan.md "Terraform modules"):
# network + secrets + database land in Phase 2, cache in Phase 3, the rest
# from Phase 11 onward. No resources yet — this file exists so
# `terraform init && terraform validate` is green from Phase 0.
