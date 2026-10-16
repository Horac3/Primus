// Package credential holds the pure parts of credential pools: filling
// {{field}} placeholders from a credential row and extracting a token from
// a login response. No I/O happens here.
package credential

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Horac3/Primus/internal/model"
)

// placeholderPattern matches {{field}} with optional inner spaces.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// Response is the part of an HTTP response an Extraction can read.
type Response struct {
	Header http.Header
	Body   []byte
}

// Render replaces every {{field}} placeholder in tmpl with fields[field].
// A placeholder with no matching field is an error, never an empty string.
// Errors name the field only; they never echo credential values.
func Render(tmpl string, fields map[string]string) (string, error) {
	return render(tmpl, fields, func(v string) string { return v })
}

// RenderBody is Render with values escaped for the body's content type:
// JSON string escaping for JSON bodies, URL query escaping for
// form-encoded bodies, raw substitution otherwise.
func RenderBody(tmpl string, fields map[string]string, contentType string) (string, error) {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		return render(tmpl, fields, jsonEscape)
	case mediaType == "application/x-www-form-urlencoded":
		return render(tmpl, fields, url.QueryEscape)
	default:
		return Render(tmpl, fields)
	}
}

func render(tmpl string, fields map[string]string, escape func(string) string) (string, error) {
	var missing []string
	out := placeholderPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := placeholderPattern.FindStringSubmatch(match)[1]
		value, ok := fields[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return escape(value)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("template placeholder {{%s}} has no matching credential field", missing[0])
	}
	return out, nil
}

// jsonEscape returns s escaped for use inside a JSON string literal.
func jsonEscape(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // encoding a string cannot fail
	out := strings.TrimSuffix(b.String(), "\n")
	return out[1 : len(out)-1]
}

// Placeholders lists the distinct placeholder names in tmpl, in order.
func Placeholders(tmpl string) []string {
	var names []string
	seen := make(map[string]bool)
	for _, m := range placeholderPattern.FindAllStringSubmatch(tmpl, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}

// Extract pulls the value described by ex out of resp: a dot path into a
// JSON body or a header name, with ex.Prefix stripped. The value must be a
// non-empty string. Errors never echo the response body.
func Extract(resp Response, ex model.Extraction) (string, error) {
	var value string
	switch ex.From {
	case model.ExtractFromBody:
		v, err := extractJSONPath(resp.Body, ex.Path)
		if err != nil {
			return "", err
		}
		value = v
	case model.ExtractFromHeader:
		value = resp.Header.Get(ex.Path)
		if value == "" {
			return "", fmt.Errorf("header %q not present in response", ex.Path)
		}
	default:
		return "", fmt.Errorf("unknown extraction source %q (want body or header)", ex.From)
	}
	if ex.Prefix != "" {
		if !strings.HasPrefix(value, ex.Prefix) {
			return "", fmt.Errorf("extracted value does not start with prefix %q", ex.Prefix)
		}
		value = strings.TrimPrefix(value, ex.Prefix)
	}
	if value == "" {
		return "", fmt.Errorf("extracted value at %q is empty", ex.Path)
	}
	return value, nil
}

// extractJSONPath walks a dot path (data.items.0.token) through a JSON
// document. Numeric segments index arrays.
func extractJSONPath(body []byte, path string) (string, error) {
	var node any
	if err := json.Unmarshal(body, &node); err != nil {
		return "", fmt.Errorf("response body is not JSON")
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("extraction path is empty")
	}
	for _, segment := range strings.Split(path, ".") {
		switch current := node.(type) {
		case map[string]any:
			next, ok := current[segment]
			if !ok {
				return "", fmt.Errorf("path %q not found in response body", path)
			}
			node = next
		case []any:
			i, err := strconv.Atoi(segment)
			if err != nil || i < 0 || i >= len(current) {
				return "", fmt.Errorf("path %q not found in response body", path)
			}
			node = current[i]
		default:
			return "", fmt.Errorf("path %q not found in response body", path)
		}
	}
	value, ok := node.(string)
	if !ok {
		return "", fmt.Errorf("value at %q is not a string", path)
	}
	return value, nil
}
