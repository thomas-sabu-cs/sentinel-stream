package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/thomas-sabu-cs/sentinel-stream/internal/transport"
)

type Metric struct {
	Timestamp        int64   `json:"timestamp"`
	CPUUsage         float64 `json:"cpu_usage"`
	MemUsage         float64 `json:"mem_usage"`
	SendTimeUnixNano int64   `json:"send_time_unix_nano"`
}

func main() {
	var (
		workers  = flag.Int("workers", 32, "number of concurrent publisher goroutines")
		duration = flag.Duration("duration", 60*time.Second, "how long to run the benchmark")
		redis    = flag.String("redis", "localhost:6379", "Redis address")
		channel  = flag.String("channel", "metrics", "Redis Pub/Sub channel")
	)
	flag.Parse()

	log.Printf("Starting load generator with %d workers for %s...\n", *workers, duration.String())

	rdb := transport.NewRedisClient(*redis)
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Signal received, stopping load generator...")
		cancel()
	}()

	var (
		wg        sync.WaitGroup
		totalSent uint64
	)

	rand.Seed(time.Now().UnixNano())

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					now := time.Now()
					m := &Metric{
						Timestamp:        now.Unix(),
						CPUUsage:         20 + 60*rand.Float64(),
						MemUsage:         10 + 70*rand.Float64(),
						SendTimeUnixNano: now.UnixNano(),
					}

					if err := rdb.PublishMetric(context.Background(), *channel, m); err != nil {
						log.Printf("worker=%d publish error: %v", id, err)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					atomic.AddUint64(&totalSent, 1)
				}
			}
		}(i)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			sent := atomic.LoadUint64(&totalSent)
			log.Printf("Progress: total_sent=%d", sent)
		}
	}

	wg.Wait()
	sent := atomic.LoadUint64(&totalSent)
	fmt.Printf("✅ Load generator finished. Total messages sent: %d\n", sent)
}

