package buildinfo

import (
	"embed"
	"strings"
)

//go:embed VERSION
var files embed.FS

// Commit may be populated by a release build. No runtime account data is used.
var Commit = "unknown"

func Version() string { b, _ := files.ReadFile("VERSION"); return strings.TrimSpace(string(b)) }
