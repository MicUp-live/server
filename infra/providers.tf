provider "aws" {
  region              = var.aws_region
  allowed_account_ids = ["223662764170"]

  default_tags {
    tags = {
      Project     = "MicUp"
      Environment = var.environment
      ManagedBy   = "OpenTofu"
    }
  }
}

# CloudFront viewer certificates must reside in us-east-1 (N. Virginia)
provider "aws" {
  alias               = "us-east-1"
  region              = "us-east-1"
  allowed_account_ids = ["223662764170"]

  default_tags {
    tags = {
      Project     = "MicUp"
      Environment = var.environment
      ManagedBy   = "OpenTofu"
    }
  }
}
