package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thomas-sabu-cs/sentinel-stream/internal/transport" // Use YOUR module name here
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type Metric struct {
	Timestamp int64   `json:"timestamp"`
	CPUUsage  float64 `json:"cpu_usage"`
	MemUsage  float64 `json:"mem_usage"`
}

func main() {
	fmt.Println("🚀 Sentinel Agent starting...")

	// 1. Initialize Redis Client (connecting to our Docker container)
	// In a real app, "localhost:6379" would come from an environment variable
	rdb := transport.NewRedisClient("localhost:6379")
	defer rdb.Close()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Context is used in Go to handle timeouts and cancellations
	ctx := context.Background()

	for {
		select {
		case <-sigChan:
			fmt.Println("\n🛑 Gracefully shutting down...")
			return

		case t := <-ticker.C:
			m, err := collectMetrics()
			if err != nil {
				log.Printf("Error collecting: %v", err)
				continue
			}

			// 2. Publish to Redis
			err = rdb.PublishMetric(ctx, "metrics", m)
			if err != nil {
				log.Printf("Error publishing to Redis: %v", err)
			} else {
				fmt.Printf("[%s] Sent to Redis: CPU: %.2f%% | MEM: %.2f%%\n", t.Format("15:04:05"), m.CPUUsage, m.MemUsage)
			}
		}
	}
}

func collectMetrics() (*Metric, error) {
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}
	vMem, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	return &Metric{
		Timestamp: time.Now().Unix(),
		CPUUsage:  cpuPercent[0],
		MemUsage:  vMem.UsedPercent,
	}, nil
}