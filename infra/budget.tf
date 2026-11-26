# AWS Budget Guardrail
# Protects the account with proactive alerts if monthly spend approaches $5.00.

resource "aws_budgets_budget" "cost_guardrail" {
  name         = "micup-${var.environment}-cost-guardrail"
  budget_type  = "COST"
  limit_amount = "5.0"
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  # Alert when actual spend exceeds 80% ($4.00)
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = ["contact@micup.live"]
  }

  # Alert when forecasted spend exceeds 100% ($5.00)
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = ["contact@micup.live"]
  }
}
