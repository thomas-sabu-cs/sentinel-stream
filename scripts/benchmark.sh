#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."

cd "${ROOT_DIR}"

WORKERS="${WORKERS:-32}"
DURATION="${DURATION:-60s}"
REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"

echo ">>> Starting Docker services (redis, influxdb, agent, server)..."
docker compose up -d --build

echo ">>> Waiting for services to become ready..."
sleep 15

echo ">>> Running load generator: workers=${WORKERS} duration=${DURATION} redis=${REDIS_ADDR}"
mkdir -p profiles

go run ./cmd/bench/main.go -workers "${WORKERS}" -duration "${DURATION}" -redis "${REDIS_ADDR}" &
BENCH_PID=$!

echo ">>> Collecting 30s CPU profile from pprof while load is running..."
curl -sS "http://localhost:6060/debug/pprof/profile?seconds=30" -o "profiles/cpu-$(date +%Y%m%d-%H%M%S).pb"

echo ">>> Waiting for load generator to finish..."
wait "${BENCH_PID}"

echo ">>> Collecting heap profile from pprof..."
curl -sS "http://localhost:6060/debug/pprof/heap" -o "profiles/heap-$(date +%Y%m%d-%H%M%S).pb"

echo ">>> Recent latency stats from server logs:"
if ! docker compose logs server --tail=200 | grep -E "LATENCY_STATS"; then
  echo "No LATENCY_STATS lines found yet. Ensure the benchmark ran long enough and load was high."
fi

echo "✅ Benchmark completed. Profiles are in the ./profiles directory."

