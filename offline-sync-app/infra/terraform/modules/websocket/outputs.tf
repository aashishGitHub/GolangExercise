data "aws_region" "current" {}

output "api_id" {
  value = aws_apigatewayv2_api.main.id
}

output "api_arn" {
  value = aws_apigatewayv2_api.main.arn
}

output "connection_url" {
  value = aws_apigatewayv2_stage.default.invoke_url
}

# The Management API's endpoint is the same host as the WebSocket API but
# with an https:// scheme instead of wss:// — there's no direct attribute
# for this, so it's assembled the same way AWS's own docs describe.
output "management_api_endpoint" {
  value = "https://${aws_apigatewayv2_api.main.id}.execute-api.${data.aws_region.current.name}.amazonaws.com/${aws_apigatewayv2_stage.default.name}"
}
