package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/edibez/priceforagent/internal/ai"
	"github.com/edibez/priceforagent/internal/auth"
	"github.com/edibez/priceforagent/internal/pairs"
	"github.com/edibez/priceforagent/internal/price"
	"github.com/edibez/priceforagent/internal/ranking"
	"github.com/edibez/priceforagent/internal/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

var (
	priceClient       *price.Client
	wsClient          *price.WSClient
	dynamicSubscriber *price.DynamicSubscriber
	authStore         *auth.Store
	rateLimiter       *ratelimit.Limiter
	rankingClient     *ranking.CoinGecko
	pairsSyncer       *pairs.Syncer
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	sourceURL := os.Getenv("SOURCE_URL")
	if sourceURL == "" {
		sourceURL = "https://api.price.usenobi.com/v1"
	}

	apiKey := os.Getenv("SOURCE_API_KEY")
	if apiKey == "" {
		log.Fatal("SOURCE_API_KEY is required")
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/priceforagent.db"
	}

	// Initialize price client
	priceClient = price.NewClient(sourceURL, apiKey)

	// Initialize auth store
	var err error
	authStore, err = auth.NewStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize auth store: %v", err)
	}
	defer authStore.Close()

	// Initialize rate limiter
	rateLimiter, err = ratelimit.NewLimiter(redisAddr, 2) // 2 req/sec
	if err != nil {
		log.Fatalf("Failed to initialize rate limiter: %v", err)
	}
	defer rateLimiter.Close()

	// Initialize ranking client (CoinGecko)
	rankingClient = ranking.NewCoinGecko()

	// Initialize pairs syncer (daily sync to Redis)
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	pairsSyncer = pairs.NewSyncer(sourceURL, apiKey, redisClient)
	pairsSyncer.StartDailySync(context.Background())

	// Initialize WebSocket client for real-time prices
	wsURL := os.Getenv("WS_URL")
	if wsURL == "" {
		wsURL = "wss://ws.price.usenobi.com/v1"
	}
	wsClient = price.NewWSClient(wsURL, apiKey)
	if err := wsClient.Connect(); err != nil {
		log.Printf("WebSocket connection failed (will use HTTP fallback): %v", err)
	} else {
		log.Println("WebSocket connected")
		defer wsClient.Close()
	}
	
	// Initialize dynamic subscriber (top 10 + on-demand)
	dynamicSubscriber = price.NewDynamicSubscriber(wsClient, redisClient)
	dynamicSubscriber.Start(context.Background())

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Public endpoints (no auth)
	r.GET("/health", handleHealth)
	r.GET("/v1/info", handleInfo)
	r.POST("/v1/register", handleRegister)
	r.GET("/v1/openapi.yaml", handleOpenAPI)
	r.GET("/v1/function-schema", handleFunctionSchema)

	// Admin endpoints (require admin key)
	adminKey := os.Getenv("ADMIN_KEY")
	if adminKey == "" {
		adminKey = "admin_priceforagent_secret"
	}
	admin := r.Group("/admin")
	admin.Use(adminAuthMiddleware(adminKey))
	{
		admin.GET("/stats", handleAdminStats)
		admin.GET("/keys", handleAdminListKeys)
		admin.GET("/usage/:key", handleAdminKeyUsage)
		admin.GET("/daily", handleAdminDailyBreakdown)
	}

	// Protected endpoints (require API key)
	protected := r.Group("/v1")
	protected.Use(authMiddleware())
	protected.Use(rateLimitMiddleware())
	{
		protected.POST("/query", handleQuery)
		protected.GET("/price/:pair", handlePrice)
		protected.POST("/batch", handleBatch)
		protected.GET("/pairs", handlePairs)
		protected.GET("/usage", handleUsage)
		protected.GET("/top", handleTop)
	}

	log.Printf("Starting Price for Agent on :%s", port)
	r.Run(":" + port)
}

