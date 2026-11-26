# AWS Lambda Backend for MicUp.live API
# Runs ARM64 Go on AWS Graviton2 with zero idle compute cost.

data "aws_caller_identity" "current" {}

resource "random_password" "auth_secret" {
  length  = 48
  special = false
}

# IAM Role for Lambda
resource "aws_iam_role" "lambda_exec" {
  name = "micup-${var.environment}-lambda-exec"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
      }
    ]
  })

  tags = {
    Name = "micup-${var.environment}-lambda-exec"
  }
}

# IAM Policy for API Lambda (DynamoDB, SES, S3 uploads/manifests, SQS enqueue, API logs)
resource "aws_iam_role_policy" "lambda_policy" {
  name = "micup-${var.environment}-lambda-policy"
  role = aws_iam_role.lambda_exec.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # CloudWatch Logging
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = [
          "${aws_cloudwatch_log_group.lambda.arn}:*"
        ]
      },
      # Scoped DynamoDB Access to MicUp tables and GSIs
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Query",
          "dynamodb:Scan",
          "dynamodb:BatchGetItem",
          "dynamodb:BatchWriteItem",
          "dynamodb:ConditionCheckItem",
          "dynamodb:TransactWriteItems",
          "dynamodb:TransactGetItems",
          "dynamodb:DescribeTable"
        ]
        Resource = [
          "arn:aws:dynamodb:${var.aws_region}:${data.aws_caller_identity.current.account_id}:table/${local.dynamo_prefix}*",
          "arn:aws:dynamodb:${var.aws_region}:${data.aws_caller_identity.current.account_id}:table/${local.dynamo_prefix}*/index/*"
        ]
      },
      # SES Sending Permissions
      {
        Effect = "Allow"
        Action = [
          "ses:SendEmail",
          "ses:SendRawEmail"
        ]
        Resource = "*"
      },
      # List bucket for asset checks
      {
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = [aws_s3_bucket.app.arn]
      },
      # Immutable public catalog assets, import manifests, and reference archives
      {
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:GetObject",
          "s3:DeleteObject"
        ]
        Resource = ["${aws_s3_bucket.app.arn}/catalogs/*", "${aws_s3_bucket.app.arn}/imports/*", "${aws_s3_bucket.app.arn}/reference/*", "${aws_s3_bucket.app.arn}/private/*"]
      },
      # API only enqueues background jobs
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = [aws_sqs_queue.catalog_build.arn, aws_sqs_queue.import_jobs.arn]
      }
    ]
  })
}

locals {
  lambda_assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
      }
    ]
  })
}

# Least-privilege IAM roles for each background/scheduled worker Lambda.
resource "aws_iam_role" "catalog_worker_exec" {
  name               = "micup-${var.environment}-catalog-worker-exec"
  assume_role_policy = local.lambda_assume_role_policy
  tags               = { Name = "micup-${var.environment}-catalog-worker-exec" }
}

resource "aws_iam_role_policy" "catalog_worker_policy" {
  name = "micup-${var.environment}-catalog-worker-policy"
  role = aws_iam_role.catalog_worker_exec.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = ["${aws_cloudwatch_log_group.catalog_worker.arn}:*"]
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Query",
          "dynamodb:Scan",
          "dynamodb:ConditionCheckItem",
          "dynamodb:TransactWriteItems"
        ]
        Resource = [
          aws_dynamodb_table.songbooks.arn,
          "${aws_dynamodb_table.songbooks.arn}/index/*",
          aws_dynamodb_table.catalog_versions.arn,
          "${aws_dynamodb_table.catalog_versions.arn}/index/*",
          aws_dynamodb_table.catalog_changes.arn,
          "${aws_dynamodb_table.catalog_changes.arn}/index/*",
          aws_dynamodb_table.import_jobs.arn,
          "${aws_dynamodb_table.import_jobs.arn}/index/*"
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = [aws_s3_bucket.app.arn]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"]
        Resource = ["${aws_s3_bucket.app.arn}/catalogs/*", "${aws_s3_bucket.app.arn}/imports/*", "${aws_s3_bucket.app.arn}/private/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
        Resource = [aws_sqs_queue.catalog_build.arn]
      }
    ]
  })
}

resource "aws_iam_role" "import_worker_exec" {
  name               = "micup-${var.environment}-import-worker-exec"
  assume_role_policy = local.lambda_assume_role_policy
  tags               = { Name = "micup-${var.environment}-import-worker-exec" }
}

resource "aws_iam_role_policy" "import_worker_policy" {
  name = "micup-${var.environment}-import-worker-policy"
  role = aws_iam_role.import_worker_exec.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = ["${aws_cloudwatch_log_group.import_worker.arn}:*"]
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:UpdateItem",
          "dynamodb:ConditionCheckItem",
          "dynamodb:TransactWriteItems"
        ]
        Resource = [
          aws_dynamodb_table.import_jobs.arn,
          "${aws_dynamodb_table.import_jobs.arn}/index/*",
          aws_dynamodb_table.songbooks.arn,
          "${aws_dynamodb_table.songbooks.arn}/index/*"
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = ["${aws_s3_bucket.app.arn}/imports/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
        Resource = [aws_sqs_queue.import_jobs.arn]
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = [aws_sqs_queue.catalog_build.arn]
      }
    ]
  })
}

