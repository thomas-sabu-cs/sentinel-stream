# LEARNINGS — SentinelStream
**tl;dr:** Built a Go-based real-time metrics pipeline (agent → Redis → InfluxDB) and used pprof + a custom load generator to drive it to 25k+ msgs/sec. Identified JSON decoding and per-message allocations as the main bottlenecks; reduced total allocation on the JSON path by ~58% and steady-state heap by ~65% via sync.Pool, batched writes, and jsoniter, while keeping the same JSON wire format.

## 1. What I Attempted

- Build a **real-time system metrics pipeline** in Go: agent collects CPU/RAM, publishes to Redis; server consumes from Redis and writes to InfluxDB. All orchestrated with Docker Compose.
- **Measure and optimize** throughput, latency (P50/P90/P99), and heap under high load using a dedicated load generator (`cmd/bench`) and pprof.
- Provide **reproducible benchmarks** and checked-in pprof artifacts (heap + CPU profiles, latency log lines) to back up performance claims (sub-ms internal P99, ~58% alloc reduction, ~65% inuse reduction on the JSON path).

## 2. Architecture Snapshot

**Agent (`cmd/agent`):**
- Uses `gopsutil` to read CPU percent and virtual memory; builds a `Metric` struct with `Timestamp`, `CPUUsage`, `MemUsage` (no `SendTimeUnixNano`).
- Runs a `select` loop: on a 2s ticker it calls `collectMetrics()`, then `rdb.PublishMetric(ctx, "metrics", m)` which JSON-marshals and publishes to Redis Pub/Sub channel `"metrics"`.
- Graceful shutdown via `sigChan` (OS interrupt/SIGTERM); defers `rdb.Close()`.

**Server (`cmd/server`):**
- Subscribes to Redis channel `"metrics"` with `pubsub.ReceiveMessage(context.Background())` in a single goroutine.
- For each message: if payload is 32 bytes, decodes as binary (timestamp, cpu, mem, sendTimeNano); else unmarshals JSON with **jsoniter** into a **pooled** `Metric` (`sync.Pool`), copies fields out, returns struct to pool.
- Appends to an in-memory batch of `batchPoint`; when batch reaches 256, builds Influx line protocol in a **pooled** `bytes.Buffer` and POSTs to InfluxDB `/api/v2/write` (one HTTP request per batch). No per-message Influx client; no channels for batching — just a slice and a flush threshold.
- Records **internal** latency (time from after `ReceiveMessage` to after point is added to batch) and **E2E** latency (time since `SendTimeUnixNano` in payload). Every 1000 messages logs `INTERNAL_LATENCY_STATS` and `E2E_LATENCY_STATS` with p50/p90/p99 in microseconds.
- Starts a separate goroutine for `net/http/pprof` on `:6060`; main blocks on `<-sigChan` for shutdown. No explicit flush of in-flight batch on shutdown.

**Benchmark (`cmd/bench`):**
- Configurable workers, duration, Redis addr, channel; **`-binary`** flag (default true) to send either 32-byte binary or JSON. Each message includes `SendTimeUnixNano` so the server can compute E2E latency.
- Spawns N worker goroutines that loop until context is cancelled (duration or signal); each iteration builds a metric, publishes via `PublishMetric` (JSON) or `PublishBytes` (binary), increments atomic counter. No batching at the bench level.
- Progress logged every 1s; at the end prints total messages sent. Used by `scripts/benchmark.sh` with env vars `WORKERS`, `DURATION`, `BINARY`.

**Infrastructure:**
- **Docker Compose:** `redis` (port 6379), `influxdb` (8086, init org/bucket), `agent` (build from `cmd/agent/Dockerfile`, host network for CPU), `server` (build from `cmd/server/Dockerfile`, env for Influx + Redis, **port 6060** for pprof).
- **scripts/benchmark.sh:** Brings up compose, waits 15s, runs `go run ./cmd/bench` with optional `BINARY_FLAG` from env `BINARY`, in background. Fetches 30s CPU profile and heap profile from `localhost:6060` into `profiles/` with timestamped filenames. Prints server log lines for `INTERNAL_LATENCY_STATS` and `E2E_LATENCY_STATS`.

## 3. What Broke / Got Tricky

- **Backpressure:** The server has a single consumer goroutine; if InfluxDB or the batch HTTP call slows down, the loop blocks and Redis `ReceiveMessage` blocks. Producers (bench or agent) keep publishing; Redis buffers messages. There is no backpressure signal to the bench or agent, and no bounded buffer in the server — so under heavy load, memory can grow (e.g. Redis client buffer, or Go’s recv buffers) until the consumer catches up. Batch flushing helps amortize Influx cost but doesn’t eliminate the single-threaded bottleneck.

