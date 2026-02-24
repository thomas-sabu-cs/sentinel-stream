package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/redis/go-redis/v9"
)

// Metric must match the Agent's structure
type Metric struct {
	Timestamp        int64   `json:"timestamp"`
	CPUUsage         float64 `json:"cpu_usage"`
	MemUsage         float64 `json:"mem_usage"`
	SendTimeUnixNano int64   `json:"send_time_unix_nano"`
}

func main() {
	fmt.Println("📡 Sentinel Server starting...")

	// Start pprof HTTP server in the background.
	go func() {
		log.Println("pprof listening on http://localhost:6060/debug/pprof/")
		if err := http.ListenAndServe(":6060", nil); err != nil {
			log.Printf("pprof server error: %v", err)
		}
	}()

	// 1. Setup Redis Subscriber
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379" // fallback for local dev
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	pubsub := rdb.Subscribe(context.Background(), "metrics")
	defer pubsub.Close()

	// 2. Setup InfluxDB Client (using environment variables)
	influxURL := os.Getenv("INFLUX_URL")
	influxToken := os.Getenv("INFLUX_TOKEN")
	influxOrg := os.Getenv("INFLUX_ORG")
	influxBucket := os.Getenv("INFLUX_BUCKET")

	client := influxdb2.NewClient(influxURL, influxToken)
	writeAPI := client.WriteAPI(influxOrg, influxBucket)
	defer client.Close()

	// Handle Graceful Shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	fmt.Println("Listening for metrics on Redis 'metrics' channel...")

	// This is the "Consumer" loop
	go func() {
		var (
			latencySamples = make([]time.Duration, 0, 1000)
		)

		for {
			msg, err := pubsub.ReceiveMessage(context.Background())
			if err != nil {
				log.Printf("Redis error: %v", err)
				return
			}

			var m Metric
			err = json.Unmarshal([]byte(msg.Payload), &m)
			if err != nil {
				log.Printf("JSON error: %v", err)
				continue
			}

			// 3. Create a "Point" for InfluxDB
			p := influxdb2.NewPointWithMeasurement("system_stats").
				AddField("cpu", m.CPUUsage).
				AddField("mem", m.MemUsage).
				SetTime(javaTime(m.Timestamp))

			// Write asynchronously for high performance
			writeAPI.WritePoint(p)

			// Record end-to-end latency if the producer included a send timestamp.
			if m.SendTimeUnixNano != 0 {
				sendTime := time.Unix(0, m.SendTimeUnixNano)
				latency := time.Since(sendTime)
				latencySamples = append(latencySamples, latency)

				if len(latencySamples) >= 1000 {
					printLatencyStats(latencySamples)
					latencySamples = latencySamples[:0]
				}
			}
		}
	}()

	<-sigChan
	fmt.Println("\n🛑 Server shutting down...")
}

func javaTime(ts int64) time.Time {
	return time.Unix(ts, 0)
}

func printLatencyStats(samples []time.Duration) {
	if len(samples) == 0 {
		return
	}

	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	p50 := percentile(sorted, 0.50)
	p90 := percentile(sorted, 0.90)
	p99 := percentile(sorted, 0.99)

	log.Printf(
		"LATENCY_STATS count=%d p50_us=%d p90_us=%d p99_us=%d",
		len(sorted),
		p50.Microseconds(),
		p90.Microseconds(),
		p99.Microseconds(),
	)
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