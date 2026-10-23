// Package docs serves the Markdown reference in docs/*.md — the same files
// rendered on GitHub are embedded here and served by the in-app viewer at
// /docs. Markdown is rendered lazily on first request per slug, then cached;
// source files don't change while the server's running.
package docs

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Only the public reference pages are embedded. Maintainer files in this
// directory (progress.md, decisions.md, requirements.md, architecture.md)
// must not ship in the binary: a new public page is added here and to order.
//
//go:embed load.md burst.md soak.md auth-probe.md global-auth.md credential-pools.md assertions.md history.md reports.md cli.md collections.md
var files embed.FS

// order is the sidebar order for the viewer index.
var order = []string{"load", "burst", "soak", "auth-probe", "global-auth", "credential-pools", "assertions", "history", "reports", "cli", "collections"}

// Doc is one reference page: its URL slug and display title.
type Doc struct {
	Slug  string
	Title string
}

var md = goldmark.New(goldmark.WithExtensions(extension.Table))

var cache = struct {
	sync.RWMutex
	html map[string]template.HTML
}{html: make(map[string]template.HTML)}

// List returns every reference doc in sidebar order.
func List() []Doc {
	out := make([]Doc, 0, len(order))
	for _, slug := range order {
		out = append(out, Doc{Slug: slug, Title: title(slug)})
	}
	return out
}

// title derives the display title from the doc's first "# " heading,
// falling back to the slug when no heading exists.
func title(slug string) string {
	raw, err := files.ReadFile(slug + ".md")
	if err != nil {
		return slug
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok && t != "" {
			return t
		}
	}
	return slug
}

// Render converts the named doc to sanitized HTML. Unknown slugs (including
// traversal attempts like "../x") return an error — the slugs are an
// allowlist, never a filesystem path.
func Render(slug string) (template.HTML, error) {
	known := false
	for _, s := range order {
		if s == slug {
			known = true
			break
		}
	}
	if !known {
		return "", fmt.Errorf("unknown doc %q", slug)
	}

	cache.RLock()
	if html, ok := cache.html[slug]; ok {
		cache.RUnlock()
		return html, nil
	}
	cache.RUnlock()

	raw, err := files.ReadFile(slug + ".md")
	if err != nil {
		return "", fmt.Errorf("read doc %q: %w", slug, err)
	}
	var buf bytes.Buffer
	if err := md.Convert(raw, &buf); err != nil {
		return "", fmt.Errorf("render doc %q: %w", slug, err)
	}
	html := template.HTML(buf.String()) //nolint:gosec // source is our own embedded Markdown

	cache.Lock()
	cache.html[slug] = html
	cache.Unlock()
	return html, nil
}