// Middleware

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")
		if key == "" {
			key = c.Query("api_key")
		}
		if key == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "API key required"})
			c.Abort()
			return
		}

		apiKey, err := authStore.ValidateKey(key)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			c.Abort()
			return
		}

		c.Set("api_key", apiKey)
		c.Next()

		// Track usage after request
		go authStore.IncrementUsage(key)
		go rateLimiter.IncrementUsage(context.Background(), key)
	}
}

func adminAuthMiddleware(adminKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-Admin-Key")
		if key == "" {
			key = c.Query("admin_key")
		}
		if key != adminKey {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid admin key"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.Background()
		key := c.GetHeader("X-API-Key")
		if key == "" {
			key = c.Query("api_key")
		}

		// Check global limit first
		globalAllowed, globalRemaining, _ := rateLimiter.CheckGlobalLimit(ctx)
		c.Header("X-Global-Limit", "10000000")
		c.Header("X-Global-Remaining", strconv.FormatInt(globalRemaining, 10))
		
		if !globalAllowed {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":            "Global limit exceeded",
				"message":          "Service has reached maximum capacity. Contact support for enterprise access.",
				"global_limit":     10000000,
				"global_remaining": 0,
			})
			c.Abort()
			return
		}

		// Check per-key rate limit
		allowed, remaining, err := rateLimiter.Allow(ctx, key)
		if err != nil {
			log.Printf("Rate limit error: %v", err)
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", "2")
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))

		if !allowed {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "Rate limit exceeded",
				"limit":       2,
				"remaining":   remaining,
				"retry_after": 1,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Handlers

func handleHealth(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok", "ts": time.Now().Unix()})
}

func handleInfo(c *gin.Context) {
	ctx := context.Background()
	
	// Get pairs count from cache
	allPairs, _ := pairsSyncer.GetAll(ctx)
	totalPairs := len(allPairs)
	
	// Count by type
	typeCounts := make(map[string]int)
	for _, p := range allPairs {
		// Extract type from code (e.g., "Crypto:ALL:BTC/USDT" -> "Crypto")
		parts := strings.SplitN(p.Code, ":", 2)
		if len(parts) > 0 {
			typeCounts[parts[0]]++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"service":     "Price for Agent (p4ai)",
		"version":     "1.2.0",
		"total_pairs": totalPairs,
		"pair_types":  typeCounts,
		"features": []string{
			"Real-time crypto prices via WebSocket",
			"Stocks (US, JP, HK, KR, EU)",
			"Forex pairs",
			"Commodities & Metals",
			"Prediction markets",
		},
		"endpoints": gin.H{
			"GET /v1/price/:pair": "Get price for a single asset",
			"POST /v1/query":      "Natural language price query",
			"POST /v1/batch":      "Batch price lookup",
			"GET /v1/pairs":       "List all pairs (2700+)",
			"GET /v1/top":         "Top coins by market cap",
		},
		"docs": "https://github.com/edibez/priceforagent",
	})
}

func handleRegister(c *gin.Context) {
	var req struct {
		AgentID string `json:"agent_id"`
	}
	c.ShouldBindJSON(&req)

	apiKey, err := authStore.GenerateKey(req.AgentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_key":    apiKey.Key,
		"message":    "API key generated successfully. Include this in X-API-Key header.",
		"created_at": apiKey.CreatedAt,
	})
}

// Admin handlers

func handleAdminStats(c *gin.Context) {
	ctx := context.Background()
	
	// Get global stats from Redis
	globalStats, err := rateLimiter.GetGlobalStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	// Get global usage
	globalUsage, _ := rateLimiter.GetGlobalUsage(ctx)
	
	// Get API key count from DB
	keys, _ := authStore.ListKeys()
	
	c.JSON(http.StatusOK, gin.H{
		"total_api_keys":   len(keys),
		"global_usage":     globalUsage,
		"global_limit":     10000000,
		"global_remaining": 10000000 - globalUsage,
		"today_hits":       globalStats["today_hits"],
		"timestamp":        time.Now().Unix(),
	})
}

