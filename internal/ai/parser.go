package ai

import (
	"regexp"
	"strings"
)

// Common asset aliases
var assetAliases = map[string]string{
	// Crypto
	"bitcoin":  "BTC",
	"btc":      "BTC",
	"ethereum": "ETH",
	"eth":      "ETH",
	"solana":   "SOL",
	"sol":      "SOL",
	"ripple":   "XRP",
	"xrp":      "XRP",
	"cardano":  "ADA",
	"ada":      "ADA",
	"dogecoin": "DOGE",
	"doge":     "DOGE",
	"bnb":      "BNB",
	"binance":  "BNB",
	
	// Stocks
	"nvidia":    "NVDA",
	"nvda":      "NVDA",
	"apple":     "AAPL",
	"aapl":      "AAPL",
	"microsoft": "MSFT",
	"msft":      "MSFT",
	"google":    "GOOGL",
	"googl":     "GOOGL",
	"amazon":    "AMZN",
	"amzn":      "AMZN",
	"tesla":     "TSLA",
	"tsla":      "TSLA",
	"meta":      "META",
	"facebook":  "META",
	
	// Commodities
	"gold":   "XAU",
	"xau":    "XAU",
	"silver": "XAG",
	"xag":    "XAG",
	"oil":    "WTI",
	"wti":    "WTI",
	
	// Forex
	"dollar": "USD",
	"usd":    "USD",
	"euro":   "EUR",
	"eur":    "EUR",
	"yen":    "JPY",
	"jpy":    "JPY",
	"pound":  "GBP",
	"gbp":    "GBP",
}

// Asset type detection
var cryptoAssets = map[string]bool{
	"BTC": true, "ETH": true, "SOL": true, "XRP": true, "ADA": true,
	"DOGE": true, "BNB": true, "AAVE": true, "LINK": true, "DOT": true,
}

var equityAssets = map[string]bool{
	"NVDA": true, "AAPL": true, "MSFT": true, "GOOGL": true, "AMZN": true,
	"TSLA": true, "META": true, "AMD": true, "INTC": true, "NFLX": true,
}

var metalAssets = map[string]bool{
	"XAU": true, "XAG": true,
}

// ParseQuery extracts asset codes from natural language
func ParseQuery(query string) []string {
	query = strings.ToLower(query)
	var found []string
	seen := make(map[string]bool)

	// Check for aliases
	for alias, code := range assetAliases {
		if strings.Contains(query, alias) && !seen[code] {
			found = append(found, code)
			seen[code] = true
		}
	}

	// Check for direct ticker mentions (uppercase in original)
	tickerRegex := regexp.MustCompile(`\b([A-Z]{2,5})\b`)
	matches := tickerRegex.FindAllStringSubmatch(strings.ToUpper(query), -1)
	for _, match := range matches {
		code := match[1]
		if !seen[code] && (cryptoAssets[code] || equityAssets[code] || metalAssets[code]) {
			found = append(found, code)
			seen[code] = true
		}
	}

	return found
}

// BuildCode constructs the full code for the source API
func BuildCode(asset string) string {
	asset = strings.ToUpper(asset)

	// Determine asset type and build code
	if cryptoAssets[asset] {
		return "Crypto:ALL:" + asset + "/USDT"
	}
	if equityAssets[asset] {
		return "Equity:US:" + asset + "/USD"
	}
	if metalAssets[asset] {
		return "Metal:ALL:" + asset + "/USD"
	}

	// Default to crypto
	return "Crypto:ALL:" + asset + "/USDT"
}

// NormalizeAsset converts alias to standard code
func NormalizeAsset(input string) string {
	lower := strings.ToLower(input)
	if code, ok := assetAliases[lower]; ok {
		return code
	}
	return strings.ToUpper(input)
}
