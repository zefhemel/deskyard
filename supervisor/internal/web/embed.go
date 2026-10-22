// Package web holds the built UI (supervisor/web, `npm run build`).
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