func handleAdminListKeys(c *gin.Context) {
	ctx := context.Background()
	keys, err := authStore.ListKeys()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	// Enrich with Redis stats
	var enrichedKeys []gin.H
	for _, k := range keys {
		last24h, _ := rateLimiter.GetLast24HoursUsage(ctx, k.Key)
		last7d, _ := rateLimiter.GetLast7DaysUsage(ctx, k.Key)
		
		enrichedKeys = append(enrichedKeys, gin.H{
			"id":         k.ID,
			"api_key":    k.Key[:16] + "...",
			"agent_id":   k.AgentID,
			"created_at": k.CreatedAt,
			"hit_count":  k.HitCount,
			"last_24h":   last24h,
			"last_7d":    last7d,
		})
	}
	
	c.JSON(http.StatusOK, gin.H{
		"count": len(keys),
		"keys":  enrichedKeys,
	})
}

func handleAdminKeyUsage(c *gin.Context) {
	ctx := context.Background()
	keyPrefix := c.Param("key")
	
	// Find full key
	keys, _ := authStore.ListKeys()
	var fullKey string
	for _, k := range keys {
		if len(k.Key) >= len(keyPrefix) && k.Key[:len(keyPrefix)] == keyPrefix {
			fullKey = k.Key
			break
		}
	}
	
	if fullKey == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Key not found"})
		return
	}
	
	// Get detailed stats
	total, _ := rateLimiter.GetUsage(ctx, fullKey)
	last24h, _ := rateLimiter.GetLast24HoursUsage(ctx, fullKey)
	last7d, _ := rateLimiter.GetLast7DaysUsage(ctx, fullKey)
	breakdown, _ := rateLimiter.GetDailyBreakdown(ctx, fullKey, 7)
	
	c.JSON(http.StatusOK, gin.H{
		"api_key":        fullKey[:16] + "...",
		"total":          total,
		"last_24h":       last24h,
		"last_7_days":    last7d,
		"daily_breakdown": breakdown,
	})
}

func handleAdminDailyBreakdown(c *gin.Context) {
	ctx := context.Background()
	days := 7
	if d := c.Query("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 30 {
			days = parsed
		}
	}
	
	breakdown, err := rateLimiter.GetGlobalDailyBreakdown(ctx, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"days":      days,
		"breakdown": breakdown,
	})
}

func handleUsage(c *gin.Context) {
	apiKey, _ := c.Get("api_key")
	key := apiKey.(*auth.APIKey)

	stats, err := authStore.GetUsageStats(key.Key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get usage stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_key":    stats.Key[:12] + "...",
		"hit_count":  stats.HitCount,
		"created_at": stats.CreatedAt,
		"last_used":  stats.LastUsed,
	})
}

// Request/Response types

type QueryRequest struct {
	Query string `json:"query" binding:"required"`
}

type BatchRequest struct {
	Pairs []string `json:"pairs" binding:"required"`
}

type PriceResponse struct {
	Pair      string  `json:"pair"`
	Price     float64 `json:"price"`
	Ask       float64 `json:"ask,omitempty"`
	Bid       float64 `json:"bid,omitempty"`
	Currency  string  `json:"currency"`
	Market    string  `json:"market"`
	Timestamp int64   `json:"timestamp"`
	Source    string  `json:"source,omitempty"`
}

