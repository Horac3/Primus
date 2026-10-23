package credential_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/credential"
	"github.com/Horac3/Primus/internal/model"
)

// ---- Render ----------------------------------------------------------------

func TestRender_FillsPlaceholders(t *testing.T) {
	cases := []struct {
		name   string
		tmpl   string
		fields map[string]string
		want   string
	}{
		{
			name:   "json body",
			tmpl:   `{"username":"{{username}}","password":"{{password}}"}`,
			fields: map[string]string{"username": "alice", "password": "s3cret"},
			want:   `{"username":"alice","password":"s3cret"}`,
		},
		{
			name:   "spaces inside braces",
			tmpl:   "user={{ username }}",
			fields: map[string]string{"username": "bob"},
			want:   "user=bob",
		},
		{
			name:   "repeated placeholder",
			tmpl:   "{{id}}-{{id}}",
			fields: map[string]string{"id": "7"},
			want:   "7-7",
		},
		{
			name:   "no placeholders",
			tmpl:   "grant_type=client_credentials",
			fields: map[string]string{},
			want:   "grant_type=client_credentials",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, err := credential.Render(tc.tmpl, tc.fields)

			// Assert
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tc.want {
				t.Errorf("Render = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRender_UnknownPlaceholderIsAnError(t *testing.T) {
	// Arrange
	tmpl := `{"email":"{{email}}","password":"{{password}}"}`
	fields := map[string]string{"password": "hunter2"}

	// Act
	got, err := credential.Render(tmpl, fields)

	// Assert
	if err == nil {
		t.Fatalf("Render = %q, want an error for the missing field", got)
	}
	if !strings.Contains(err.Error(), "email") {
		t.Errorf("error %q should name the missing field", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error %q must not leak credential values", err)
	}
}

func TestRenderBody_EscapesValuesForContentType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		want        string
	}{
		{"json", "application/json", `p=a\"b&c`},
		{"form", "application/x-www-form-urlencoded", "p=a%22b%26c"},
		{"other", "text/plain", `p=a"b&c`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			fields := map[string]string{"pw": `a"b&c`}

			// Act
			got, err := credential.RenderBody("p={{pw}}", fields, tc.contentType)

			// Assert
			if err != nil {
				t.Fatalf("RenderBody: %v", err)
			}
			if got != tc.want {
				t.Errorf("RenderBody = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlaceholders_ListsDistinctNamesInOrder(t *testing.T) {
	// Act
	got := credential.Placeholders("{{b}} {{ a }} {{b}}")

	// Assert
	if strings.Join(got, ",") != "b,a" {
		t.Errorf("Placeholders = %v, want [b a]", got)
	}
}

// ---- Extract ---------------------------------------------------------------

func TestExtract_Succeeds(t *testing.T) {
	cases := []struct {
		name string
		resp credential.Response
		ex   model.Extraction
		want string
	}{
		{
			name: "nested json path",
			resp: credential.Response{Body: []byte(`{"data":{"access_token":"tok-1"}}`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "data.access_token"},
			want: "tok-1",
		},
		{
			name: "top-level json field",
			resp: credential.Response{Body: []byte(`{"token":"tok-2"}`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "token"},
			want: "tok-2",
		},
		{
			name: "array index segment",
			resp: credential.Response{Body: []byte(`{"tokens":[{"v":"first"},{"v":"second"}]}`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "tokens.1.v"},
			want: "second",
		},
		{
			name: "header",
			resp: credential.Response{Header: http.Header{"X-Auth-Token": {"tok-3"}}},
			ex:   model.Extraction{From: model.ExtractFromHeader, Path: "x-auth-token"},
			want: "tok-3",
		},
		{
			name: "header with prefix stripped",
			resp: credential.Response{Header: http.Header{"Authorization": {"Bearer tok-4"}}},
			ex:   model.Extraction{From: model.ExtractFromHeader, Path: "Authorization", Prefix: "Bearer "},
			want: "tok-4",
		},
		{
			name: "body with prefix stripped",
			resp: credential.Response{Body: []byte(`{"auth":"Token tok-5"}`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "auth", Prefix: "Token "},
			want: "tok-5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, err := credential.Extract(tc.resp, tc.ex)

			// Assert
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if got != tc.want {
				t.Errorf("Extract = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtract_Fails(t *testing.T) {
	cases := []struct {
		name string
		resp credential.Response
		ex   model.Extraction
	}{
		{
			name: "missing path",
			resp: credential.Response{Body: []byte(`{"data":{}}`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "data.access_token"},
		},
		{
			name: "non-json body",
			resp: credential.Response{Body: []byte(`<html>login page</html>`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "token"},
		},
		{
			name: "non-string value",
			resp: credential.Response{Body: []byte(`{"token":{"nested":true}}`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "token"},
		},
		{
			name: "empty value",
			resp: credential.Response{Body: []byte(`{"token":""}`)},
			ex:   model.Extraction{From: model.ExtractFromBody, Path: "token"},
		},
		{
			name: "missing header",
			resp: credential.Response{Header: http.Header{}},
			ex:   model.Extraction{From: model.ExtractFromHeader, Path: "X-Auth-Token"},
		},
		{
			name: "prefix required but absent",
			resp: credential.Response{Header: http.Header{"Authorization": {"Basic abc"}}},
			ex:   model.Extraction{From: model.ExtractFromHeader, Path: "Authorization", Prefix: "Bearer "},
		},
		{
			name: "unknown source",
			resp: credential.Response{Body: []byte(`{"token":"t"}`)},
			ex:   model.Extraction{From: "cookie", Path: "token"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, err := credential.Extract(tc.resp, tc.ex)

			// Assert
			if err == nil {
				t.Errorf("Extract = %q, want an error", got)
			}
		})
	}
}

func TestExtract_ErrorDoesNotEchoResponseBody(t *testing.T) {
	// Arrange
	resp := credential.Response{Body: []byte(`{"refresh":"secret-refresh-value"}`)}
	ex := model.Extraction{From: model.ExtractFromBody, Path: "token"}

	// Act
	_, err := credential.Extract(resp, ex)

	// Assert
	if err == nil || strings.Contains(err.Error(), "secret-refresh-value") {
		t.Errorf("Extract error = %v, want an error that does not echo the body", err)
	}
}
