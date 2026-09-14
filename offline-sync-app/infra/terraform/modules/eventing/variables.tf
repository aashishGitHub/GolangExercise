variable "name_prefix" {
  type = string
}
variable "private_subnet_ids" {
  type = list(string)
}
variable "lambda_security_group_id" {
  type = string
}
variable "db_secret_arn" {
  type = string
}
variable "base_environment" {
  type        = map(string)
  description = "Shared env vars every consumer needs: DATABASE_URL, AWS_REGION (matches modules/compute's environment map minus API-specific vars)"
}
variable "websocket_api_arn" {
  type        = string
  description = "For the notifier Lambda's execute-api:ManageConnections IAM permission"
}
variable "websocket_management_api_endpoint" {
  type = string
}
