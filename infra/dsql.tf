# Amazon Aurora DSQL cluster and IAM identity-based auth policies (PG1).

moved {
  from = aws_dsql_cluster.main[0]
  to   = aws_dsql_cluster.main
}

resource "aws_dsql_cluster" "main" {
  deletion_protection_enabled = var.dsql_deletion_protection

  tags = {
    Name        = "micup-${var.environment}-dsql"
    Environment = var.environment
  }
}

locals {
  dsql_endpoint    = "${aws_dsql_cluster.main.identifier}.dsql.${var.aws_region}.on.aws"
  dsql_cluster_arn = aws_dsql_cluster.main.arn

  dsql_lambda_env = {
    STORE_BACKEND      = "postgres"
    POSTGRES_DSQL_MODE = "true"
    DSQL_ENDPOINT      = local.dsql_endpoint
  }

  dsql_lambda_roles = {
    api                   = aws_iam_role.lambda_exec.id
    catalog_worker        = aws_iam_role.catalog_worker_exec.id
    import_worker         = aws_iam_role.import_worker_exec.id
    compaction_dispatcher = aws_iam_role.compaction_dispatcher_exec.id
    import_reconciler     = aws_iam_role.import_reconciler_exec.id
    catalog_gc            = aws_iam_role.catalog_gc_exec.id
  }
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
