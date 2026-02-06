package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	r := gin.Default()

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Price endpoints
	v1 := r.Group("/v1")
	{
		// Natural language query
		v1.POST("/query", handleQuery)
		
		// Direct price lookup
		v1.GET("/price/:pair", handlePrice)
		
		// Batch query
		v1.POST("/batch", handleBatch)
		
		// OpenAPI spec
		v1.GET("/openapi.yaml", handleOpenAPI)
	}

	log.Printf("Starting server on :%s", port)
	r.Run(":" + port)
}

func handleQuery(c *gin.Context) {
	// TODO: Implement natural language query
	c.JSON(501, gin.H{"error": "not implemented"})
}

func handlePrice(c *gin.Context) {
	pair := c.Param("pair")
	// TODO: Fetch from source service
	c.JSON(200, gin.H{
		"pair":  pair,
		"price": 0,
		"timestamp": "",
	})
}

func handleBatch(c *gin.Context) {
	// TODO: Implement batch query
	c.JSON(501, gin.H{"error": "not implemented"})
}

func handleOpenAPI(c *gin.Context) {
	c.File("api/openapi.yaml")
}
