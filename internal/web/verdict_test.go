package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/model"
)

func TestRunResultFragment_VerdictText(t *testing.T) {
	cases := []struct {
		name    string
		run     model.Run
		want    string
		notWant string
	}{
		{
			name: "zero completed requests names the reason",
			run:  model.Run{ID: "r1", Outcome: model.OutcomeFailNoRequests},
			want: "FAILED — no requests completed",
		},
		{
			name: "stopped run says it was stopped early",
			run:  model.Run{ID: "r2", Outcome: model.OutcomePass, Passed: true, Stopped: true},
			want: "Stopped early",
		},
		{
			name:    "finished run has no stopped note",
			run:     model.Run{ID: "r3", Outcome: model.OutcomePass, Passed: true},
			notWant: "Stopped early",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			data := runResultTemplateData{Run: tc.run, InsightView: insightFragmentData{RunID: tc.run.ID}}

			// Act
			err := loadEmbeddedTemplates().ExecuteTemplate(&output, "run-result-fragment.html", data)

			// Assert
			if err != nil {
				t.Fatalf("execute run-result-fragment: %v", err)
			}
			body := output.String()
			if tc.want != "" && !strings.Contains(body, tc.want) {
				t.Errorf("result card missing %q:\n%s", tc.want, body)
			}
			if tc.notWant != "" && strings.Contains(body, tc.notWant) {
				t.Errorf("result card should not contain %q:\n%s", tc.notWant, body)
			}
		})
	}
}
