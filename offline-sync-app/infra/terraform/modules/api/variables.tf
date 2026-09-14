variable "name_prefix" {
  type = string
}
variable "lambda_invoke_arn" {
  type = string
}
variable "lambda_function_name" {
  type = string
}
variable "cognito_user_pool_endpoint" {
  type        = string
  description = "Full issuer URL, e.g. https://cognito-idp.<region>.amazonaws.com/<pool_id>"
}
variable "cognito_client_id" {
  type = string
}
variable "web_acl_arn" {
  type        = string
  description = "CLOUDFRONT-scope WAFv2 WebACL ARN — HTTP API v2 has no direct WAF association, so this fronts the API with CloudFront specifically to get one"
}
variable "allowed_origin" {
  type        = string
  description = "The web app's origin (CloudFront domain), for the API's CORS configuration"
}
