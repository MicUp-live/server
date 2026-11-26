# Automated weekly catalog garbage collection.
# Purges unreferenced immutable S3 assets, orphan prefixes, stale import files,
# and expired DynamoDB catalog version / import job records.

resource "aws_cloudwatch_log_group" "catalog_gc" {
  name              = "/aws/lambda/micup-${var.environment}-catalog-gc"
  retention_in_days = 7
}

resource "aws_lambda_function" "catalog_gc" {
  function_name    = "micup-${var.environment}-catalog-gc"
  role             = aws_iam_role.catalog_gc_exec.arn
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  filename         = "${path.module}/../dist/catalog-gc.zip"
  source_code_hash = fileexists("${path.module}/../dist/catalog-gc.zip") ? filebase64sha256("${path.module}/../dist/catalog-gc.zip") : null
  memory_size      = 512
  timeout          = 120

  environment {
    variables = {
      APP_ENV              = "production"
      DYNAMO_TABLE_PREFIX  = local.dynamo_prefix
      CATALOG_ASSET_BUCKET = aws_s3_bucket.app.id
      GC_GRACE_PERIOD      = "24h"
      GC_DRY_RUN           = "false"
    }
  }

  depends_on = [aws_cloudwatch_log_group.catalog_gc, aws_iam_role_policy.catalog_gc_policy]
}

resource "aws_cloudwatch_event_rule" "catalog_gc" {
  name                = "micup-${var.environment}-catalog-gc"
  description         = "Trigger catalog garbage collection weekly on Sunday at 03:00 UTC"
  schedule_expression = "cron(0 3 ? * SUN *)"
}

resource "aws_cloudwatch_event_target" "catalog_gc" {
  rule      = aws_cloudwatch_event_rule.catalog_gc.name
  target_id = "catalog-gc"
  arn       = aws_lambda_function.catalog_gc.arn
}

resource "aws_lambda_permission" "catalog_gc_schedule" {
  statement_id  = "AllowEventBridgeCatalogGC"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.catalog_gc.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.catalog_gc.arn
}