// getPriceWithCache tries WS cache first, falls back to HTTP
// Also triggers dynamic subscription for future requests
func getPriceWithCache(code string) (*price.PriceData, string) {
	ctx := context.Background()
	
	// Try WebSocket cache first
	if wsClient != nil {
		if data, ok := wsClient.GetCached(code); ok {
			// Record access for dynamic subscriber
			if dynamicSubscriber != nil {
				dynamicSubscriber.OnPairRequested(ctx, code)
			}
			return data, "ws"
		}
	}
	
	// Fallback to HTTP - also subscribe for future requests
	data, err := priceClient.GetPrice(code)
	if err != nil {
		return nil, ""
	}
	
	// Add to dynamic subscription for next time
	if dynamicSubscriber != nil {
		dynamicSubscriber.OnPairRequested(ctx, code)
	}
	
	return data, "http"
}

func handleQuery(c *gin.Context) {
	var req QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	assets := ai.ParseQuery(req.Query)
	if len(assets) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"query":   req.Query,
			"results": []interface{}{},
			"message": "No assets found in query. Try: 'What's the price of Bitcoin?'",
		})
		return
	}

	var results []PriceResponse
	for _, asset := range assets {
		code := ai.BuildCode(asset)
		data, source := getPriceWithCache(code)
		if data == nil {
			continue
		}
		resp := toPriceResponse(asset, data)
		resp.Source = source
		results = append(results, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"query":   req.Query,
		"results": results,
	})
}

func handlePrice(c *gin.Context) {
	pair := c.Param("pair")
	asset := ai.NormalizeAsset(pair)
	code := ai.BuildCode(asset)

	data, source := getPriceWithCache(code)
	if data == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pair not found", "pair": pair})
		return
	}

	resp := toPriceResponse(asset, data)
	resp.Source = source
	c.JSON(http.StatusOK, resp)
}

func handleBatch(c *gin.Context) {
	var req BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pairs array is required"})
		return
	}

	// Parallel fetch
	type result struct {
		index int
		data  *PriceResponse
		err   error
		pair  string
	}

	results := make([]PriceResponse, 0, len(req.Pairs))
	errors := make([]gin.H, 0)
	resultChan := make(chan result, len(req.Pairs))

	var wg sync.WaitGroup
	for i, pair := range req.Pairs {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			asset := ai.NormalizeAsset(p)
			code := ai.BuildCode(asset)
			data, source := getPriceWithCache(code)
			if data == nil {
				resultChan <- result{index: idx, err: fmt.Errorf("not found"), pair: p}
				return
			}
			resp := toPriceResponse(asset, data)
			resp.Source = source
			resultChan <- result{index: idx, data: &resp, pair: p}
		}(i, pair)
	}

	// Close channel when all goroutines done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results maintaining order
	ordered := make([]*PriceResponse, len(req.Pairs))
	for r := range resultChan {
		if r.err != nil {
			errors = append(errors, gin.H{"pair": r.pair, "error": r.err.Error()})
		} else {
			ordered[r.index] = r.data
		}
	}

	// Build final results (skip nils)
	for _, r := range ordered {
		if r != nil {
			results = append(results, *r)
		}
	}

	response := gin.H{"results": results}
	if len(errors) > 0 {
		response["errors"] = errors
	}
	c.JSON(http.StatusOK, response)
}

func handleTop(c *gin.Context) {
	limit := 10
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	// Get top coins from CoinGecko (symbols + ranking info)
	coins, err := rankingClient.GetTopCoins(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch rankings", "details": err.Error()})
		return
	}

	// Fetch prices in parallel from source
	type priceResult struct {
		index int
		price *PriceResponse
		err   error
	}

	priceChan := make(chan priceResult, len(coins))
	var wg sync.WaitGroup

	for i, coin := range coins {
		wg.Add(1)
		go func(idx int, symbol string) {
			defer wg.Done()
			asset := ai.NormalizeAsset(symbol)
			code := ai.BuildCode(asset)
			data, source := getPriceWithCache(code)
			if data == nil {
				priceChan <- priceResult{index: idx, err: fmt.Errorf("not found")}
				return
			}
			resp := toPriceResponse(asset, data)
			resp.Source = source
			priceChan <- priceResult{index: idx, price: &resp}
		}(i, coin.Symbol)
	}

	go func() {
		wg.Wait()
		close(priceChan)
	}()

	// Collect prices
	prices := make([]*PriceResponse, len(coins))
	for r := range priceChan {
		if r.err == nil {
			prices[r.index] = r.price
		}
	}

	// Build response with ranking + price
	var results []gin.H
	for i, coin := range coins {
		entry := gin.H{
			"rank":              coin.MarketCapRank,
			"symbol":            coin.Symbol,
			"name":              coin.Name,
			"market_cap":        coin.MarketCap,
			"price_change_24h":  coin.PriceChange24h,
		}
		if prices[i] != nil {
			entry["price"] = prices[i].Price
			entry["currency"] = prices[i].Currency
		}
		results = append(results, entry)
	}

	c.JSON(http.StatusOK, gin.H{
		"limit":   limit,
		"results": results,
	})
}

