output "ws_endpoint" {
  value = aws_apigatewayv2_stage.prod.invoke_url
}
