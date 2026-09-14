variable "name_prefix" {
  type = string
}
variable "private_subnet_ids" {
  type = list(string)
}
variable "redis_security_group_id" {
  type = string
}
variable "redis_auth_token" {
  type      = string
  sensitive = true
}
variable "node_type" {
  type    = string
  default = "cache.t4g.small"
}
