package types

import "time"

// PriceResponse is the standard response format for price queries
type PriceResponse struct {
	Pair      string    `json:"pair"`
	Price     float64   `json:"price"`
	Currency  string    `json:"currency"`
	Change24h float64   `json:"change_24h,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// QueryRequest for natural language queries
type QueryRequest struct {
	Query string `json:"query" binding:"required"`
}

// QueryResponse for natural language queries
type QueryResponse struct {
	Query   string          `json:"query"`
	Results []PriceResponse `json:"results"`
	Message string          `json:"message,omitempty"`
}

// BatchRequest for multiple price lookups
type BatchRequest struct {
	Pairs []string `json:"pairs" binding:"required"`
}

// BatchResponse for batch queries
type BatchResponse struct {
	Results []PriceResponse `json:"results"`
	Errors  []BatchError    `json:"errors,omitempty"`
}

// BatchError for failed lookups in batch
type BatchError struct {
	Pair  string `json:"pair"`
	Error string `json:"error"`
}

// FunctionCall format for LLM tool use
type FunctionCall struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ErrorResponse standard error format
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}
