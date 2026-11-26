# Durable, asynchronous catalog compilation. The API enqueues only songbook IDs;
# the worker loads the snapshot and publishes immutable S3 assets.
resource "aws_sqs_queue" "catalog_build_dlq" {
  name                      = "micup-${var.environment}-catalog-build-dlq"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" "catalog_build" {
  name                       = "micup-${var.environment}-catalog-build"
  visibility_timeout_seconds = 900
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.catalog_build_dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_sqs_queue" "import_jobs_dlq" {
  name                      = "micup-${var.environment}-import-jobs-dlq"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" "import_jobs" {
  name                       = "micup-${var.environment}-import-jobs"
  visibility_timeout_seconds = 900
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.import_jobs_dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_cloudwatch_log_group" "catalog_worker" {
  name              = "/aws/lambda/micup-${var.environment}-catalog-worker"
  retention_in_days = 7
}

resource "aws_lambda_function" "catalog_worker" {
  function_name    = "micup-${var.environment}-catalog-worker"
  role             = aws_iam_role.catalog_worker_exec.arn
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  filename         = "${path.module}/../dist/catalog-worker.zip"
  source_code_hash = fileexists("${path.module}/../dist/catalog-worker.zip") ? filebase64sha256("${path.module}/../dist/catalog-worker.zip") : null
  memory_size      = 1024
  timeout          = 900

  environment {
    variables = merge({
      APP_ENV              = "production"
      CATALOG_ASSET_BUCKET = aws_s3_bucket.app.id
    }, local.dsql_lambda_env)
  }

  depends_on = [aws_cloudwatch_log_group.catalog_worker, aws_iam_role_policy.catalog_worker_policy]
}

resource "aws_lambda_event_source_mapping" "catalog_build" {
  event_source_arn = aws_sqs_queue.catalog_build.arn
  function_name    = aws_lambda_function.catalog_worker.arn
  batch_size       = 1
}

resource "aws_cloudwatch_log_group" "import_worker" {
  name              = "/aws/lambda/micup-${var.environment}-import-worker"
  retention_in_days = 7
}

resource "aws_lambda_function" "import_worker" {
  function_name    = "micup-${var.environment}-import-worker"
  role             = aws_iam_role.import_worker_exec.arn
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  filename         = "${path.module}/../dist/import-worker.zip"
  source_code_hash = fileexists("${path.module}/../dist/import-worker.zip") ? filebase64sha256("${path.module}/../dist/import-worker.zip") : null
  memory_size      = 1024
  timeout          = 900
  environment {
    variables = merge({
      APP_ENV                 = "production"
      CATALOG_ASSET_BUCKET    = aws_s3_bucket.app.id
      CATALOG_BUILD_QUEUE_URL = aws_sqs_queue.catalog_build.url
    }, local.dsql_lambda_env)
  }
  depends_on = [aws_cloudwatch_log_group.import_worker, aws_iam_role_policy.import_worker_policy]
}

resource "aws_lambda_event_source_mapping" "import_jobs" {
  event_source_arn = aws_sqs_queue.import_jobs.arn
  function_name    = aws_lambda_function.import_worker.arn
  batch_size       = 1
}

resource "aws_cloudwatch_log_group" "compaction_dispatcher" {
  name              = "/aws/lambda/micup-${var.environment}-compaction-dispatcher"
  retention_in_days = 7
}

resource "aws_lambda_function" "compaction_dispatcher" {
  function_name    = "micup-${var.environment}-compaction-dispatcher"
  role             = aws_iam_role.compaction_dispatcher_exec.arn
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  filename         = "${path.module}/../dist/compaction-dispatcher.zip"
  source_code_hash = fileexists("${path.module}/../dist/compaction-dispatcher.zip") ? filebase64sha256("${path.module}/../dist/compaction-dispatcher.zip") : null
  memory_size      = 256
  timeout          = 60
  environment {
    variables = merge({
      APP_ENV                 = "production"
      CATALOG_BUILD_QUEUE_URL = aws_sqs_queue.catalog_build.url
    }, local.dsql_lambda_env)
  }
  depends_on = [aws_cloudwatch_log_group.compaction_dispatcher, aws_iam_role_policy.compaction_dispatcher_policy]
}

resource "aws_cloudwatch_event_rule" "catalog_compaction" {
  name                = "micup-${var.environment}-catalog-compaction"
  schedule_expression = "rate(1 hour)"
}

resource "aws_cloudwatch_event_target" "catalog_compaction" {
  rule      = aws_cloudwatch_event_rule.catalog_compaction.name
  target_id = "compaction-dispatcher"
  arn       = aws_lambda_function.compaction_dispatcher.arn
}

resource "aws_lambda_permission" "compaction_schedule" {
  statement_id  = "AllowEventBridgeCompaction"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.compaction_dispatcher.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.catalog_compaction.arn
}

resource "aws_cloudwatch_log_group" "import_reconciler" {
  name              = "/aws/lambda/micup-${var.environment}-import-reconciler"
  retention_in_days = 7
}

resource "aws_lambda_function" "import_reconciler" {
  function_name    = "micup-${var.environment}-import-reconciler"
  role             = aws_iam_role.import_reconciler_exec.arn
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  filename         = "${path.module}/../dist/import-reconciler.zip"
  source_code_hash = fileexists("${path.module}/../dist/import-reconciler.zip") ? filebase64sha256("${path.module}/../dist/import-reconciler.zip") : null
  memory_size      = 256
  timeout          = 60
  environment {
    variables = merge({
      APP_ENV              = "production"
      IMPORT_JOB_QUEUE_URL = aws_sqs_queue.import_jobs.url
    }, local.dsql_lambda_env)
  }
  depends_on = [aws_cloudwatch_log_group.import_reconciler, aws_iam_role_policy.import_reconciler_policy]
}

resource "aws_cloudwatch_event_rule" "import_reconciler" {
  name                = "micup-${var.environment}-import-reconciler"
  schedule_expression = "rate(5 minutes)"
}

resource "aws_cloudwatch_event_target" "import_reconciler" {
  rule      = aws_cloudwatch_event_rule.import_reconciler.name
  target_id = "import-reconciler"
  arn       = aws_lambda_function.import_reconciler.arn
}

resource "aws_lambda_permission" "import_reconciler_schedule" {
  statement_id  = "AllowEventBridgeImportReconciler"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.import_reconciler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.import_reconciler.arn
}
