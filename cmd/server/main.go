package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/redis/go-redis/v9"
)

// Metric must match the Agent's structure
type Metric struct {
	Timestamp        int64   `json:"timestamp"`
	CPUUsage         float64 `json:"cpu_usage"`
	MemUsage         float64 `json:"mem_usage"`
	SendTimeUnixNano int64   `json:"send_time_unix_nano"`
}

const influxBatchSize = 256

type batchPoint struct {
	ts  int64
	cpu float64
	mem float64
}

var (
	metricPool = sync.Pool{
		New: func() interface{} { return &Metric{} },
	}
	bufferPool = sync.Pool{
		New: func() interface{} { return &bytes.Buffer{} },
	}
)

func main() {
	fmt.Println("📡 Sentinel Server starting...")

	go func() {
		log.Println("pprof listening on http://localhost:6060/debug/pprof/")
		if err := http.ListenAndServe(":6060", nil); err != nil {
			log.Printf("pprof server error: %v", err)
		}
	}()

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	pubsub := rdb.Subscribe(context.Background(), "metrics")
	defer pubsub.Close()

	influxURL := os.Getenv("INFLUX_URL")
	influxToken := os.Getenv("INFLUX_TOKEN")
	influxOrg := os.Getenv("INFLUX_ORG")
	influxBucket := os.Getenv("INFLUX_BUCKET")
	writeURL := influxURL + "/api/v2/write?org=" + url.QueryEscape(influxOrg) + "&bucket=" + url.QueryEscape(influxBucket)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	fmt.Println("Listening for metrics on Redis 'metrics' channel...")

	go func() {
		var (
			latencySamples  = make([]time.Duration, 0, 1000)
			internalSamples = make([]time.Duration, 0, 1000)
			batch           = make([]batchPoint, 0, influxBatchSize)
		)

		for {
			msg, err := pubsub.ReceiveMessage(context.Background())
			if err != nil {
				log.Printf("Redis error: %v", err)
				return
			}

			recvAt := time.Now()
			payload := []byte(msg.Payload)

			var ts int64
			var cpuUsage, memUsage float64
			var sendTimeNano int64

			if len(payload) == 32 {
				ts = int64(binary.LittleEndian.Uint64(payload[0:8]))
				cpuUsage = math.Float64frombits(binary.LittleEndian.Uint64(payload[8:16]))
				memUsage = math.Float64frombits(binary.LittleEndian.Uint64(payload[16:24]))
				sendTimeNano = int64(binary.LittleEndian.Uint64(payload[24:32]))
			} else {
				m := metricPool.Get().(*Metric)
				*m = Metric{}
				err = jsoniter.Unmarshal(payload, m)
				if err != nil {
					metricPool.Put(m)
					log.Printf("JSON error: %v", err)
					continue
				}
				ts, cpuUsage, memUsage, sendTimeNano = m.Timestamp, m.CPUUsage, m.MemUsage, m.SendTimeUnixNano
				metricPool.Put(m)
			}

			batch = append(batch, batchPoint{ts: ts, cpu: cpuUsage, mem: memUsage})
			internalDuration := time.Since(recvAt) // Core engine: Redis recv → point created (batch entry)
			internalSamples = append(internalSamples, internalDuration)

			if len(batch) >= influxBatchSize {
				flushInfluxBatch(writeURL, influxToken, batch)
				batch = batch[:0]
			}

			if sendTimeNano != 0 {
				latencySamples = append(latencySamples, time.Since(time.Unix(0, sendTimeNano)))
			}
			if len(internalSamples) >= 1000 {
				printLatencyStats("E2E", latencySamples)
				printLatencyStats("INTERNAL", internalSamples)
				latencySamples = latencySamples[:0]
				internalSamples = internalSamples[:0]
			}
		}
	}()

	<-sigChan
	fmt.Println("\n🛑 Server shutting down...")
}

func flushInfluxBatch(writeURL, token string, batch []batchPoint) {
	if len(batch) == 0 {
		return
	}
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	for _, p := range batch {
		tsNano := p.ts * 1e9
		_, _ = fmt.Fprintf(buf, "system_stats cpu=%f,mem=%f %d\n", p.cpu, p.mem, tsNano)
	}
	body := buf.Bytes()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, writeURL, bytes.NewReader(body))
	if err != nil {
		bufferPool.Put(buf)
		log.Printf("Influx batch request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Content-Type", "application/vnd.influxdb.lineprotocol")
	resp, err := http.DefaultClient.Do(req)
	bufferPool.Put(buf)
	if err != nil {
		log.Printf("Influx batch write: %v", err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		log.Printf("Influx batch write status: %d", resp.StatusCode)
	}
}

func printLatencyStats(label string, samples []time.Duration) {
	if len(samples) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 := percentile(sorted, 0.50)
	p90 := percentile(sorted, 0.90)
	p99 := percentile(sorted, 0.99)
	prefix := "E2E_LATENCY_STATS"
	if label == "INTERNAL" {
		prefix = "INTERNAL_LATENCY_STATS"
	}
	log.Printf("%s count=%d p50_us=%d p90_us=%d p99_us=%d",
		prefix, len(sorted), p50.Microseconds(), p90.Microseconds(), p99.Microseconds())
}

func percentile(durations []time.Duration, p float64) time.Duration {
	n := len(durations)
	if n == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(n))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= n {
		rank = n - 1
	}
	return durations[rank]
}
