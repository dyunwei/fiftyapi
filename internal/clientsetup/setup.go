// Package clientsetup serves the auditable, standalone client configuration helper.
package clientsetup

import (
	"embed"
	"net/http"
)

//go:embed setup.py
var files embed.FS

func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	data, _ := files.ReadFile("setup.py")
	w.Write(data)
}
