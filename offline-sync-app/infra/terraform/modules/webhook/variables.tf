variable "name_prefix" {
  type = string
}
variable "private_subnet_ids" {
  type = list(string)
}
variable "lambda_security_group_id" {
  type = string
}
variable "api_id" {
  type        = string
  description = "The existing HTTP API (modules/api) the webhook route is added to"
}
variable "api_execution_arn" {
  type = string
}
variable "system" {
  type        = string
  description = "Becomes both the route path segment and EXTERNAL_SYSTEM, so they cannot drift"
}
variable "webhook_secret" {
  type      = string
  sensitive = true
}
variable "base_environment" {
  type = map(string)
}
