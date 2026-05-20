// Package ui embeds the admin SPA static assets into the binary.
package ui

import "embed"

//go:embed static
var Static embed.FS
