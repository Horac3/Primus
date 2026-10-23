package main

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// FileChange is one staged file: its git status letter (A, M, D, R, ...) and
// the added and deleted line counts from `git diff --cached --numstat`.
type FileChange struct {
	Status  string
	Path    string
	Added   int
	Deleted int
}

// fallbackScope is used for paths that match no rule, such as go.mod.
const fallbackScope = "ci"

// scopeRules map path prefixes to commit scopes. Longer prefixes come first so
// internal/web/templates wins over internal/web.
var scopeRules = []struct{ prefix, scope string }{
	{"internal/web/templates/", "ui"},
	{"internal/web/static/", "ui"},
	{"internal/engine/", "engine"},
	{"internal/model/", "model"},
	{"internal/store/", "store"},
	{"internal/web/", "web"},
	{"internal/insight/", "insight"},
	{"internal/author/", "author"},
	{"internal/credential/", "auth"},
	{"internal/importer/", "importer"},
	{"cmd/primus/", "cli"},
	{"docs/", "docs"},
	{"deploy/", "docker"},
	{".dockerignore", "docker"},
	{".github/", "ci"},
	{"scripts/", "ci"},
	{"tools/", "tools"},
}

func scopeForPath(p string) string {
	for _, rule := range scopeRules {
		if strings.HasPrefix(p, rule.prefix) {
			return rule.scope
		}
	}
	return fallbackScope
}

func isTestFile(p string) bool { return strings.HasSuffix(p, "_test.go") }

// RulesDraft builds a commit message from staged paths alone: the scope is the
// one with the most changed lines, the type comes from the kind of change.
func RulesDraft(changes []FileChange) string {
	scope := dominantScope(changes)
	commitType := commitTypeFor(changes)

	subject := fmt.Sprintf("update %d files", len(changes))
	if len(changes) == 1 {
		subject = verbFor(changes[0].Status) + " " + path.Base(changes[0].Path)
	}
	if header := fmt.Sprintf("%s(%s): %s", commitType, scope, subject); len(header) > maxHeaderLen {
		subject = fmt.Sprintf("update %d files", len(changes))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s(%s): %s\n\n", commitType, scope, subject)
	var tests []string
	for _, c := range changes {
		fmt.Fprintf(&b, "- %s %s (+%d/-%d)\n", verbFor(c.Status), c.Path, c.Added, c.Deleted)
		if isTestFile(c.Path) {
			tests = append(tests, c.Path)
		}
	}
	if len(tests) == 0 {
		b.WriteString("\nTests: none changed")
	} else {
		b.WriteString("\nTests: " + strings.Join(tests, ", "))
	}
	return b.String()
}

func dominantScope(changes []FileChange) string {
	lines := map[string]int{}
	for _, c := range changes {
		lines[scopeForPath(c.Path)] += c.Added + c.Deleted
	}
	scopes := make([]string, 0, len(lines))
	for scope := range lines {
		scopes = append(scopes, scope)
	}
	// Ties break alphabetically so the draft is deterministic.
	sort.Slice(scopes, func(i, j int) bool {
		if lines[scopes[i]] != lines[scopes[j]] {
			return lines[scopes[i]] > lines[scopes[j]]
		}
		return scopes[i] < scopes[j]
	})
	if len(scopes) == 0 {
		return fallbackScope
	}
	return scopes[0]
}

func commitTypeFor(changes []FileChange) string {
	allTests, allDocs, addedSource := true, true, false
	for _, c := range changes {
		test := isTestFile(c.Path)
		allTests = allTests && test
		allDocs = allDocs && scopeForPath(c.Path) == "docs"
		if c.Status == "A" && !test {
			addedSource = true
		}
	}
	switch {
	case len(changes) == 0:
		return "chore"
	case allTests:
		return "test"
	case allDocs:
		return "docs"
	case addedSource:
		return "feat"
	}
	return "chore"
}

func verbFor(status string) string {
	switch status {
	case "A":
		return "add"
	case "D":
		return "delete"
	case "R":
		return "rename"
	}
	return "update"
}