func handlePairs(c *gin.Context) {
	ctx := context.Background()
	search := c.Query("search")
	
	// Get total count from cache
	allPairs, cacheErr := pairsSyncer.GetAll(ctx)
	totalPairs := len(allPairs)
	
	// Try Redis cache first
	if search != "" {
		// Search mode
		results, err := pairsSyncer.Search(ctx, search)
		if err == nil && len(results) > 0 {
			c.JSON(http.StatusOK, gin.H{
				"pairs":  results,
				"count":  len(results),
				"total":  totalPairs,
				"source": "cache",
			})
			return
		}
	} else if cacheErr == nil && totalPairs > 0 {
		// Return all from cache with pagination info
		c.JSON(http.StatusOK, gin.H{
			"pairs":  allPairs,
			"count":  totalPairs,
			"total":  totalPairs,
			"source": "cache",
			"note":   "2700+ pairs available. Use ?search=XXX to filter.",
		})
		return
	}

	// Fallback to direct API
	assetType := c.Query("type")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "50"))

	pairsData, err := priceClient.GetPairs(assetType, page, perPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"pairs":  pairsData,
		"source": "api",
		"note":   "Paginated response. Total 2700+ pairs available.",
	})
}

func handleOpenAPI(c *gin.Context) {
	c.File("api/openapi.yaml")
}

func handleFunctionSchema(c *gin.Context) {
	schema := []gin.H{
		{
			"name":        "get_price",
			"description": "Get current price for an asset (crypto, stock, commodity)",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"asset": gin.H{
						"type":        "string",
						"description": "Asset name or ticker (e.g., 'bitcoin', 'BTC', 'NVDA', 'gold')",
					},
				},
				"required": []string{"asset"},
			},
		},
		{
			"name":        "get_prices",
			"description": "Get prices for multiple assets at once",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"assets": gin.H{
						"type":        "array",
						"items":       gin.H{"type": "string"},
						"description": "List of asset names or tickers",
					},
				},
				"required": []string{"assets"},
			},
		},
	}
	c.JSON(http.StatusOK, schema)
}

func toPriceResponse(asset string, data *price.PriceData) PriceResponse {
	priceVal, _ := strconv.ParseFloat(data.Price, 64)
	askVal, _ := strconv.ParseFloat(data.Ask, 64)
	bidVal, _ := strconv.ParseFloat(data.Bid, 64)

	currency := "USD"
	if len(data.Code) > 0 {
		parts := splitCode(data.Code)
		if len(parts) > 0 {
			currency = parts[len(parts)-1]
		}
	}

	market := "open"
	if !data.Market.Open {
		market = "closed"
	}

	return PriceResponse{
		Pair:      asset,
		Price:     priceVal,
		Ask:       askVal,
		Bid:       bidVal,
		Currency:  currency,
		Market:    market,
		Timestamp: time.Now().Unix(),
	}
}

func splitCode(code string) []string {
	result := make([]string, 0)
	current := ""
	for _, c := range code {
		if c == ':' || c == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
