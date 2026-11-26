variable "aws_region" {
  type        = string
  description = "AWS region for primary resources"
  default     = "us-east-2"
}

variable "environment" {
  type        = string
  description = "Deployment environment name"
  default     = "production"
}

variable "domain_name" {
  type        = string
  description = "Primary domain name for the application"
  default     = "micup.live"
}

variable "include_subdomains" {
  type        = bool
  description = "Whether to include wildcard subdomains in ACM certificate"
  default     = true
}

variable "developer_key_scope_mode" {
  type        = string
  description = "Developer-key scope rollout mode: report logs would-deny requests; enforce returns 403."
  default     = "enforce"

  validation {
    condition     = contains(["report", "enforce"], var.developer_key_scope_mode)
    error_message = "developer_key_scope_mode must be report or enforce."
  }
}
