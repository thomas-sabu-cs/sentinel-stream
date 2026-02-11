package main

import (
	"time"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

// Metric must match the Agent's structure
type Metric struct {
	Timestamp int64   `json:"timestamp"`
	CPUUsage  float64 `json:"cpu_usage"`
	MemUsage  float64 `json:"mem_usage"`
}

func main() {
	fmt.Println("📡 Sentinel Server starting...")

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
			log.Printf("INFO: Metric stored successfully - CPU: %.2f%%, MEM: %.2f%%", m.CPUUsage, m.MemUsage)
		}
	}()

	<-sigChan
	fmt.Println("\n🛑 Server shutting down...")
}

func javaTime(ts int64) time.Time {
	return time.Unix(ts, 0)
}