# DynamoDB Tables for MicUp.live Production Persistence
# All tables use PAY_PER_REQUEST (On-Demand) billing for $0.00 idle cost.

locals {
  dynamo_prefix = "micup-${var.environment}-"
}

# 1. Users Table
resource "aws_dynamodb_table" "users" {
  name         = "${local.dynamo_prefix}users"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}users"
  }
}

# 2. Identity Claims (email lookup / deduplication)
resource "aws_dynamodb_table" "identity_claims" {
  name         = "${local.dynamo_prefix}identity-claims"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "email"

  attribute {
    name = "email"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}identity-claims"
  }
}

# 3. Sessions (with TTL)
resource "aws_dynamodb_table" "sessions" {
  name         = "${local.dynamo_prefix}sessions"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "token_hash"

  attribute {
    name = "token_hash"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}sessions"
  }
}

# 4. Songbooks (Master catalogs with public & slug indexes)
resource "aws_dynamodb_table" "songbooks" {
  name         = "${local.dynamo_prefix}songbooks"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  attribute {
    name = "public_id"
    type = "S"
  }

  attribute {
    name = "slug"
    type = "S"
  }

  global_secondary_index {
    name            = "public-id-index"
    hash_key        = "public_id"
    projection_type = "ALL"
  }

  global_secondary_index {
    name            = "slug-index"
    hash_key        = "slug"
    projection_type = "ALL"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}songbooks"
  }
}

# 5. Event Requests (Canonical strongly-consistent event-scoped live queue)
resource "aws_dynamodb_table" "event_requests" {
  name         = "${local.dynamo_prefix}event-requests"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "event_id"
  range_key    = "id"

  attribute {
    name = "event_id"
    type = "S"
  }

  attribute {
    name = "id"
    type = "S"
  }

  global_secondary_index {
    name            = "request-id-index"
    hash_key        = "id"
    projection_type = "ALL"
  }

  point_in_time_recovery {
    enabled = true
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name = "${local.dynamo_prefix}event-requests"
  }
}

# 8. Events (Live shows / gigs)
resource "aws_dynamodb_table" "events" {
  name         = "${local.dynamo_prefix}events"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  attribute {
    name = "join_code"
    type = "S"
  }

  global_secondary_index {
    name            = "join-code-index"
    hash_key        = "join_code"
    projection_type = "ALL"
  }

  point_in_time_recovery {
    enabled = true
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name = "${local.dynamo_prefix}events"
  }
}

# 9. Passkeys (FIDO2 / WebAuthn credentials)
resource "aws_dynamodb_table" "passkeys" {
  name         = "${local.dynamo_prefix}passkeys"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}passkeys"
  }
}

# 10. WebAuthn Sessions (Challenge tokens with TTL)
resource "aws_dynamodb_table" "webauthn_sessions" {
  name         = "${local.dynamo_prefix}webauthn-sessions"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "session_id"

  attribute {
    name = "session_id"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name = "${local.dynamo_prefix}webauthn-sessions"
  }
}

# 11. Catalog Versions (immutable, content-addressed catalog build metadata)
resource "aws_dynamodb_table" "catalog_versions" {
  name         = "${local.dynamo_prefix}catalog-versions"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}catalog-versions"
  }
}

# 12. Durable Assistant Importer jobs
resource "aws_dynamodb_table" "import_jobs" {
  name         = "${local.dynamo_prefix}import-jobs"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}import-jobs"
  }
}

# Temporary songbook deltas waiting for periodic S3 catalog compaction.
resource "aws_dynamodb_table" "catalog_changes" {
  name         = "${local.dynamo_prefix}catalog-changes"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "songbook_id"
  range_key    = "song_id"

  attribute {
    name = "songbook_id"
    type = "S"
  }
  attribute {
    name = "song_id"
    type = "S"
  }

  point_in_time_recovery { enabled = true }
}

# 13. Developer Keys (scoped Personal Access Tokens for third-party integrations)
resource "aws_dynamodb_table" "developer_keys" {
  name         = "${local.dynamo_prefix}developer-keys"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "key_hash"

  attribute {
    name = "key_hash"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}developer-keys"
  }
}

# 14. Distributed Rate Limits (Fixed-window token counters with TTL)
resource "aws_dynamodb_table" "rate_limits" {
  name         = "${local.dynamo_prefix}rate-limits"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "key"

  attribute {
    name = "key"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name = "${local.dynamo_prefix}rate-limits"
  }
}

# 15. Idempotent queue mutation results (bounded offline replay records)
resource "aws_dynamodb_table" "idempotency" {
  name         = "${local.dynamo_prefix}idempotency"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "idempotency_key"

  attribute {
    name = "idempotency_key"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}idempotency"
  }
}

# 16. Songbook Collaboration Grants
resource "aws_dynamodb_table" "songbook_grants" {
  name         = "${local.dynamo_prefix}songbook-grants"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "songbook_id"
  range_key    = "user_id"

  attribute {
    name = "songbook_id"
    type = "S"
  }

  attribute {
    name = "user_id"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}songbook-grants"
  }
}

# 17. Songbook Collaboration Invitations
resource "aws_dynamodb_table" "songbook_invitations" {
  name         = "${local.dynamo_prefix}songbook-invitations"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  attribute {
    name = "token_hash"
    type = "S"
  }

  global_secondary_index {
    name            = "token-hash-index"
    hash_key        = "token_hash"
    projection_type = "ALL"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = {
    Name = "${local.dynamo_prefix}songbook-invitations"
  }
}
