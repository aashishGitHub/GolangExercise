variable "name_prefix" {
  type = string
}

variable "server_function_arn" {
  type = string
}

variable "server_function_name" {
  type = string
}

variable "cognito_user_pool_id" {
  type = string
}

variable "cognito_client_id" {
  type = string
}

variable "waf_web_acl_arn" {
  type    = string
  default = null # nullable so envs/local can wire modules/api before modules/waf if it ever needs to
}
