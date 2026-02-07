package price

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DynamicPairsKey    = "p4ai:ws:dynamic_pairs"    // Hash: pair -> last_access_ts
	Top10Key           = "p4ai:ws:top10"            // List of top 10 pairs
	CleanupInterval    = 1 * time.Hour              // Check for stale pairs every hour
	StaleThreshold     = 24 * time.Hour             // Remove if not accessed in 24h
)

// DynamicSubscriber manages dynamic WebSocket subscriptions
type DynamicSubscriber struct {
	ws       *WSClient
	redis    *redis.Client
	mu       sync.RWMutex
	top10    []string
	subscribed map[string]bool
}

// NewDynamicSubscriber creates a new dynamic subscriber
func NewDynamicSubscriber(ws *WSClient, redisClient *redis.Client) *DynamicSubscriber {
	return &DynamicSubscriber{
		ws:         ws,
		redis:      redisClient,
		subscribed: make(map[string]bool),
	}
}

// Start initializes top 10 subscription and cleanup routine
func (d *DynamicSubscriber) Start(ctx context.Context) {
	// Load or set default top 10
	d.refreshTop10(ctx)
	
	// Subscribe to top 10 immediately
	d.subscribeTop10()
	
	// Start cleanup routine
	go d.cleanupRoutine(ctx)
	
	// Daily top 10 refresh
	go d.dailyTop10Refresh(ctx)
}

// OnPairRequested is called when a pair is requested
// Returns true if pair is in WS cache, false if HTTP needed
func (d *DynamicSubscriber) OnPairRequested(ctx context.Context, pairCode string) {
	// Record access time
	now := time.Now().Unix()
	d.redis.HSet(ctx, DynamicPairsKey, pairCode, now)
	
	// Check if already subscribed
	d.mu.RLock()
	alreadySubscribed := d.subscribed[pairCode]
	d.mu.RUnlock()
	
	if !alreadySubscribed {
		// Add to subscription
		d.addSubscription(pairCode)
	}
}

// IsSubscribed checks if a pair is currently subscribed
func (d *DynamicSubscriber) IsSubscribed(pairCode string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.subscribed[pairCode]
}

// addSubscription adds a pair to WS subscription
func (d *DynamicSubscriber) addSubscription(pairCode string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.subscribed[pairCode] {
		return
	}
	
	// Subscribe via WebSocket
	if d.ws != nil && d.ws.conn != nil {
		if err := d.ws.doSubscribe([]string{pairCode}); err != nil {
			log.Printf("Failed to subscribe to %s: %v", pairCode, err)
			return
		}
	}
	
	d.subscribed[pairCode] = true
	log.Printf("Dynamic subscribe: %s (total: %d)", pairCode, len(d.subscribed))
}

// removeSubscription removes a pair from tracking (WS doesn't support unsubscribe easily)
func (d *DynamicSubscriber) removeSubscription(pairCode string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	// Check if it's a top 10 pair - never remove those
	for _, top := range d.top10 {
		if top == pairCode {
			return
		}
	}
	
	delete(d.subscribed, pairCode)
	log.Printf("Removed stale pair: %s", pairCode)
}

// subscribeTop10 subscribes to current top 10
func (d *DynamicSubscriber) subscribeTop10() {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if d.ws == nil {
		return
	}
	
	for _, pair := range d.top10 {
		d.subscribed[pair] = true
	}
	
	if len(d.top10) > 0 {
		d.ws.Subscribe(d.top10)
		log.Printf("Subscribed to top 10: %v", d.top10)
	}
}

// refreshTop10 loads top 10 from Redis or sets defaults
func (d *DynamicSubscriber) refreshTop10(ctx context.Context) {
	// Try to load from Redis
	pairs, err := d.redis.LRange(ctx, Top10Key, 0, 9).Result()
	if err == nil && len(pairs) == 10 {
		d.mu.Lock()
		d.top10 = pairs
		d.mu.Unlock()
		return
	}
	
	// Default top 10 (will be updated by daily CoinGecko sync)
	defaultTop10 := []string{
		"Crypto:ALL:BTC/USDT",
		"Crypto:ALL:ETH/USDT",
		"Crypto:ALL:BNB/USDT",
		"Crypto:ALL:XRP/USDT",
		"Crypto:ALL:SOL/USDT",
		"Crypto:ALL:DOGE/USDT",
		"Crypto:ALL:ADA/USDT",
		"Crypto:ALL:TRX/USDT",
		"Crypto:ALL:AVAX/USDT",
		"Crypto:ALL:SHIB/USDT",
	}
	
	d.mu.Lock()
	d.top10 = defaultTop10
	d.mu.Unlock()
	
	// Store in Redis
	d.redis.Del(ctx, Top10Key)
	for _, p := range defaultTop10 {
		d.redis.RPush(ctx, Top10Key, p)
	}
}

// cleanupRoutine removes stale pairs every hour
func (d *DynamicSubscriber) cleanupRoutine(ctx context.Context) {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.cleanupStalePairs(ctx)
		}
	}
}

// cleanupStalePairs removes pairs not accessed in 24h
func (d *DynamicSubscriber) cleanupStalePairs(ctx context.Context) {
	now := time.Now().Unix()
	threshold := now - int64(StaleThreshold.Seconds())
	
	// Get all dynamic pairs
	pairs, err := d.redis.HGetAll(ctx, DynamicPairsKey).Result()
	if err != nil {
		return
	}
	
	for pair, lastAccessStr := range pairs {
		var lastAccess int64
		fmt.Sscanf(lastAccessStr, "%d", &lastAccess)
		
		if lastAccess < threshold {
			// Remove stale pair
			d.redis.HDel(ctx, DynamicPairsKey, pair)
			d.removeSubscription(pair)
		}
	}
}

// dailyTop10Refresh updates top 10 from CoinGecko daily
func (d *DynamicSubscriber) dailyTop10Refresh(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// This will be called by ranking.CoinGecko.GetTopCoins
			// and update via UpdateTop10()
			log.Println("Daily top 10 refresh triggered")
		}
	}
}

// UpdateTop10 updates the top 10 list (called by ranking sync)
func (d *DynamicSubscriber) UpdateTop10(ctx context.Context, pairs []string) {
	if len(pairs) < 10 {
		return
	}
	
	top10 := pairs[:10]
	
	d.mu.Lock()
	d.top10 = top10
	d.mu.Unlock()
	
	// Store in Redis
	d.redis.Del(ctx, Top10Key)
	for _, p := range top10 {
		d.redis.RPush(ctx, Top10Key, p)
	}
	
	// Resubscribe
	d.subscribeTop10()
	
	log.Printf("Updated top 10: %v", top10)
}

// GetStats returns current subscription stats
func (d *DynamicSubscriber) GetStats() map[string]interface{} {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	return map[string]interface{}{
		"top10_count":    len(d.top10),
		"total_subscribed": len(d.subscribed),
		"top10":          d.top10,
	}
}
