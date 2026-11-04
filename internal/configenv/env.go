// Package configenv reads FiftyAPI configuration with legacy deployment aliases.
package configenv

import (
	"os"
	"strings"
)

// Value prefers an explicitly set FIFTYAPI_ variable, even when empty. This
// allows an operator to clear a setting without reviving its legacy value.
func Value(name string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	if strings.HasPrefix(name, "FIFTYAPI_") {
		return os.Getenv("ZTOKENMGR_" + strings.TrimPrefix(name, "FIFTYAPI_"))
	}
	return ""
}
