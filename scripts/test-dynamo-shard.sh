#!/usr/bin/env bash
set -euo pipefail

# Runs either:
#   scripts/test-dynamo-shard.sh packages
#       All Go packages except ./internal/store/dynamo, backed by an ephemeral
#       in-memory DynamoDB Local container (so ./internal/app integration tests
#       run fast without contending with the persistent dev volume).
#   scripts/test-dynamo-shard.sh <shard_index> <total_shards>
#       A deterministic modulo shard (1-based) of all top-level tests in
#       ./internal/store/dynamo, backed by its own ephemeral in-memory
#       DynamoDB Local container.
#
# Set DYNAMO_USE_EXISTING=1 to reuse the caller's DYNAMO_ENDPOINT instead of
# launching an ephemeral in-memory DynamoDB Local container.

MODE="${1:-}"
if [ -z "$MODE" ]; then
  echo "usage: $0 packages | <shard_index> <total_shards>" >&2
  exit 2
fi

CID=""
cleanup() {
  if [ -n "$CID" ]; then
    docker rm -f "$CID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

if [ "${DYNAMO_USE_EXISTING:-0}" != "1" ] && command -v docker >/dev/null 2>&1; then
  CID=$(docker run -d --rm -p 127.0.0.1:0:8000 amazon/dynamodb-local:2.6.1 \
    -jar DynamoDBLocal.jar -sharedDb -inMemory)
  HOST_PORT=$(docker port "$CID" 8000/tcp | head -n1 | awk -F: '{print $NF}')
  export DYNAMO_ENDPOINT="http://127.0.0.1:${HOST_PORT}"

  for _ in $(seq 1 60); do
    if curl -sS "$DYNAMO_ENDPOINT" >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
  if ! curl -sS "$DYNAMO_ENDPOINT" >/dev/null 2>&1; then
    echo "ephemeral DynamoDB Local did not become ready on $DYNAMO_ENDPOINT" >&2
    exit 1
  fi
fi

if [ "$MODE" = "packages" ]; then
  PKGS=$(go list ./... | grep -v '/internal/store/dynamo$')
  # shellcheck disable=SC2086
  go test -count=1 -timeout=10m $PKGS
  exit $?
fi

SHARD_INDEX="$MODE"
TOTAL_SHARDS="${2:-}"
if ! [[ "$SHARD_INDEX" =~ ^[0-9]+$ ]] || ! [[ "$TOTAL_SHARDS" =~ ^[0-9]+$ ]] || [ "$SHARD_INDEX" -lt 1 ] || [ "$SHARD_INDEX" -gt "$TOTAL_SHARDS" ]; then
  echo "invalid shard arguments: index='$SHARD_INDEX' total='$TOTAL_SHARDS'" >&2
  exit 2
fi

mapfile -t ALL_TESTS < <(go test -list '^Test' ./internal/store/dynamo | grep '^Test' | sort)
if [ "${#ALL_TESTS[@]}" -eq 0 ]; then
  echo "no tests discovered in ./internal/store/dynamo" >&2
  exit 1
fi

SHARD_TESTS=()
for i in "${!ALL_TESTS[@]}"; do
  if [ $(( (i % TOTAL_SHARDS) + 1 )) -eq "$SHARD_INDEX" ]; then
    SHARD_TESTS+=("${ALL_TESTS[$i]}")
  fi
done

if [ "${#SHARD_TESTS[@]}" -eq 0 ]; then
  echo "shard ${SHARD_INDEX}/${TOTAL_SHARDS}: 0 tests"
  exit 0
fi

REGEX="^($(IFS='|'; echo "${SHARD_TESTS[*]}"))$"
echo "shard ${SHARD_INDEX}/${TOTAL_SHARDS}: running ${#SHARD_TESTS[@]} tests against ${DYNAMO_ENDPOINT:-<unset>}"
go test -count=1 -timeout=10m -run "$REGEX" ./internal/store/dynamo
