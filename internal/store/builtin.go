package store

import (
	"embed"
	"encoding/json"
	"sync"
)

//go:embed builtin_prices.json
var priceFiles embed.FS
var builtinOnce sync.Once
var builtinPrices map[string]Price

func builtinPrice(model string) (Price, bool) {
	builtinOnce.Do(func() { b, _ := priceFiles.ReadFile("builtin_prices.json"); json.Unmarshal(b, &builtinPrices) })
	p, ok := builtinPrices[model]
	return p, ok
}
