// Package templates embeds the HTML template tree for handlers.
package templates

import "embed"

// FS is the embedded HTML template tree (pages + HTMX fragments).
//
//go:embed layout.html admin/*.html admin/partials/*.html public/*.html public/partials/*.html
var FS embed.FS
