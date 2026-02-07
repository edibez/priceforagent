package pairs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	PairsKey     = "p4ai:pairs:all"
	PairsHashKey = "p4ai:pairs:hash" // code -> pair data
	SyncInterval = 24 * time.Hour
)

// Pair represents a trading pair
type Pair struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Base  string `json:"base"`
	Quote string `json:"quote"`
}

// PairsResponse from NOBI API
type PairsResponse struct {
	StatusNumber string `json:"status_number"`
	Data         []Pair `json:"data"`
}

// Syncer handles pairs synchronization
type Syncer struct {
	apiURL     string
	apiKey     string
	redis      *redis.Client
	httpClient *http.Client
}

// NewSyncer creates a new pairs syncer
func NewSyncer(apiURL, apiKey string, redisClient *redis.Client) *Syncer {
	return &Syncer{
		apiURL: apiURL,
		apiKey: apiKey,
		redis:  redisClient,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SyncAll fetches all pairs and stores in Redis
func (s *Syncer) SyncAll(ctx context.Context) error {
	log.Println("Starting pairs sync...")
	
	allPairs := []Pair{}
	page := 1
	perPage := 50

	for {
		url := fmt.Sprintf("%s/pairs?page=%d&per_page=%d", s.apiURL, page, perPage)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("X-API-Key", s.apiKey)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("fetch page %d: %w", page, err)
		}
		
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}

		var pairsResp PairsResponse
		if err := json.Unmarshal(body, &pairsResp); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		if len(pairsResp.Data) == 0 {
			break
		}

		allPairs = append(allPairs, pairsResp.Data...)
		log.Printf("Fetched page %d: %d pairs (total: %d)", page, len(pairsResp.Data), len(allPairs))

		if len(pairsResp.Data) < perPage {
			break
		}
		page++
		
		// Rate limit protection
		time.Sleep(100 * time.Millisecond)
	}

	// Store all pairs as JSON list
	pairsJSON, err := json.Marshal(allPairs)
	if err != nil {
		return fmt.Errorf("marshal pairs: %w", err)
	}

	pipe := s.redis.Pipeline()
	
	// Store full list
	pipe.Set(ctx, PairsKey, pairsJSON, 25*time.Hour)
	
	// Store as hash for quick lookup
	pipe.Del(ctx, PairsHashKey)
	for _, p := range allPairs {
		pairJSON, _ := json.Marshal(p)
		pipe.HSet(ctx, PairsHashKey, p.Code, pairJSON)
	}
	pipe.Expire(ctx, PairsHashKey, 25*time.Hour)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("store in redis: %w", err)
	}

	log.Printf("Pairs sync complete: %d pairs stored", len(allPairs))
	return nil
}

// GetAll returns all cached pairs
func (s *Syncer) GetAll(ctx context.Context) ([]Pair, error) {
	data, err := s.redis.Get(ctx, PairsKey).Bytes()
	if err != nil {
		return nil, err
	}

	var pairs []Pair
	if err := json.Unmarshal(data, &pairs); err != nil {
		return nil, err
	}
	return pairs, nil
}

// Exists checks if a pair code exists
func (s *Syncer) Exists(ctx context.Context, code string) bool {
	exists, _ := s.redis.HExists(ctx, PairsHashKey, code).Result()
	return exists
}

// Search finds pairs matching a query
func (s *Syncer) Search(ctx context.Context, query string) ([]Pair, error) {
	pairs, err := s.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	var results []Pair
	for _, p := range pairs {
		if containsIgnoreCase(p.Code, query) || 
		   containsIgnoreCase(p.Name, query) || 
		   containsIgnoreCase(p.Base, query) {
			results = append(results, p)
		}
	}
	return results, nil
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && 
		(s == substr || 
		 len(substr) > 0 && 
		 (s[0:len(substr)] == substr || 
		  containsIgnoreCase(s[1:], substr)))
}

// StartDailySync starts a goroutine that syncs pairs daily
func (s *Syncer) StartDailySync(ctx context.Context) {
	// Sync immediately on start
	go func() {
		if err := s.SyncAll(ctx); err != nil {
			log.Printf("Initial pairs sync failed: %v", err)
		}
	}()

	// Then sync daily
	go func() {
		ticker := time.NewTicker(SyncInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.SyncAll(ctx); err != nil {
					log.Printf("Daily pairs sync failed: %v", err)
				}
			}
		}
	}()
}
