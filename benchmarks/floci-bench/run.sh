#!/usr/bin/env bash
#
# Run the listing benchmark against either a local floci mock or a real S3
# bucket. Pick the target with TARGET (default: floci).
#
#   # local floci mock (started/stopped automatically; no real network latency)
#   ./benchmarks/floci-bench/run.sh
#   TARGET=floci OBJECTS=300000 LAYOUT=nested RUNS=3 ./benchmarks/floci-bench/run.sh
#
#   # real S3 — bucket must already exist and be empty; data is cleaned up after
#   # (and on Ctrl-C). Credentials come from env/profile/IAM unless ACCESS_KEY set.
#   TARGET=s3 BUCKET=my-empty-bucket REGION=us-east-1 ./benchmarks/floci-bench/run.sh
#
# Other knobs: DO_SEED (default true), RUNS (0 = seed only), CLEANUP,
# OBJ_SIZE, PREFIXES, SEED_WORKERS, CONCURRENCY, ENDPOINT, ACCESS_KEY, SECRET_KEY.
set -euo pipefail

TARGET="${TARGET:-floci}" # floci | s3

# ---- common parameters (env-overridable) ------------------------------------
BUCKET="${BUCKET:-bench}"
REGION="${REGION:-us-east-1}"
OBJECTS="${OBJECTS:-300000}"
OBJ_SIZE="${OBJ_SIZE:-1024}"
LAYOUT="${LAYOUT:-nested}"
PREFIXES="${PREFIXES:-256}"
VERSIONS="${VERSIONS:-30000}"
DELETE_MARKERS="${DELETE_MARKERS:-10000}"
SEED_WORKERS="${SEED_WORKERS:-64}"
CONCURRENCY="${CONCURRENCY:-25}"
RUNS="${RUNS:-3}"
DO_SEED="${DO_SEED:-true}"

# Run from the repo root (two levels up from this script).
cd "$(dirname "$0")/../.."

case "$TARGET" in
floci)
  ENDPOINT="${ENDPOINT:-http://localhost:4566}"
  ACCESS_KEY="${ACCESS_KEY:-test}"
  SECRET_KEY="${SECRET_KEY:-test}"
  CLEANUP="${CLEANUP:-false}" # the container is removed instead of cleaning objects

  PORT="${PORT:-4566}"
  FLOCI_IMAGE="${FLOCI_IMAGE:-hectorvent/floci:latest}"
  FLOCI_NAME="${FLOCI_NAME:-floci-bench}"
  STOP_FLOCI="${STOP_FLOCI:-true}"

  if docker ps --format '{{.Names}}' | grep -qx "$FLOCI_NAME"; then
    echo ">> floci already running (container: $FLOCI_NAME)"
  else
    echo ">> starting floci ($FLOCI_IMAGE) on port $PORT..."
    docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
    docker run -d --name "$FLOCI_NAME" -p "${PORT}:4566" -e FLOCI_HOSTNAME=floci "$FLOCI_IMAGE" >/dev/null
  fi

  # Terminate floci when the benchmark finishes (also on error/interrupt).
  cleanup() {
    if [ "$STOP_FLOCI" = "true" ]; then
      echo ">> stopping floci..."
      docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
    fi
  }
  trap cleanup EXIT

  echo ">> waiting for floci at $ENDPOINT ..."
  ready=false
  for _ in $(seq 1 60); do
    if curl -s -o /dev/null --max-time 2 "$ENDPOINT"; then
      ready=true
      break
    fi
    sleep 1
  done
  if [ "$ready" != "true" ]; then
    echo "!! floci did not become ready in time" >&2
    docker logs --tail 30 "$FLOCI_NAME" >&2 || true
    exit 1
  fi
  echo ">> floci is ready"
  ;;

s3)
  ENDPOINT="${ENDPOINT:-}"       # empty = real AWS (virtual-hosted, path-style off)
  ACCESS_KEY="${ACCESS_KEY:-}"   # empty = default credential chain (env/profile/IAM)
  SECRET_KEY="${SECRET_KEY:-}"
  CLEANUP="${CLEANUP:-true}"     # delete seeded objects from the real bucket afterwards
  echo ">> target: real S3 (endpoint='${ENDPOINT:-AWS}', bucket=$BUCKET, region=$REGION)"
  echo ">> NOTE: the bucket must already exist and be empty; its objects are deleted on exit"
  ;;

*)
  echo "unknown TARGET '$TARGET' (use 'floci' or 's3')" >&2
  exit 2
  ;;
esac

# ---- build args and run a single seed+measure+cleanup process ----------------
# One invocation so cleanup (when enabled) also fires if seeding is interrupted.
args=(
  -endpoint "$ENDPOINT" -bucket "$BUCKET" -region "$REGION"
  -objects "$OBJECTS" -obj-size "$OBJ_SIZE" -layout "$LAYOUT"
  -prefixes "$PREFIXES" -versions "$VERSIONS" -delete-markers "$DELETE_MARKERS" -seed-workers "$SEED_WORKERS"
  -concurrency "$CONCURRENCY" -runs "$RUNS"
)
if [ -n "$ACCESS_KEY" ]; then args+=(-access-key "$ACCESS_KEY"); fi
if [ -n "$SECRET_KEY" ]; then args+=(-secret-key "$SECRET_KEY"); fi
if [ "$DO_SEED" = "true" ]; then args+=(-seed); fi
if [ "$CLEANUP" = "true" ]; then args+=(-cleanup); fi

echo ">> running: go run ./benchmarks/floci-bench ${args[*]}"
go run ./benchmarks/floci-bench "${args[@]}"
