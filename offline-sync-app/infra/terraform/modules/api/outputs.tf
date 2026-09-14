output "api_endpoint" {
  value = aws_apigatewayv2_stage.default.invoke_url
}

output "cloudfront_domain" {
  value = aws_cloudfront_distribution.api.domain_name
}