resource "aws_iam_role" "compaction_dispatcher_exec" {
  name               = "micup-${var.environment}-compaction-dispatcher-exec"
  assume_role_policy = local.lambda_assume_role_policy
  tags               = { Name = "micup-${var.environment}-compaction-dispatcher-exec" }
}

resource "aws_iam_role_policy" "compaction_dispatcher_policy" {
  name = "micup-${var.environment}-compaction-dispatcher-policy"
  role = aws_iam_role.compaction_dispatcher_exec.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = ["${aws_cloudwatch_log_group.compaction_dispatcher.arn}:*"]
      },
      {
        Effect = "Allow"
        Action = ["dynamodb:GetItem", "dynamodb:Query", "dynamodb:Scan"]
        Resource = [
          aws_dynamodb_table.songbooks.arn,
          "${aws_dynamodb_table.songbooks.arn}/index/*",
          aws_dynamodb_table.catalog_changes.arn,
          "${aws_dynamodb_table.catalog_changes.arn}/index/*"
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = [aws_sqs_queue.catalog_build.arn]
      }
    ]
  })
}

resource "aws_iam_role" "import_reconciler_exec" {
  name               = "micup-${var.environment}-import-reconciler-exec"
  assume_role_policy = local.lambda_assume_role_policy
  tags               = { Name = "micup-${var.environment}-import-reconciler-exec" }
}

resource "aws_iam_role_policy" "import_reconciler_policy" {
  name = "micup-${var.environment}-import-reconciler-policy"
  role = aws_iam_role.import_reconciler_exec.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = ["${aws_cloudwatch_log_group.import_reconciler.arn}:*"]
      },
      {
        Effect = "Allow"
        Action = ["dynamodb:GetItem", "dynamodb:UpdateItem", "dynamodb:Query", "dynamodb:Scan", "dynamodb:ConditionCheckItem"]
        Resource = [
          aws_dynamodb_table.import_jobs.arn,
          "${aws_dynamodb_table.import_jobs.arn}/index/*"
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = [aws_sqs_queue.import_jobs.arn]
      }
    ]
  })
}

resource "aws_iam_role" "catalog_gc_exec" {
  name               = "micup-${var.environment}-catalog-gc-exec"
  assume_role_policy = local.lambda_assume_role_policy
  tags               = { Name = "micup-${var.environment}-catalog-gc-exec" }
}

resource "aws_iam_role_policy" "catalog_gc_policy" {
  name = "micup-${var.environment}-catalog-gc-policy"
  role = aws_iam_role.catalog_gc_exec.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = ["${aws_cloudwatch_log_group.catalog_gc.arn}:*"]
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Query",
          "dynamodb:Scan",
          "dynamodb:ConditionCheckItem",
          "dynamodb:TransactWriteItems"
        ]
        Resource = [
          aws_dynamodb_table.songbooks.arn,
          "${aws_dynamodb_table.songbooks.arn}/index/*",
          aws_dynamodb_table.catalog_versions.arn,
          "${aws_dynamodb_table.catalog_versions.arn}/index/*",
          aws_dynamodb_table.import_jobs.arn,
          "${aws_dynamodb_table.import_jobs.arn}/index/*"
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = [aws_s3_bucket.app.arn]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:DeleteObject"]
        Resource = ["${aws_s3_bucket.app.arn}/catalogs/*", "${aws_s3_bucket.app.arn}/imports/*", "${aws_s3_bucket.app.arn}/private/*"]
      }
    ]
  })
}

# CloudWatch Log Group with 7-day retention (Free Tier safe)
resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/micup-${var.environment}-api"
  retention_in_days = 7

  tags = {
    Name = "micup-${var.environment}-api-logs"
  }
}

# The Go ARM64 Lambda Function
resource "aws_lambda_function" "api" {
  function_name = "micup-${var.environment}-api"
  role          = aws_iam_role.lambda_exec.arn
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]

  filename         = "${path.module}/../dist/lambda.zip"
  source_code_hash = fileexists("${path.module}/../dist/lambda.zip") ? filebase64sha256("${path.module}/../dist/lambda.zip") : null

  memory_size = 512
  timeout     = 29

  environment {
    variables = {
      APP_ENV                  = "production"
      PUBLIC_BASE_URL          = "https://${var.domain_name}"
      WEBAUTHN_RP_ID           = var.domain_name
      WEBAUTHN_ORIGINS         = "https://${var.domain_name},https://www.${var.domain_name}"
      DYNAMO_TABLE_PREFIX      = local.dynamo_prefix
      AUTH_SECRET              = random_password.auth_secret.result
      CORS_ALLOW_ORIGIN        = "https://${var.domain_name},https://www.${var.domain_name},https://player.${var.domain_name}"
      CATALOG_ASSET_BUCKET     = aws_s3_bucket.app.id
      CATALOG_BUILD_QUEUE_URL  = aws_sqs_queue.catalog_build.url
      IMPORT_JOB_QUEUE_URL     = aws_sqs_queue.import_jobs.url
      DEVELOPER_KEY_SCOPE_MODE = var.developer_key_scope_mode
      PORT                     = "8080"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda,
    aws_iam_role_policy.lambda_policy
  ]

  tags = {
    Name = "micup-${var.environment}-api"
  }
}
