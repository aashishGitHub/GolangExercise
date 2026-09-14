# docs/plan.md "websocket" module: WS API, $connect validates the JWT
# from the query string.
#
# HONEST GAP, not papered over: cmd/server's internal/wshub is a
# long-lived-process design -- one Go process holds every connection open
# and pushes deltas directly over that same socket. API Gateway WebSocket
# is the opposite shape: each $connect/$default/$disconnect event is its
# OWN Lambda invocation, and pushing a message to an already-connected
# client requires a SEPARATE call to the API Gateway Management API
# (`PostToConnection`) from whichever process produced the delta (i.e.
# projector-lambda), using `connectionId` looked up from `ws_connections`
# (docs/plan.md decision #7 -- Postgres holds exactly this mapping
# already). None of that push-side integration is implemented in this
# phase: the resources below declare the real AWS wiring a prod
# deployment needs, but $connect currently integrates with server-lambda,
# which does not speak API Gateway's WebSocket Lambda-proxy contract and
# would not actually authorize a real connection. This is genuinely
# unfinished, not a "should work" -- internal/wshub's local design is the
# thing that would need to change (each delta write becoming a
# PostToConnection call instead of a direct websocket.WriteMessage) for
# this to be real, and that's a bigger redesign than Phase 11's scope.
resource "aws_apigatewayv2_api" "ws" {
  name                       = "${var.name_prefix}-ws"
  protocol_type              = "WEBSOCKET"
  route_selection_expression = "$request.body.action"
}

resource "aws_apigatewayv2_integration" "connect" {
  api_id           = aws_apigatewayv2_api.ws.id
  integration_type = "AWS_PROXY"
  integration_uri  = var.server_function_arn
}

resource "aws_apigatewayv2_route" "connect" {
  api_id    = aws_apigatewayv2_api.ws.id
  route_key = "$connect"
  target    = "integrations/${aws_apigatewayv2_integration.connect.id}"
  # A real deployment authorizes here via a REQUEST authorizer reading
  # ?token=... from the query string (internal/auth.VerifyToken's own doc
  # comment already anticipated this exact mechanism) -- not wired in
  # this phase; see the module-level comment above.
}

resource "aws_apigatewayv2_route" "disconnect" {
  api_id    = aws_apigatewayv2_api.ws.id
  route_key = "$disconnect"
  target    = "integrations/${aws_apigatewayv2_integration.connect.id}"
}

resource "aws_apigatewayv2_route" "default" {
  api_id    = aws_apigatewayv2_api.ws.id
  route_key = "$default"
  target    = "integrations/${aws_apigatewayv2_integration.connect.id}"
}

resource "aws_apigatewayv2_stage" "prod" {
  api_id      = aws_apigatewayv2_api.ws.id
  name        = "prod"
  auto_deploy = true
}

resource "aws_lambda_permission" "ws_invoke" {
  statement_id  = "AllowWSAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = var.server_function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.ws.execution_arn}/*/*"
}
