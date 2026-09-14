variable "name_prefix" {
  type = string
}
variable "private_subnet_ids" {
  type = list(string)
}
variable "lambda_security_group_id" {
  type = string
}
variable "photos_bucket_arn" {
  type = string
}
variable "db_secret_arn" {
  type = string
}
variable "environment" {
  type        = map(string)
  description = "Env vars matching internal/config's getenv keys — ADDR is intentionally omitted, Lambda doesn't listen on a port"
}
