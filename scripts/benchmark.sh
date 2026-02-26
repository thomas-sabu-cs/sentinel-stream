#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."

cd "${ROOT_DIR}"

WORKERS="${WORKERS:-32}"
DURATION="${DURATION:-60s}"
REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
# Set BINARY=false for JSON-only runs (e.g. to capture heap-json-optimized profile)
BINARY_FLAG=""
if [ "${BINARY:-true}" = "false" ]; then
  BINARY_FLAG="-binary=false"
  echo ">>> JSON-only mode (BINARY=false): load generator will send JSON, not binary."
fi

echo ">>> Starting Docker services (redis, influxdb, agent, server)..."
docker compose up -d --build

echo ">>> Waiting for services to become ready..."
sleep 15

echo ">>> Running load generator: workers=${WORKERS} duration=${DURATION} redis=${REDIS_ADDR} ${BINARY_FLAG}"
mkdir -p profiles

go run ./cmd/bench/main.go -workers "${WORKERS}" -duration "${DURATION}" -redis "${REDIS_ADDR}" ${BINARY_FLAG} &
BENCH_PID=$!

echo ">>> Collecting 30s CPU profile from pprof while load is running..."
curl -sS "http://localhost:6060/debug/pprof/profile?seconds=30" -o "profiles/cpu-$(date +%Y%m%d-%H%M%S).pb"

echo ">>> Waiting for load generator to finish..."
wait "${BENCH_PID}"

echo ">>> Collecting heap profile from pprof..."
curl -sS "http://localhost:6060/debug/pprof/heap" -o "profiles/heap-$(date +%Y%m%d-%H%M%S).pb"

echo ">>> Internal (core engine) latency — sub-ms P99:"
docker compose logs server --tail=200 | grep -E "INTERNAL_LATENCY_STATS" || echo "No INTERNAL_LATENCY_STATS yet (ensure load ran 60s+)."
echo ">>> E2E latency (producer → consumer complete):"
docker compose logs server --tail=200 | grep -E "E2E_LATENCY_STATS" || echo "No E2E_LATENCY_STATS yet (ensure load ran 60s+)."

echo "✅ Benchmark completed. Profiles in ./profiles. Use INTERNAL_LATENCY_STATS for sub-ms P99, E2E_LATENCY_STATS for full pipeline."

