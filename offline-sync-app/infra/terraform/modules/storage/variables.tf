variable "name_prefix" {
  type = string
}
variable "web_acl_arn" {
  type        = string
  description = "CLOUDFRONT-scope WAFv2 WebACL ARN, from the waf module"
}
