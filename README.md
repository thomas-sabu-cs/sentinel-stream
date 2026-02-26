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
   - Custom load: `WORKERS=128 DURATION=120s bash scripts/benchmark.sh`
   - **JSON-only** (for heap-json-optimized profile): `BINARY=false WORKERS=128 DURATION=120s bash scripts/benchmark.sh`
3. The script will:
   - Build and start `redis`, `influxdb`, `agent`, and `server` via Docker Compose.
   - Run a high-speed load generator (`cmd/bench`) that publishes mock CPU/RAM metrics at 5,000+ msgs/sec.
   - Expose pprof on `http://localhost:6060/debug/pprof/` from the server.
   - Collect a 30-second CPU profile and a heap profile into the `profiles/` directory.
   - Print **Internal** (core engine) and **E2E** latency from the consumer:  
     `INTERNAL_LATENCY_STATS` and `E2E_LATENCY_STATS` with `p50_us`, `p90_us`, `p99_us` (microseconds).
4. Inspect profiles locally:
   - Build the server binary: `go build -o server ./cmd/server/main.go`
   - CPU profile: `go tool pprof server profiles/cpu-*.pb`
   - Heap profile: `go tool pprof server profiles/heap-*.pb`

These artifacts (latency logs and `.pb` profiles) can be checked into the repo or used as evidence for latency and heap optimization work.

---

## 📊 Performance & Scalability

Benchmarks use the high-speed load generator (`cmd/bench`) against the full stack (Redis → consumer → InfluxDB) in Docker. Two latency metrics are reported so resume claims are reproducible:

- **Internal (Core Engine) Latency:** Timer starts *after* the message is received from Redis and stops *after* the InfluxDB point is created (decode + point construction). This is the **sub-millisecond** path we optimize.
- **End-to-End (E2E) Latency:** Producer send timestamp to consumer processing complete (includes Redis round-trip and all in-process work). Typically ~6–9 ms at 25k+ msgs/sec over Docker/Redis.

### Key Results

| Metric | Target / Observed |
|--------|-------------------|
| **Throughput** | 25k+ msgs/sec |
| **Internal P99 (core engine)** | **&lt;1 ms** (sub-millisecond) |
| **E2E P99** | ~6–9 ms |
| **Total allocated bytes (pprof alloc_space) reduction, JSON path** | **~58%** (4.60 GB → 1.94 GB; sync.Pool + batched Influx + jsoniter) |

*Run: `WORKERS=128 DURATION=120s`; server logs `INTERNAL_LATENCY_STATS` and `E2E_LATENCY_STATS` every 1000 messages. Use `scripts/benchmark.sh` or the PowerShell flow in the benchmarking section to reproduce.*

**Example server log lines (every 1000 messages):**

```
INTERNAL_LATENCY_STATS count=1000 p50_us=120 p90_us=380 p99_us=890
E2E_LATENCY_STATS count=1000 p50_us=4200 p90_us=6200 p99_us=9100
```

*(Values in microseconds; divide by 1000 for milliseconds. Internal P99 &lt; 1000 µs = sub-millisecond.)*

### Technical Deep Dive

**Allocation bottleneck:** `go tool pprof` (heap profile, `top -alloc_space`) identified **JSON unmarshaling** and per-message Influx point construction as the primary allocators on the ingestion hot path.

**Architectural fix (same JSON format):** (1) **`sync.Pool`** for the `Metric` struct to reuse decode targets. (2) **Batched Influx writes** — line protocol in a pooled buffer, one HTTP POST per 256 points — to remove per-point encoder allocations. (3) **jsoniter** for JSON unmarshaling (drop-in, fewer allocs than `encoding/json`) so the wire format stays JSON. Together these give a **~58% reduction in total allocated bytes (pprof alloc_space)** on the JSON path (4.60 GB → 1.94 GB under identical load); no format change — same JSON wire format.

**Measured (JSON-only, 128 workers, 120s):** Baseline `heap-before-pool-20260225.pb` = 4.60 GB **alloc_space**; optimized (sync.Pool + batched Influx + jsoniter) with `-binary=false` = 1.94 GB → **(4.60 − 1.94) / 4.60 ≈ 58%**. All figures above are **pprof alloc_space** (total bytes allocated over the run). For **steady-state live heap** at snapshot time, use the same profile files with `-inuse_space`:

```powershell
go tool pprof -top -inuse_space server profiles/heap-before-pool-20260225.pb
go tool pprof -top -inuse_space server profiles/heap-json-optimized-20260225.pb
```

**Reproducibility — JSON path (alloc_space):**

```powershell
go build -o server .\cmd\server\main.go
go tool pprof -top -alloc_space "-base=profiles/heap-before-pool-20260225.pb" server "profiles/heap-json-optimized-20260225.pb"
```

### Proof Artifacts

| File (in `profiles/`) | Purpose |
|------------------------|--------|
| `heap-before-pool-20260225.pb` | Baseline heap (no pooling, per-point Influx, stdlib JSON). |
| `heap-json-optimized-20260225.pb` | Heap after sync.Pool + batched Influx + jsoniter (**JSON only**, bench run with `-binary=false`). → ~58% alloc_space reduction vs baseline. |
| `heap-after-optimized-20260225.pb` | Heap with binary protocol (bench default). |
| `cpu-20260225-211922.pb` (or other `cpu-*.pb`) | 30s CPU profile. |

Server logs: `INTERNAL_LATENCY_STATS` (core engine, sub-ms P99) and `E2E_LATENCY_STATS` (full pipeline). Grep these after a benchmark run to verify the table above.
