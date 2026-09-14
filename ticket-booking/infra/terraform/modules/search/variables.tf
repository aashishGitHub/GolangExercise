variable "name_prefix" {
  type = string
}

variable "enable_search" {
  type    = bool
  default = false # docs/plan.md decision #8: pg_trgm + GIN is the real search path
  # (internal/db's ListEvents query, ilike+trigram-accelerated).
  # Two OpenSearch data nodes cost more per month than Aurora +
  # ElastiCache + all Lambda combined for a catalog of thousands
  # of events — this module exists and validates, gated off.
}

variable "vpc_id" {
  type = string
}

variable "private_subnet_ids" {
  type = list(string)
}
