#!/usr/bin/env bash
#
# End-to-end run of the floci listing benchmark:
#   1. start floci (S3-compatible mock) if not already running
#   2. wait until it accepts connections
#   3. seed the bucket with objects (once)
#   4. measure controllers.S3UsageInfo (the exporter's listing path)
#
# All parameters are overridable via environment variables, e.g.:
#   OBJECTS=300000 OBJ_SIZE=1024 LAYOUT=nested CONCURRENCY=25 RUNS=3 ./benchmarks/floci-bench/run.sh
#
# floci is started fresh and terminated when the benchmark finishes by default.
#
# Toggle stages:
#   DO_SEED=false ./benchmarks/floci-bench/run.sh      # skip seeding (data already present)
#   DO_MEASURE=false ./benchmarks/floci-bench/run.sh   # seed only
#   STOP_FLOCI=false ./benchmarks/floci-bench/run.sh   # keep floci running after the benchmark
set -euo pipefail

# ---- configuration (env-overridable) ----------------------------------------
ENDPOINT="${ENDPOINT:-http://localhost:4566}"
PORT="${PORT:-4566}"
BUCKET="${BUCKET:-bench}"
REGION="${REGION:-us-east-1}"

OBJECTS="${OBJECTS:-300000}"
OBJ_SIZE="${OBJ_SIZE:-1024}"
LAYOUT="${LAYOUT:-nested}"
PREFIXES="${PREFIXES:-256}"
SEED_WORKERS="${SEED_WORKERS:-64}"

CONCURRENCY="${CONCURRENCY:-25}"
RUNS="${RUNS:-3}"

DO_SEED="${DO_SEED:-true}"
DO_MEASURE="${DO_MEASURE:-true}"
STOP_FLOCI="${STOP_FLOCI:-true}"

FLOCI_IMAGE="${FLOCI_IMAGE:-hectorvent/floci:latest}"
FLOCI_NAME="${FLOCI_NAME:-floci-bench}"

# Run from the repo root (two levels up from this script).
cd "$(dirname "$0")/../.."

# ---- 1. start floci ----------------------------------------------------------
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

# ---- 2. wait for readiness ---------------------------------------------------
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

# ---- 3. seed -----------------------------------------------------------------
if [ "$DO_SEED" = "true" ]; then
  echo ">> seeding $OBJECTS objects (${OBJ_SIZE}B, layout=$LAYOUT)..."
  go run ./benchmarks/floci-bench \
    -endpoint "$ENDPOINT" -bucket "$BUCKET" -region "$REGION" \
    -seed -objects "$OBJECTS" -obj-size "$OBJ_SIZE" \
    -layout "$LAYOUT" -prefixes "$PREFIXES" -seed-workers "$SEED_WORKERS" \
    -runs 0
fi

# ---- 4. measure --------------------------------------------------------------
if [ "$DO_MEASURE" = "true" ]; then
  echo ">> measuring (concurrency=$CONCURRENCY, runs=$RUNS)..."
  go run ./benchmarks/floci-bench \
    -endpoint "$ENDPOINT" -bucket "$BUCKET" -region "$REGION" \
    -concurrency "$CONCURRENCY" -runs "$RUNS"
fi
