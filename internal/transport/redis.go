package transport

import (
	"context"
	"encoding/json"
	"github.com/redis/go-redis/v9"
)

// RedisClient wraps the official redis client to add our custom logic
type RedisClient struct {
	client *redis.Client
}

// NewRedisClient initializes a connection to the Docker container
func NewRedisClient(addr string) *RedisClient {
	rdb := redis.NewClient(&redis.Options{
		Addr: addr, // Usually "localhost:6379"
	})
	return &RedisClient{client: rdb}
}

// PublishMetric converts our struct to JSON and sends it to a Redis channel
func (r *RedisClient) PublishMetric(ctx context.Context, channel string, data interface{}) error {
	// Convert the Go struct to a JSON string
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	// Publish to Redis
	return r.client.Publish(ctx, channel, payload).Err()
}

// Close cleans up the connection
func (r *RedisClient) Close() error {
	return r.client.Close()
}