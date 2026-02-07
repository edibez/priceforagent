package price

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSClient handles WebSocket connection to price source
type WSClient struct {
	url       string
	apiKey    string
	conn      *websocket.Conn
	cache     map[string]*PriceData
	cacheMu   sync.RWMutex
	done      chan struct{}
	reconnect chan struct{}
}

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// WSPriceUpdate represents a price update from WebSocket
type WSPriceUpdate struct {
	Code   string `json:"code"`
	Price  string `json:"price"`
	Ask    string `json:"ask"`
	Bid    string `json:"bid"`
	Market Market `json:"market"`
}

// NewWSClient creates a new WebSocket client
func NewWSClient(url, apiKey string) *WSClient {
	return &WSClient{
		url:       url,
		apiKey:    apiKey,
		cache:     make(map[string]*PriceData),
		done:      make(chan struct{}),
		reconnect: make(chan struct{}, 1),
	}
}

// Connect establishes WebSocket connection
func (w *WSClient) Connect() error {
	header := make(map[string][]string)
	header["X-API-Key"] = []string{w.apiKey}

	conn, _, err := websocket.DefaultDialer.Dial(w.url, header)
	if err != nil {
		return err
	}

	w.conn = conn
	go w.readPump()
	go w.keepAlive()

	return nil
}

// Subscribe to price updates for given pairs
func (w *WSClient) Subscribe(pairs []string) error {
	if w.conn == nil {
		return nil
	}

	for _, pair := range pairs {
		msg := map[string]interface{}{
			"type":    "subscribe",
			"channel": "price:" + pair,
		}
		if err := w.conn.WriteJSON(msg); err != nil {
			log.Printf("Failed to subscribe to %s: %v", pair, err)
		}
	}

	return nil
}

// GetCached returns cached price data
func (w *WSClient) GetCached(code string) (*PriceData, bool) {
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	
	data, ok := w.cache[code]
	return data, ok
}

// readPump reads messages from WebSocket
func (w *WSClient) readPump() {
	defer func() {
		w.conn.Close()
		select {
		case w.reconnect <- struct{}{}:
		default:
		}
	}()

	for {
		select {
		case <-w.done:
			return
		default:
			_, message, err := w.conn.ReadMessage()
			if err != nil {
				log.Printf("WebSocket read error: %v", err)
				return
			}

			var msg WSMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			if msg.Type == "price" {
				var update WSPriceUpdate
				if err := json.Unmarshal(msg.Data, &update); err != nil {
					continue
				}

				w.cacheMu.Lock()
				w.cache[update.Code] = &PriceData{
					Code:   update.Code,
					Price:  update.Price,
					Ask:    update.Ask,
					Bid:    update.Bid,
					Market: update.Market,
				}
				w.cacheMu.Unlock()
			}
		}
	}
}

// keepAlive sends ping messages
func (w *WSClient) keepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			if w.conn != nil {
				w.conn.WriteMessage(websocket.PingMessage, nil)
			}
		case <-w.reconnect:
			log.Println("Attempting to reconnect WebSocket...")
			time.Sleep(5 * time.Second)
			if err := w.Connect(); err != nil {
				log.Printf("Reconnect failed: %v", err)
			}
		}
	}
}

// Close closes the WebSocket connection
func (w *WSClient) Close() {
	close(w.done)
	if w.conn != nil {
		w.conn.Close()
	}
}
