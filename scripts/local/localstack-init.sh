#!/usr/bin/env bash

# Provision the AWS-shaped local dependencies used by MicUp. LocalStack runs
# this script after its edge service is ready; every operation is idempotent so
# restarting the container does not destroy local data.
set -euo pipefail

region="${AWS_DEFAULT_REGION:-us-east-2}"
endpoint="http://127.0.0.1:4566"
bucket="${CATALOG_ASSET_BUCKET:-micup-app-local}"
catalog_queue="${CATALOG_QUEUE_NAME:-micup-local-catalog-build}"
catalog_dlq="${CATALOG_DLQ_NAME:-micup-local-catalog-build-dlq}"
import_queue="${IMPORT_QUEUE_NAME:-micup-local-import-jobs}"
import_dlq="${IMPORT_DLQ_NAME:-micup-local-import-jobs-dlq}"
ses_identity="${SES_IDENTITY:-noreply@micup.live}"

aws_local() {
  AWS_PAGER="" aws --endpoint-url "$endpoint" --region "$region" "$@"
}

ensure_bucket() {
  if ! aws_local s3api head-bucket --bucket "$bucket" >/dev/null 2>&1; then
    aws_local s3api create-bucket \
      --bucket "$bucket" \
      --create-bucket-configuration "LocationConstraint=$region" >/dev/null
  fi
  aws_local s3api put-bucket-cors \
    --bucket "$bucket" \
    --cors-configuration '{
      "CORSRules": [{
        "AllowedOrigins": [
          "http://localhost:3000",
          "http://localhost:5173",
          "http://127.0.0.1:3000",
          "http://127.0.0.1:5173"
        ],
        "AllowedMethods": ["GET", "HEAD", "PUT"],
        "AllowedHeaders": ["*"],
        "ExposeHeaders": ["ETag", "Content-Length", "Content-Type", "Content-Disposition"],
        "MaxAgeSeconds": 900
      }]
    }' >/dev/null
}

ensure_queue() {
  local queue_name="$1"
  local dlq_name="$2"
  local dlq_url dlq_arn queue_url redrive_policy

  dlq_url="$(aws_local sqs create-queue \
    --queue-name "$dlq_name" \
    --attributes MessageRetentionPeriod=1209600 \
    --query QueueUrl --output text)"
  dlq_arn="$(aws_local sqs get-queue-attributes \
    --queue-url "$dlq_url" \
    --attribute-names QueueArn \
    --query 'Attributes.QueueArn' --output text)"

  queue_url="$(aws_local sqs create-queue \
    --queue-name "$queue_name" \
    --attributes VisibilityTimeout=900 \
    --query QueueUrl --output text)"
  redrive_policy="{\"deadLetterTargetArn\":\"$dlq_arn\",\"maxReceiveCount\":\"3\"}"
  local attributes_file escaped_redrive_policy
  attributes_file="$(mktemp)"
  escaped_redrive_policy="$(printf '%s' "$redrive_policy" | sed 's/\\/\\\\/g; s/"/\\"/g')"
  printf '{"VisibilityTimeout":"900","RedrivePolicy":"%s"}\n' \
    "$escaped_redrive_policy" >"$attributes_file"
  aws_local sqs set-queue-attributes \
    --queue-url "$queue_url" \
    --attributes "file://$attributes_file" >/dev/null
  rm -f "$attributes_file"
}

ensure_email_identity() {
  # LocalStack Community implements the SES API (the v1 API), while SESv2 is a
  # paid-only emulation feature. The application uses a log sender by default
  # in development, but provisioning a classic identity keeps CLI/manual mail
  # tests local and makes startup independent of SESv2 coverage.
  aws_local ses verify-email-identity --email-address "$ses_identity" >/dev/null
}

echo "[micup-local] provisioning S3 bucket $bucket"
ensure_bucket
echo "[micup-local] provisioning catalog SQS queue and DLQ"
ensure_queue "$catalog_queue" "$catalog_dlq"
echo "[micup-local] provisioning import SQS queue and DLQ"
ensure_queue "$import_queue" "$import_dlq"
echo "[micup-local] provisioning SES identity $ses_identity"
ensure_email_identity
echo "[micup-local] LocalStack AWS-shaped services are ready"
