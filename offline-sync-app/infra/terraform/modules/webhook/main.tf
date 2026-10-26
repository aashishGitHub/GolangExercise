resource "aws_iam_role" "webhook" {
  name = "${var.name_prefix}-webhook-receiver-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

# The only grant (logs + VPC ENIs): the binary calls no AWS API, its secrets arrive as env vars.
resource "aws_iam_role_policy_attachment" "vpc_access" {
  role       = aws_iam_role.webhook.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

resource "aws_lambda_function" "webhook" {
  function_name = "${var.name_prefix}-webhook-receiver"
  role          = aws_iam_role.webhook.arn

  filename         = "${path.module}/build/bootstrap.zip"
  source_code_hash = filebase64sha256("${path.module}/build/bootstrap.zip")

  runtime       = "provided.al2023"
  architectures = ["arm64"]
  handler       = "bootstrap"
  timeout       = 10
  memory_size   = 256

  vpc_config {
    subnet_ids         = var.private_subnet_ids
    security_group_ids = [var.lambda_security_group_id]
  }

  environment {
    variables = merge(var.base_environment, {
      EXTERNAL_SYSTEM = var.system
      WEBHOOK_SECRET  = var.webhook_secret
    })
  }
}

resource "aws_apigatewayv2_integration" "webhook" {
  api_id                 = var.api_id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.webhook.invoke_arn
  payload_format_version = "2.0"
}

# No JWT authorizer: the vendor has no Cognito token; the handler's HMAC check is the authentication.
resource "aws_apigatewayv2_route" "webhook" {
  api_id    = var.api_id
  route_key = "POST /webhooks/${var.system}"
  target    = "integrations/${aws_apigatewayv2_integration.webhook.id}"
}

resource "aws_lambda_permission" "apigw" {
  statement_id  = "AllowAPIGatewayInvokeWebhook"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.webhook.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${var.api_execution_arn}/*/*/webhooks/${var.system}"
}
