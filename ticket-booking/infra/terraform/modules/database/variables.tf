variable "name_prefix" {
  type = string
}
variable "vpc_id" {
  type = string
}
variable "private_subnet_ids" {
  type = list(string)
}
variable "aurora_security_group_id" {
  type = string
}
variable "rds_proxy_security_group_id" {
  type = string
}
variable "db_secret_arn" {
  type = string
}
variable "db_username" {
  type = string
}
variable "db_password" {
  type      = string
  sensitive = true
}
variable "min_capacity" {
  type        = number
  default     = 0.5
  description = "Aurora Serverless v2 min ACU. This floor bills even at zero traffic — 0.5 is the minimum AWS allows, not a free idle state."
}
variable "max_capacity" {
  type        = number
  default     = 32
  description = "Higher than a typical CRUD default: event_seats is the contended write path during an on-sale (docs/plan.md \"database\" module)."
}
