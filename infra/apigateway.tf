# API Gateway HTTP API (v2) for MicUp.live
# Lightweight, ultra-low-latency HTTP API with default route throttling.

resource "aws_apigatewayv2_api" "http" {
  name          = "micup-${var.environment}-http-api"
  protocol_type = "HTTP"

  description = "MicUp.live serverless backend API (${var.environment})"

  tags = {
    Name = "micup-${var.environment}-http-api"
  }
}

resource "aws_apigatewayv2_integration" "lambda" {
  api_id                 = aws_apigatewayv2_api.http.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.api.invoke_arn
  payload_format_version = "2.0"
}

locals {
  # Verified against the Go router by TestGatewayRouteManifestMatchesRouter.
  api_routes = jsondecode(file("${path.module}/api-routes.json"))
  # ServeMux also matches HEAD on GET routes. OPTIONS reaches Go's CORS
  # middleware on every registered path, including cross-origin API clients.
  api_route_keys = toset(concat(
    local.api_routes,
    [for route in local.api_routes : "HEAD ${split(" ", route)[1]}" if startswith(route, "GET ")],
    [for route in local.api_routes : "OPTIONS ${split(" ", route)[1]}"],
  ))
}

resource "aws_apigatewayv2_route" "api" {
  for_each  = local.api_route_keys
  api_id    = aws_apigatewayv2_api.http.id
  route_key = each.key
  target    = "integrations/${aws_apigatewayv2_integration.lambda.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http.id
  name        = "$default"
  auto_deploy = true

  default_route_settings {
    throttling_burst_limit = 400
    throttling_rate_limit  = 200.0
  }

  tags = {
    Name = "micup-${var.environment}-api-default-stage"
  }
}

resource "aws_lambda_permission" "apigateway" {
  statement_id  = "AllowAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.api.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.http.execution_arn}/*/*"
}
