# API Gateway WebSocket API — the real-time-push driver from the original
# architecture. $connect/$disconnect run cmd/ws-connect-lambda (auth via
# JWT-in-query-string, since a browser's WebSocket API can't set an
# Authorization header); the notifier Lambda (modules/eventing) pushes to
# connections via the Management API, not through this API's own routes.

resource "aws_apigatewayv2_api" "main" {
  name                       = "${var.name_prefix}-ws"
  protocol_type              = "WEBSOCKET"
  route_selection_expression = "$request.body.action"
}

resource "aws_iam_role" "connect" {
  name = "${var.name_prefix}-ws-connect-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "connect_vpc_access" {
  role       = aws_iam_role.connect.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

resource "aws_iam_role_policy" "connect_app" {
  name = "${var.name_prefix}-ws-connect-app-policy"
  role = aws_iam_role.connect.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = var.db_secret_arn
    }]
  })
}

resource "aws_lambda_function" "connect" {
  function_name = "${var.name_prefix}-ws-connect"
  role          = aws_iam_role.connect.arn

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
    variables = var.environment
  }
}

resource "aws_apigatewayv2_integration" "connect" {
  api_id             = aws_apigatewayv2_api.main.id
  integration_type   = "AWS_PROXY"
  integration_uri    = aws_lambda_function.connect.invoke_arn
  integration_method = "POST"
}

resource "aws_apigatewayv2_route" "connect" {
  api_id    = aws_apigatewayv2_api.main.id
  route_key = "$connect"
  target    = "integrations/${aws_apigatewayv2_integration.connect.id}"
}

resource "aws_apigatewayv2_route" "disconnect" {
  api_id    = aws_apigatewayv2_api.main.id
  route_key = "$disconnect"
  target    = "integrations/${aws_apigatewayv2_integration.connect.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.main.id
  name        = "$default"
  auto_deploy = true
}

resource "aws_lambda_permission" "apigw" {
  statement_id  = "AllowWebSocketAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.connect.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.main.execution_arn}/*/*"
}
