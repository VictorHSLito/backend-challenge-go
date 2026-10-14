#!/usr/bin/env bash
# LocalStack init hook: creates the FIFO queues the wager service uses,
# plus a DLQ for each. Runs once at container start (ready.d/*.sh).
#
# Keeping queue creation out of the Go startup is deliberate — infra is
# provisioned by the deploy layer, the app only connects.

set -euo pipefail

AWS_REGION="${AWS_REGION:-us-east-1}"
ENDPOINT_URL="http://localhost:4566"

awslocal() {
  AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION="$AWS_REGION" \
    aws --endpoint-url "$ENDPOINT_URL" "$@"
}

create_fifo() {
  local name="$1"
  local attrs_json="$2"
  echo "creating queue $name"
  awslocal sqs create-queue \
    --queue-name "$name" \
    --attributes "$attrs_json" >/dev/null
}

queue_arn() {
  local name="$1"
  awslocal sqs get-queue-attributes \
    --queue-url "$(awslocal sqs get-queue-url --queue-name "$name" --query 'QueueUrl' --output text)" \
    --attribute-names QueueArn \
    --query 'Attributes.QueueArn' \
    --output text
}

# --- DLQs first (so we can resolve their ARNs for redrive policies) ---

create_fifo "wager-transactions-dlq.fifo" '{
  "FifoQueue": "true",
  "ContentBasedDeduplication": "false",
  "MessageRetentionPeriod": "1209600"
}'

create_fifo "wallet-events-dlq.fifo" '{
  "FifoQueue": "true",
  "ContentBasedDeduplication": "false",
  "MessageRetentionPeriod": "1209600"
}'

TX_DLQ_ARN="$(queue_arn wager-transactions-dlq.fifo)"
EVT_DLQ_ARN="$(queue_arn wallet-events-dlq.fifo)"

# --- Primary queues with redrive to their DLQs ---

create_fifo "wager-transactions.fifo" "$(cat <<EOF
{
  "FifoQueue": "true",
  "ContentBasedDeduplication": "false",
  "VisibilityTimeout": "30",
  "MessageRetentionPeriod": "345600",
  "RedrivePolicy": "{\"deadLetterTargetArn\":\"$TX_DLQ_ARN\",\"maxReceiveCount\":\"5\"}"
}
EOF
)"

create_fifo "wallet-events.fifo" "$(cat <<EOF
{
  "FifoQueue": "true",
  "ContentBasedDeduplication": "false",
  "VisibilityTimeout": "30",
  "MessageRetentionPeriod": "345600",
  "RedrivePolicy": "{\"deadLetterTargetArn\":\"$EVT_DLQ_ARN\",\"maxReceiveCount\":\"5\"}"
}
EOF
)"

echo "SQS queues ready."
