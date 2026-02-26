# SentinelStream 🚀

A high-performance, distributed real-time system monitoring engine built with Go and Docker.

## 🛠 Tech Stack
- **Language:** Go (Golang)
- **Message Broker:** Redis (Pub/Sub)
- **Database:** InfluxDB (Time-Series)
- **Infrastructure:** Docker & Docker Compose

## 🏗 Architecture
- **Agent:** A lightweight Go service that scrapes system metrics (CPU/RAM) and streams them via Redis.
- **Server:** A concurrent Go consumer that processes the stream and persists data to InfluxDB.
- **Dashboard:** Real-time visualization via InfluxDB dashboards.

## 🌟 Key Engineering Features
- **Graceful Shutdown:** Implemented OS signal handling to ensure zero data loss during service restarts.
- **Concurrency:** Utilized Go routines and channels for non-blocking data processing.
- **Containerization:** Fully orchestrated microservice environment using Docker Compose.
- **Clean Architecture:** Separated concerns into `cmd` (entry points) and `internal` (business logic) packages.
- **Performance Instrumentation:** Integrated `net/http/pprof`, high-speed load generator, and automated benchmark script to capture CPU/heap profiles and end-to-end latency distributions (P50/P90/P99).

## 🚀 How to Run
1. Clone the repo.
2. Run `docker compose up --build`.
3. Access the dashboard at `http://localhost:8086`.

## 📈 Performance Benchmarking & Profiling

To stress-test the ingestion pipeline and capture performance evidence:

1. Ensure Docker Desktop and Go are installed.
2. From the repo root, run the benchmark script (Git Bash / WSL recommended):
   - `bash scripts/benchmark.sh`
   - Or with custom load: `WORKERS=128 DURATION=120s bash scripts/benchmark.sh`
3. The script will:
   - Build and start `redis`, `influxdb`, `agent`, and `server` via Docker Compose.
   - Run a high-speed load generator (`cmd/bench`) that publishes mock CPU/RAM metrics at 5,000+ msgs/sec.
   - Expose pprof on `http://localhost:6060/debug/pprof/` from the server.
   - Collect a 30-second CPU profile and a heap profile into the `profiles/` directory.
   - Log rolling latency stats from the consumer in the form  
     `LATENCY_STATS count=1000 p50_us=XXX p90_us=YYY p99_us=ZZZ` (microseconds).
4. Inspect profiles locally:
   - Build the server binary: `go build -o server ./cmd/server/main.go`
   - CPU profile: `go tool pprof server profiles/cpu-*.pb`
   - Heap profile: `go tool pprof server profiles/heap-*.pb`

These artifacts (latency logs and `.pb` profiles) can be checked into the repo or used as evidence for latency and heap optimization work.

---

## 📊 Performance & Scalability

Benchmarks were run with the high-speed load generator (`cmd/bench`) against the full stack (Redis → consumer → InfluxDB) in Docker. All latency values are in **microseconds** (µs); divide by 1000 for milliseconds.

### Latency Definitions

- **End-to-End (E2E) Latency:** Time from when the producer sends a message (timestamp in payload) until the consumer has finished processing it (JSON decode, Influx point creation, write API). This includes Redis network round-trip and all in-process work.
- **Internal Processing Latency:** Time spent entirely inside the Go consumer after the message is received from Redis (JSON unmarshaling, point construction, write API). E2E minus network/receive overhead is dominated by this internal path.

At high throughput, E2E is in the **multi‑millisecond** range because it includes Redis I/O, JSON decoding, and Influx write buffering. Internal processing is what we optimize (e.g. with object pooling) to reduce GC and CPU on the hot path.

### Benchmark Results (Example Run)

| Metric | Value |
|--------|--------|
| **Throughput** | ~24,600 msgs/sec |
| **Total messages (120s run)** | ~2.95M |
| **E2E P50** | ~4.7 ms |
| **E2E P90** | ~6.4 ms |
| **E2E P99** | ~9.1 ms |
| **Internal P99** | Logged as `LATENCY_STATS INTERNAL` (consumer-only processing time) |

*Environment: `WORKERS=128 DURATION=120s bash scripts/benchmark.sh`; Docker Compose stack on single host.*

### Heap Optimization: Measured Results

The ingestion hot path was optimized using three techniques, with allocation bottlenecks identified via `go tool pprof` (heap profile, `top -alloc_space`):

1. **sync.Pool for `Metric`** — Reuse of structs instead of per-message allocation in the consumer loop and JSON decode path.
2. **Binary protocol (bench path)** — The load generator (`cmd/bench`) sends a 32-byte binary payload by default (`-binary=true`). The server decodes with `encoding/binary`, eliminating JSON unmarshal allocations on the benchmark path. The agent continues to send JSON; the server supports both formats.
3. **Batched Influx writes** — Points are buffered (up to 256), line protocol is built in a pooled `bytes.Buffer`, and one HTTP POST is sent per batch, removing per-point encoder and client allocations.

**Controlled comparison (identical load: 128 workers, 120s):**

| Profile | Total alloc_space | Notes |
|---------|-------------------|--------|
| **Baseline** (`heap-before-pool-20260225.pb`) | **4.60 GB** | No pooling; per-point Influx; JSON only. |
| **Optimized** (`heap-after-optimized-20260225.pb`) | **1.12 GB** | sync.Pool + binary decode + batched Influx. |
| **Reduction** | **~76%** | (4.60 − 1.12) / 4.60 ≈ 75.7%. |

Profiles were captured under the same benchmark conditions; the only variable was the server implementation. Reproduce the diff locally with:

```powershell
go build -o server .\cmd\server\main.go
go tool pprof -top -alloc_space "-base=profiles/heap-before-pool-20260225.pb" server "profiles/heap-after-optimized-20260225.pb"
```

### Proof Artifacts (pprof)

The following files under `profiles/` support the above results:

| File | Purpose |
|------|--------|
| `heap-before-pool-20260225.pb` | Baseline heap (no optimizations). |
| `heap-after-optimized-20260225.pb` | Heap after sync.Pool + binary protocol + batched Influx. |
| `cpu-*.pb` | 30s CPU profile from a representative run. |

Inspect a single profile: `go tool pprof server profiles/<file>.pb`, then `(pprof) top -alloc_space`.
