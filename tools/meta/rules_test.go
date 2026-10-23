package main

import (
	"strings"
	"testing"
)

func TestScopeForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"internal/engine/engine.go", "engine"},
		{"internal/model/run.go", "model"},
		{"internal/store/store.go", "store"},
		{"internal/web/runs.go", "web"},
		{"internal/web/templates/pools-panel.html", "ui"},
		{"internal/web/static/app.css", "ui"},
		{"internal/insight/analyzer.go", "insight"},
		{"internal/author/generator.go", "author"},
		{"internal/credential/pool.go", "auth"},
		{"internal/importer/importer.go", "importer"},
		{"cmd/primus/main.go", "cli"},
		{"docs/load.md", "docs"},
		{"deploy/Dockerfile", "docker"},
		{"deploy/docker-compose.yml", "docker"},
		{".dockerignore", "docker"},
		{".github/workflows/ci.yml", "ci"},
		{"scripts/race.sh", "ci"},
		{"tools/meta/main.go", "tools"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := scopeForPath(tt.path); got != tt.want {
				t.Errorf("scopeForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestRulesDraft_ScopeIsTheDirectoryWithMostChangedLines(t *testing.T) {
	draft := RulesDraft([]FileChange{
		{Status: "M", Path: "internal/engine/engine.go", Added: 40, Deleted: 10},
		{Status: "M", Path: "docs/load.md", Added: 3, Deleted: 1},
	})
	if !strings.HasPrefix(draft, "chore(engine): ") && !strings.HasPrefix(draft, "feat(engine): ") {
		t.Errorf("draft header = %q, want engine scope", strings.SplitN(draft, "\n", 2)[0])
	}
}

func TestRulesDraft_TypeFromChangeKind(t *testing.T) {
	tests := []struct {
		name    string
		changes []FileChange
		want    string
	}{
		{"only tests", []FileChange{{Status: "M", Path: "internal/engine/engine_test.go", Added: 5}}, "test(engine): "},
		{"only docs", []FileChange{{Status: "M", Path: "docs/load.md", Added: 5}}, "docs(docs): "},
		{"added source file", []FileChange{{Status: "A", Path: "internal/engine/ramp.go", Added: 50}}, "feat(engine): "},
		{"modified source file", []FileChange{{Status: "M", Path: "internal/engine/engine.go", Added: 5, Deleted: 2}}, "chore(engine): "},
		{"added test only is still test", []FileChange{{Status: "A", Path: "internal/engine/ramp_test.go", Added: 20}}, "test(engine): "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := RulesDraft(tt.changes)
			if !strings.HasPrefix(draft, tt.want) {
				t.Errorf("draft header = %q, want prefix %q", strings.SplitN(draft, "\n", 2)[0], tt.want)
			}
		})
	}
}

func TestRulesDraft_BulletsAndTestsLine(t *testing.T) {
	draft := RulesDraft([]FileChange{
		{Status: "A", Path: "internal/engine/ramp.go", Added: 50},
		{Status: "M", Path: "internal/engine/engine.go", Added: 5, Deleted: 2},
		{Status: "D", Path: "internal/engine/old.go", Deleted: 9},
		{Status: "M", Path: "internal/engine/engine_test.go", Added: 7},
	})
	for _, want := range []string{
		"- add internal/engine/ramp.go (+50/-0)",
		"- update internal/engine/engine.go (+5/-2)",
		"- delete internal/engine/old.go (+0/-9)",
		"Tests: internal/engine/engine_test.go",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
}

func TestRulesDraft_NoTestFilesSaysNoneChanged(t *testing.T) {
	draft := RulesDraft([]FileChange{{Status: "M", Path: "docs/load.md", Added: 1}})
	if !strings.Contains(draft, "Tests: none changed") {
		t.Errorf("draft = %q, want a \"Tests: none changed\" line", draft)
	}
}

func TestRulesDraft_AlwaysPassesValidate(t *testing.T) {
	inputs := [][]FileChange{
		{{Status: "M", Path: "internal/engine/engine.go", Added: 1}},
		{{Status: "A", Path: "tools/meta/main.go", Added: 10}, {Status: "A", Path: "tools/meta/main_test.go", Added: 10}},
		{{Status: "D", Path: "deploy/Dockerfile", Deleted: 3}},
		{{Status: "M", Path: "unknown/place.txt", Added: 1}},
		{{Status: "R", Path: "docs/old.md", Added: 0}},
		{{Status: "M", Path: "internal/web/templates/a.html", Added: 1}, {Status: "M", Path: "scripts/x.sh", Added: 1}},
	}
	for i, changes := range inputs {
		draft := RulesDraft(changes)
		if err := Validate(draft); err != nil {
			t.Errorf("input %d: rules draft failed Validate: %v\n%s", i, err, draft)
		}
	}
}
