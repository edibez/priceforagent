# Price for Agent

LLM-friendly price service for crypto, stocks, and commodities.

## Features

- 🗣️ **Natural Language Query** - Ask in plain English
- 🔧 **Function Calling Format** - Ready for OpenAI/Anthropic tool use
- 📦 **Batch Query** - Multiple prices in one call
- ⚡ **Low Latency** - Go-powered, <50ms overhead

## Quick Start

```bash
# Run locally
go run cmd/server/main.go

# With Docker
docker build -t priceforagent .
docker run -p 8080:8080 priceforagent
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/query` | POST | Natural language query |
| `/v1/price/:pair` | GET | Get single price |
| `/v1/batch` | POST | Get multiple prices |
| `/v1/openapi.yaml` | GET | OpenAPI spec |

## Examples

### Natural Language Query
```bash
curl -X POST http://localhost:8080/v1/query \
  -H "Content-Type: application/json" \
  -d '{"query": "What is the price of Bitcoin?"}'
```

### Direct Price Lookup
```bash
curl http://localhost:8080/v1/price/BTC-USD
```

### Batch Query
```bash
curl -X POST http://localhost:8080/v1/batch \
  -H "Content-Type: application/json" \
  -d '{"pairs": ["BTC/USD", "ETH/USD", "SOL/USD"]}'
```

## Response Format

```json
{
  "pair": "BTC/USD",
  "price": 67234.50,
  "currency": "USD",
  "change_24h": 2.3,
  "timestamp": "2026-02-06T10:00:00Z"
}
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | 8080 |
| `SOURCE_URL` | Price engine URL | - |
| `REDIS_URL` | Redis for caching | - |

## For LLM/Agent Integration

See `api/openapi.yaml` for machine-readable spec.

### Function Calling Schema
```json
{
  "name": "get_price",
  "description": "Get current price for a trading pair",
  "parameters": {
    "type": "object",
    "properties": {
      "pair": {
        "type": "string",
        "description": "Trading pair (e.g., BTC/USD)"
      }
    },
    "required": ["pair"]
  }
}
```

## License

MIT
