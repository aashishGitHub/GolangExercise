output "api_endpoint" {
  value = aws_apigatewayv2_api.http.api_endpoint
}

output "cloudfront_distribution_id" {
  value = aws_cloudfront_distribution.api.id
}

output "cloudfront_distribution_arn" {
  value = aws_cloudfront_distribution.api.arn
}

output "cloudfront_domain" {
  value = aws_cloudfront_distribution.api.domain_name
}

output "jwt_authorizer_id" {
  value = aws_apigatewayv2_authorizer.jwt.id
}
