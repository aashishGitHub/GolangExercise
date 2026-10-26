output "function_name" {
  value = aws_lambda_function.webhook.function_name
}

output "route_key" {
  value = aws_apigatewayv2_route.webhook.route_key
}
