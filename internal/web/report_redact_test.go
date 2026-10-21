package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
)

func TestErrorCausesAreRedactedOnRunPageAndCard(t *testing.T) {
	cause := `Request failed: Get "http://u:p@h.test/x?api_key=SECRET": dial tcp: refused`
	run := model.Run{
		ID:      "run-leak",
		Outcome: model.OutcomeFail,
		Summary: model.RunSummary{ErrorDetails: []model.ErrorDetail{{Cause: cause, Count: 3}}},
		Login: &model.LoginSummary{
			Attempted:      1,
			Failed:         1,
			FailureReasons: []model.ErrorDetail{{Cause: cause, Count: 1}},
		},
	}
	tmpl := loadEmbeddedTemplates()
	for _, name := range []string{"run-page.html", "run-result-fragment.html"} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, name, runResultTemplateData{Run: run}); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			out := buf.String()
			if strings.Contains(out, "SECRET") || strings.Contains(out, "u:p") {
				t.Fatalf("%s leaks the URL query or userinfo: %s", name, out)
			}
			if !strings.Contains(out, "http://h.test/x") {
				t.Fatalf("%s lost the redacted URL: %s", name, out)
			}
		})
	}
}

func TestRedactURLs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strips query and userinfo from a transport error",
			in:   `Request failed: Get "http://u:p@h.test/x?api_key=s": dial tcp 127.0.0.1:1: connect: refused`,
			want: `Request failed: Get "http://h.test/x": dial tcp 127.0.0.1:1: connect: refused`,
		},
		{
			name: "strips fragment",
			in:   "see https://h.test/a/b?token=t#frag now",
			want: "see https://h.test/a/b now",
		},
		{
			name: "leaves a URL without query alone",
			in:   "GET https://h.test/v1/items failed",
			want: "GET https://h.test/v1/items failed",
		},
		{
			name: "leaves plain text alone",
			in:   "context deadline exceeded",
			want: "context deadline exceeded",
		},
		{
			name: "redacts every URL in the text",
			in:   "a http://x.test/?k=1 b https://y.test/p?k=2",
			want: "a http://x.test/ b https://y.test/p",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactURLs(tc.in)
			if got != tc.want {
				t.Fatalf("redactURLs(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for _, secret := range []string{"api_key", "u:p", "token=", "k=1", "k=2"} {
				if strings.Contains(got, secret) {
					t.Fatalf("redactURLs(%q) = %q still contains %q", tc.in, got, secret)
				}
			}
		})
	}
}
