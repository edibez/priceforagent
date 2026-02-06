package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter handles rate limiting using Redis
type Limiter struct {
	client *redis.Client
	limit  int
	window time.Duration
}

// NewLimiter creates a new rate limiter
func NewLimiter(redisAddr string, requestsPerSecond int) (*Limiter, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "",
		DB:       0,
	})

	// Test connection
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	return &Limiter{
		client: client,
		limit:  requestsPerSecond,
		window: time.Second,
	}, nil
}

// Allow checks if a request is allowed for the given key
func (l *Limiter) Allow(ctx context.Context, key string) (bool, int, error) {
	now := time.Now().Unix()
	redisKey := fmt.Sprintf("ratelimit:%s:%d", key, now)

	// Increment counter
	count, err := l.client.Incr(ctx, redisKey).Result()
	if err != nil {
		return false, 0, err
	}

	// Set expiry on first request
	if count == 1 {
		l.client.Expire(ctx, redisKey, l.window*2)
	}

	remaining := l.limit - int(count)
	if remaining < 0 {
		remaining = 0
	}

	return count <= int64(l.limit), remaining, nil
}

// IncrementUsage increments the usage counter for tracking (separate from rate limit)
func (l *Limiter) IncrementUsage(ctx context.Context, key string) (int64, error) {
	usageKey := fmt.Sprintf("usage:%s", key)
	return l.client.Incr(ctx, usageKey).Result()
}

// GetUsage returns the total usage count
func (l *Limiter) GetUsage(ctx context.Context, key string) (int64, error) {
	usageKey := fmt.Sprintf("usage:%s", key)
	count, err := l.client.Get(ctx, usageKey).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

// Close closes the Redis connection
func (l *Limiter) Close() error {
	return l.client.Close()
}
