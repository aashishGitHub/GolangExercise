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
variable "environment" {
  type = map(string)
}
