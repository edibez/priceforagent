package ratelimit

import (
	"context"
	"fmt"
	"strconv"
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

// IncrementUsage increments the usage counter for tracking
func (l *Limiter) IncrementUsage(ctx context.Context, key string) error {
	now := time.Now()
	
	// Total usage
	l.client.Incr(ctx, fmt.Sprintf("usage:total:%s", key))
	
	// Daily usage (YYYYMMDD)
	dayKey := fmt.Sprintf("usage:daily:%s:%s", key, now.Format("20060102"))
	l.client.Incr(ctx, dayKey)
	l.client.Expire(ctx, dayKey, 8*24*time.Hour) // Keep 8 days
	
	// Hourly usage for last 24h tracking
	hourKey := fmt.Sprintf("usage:hourly:%s:%s", key, now.Format("2006010215"))
	l.client.Incr(ctx, hourKey)
	l.client.Expire(ctx, hourKey, 25*time.Hour)
	
	return nil
}

// GetUsage returns the total usage count
func (l *Limiter) GetUsage(ctx context.Context, key string) (int64, error) {
	count, err := l.client.Get(ctx, fmt.Sprintf("usage:total:%s", key)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

// GetDailyUsage returns usage for a specific day
func (l *Limiter) GetDailyUsage(ctx context.Context, key string, date time.Time) (int64, error) {
	dayKey := fmt.Sprintf("usage:daily:%s:%s", key, date.Format("20060102"))
	count, err := l.client.Get(ctx, dayKey).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

// GetLast24HoursUsage returns usage for last 24 hours
func (l *Limiter) GetLast24HoursUsage(ctx context.Context, key string) (int64, error) {
	var total int64
	now := time.Now()
	
	for i := 0; i < 24; i++ {
		t := now.Add(-time.Duration(i) * time.Hour)
		hourKey := fmt.Sprintf("usage:hourly:%s:%s", key, t.Format("2006010215"))
		count, err := l.client.Get(ctx, hourKey).Int64()
		if err == nil {
			total += count
		}
	}
	
	return total, nil
}

// GetLast7DaysUsage returns usage for last 7 days
func (l *Limiter) GetLast7DaysUsage(ctx context.Context, key string) (int64, error) {
	var total int64
	now := time.Now()
	
	for i := 0; i < 7; i++ {
		t := now.AddDate(0, 0, -i)
		dayKey := fmt.Sprintf("usage:daily:%s:%s", key, t.Format("20060102"))
		count, err := l.client.Get(ctx, dayKey).Int64()
		if err == nil {
			total += count
		}
	}
	
	return total, nil
}

// UsageStats for a single API key
type UsageStats struct {
	APIKey     string `json:"api_key"`
	Total      int64  `json:"total"`
	Last24h    int64  `json:"last_24h"`
	Last7Days  int64  `json:"last_7_days"`
}

// GetAllUsageStats returns stats for all keys matching pattern
func (l *Limiter) GetAllUsageStats(ctx context.Context, keys []string) ([]UsageStats, error) {
	var stats []UsageStats
	
	for _, key := range keys {
		total, _ := l.GetUsage(ctx, key)
		last24h, _ := l.GetLast24HoursUsage(ctx, key)
		last7d, _ := l.GetLast7DaysUsage(ctx, key)
		
		stats = append(stats, UsageStats{
			APIKey:    key[:12] + "...",
			Total:     total,
			Last24h:   last24h,
			Last7Days: last7d,
		})
	}
	
	return stats, nil
}

// GetGlobalStats returns overall stats
func (l *Limiter) GetGlobalStats(ctx context.Context) (map[string]int64, error) {
	stats := make(map[string]int64)
	
	// Count total keys
	keys, err := l.client.Keys(ctx, "usage:total:*").Result()
	if err == nil {
		stats["total_api_keys"] = int64(len(keys))
	}
	
	// Sum all usage
	var totalHits int64
	for _, k := range keys {
		count, _ := l.client.Get(ctx, k).Int64()
		totalHits += count
	}
	stats["total_hits"] = totalHits
	
	// Today's hits
	today := time.Now().Format("20060102")
	dailyKeys, _ := l.client.Keys(ctx, fmt.Sprintf("usage:daily:*:%s", today)).Result()
	var todayHits int64
	for _, k := range dailyKeys {
		count, _ := l.client.Get(ctx, k).Int64()
		todayHits += count
	}
	stats["today_hits"] = todayHits
	
	return stats, nil
}

// DailyBreakdown for charting
type DailyBreakdown struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// GetDailyBreakdown returns daily stats for last N days
func (l *Limiter) GetDailyBreakdown(ctx context.Context, key string, days int) ([]DailyBreakdown, error) {
	var breakdown []DailyBreakdown
	now := time.Now()
	
	for i := days - 1; i >= 0; i-- {
		t := now.AddDate(0, 0, -i)
		dayKey := fmt.Sprintf("usage:daily:%s:%s", key, t.Format("20060102"))
		count, _ := l.client.Get(ctx, dayKey).Int64()
		
		breakdown = append(breakdown, DailyBreakdown{
			Date:  t.Format("2006-01-02"),
			Count: count,
		})
	}
	
	return breakdown, nil
}

// GetGlobalDailyBreakdown returns overall daily stats
func (l *Limiter) GetGlobalDailyBreakdown(ctx context.Context, days int) ([]DailyBreakdown, error) {
	var breakdown []DailyBreakdown
	now := time.Now()
	
	for i := days - 1; i >= 0; i-- {
		t := now.AddDate(0, 0, -i)
		dayStr := t.Format("20060102")
		
		// Sum all keys for this day
		dailyKeys, _ := l.client.Keys(ctx, fmt.Sprintf("usage:daily:*:%s", dayStr)).Result()
		var dayTotal int64
		for _, k := range dailyKeys {
			count, _ := l.client.Get(ctx, k).Int64()
			dayTotal += count
		}
		
		breakdown = append(breakdown, DailyBreakdown{
			Date:  t.Format("2006-01-02"),
			Count: dayTotal,
		})
	}
	
	return breakdown, nil
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// Close closes the Redis connection
func (l *Limiter) Close() error {
	return l.client.Close()
}
