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

variable "redis_auth_secret_arn" {
  type = string
}

variable "qr_signing_key_arn" {
  type = string
}

variable "tickets_bucket_arn" {
  type = string
}

variable "layouts_bucket_arn" {
  type = string
}
