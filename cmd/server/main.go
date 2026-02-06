package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/edibez/priceforagent/internal/ai"
	"github.com/edibez/priceforagent/internal/price"
	"github.com/gin-gonic/gin"
)

var priceClient *price.Client

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

	priceClient = price.NewClient(sourceURL, apiKey)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "ts": time.Now().Unix()})
	})

	// Price endpoints
	v1 := r.Group("/v1")
	{
		v1.POST("/query", handleQuery)
		v1.GET("/price/:pair", handlePrice)
		v1.POST("/batch", handleBatch)
		v1.GET("/pairs", handlePairs)
		v1.GET("/openapi.yaml", handleOpenAPI)
		v1.GET("/function-schema", handleFunctionSchema)
	}

	log.Printf("Starting Price for Agent on :%s", port)
	r.Run(":" + port)
}

// QueryRequest for natural language
type QueryRequest struct {
	Query string `json:"query" binding:"required"`
}

// BatchRequest for multiple pairs
type BatchRequest struct {
	Pairs []string `json:"pairs" binding:"required"`
}

// PriceResponse simplified for agents
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

	// Parse natural language
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

	// Determine currency from code
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
	// Crypto:ALL:BTC/USDT -> [Crypto, ALL, BTC, USDT]
	var parts []string
	for _, p := range []byte(code) {
		if p == ':' || p == '/' {
			continue
		}
	}
	// Simple split
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
