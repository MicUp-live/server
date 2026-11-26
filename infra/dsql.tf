# Amazon Aurora DSQL cluster and IAM identity-based auth policies (PG1).
# Gated by var.enable_dsql (default false) and var.store_backend (default "dynamo")
# so provisioning, DynamoDB->DSQL data migration, and cutover happen cleanly in stages.

resource "aws_dsql_cluster" "main" {
  count                       = var.enable_dsql ? 1 : 0
  deletion_protection_enabled = var.dsql_deletion_protection

  tags = {
    Name        = "micup-${var.environment}-dsql"
    Environment = var.environment
  }
}

locals {
  dsql_endpoint    = var.enable_dsql ? "${aws_dsql_cluster.main[0].identifier}.dsql.${var.aws_region}.on.aws" : ""
  dsql_cluster_arn = var.enable_dsql ? aws_dsql_cluster.main[0].arn : ""

  dsql_lambda_env = var.enable_dsql ? {
    STORE_BACKEND      = var.store_backend
    POSTGRES_DSQL_MODE = "true"
    DSQL_ENDPOINT      = local.dsql_endpoint
    } : {
    STORE_BACKEND = var.store_backend
  }

  dsql_lambda_roles = var.enable_dsql ? {
    api                   = aws_iam_role.lambda_exec.id
    catalog_worker        = aws_iam_role.catalog_worker_exec.id
    import_worker         = aws_iam_role.import_worker_exec.id
    compaction_dispatcher = aws_iam_role.compaction_dispatcher_exec.id
    import_reconciler     = aws_iam_role.import_reconciler_exec.id
    catalog_gc            = aws_iam_role.catalog_gc_exec.id
  } : {}
}

resource "aws_iam_role_policy" "dsql_connect" {
  for_each = local.dsql_lambda_roles

  name = "micup-${var.environment}-${each.key}-dsql-connect"
  role = each.value

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dsql:DbConnectAdmin",
          "dsql:DbConnect"
        ]
        Resource = [local.dsql_cluster_arn]
      }
    ]
  })
}
