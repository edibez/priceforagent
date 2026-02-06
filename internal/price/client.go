package price

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client for the source price service
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new price client
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// SourceResponse from the price API
type SourceResponse struct {
	StatusNumber string      `json:"status_number"`
	Timestamp    int64       `json:"ts"`
	Message      string      `json:"message,omitempty"`
	Data         interface{} `json:"data"`
}

// PriceData from the price API
type PriceData struct {
	Code   string `json:"code"`
	Ask    string `json:"ask"`
	Bid    string `json:"bid"`
	Price  string `json:"price"`
	Market Market `json:"market"`
}

// Market status
type Market struct {
	Open     bool   `json:"open"`
	Reason   string `json:"reason"`
	Session  string `json:"session"`
	Timezone string `json:"timezone,omitempty"`
}

// PairData from pairs endpoint
type PairData struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Base  string `json:"base"`
	Quote string `json:"quote"`
}

// GetPrice fetches price for a specific code
func (c *Client) GetPrice(code string) (*PriceData, error) {
	endpoint := fmt.Sprintf("%s/price?code=%s", c.baseURL, url.QueryEscape(code))
	
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		StatusNumber string    `json:"status_number"`
		Timestamp    int64     `json:"ts"`
		Message      string    `json:"message,omitempty"`
		Data         PriceData `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if result.StatusNumber != "200" {
		return nil, fmt.Errorf("API error: %s - %s", result.StatusNumber, result.Message)
	}

	return &result.Data, nil
}

// GetPairs fetches available pairs
func (c *Client) GetPairs(assetType string, page, perPage int) ([]PairData, error) {
	endpoint := fmt.Sprintf("%s/pairs?page=%d&per_page=%d", c.baseURL, page, perPage)
	if assetType != "" {
		endpoint += "&type=" + url.QueryEscape(assetType)
	}

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		StatusNumber string     `json:"status_number"`
		Data         []PairData `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Data, nil
}
