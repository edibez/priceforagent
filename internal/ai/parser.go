package ai

import (
	"regexp"
	"strings"
)

// Common asset aliases
var assetAliases = map[string]string{
	// Crypto - Major
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
	
	// Crypto - Top 50
	"tron":      "TRX",
	"trx":       "TRX",
	"avalanche": "AVAX",
	"avax":      "AVAX",
	"shiba":     "SHIB",
	"shib":      "SHIB",
	"polkadot":  "DOT",
	"dot":       "DOT",
	"polygon":   "MATIC",
	"matic":     "MATIC",
	"chainlink": "LINK",
	"link":      "LINK",
	"litecoin":  "LTC",
	"ltc":       "LTC",
	"uniswap":   "UNI",
	"uni":       "UNI",
	"cosmos":    "ATOM",
	"atom":      "ATOM",
	"stellar":   "XLM",
	"xlm":       "XLM",
	"filecoin":  "FIL",
	"fil":       "FIL",
	"near":      "NEAR",
	"aave":      "AAVE",
	"injective": "INJ",
	"inj":       "INJ",
	"aptos":     "APT",
	"apt":       "APT",
	"arbitrum":  "ARB",
	"arb":       "ARB",
	"optimism":  "OP",
	"op":        "OP",
	"sui":       "SUI",
	"pepe":      "PEPE",
	"bonk":      "BONK",
	"wif":       "WIF",
	"render":    "RNDR",
	"rndr":      "RNDR",
	"kaspa":     "KAS",
	"kas":       "KAS",
	
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

// Asset type detection - expanded list
var cryptoAssets = map[string]bool{
	// Top 10
	"BTC": true, "ETH": true, "BNB": true, "XRP": true, "SOL": true,
	"DOGE": true, "ADA": true, "TRX": true, "AVAX": true, "SHIB": true,
	// Top 11-30
	"DOT": true, "LINK": true, "MATIC": true, "LTC": true, "BCH": true,
	"UNI": true, "ATOM": true, "XLM": true, "ETC": true, "FIL": true,
	"NEAR": true, "APT": true, "ARB": true, "OP": true, "INJ": true,
	"AAVE": true, "LDO": true, "KAVA": true, "SUI": true, "SEI": true,
	// Memecoins
	"PEPE": true, "BONK": true, "WIF": true, "FLOKI": true,
	// AI/Infra
	"RNDR": true, "FET": true, "AGIX": true, "TAO": true,
	// Others
	"KAS": true, "IMX": true, "STX": true, "RUNE": true, "GRT": true,
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
