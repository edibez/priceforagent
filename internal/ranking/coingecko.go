package ranking

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// CoinGecko client for market cap rankings
type CoinGecko struct {
	baseURL    string
	cache      []CoinRank
	cacheMu    sync.RWMutex
	cacheTime  time.Time
	cacheTTL   time.Duration
}

// CoinRank represents a coin's ranking info
type CoinRank struct {
	ID                string  `json:"id"`
	Symbol            string  `json:"symbol"`
	Name              string  `json:"name"`
	MarketCapRank     int     `json:"market_cap_rank"`
	MarketCap         float64 `json:"market_cap"`
	PriceChange24h    float64 `json:"price_change_percentage_24h"`
}

// NewCoinGecko creates a new CoinGecko client
func NewCoinGecko() *CoinGecko {
	return &CoinGecko{
		baseURL:  "https://api.coingecko.com/api/v3",
		cacheTTL: 5 * time.Minute,
	}
}

// GetTopCoins returns top N coins by market cap
func (c *CoinGecko) GetTopCoins(limit int) ([]CoinRank, error) {
	// Check cache
	c.cacheMu.RLock()
	if time.Since(c.cacheTime) < c.cacheTTL && len(c.cache) >= limit {
		result := make([]CoinRank, limit)
		copy(result, c.cache[:limit])
		c.cacheMu.RUnlock()
		return result, nil
	}
	c.cacheMu.RUnlock()

	// Fetch from API
	url := fmt.Sprintf("%s/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=100&page=1", c.baseURL)
	
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from CoinGecko: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("CoinGecko returned status %d", resp.StatusCode)
	}

	var coins []CoinRank
	if err := json.NewDecoder(resp.Body).Decode(&coins); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Update cache
	c.cacheMu.Lock()
	c.cache = coins
	c.cacheTime = time.Now()
	c.cacheMu.Unlock()

	if limit > len(coins) {
		limit = len(coins)
	}

	return coins[:limit], nil
}

// GetSymbols returns just the symbols of top N coins
func (c *CoinGecko) GetSymbols(limit int) ([]string, error) {
	coins, err := c.GetTopCoins(limit)
	if err != nil {
		return nil, err
	}

	symbols := make([]string, len(coins))
	for i, coin := range coins {
		symbols[i] = coin.Symbol
	}

	return symbols, nil
}
