package main

import (
	"strings"
	"testing"
)

const validDraft = "feat(engine): add burst ramp\n\n- add ramp to engine.RunBurst\n- cap ramp at concurrency\n\nTests: TestRunBurst_Ramp covers the ramp."

func TestValidate_AcceptsWellFormedDraft(t *testing.T) {
	if err := Validate(validDraft); err != nil {
		t.Fatalf("Validate(valid) = %v, want nil", err)
	}
}

func TestValidate_AcceptsEveryAllowedScope(t *testing.T) {
	for _, scope := range []string{"engine", "model", "store", "web", "ui", "insight", "author", "auth", "importer", "cli", "docs", "docker", "ci", "tools"} {
		msg := "chore(" + scope + "): tidy things\n\n- tidy things\n\nTests: none."
		if err := Validate(msg); err != nil {
			t.Errorf("scope %q rejected: %v", scope, err)
		}
	}
}

func TestValidate_RejectsViolations(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{"empty", "", "empty"},
		{"no scope", "feat: add thing\n\n- a\n\nTests: x", "header"},
		{"unknown type", "wibble(engine): add thing\n\n- a\n\nTests: x", "type"},
		{"unknown scope", "feat(nope): add thing\n\n- a\n\nTests: x", "scope"},
		{"uppercase subject", "feat(engine): Add thing\n\n- a\n\nTests: x", "lowercase"},
		{"trailing period", "feat(engine): add thing.\n\n- a\n\nTests: x", "period"},
		{"header too long", "feat(engine): " + strings.Repeat("a", 70) + "\n\n- a\n\nTests: x", "72"},
		{"no blank line after header", "feat(engine): add thing\n- a\n\nTests: x", "blank"},
		{"no bullets", "feat(engine): add thing\n\nplain prose\n\nTests: x", "bullet"},
		{"no tests line", "feat(engine): add thing\n\n- a", "Tests:"},
		{"empty bullet", "feat(engine): add thing\n\n- \n\nTests: x", "bullet"},
		{"empty tests line", "feat(engine): add thing\n\n- a\n\nTests:  ", "Tests:"},
		{"tests line not last", "feat(engine): add thing\n\n- a\n\nTests: x\n- b", "Tests:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.msg)
			if err == nil {
				t.Fatalf("Validate accepted %q", tt.msg)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}
