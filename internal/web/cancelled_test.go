package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
)

func TestRunResult_CancelledRequestsNote(t *testing.T) {
	cases := []struct {
		name    string
		run     model.Run
		want    string
		notWant string
	}{
		{
			name:    "stopped run counts the abandoned requests",
			run:     model.Run{ID: "c1", Outcome: model.OutcomePass, Stopped: true, Summary: model.RunSummary{Cancelled: 12}},
			want:    "12 in-flight requests abandoned on stop",
			notWant: "end of the run",
		},
		{
			name:    "duration deadline names the deadline, not a stop",
			run:     model.Run{ID: "c2", Outcome: model.OutcomePass, Summary: model.RunSummary{Cancelled: 3}},
			want:    "3 in-flight requests cut off at the end of the run",
			notWant: "abandoned on stop",
		},
		{
			name:    "nothing cancelled shows no note",
			run:     model.Run{ID: "c3", Outcome: model.OutcomePass, Stopped: true},
			notWant: "in-flight",
		},
	}
	for _, page := range []string{"run-result-fragment.html", "run-page.html"} {
		for _, tc := range cases {
			t.Run(page+"/"+tc.name, func(t *testing.T) {
				// Arrange
				var output bytes.Buffer
				data := runResultTemplateData{Run: tc.run, InsightView: insightFragmentData{RunID: tc.run.ID}}

				// Act
				err := loadEmbeddedTemplates().ExecuteTemplate(&output, page, data)

				// Assert
				if err != nil {
					t.Fatalf("execute %s: %v", page, err)
				}
				body := output.String()
				if tc.want != "" && !strings.Contains(body, tc.want) {
					t.Errorf("%s missing %q", page, tc.want)
				}
				if tc.notWant != "" && strings.Contains(body, tc.notWant) {
					t.Errorf("%s should not contain %q", page, tc.notWant)
				}
			})
		}
	}
}

func TestLoginSummary_CancelledLogins(t *testing.T) {
	cases := []struct {
		name    string
		login   model.LoginSummary
		want    string
		notWant string
	}{
		{"cancelled logins are shown apart from failures", model.LoginSummary{Attempted: 4, Failed: 1, Cancelled: 3}, "3 interrupted by stop", ""},
		{"no cancelled logins shows no note", model.LoginSummary{Attempted: 4, Failed: 1}, "", "interrupted by stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			login := tc.login
			run := model.Run{Login: &login}

			// Act
			err := loadEmbeddedTemplates().ExecuteTemplate(&output, "login-summary", run)

			// Assert
			if err != nil {
				t.Fatalf("execute login-summary: %v", err)
			}
			body := output.String()
			if tc.want != "" && !strings.Contains(body, tc.want) {
				t.Errorf("login summary missing %q:\n%s", tc.want, body)
			}
			if tc.notWant != "" && strings.Contains(body, tc.notWant) {
				t.Errorf("login summary should not contain %q:\n%s", tc.notWant, body)
			}
		})
	}
}
