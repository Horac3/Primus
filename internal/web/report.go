package web

import (
	"net/url"
	"regexp"
)

var urlInText = regexp.MustCompile(`https?://[^\s"'<>]+`)

// redactURLs reduces every URL found in free text to scheme://host/path. Query
// strings, fragments and userinfo can carry credentials, and Go transport
// errors embed the full target URL.
func redactURLs(s string) string {
	return urlInText.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return cutAt(raw, "?#")
		}
		return u.Scheme + "://" + u.Host + u.EscapedPath()
	})
}

// cutAt drops everything from the first occurrence of any byte in seps.
func cutAt(s, seps string) string {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(seps); j++ {
			if s[i] == seps[j] {
				return s[:i]
			}
		}
	}
	return s
}
