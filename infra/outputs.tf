output "route53_zone_id" {
  description = "Route 53 hosted zone ID"
  value       = aws_route53_zone.primary.zone_id
}

output "name_servers" {
  description = "Route 53 name servers to configure at your domain registrar"
  value       = aws_route53_zone.primary.name_servers
}

output "s3_bucket_name" {
  description = "Name of the S3 bucket hosting web application files"
  value       = aws_s3_bucket.app.id
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for cache invalidations"
  value       = aws_cloudfront_distribution.site.id
}

output "cloudfront_domain_name" {
  description = "CloudFront default domain name"
  value       = aws_cloudfront_distribution.site.domain_name
}

output "website_url" {
  description = "Primary website URL"
  value       = "https://${var.domain_name}"
}

output "api_gateway_endpoint" {
  description = "Direct API Gateway HTTP API endpoint"
  value       = aws_apigatewayv2_api.http.api_endpoint
}

output "lambda_function_name" {
  description = "Lambda backend function name"
  value       = aws_lambda_function.api.function_name
}

output "dsql_cluster_arn" {
  description = "Aurora DSQL cluster ARN (empty when enable_dsql is false)"
  value       = local.dsql_cluster_arn
}

output "dsql_endpoint" {
  description = "Aurora DSQL cluster endpoint hostname (empty when enable_dsql is false)"
  value       = local.dsql_endpoint
}