- **JSON vs binary:** The original pipeline used only JSON. Heap profiles showed **JSON unmarshaling** (`encoding/json`, decodeState) as a major allocator. Switching the bench to binary (32 bytes) removed that cost on the benchmark path but would be a “format change” rather than “optimizing JSON.” To keep the same wire format, the server was given **jsoniter** for JSON and **sync.Pool** for `Metric`; the bench can run with `-binary=false` to stress the JSON path and still show a large alloc reduction from pool + batching + jsoniter.

- **Memory allocation pressure:** pprof **alloc_space** (cumulative bytes allocated) showed multi-GB over a 120s run; **inuse_space** (live heap at snapshot) was much smaller (e.g. ~1.5 MB baseline, ~0.5 MB optimized). Per-message allocations (new `Metric`, new point, encoder buffers) added up; pooling and batching reduced both total allocation and live heap. Interpreting both metrics was necessary to state “total allocation reduction” vs “steady-state heap reduction” accurately in the README.

- **Graceful shutdown:** The server exits on `SIGTERM`/interrupt but does not flush the current batch to Influx before exiting — up to 255 points can be lost. The agent and bench use `signal.Notify` and exit cleanly; the server’s consumer goroutine exits when `ReceiveMessage` returns an error (e.g. after connection close). No shared “drain” channel or context cancellation for a coordinated flush.

## 4. What I Learned (Technical Delta)

- How to design a **real-time pipeline** in Go with Redis Pub/Sub and InfluxDB: one consumer goroutine, decode → batch → flush, and how to expose pprof and capture heap/CPU profiles for proof.

- How to read **pprof** output: **alloc_space** = total bytes allocated over the run (cumulative); **inuse_space** = live heap at snapshot. Same `.pb` file; different views. Comparing baseline vs optimized profiles (`-base` or separate runs) gives a clear before/after for both metrics.

- Why **sync.Pool**, **batched writes**, and **jsoniter** (same JSON wire format) reduce allocations: pool reuses structs so fewer GC allocations; batching amortizes per-point encoder and HTTP overhead; jsoniter does fewer allocations per unmarshal than `encoding/json`. All without changing the agent or the on-wire JSON shape.

- The importance of **defining latency precisely**: “Internal” = timer after Redis recv to after point creation (decode + batch append); “E2E” = producer send time to consumer done. Internal stays sub-millisecond; E2E includes Redis and network so it’s higher. Logging both with clear names (`INTERNAL_LATENCY_STATS`, `E2E_LATENCY_STATS`) avoids conflating them in claims.

## 5. Why This Version Is Better

- **Lower allocation and heap:** sync.Pool for `Metric` and for line-protocol buffer, batched Influx writes (256 points per POST), and jsoniter on the JSON path yield ~58% total allocation reduction and ~65% steady-state heap reduction (inuse_space) on the JSON path, with reproducible profiles.

- **Clear performance story:** Two latency metrics (internal vs E2E), two pprof metrics (alloc_space vs inuse_space), and a single benchmark script with JSON-only mode (`BINARY=false`) so the “same format” improvement is measurable and documented.

- **Separation of concerns:** Transport in `internal/transport` (Redis client, PublishMetric/PublishBytes); agent and server in `cmd/`; load generator in `cmd/bench` with flags; benchmark script only orchestrates run + profile capture. pprof and latency logging live in the server where the hot path is.

- **Shutdown and errors:** Signal handling in agent, server, and bench; server logs Redis and JSON errors and continues; Influx write errors are logged (non-2xx status). Not “naive” single-run; still no in-flight batch flush on server exit.

## 6. Next Iteration Plan

- **Horizontal scaling:** Multiple server instances subscribing to the same or sharded Redis channels; or move to Redis Streams / consumer groups so each message is processed once. Would require a decision on ordering and partitioning (e.g. by host or topic).

- **Alerting / anomaly detection:** Use InfluxDB (or a separate reader) to query the stored series and trigger alerts or simple anomaly detection (e.g. threshold or rate of change). Out of scope for the current “ingest and store” pipeline but a natural next use of the time-series data.

- **One-command reproducible demo:** Package `scripts/benchmark.sh`, required env vars, and optional `profiles/` into a short “run this to reproduce the numbers” section; consider a small script or Make target that runs the benchmark and prints only the Key Results table (throughput, internal P99, E2E P99, alloc/inuse deltas) so an interviewer can run one command and see the same table as the README.
