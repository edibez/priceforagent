package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/edibez/priceforagent/internal/ai"
	"github.com/edibez/priceforagent/internal/auth"
	"github.com/edibez/priceforagent/internal/price"
	"github.com/edibez/priceforagent/internal/ratelimit"
	"github.com/gin-gonic/gin"
)

var (
	priceClient *price.Client
	authStore   *auth.Store
	rateLimiter *ratelimit.Limiter
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

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Public endpoints (no auth)
	r.GET("/health", handleHealth)
	r.POST("/v1/register", handleRegister)
	r.GET("/v1/openapi.yaml", handleOpenAPI)
	r.GET("/v1/function-schema", handleFunctionSchema)

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

func rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")
		if key == "" {
			key = c.Query("api_key")
		}

		allowed, remaining, err := rateLimiter.Allow(context.Background(), key)
		if err != nil {
			log.Printf("Rate limit error: %v", err)
			c.Next()
			return
		}

		c.Header("X-RateLimit-Limit", "2")
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))

		if !allowed {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":     "Rate limit exceeded",
				"limit":     2,
				"remaining": remaining,
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
		data, err := priceClient.GetPrice(code)
		if err != nil {
			continue
		}
		results = append(results, toPriceResponse(asset, data))
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

	data, err := priceClient.GetPrice(code)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pair not found", "pair": pair})
		return
	}

	c.JSON(http.StatusOK, toPriceResponse(asset, data))
}

func handleBatch(c *gin.Context) {
	var req BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pairs array is required"})
		return
	}

	var results []PriceResponse
	var errors []gin.H

	for _, pair := range req.Pairs {
		asset := ai.NormalizeAsset(pair)
		code := ai.BuildCode(asset)
		data, err := priceClient.GetPrice(code)
		if err != nil {
			errors = append(errors, gin.H{"pair": pair, "error": err.Error()})
			continue
		}
		results = append(results, toPriceResponse(asset, data))
	}

	response := gin.H{"results": results}
	if len(errors) > 0 {
		response["errors"] = errors
	}
	c.JSON(http.StatusOK, response)
}

func handlePairs(c *gin.Context) {
	assetType := c.Query("type")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))

	pairs, err := priceClient.GetPairs(assetType, page, perPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"pairs": pairs})
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
