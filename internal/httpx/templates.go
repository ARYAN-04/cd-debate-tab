// Package httpx holds template helpers.
package httpx

import (
	"html/template"
	"io/fs"
	"strings"
)

// FuncMap holds the template helpers available to all pages.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"inc":   func(n int) int { return n + 1 },
		"add":   func(a, b int) int { return a + b },
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
	}
}

// ParseFS parses templates from fsys matching patterns with FuncMap
// installed. Patterns use forward slashes, e.g. "templates/*.html".
func ParseFS(fsys fs.FS, patterns ...string) (*template.Template, error) {
	if len(patterns) == 0 {
		patterns = []string{"**/*.html"}
	}
	return template.New("").Funcs(FuncMap()).ParseFS(fsys, patterns...)
}
