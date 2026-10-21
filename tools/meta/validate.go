package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const maxHeaderLen = 72

var allowedTypes = []string{"feat", "fix", "test", "refactor", "docs", "chore", "build", "ci", "perf", "style"}

var allowedScopes = []string{"engine", "model", "store", "web", "ui", "insight", "author", "auth", "importer", "cli", "docs", "docker", "ci", "tools"}

var headerPattern = regexp.MustCompile(`^([a-z]+)\(([a-z]+)\)(!?): (.+)$`)

// Validate reports why msg is not a Primus-format Conventional Commit: a
// `type(scope): subject` header, a blank line, bulleted body and a final
// `Tests:` line.
func Validate(msg string) error {
	msg = strings.TrimRight(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
	if strings.TrimSpace(msg) == "" {
		return errors.New("message is empty")
	}
	lines := strings.Split(msg, "\n")
	header := lines[0]
	match := headerPattern.FindStringSubmatch(header)
	if match == nil {
		return fmt.Errorf("header %q is not type(scope): subject", header)
	}
	if !contains(allowedTypes, match[1]) {
		return fmt.Errorf("type %q is not one of %s", match[1], strings.Join(allowedTypes, ", "))
	}
	if !contains(allowedScopes, match[2]) {
		return fmt.Errorf("scope %q is not one of %s", match[2], strings.Join(allowedScopes, ", "))
	}
	subject := match[4]
	if subject[0] < 'a' || subject[0] > 'z' {
		return fmt.Errorf("subject %q must start with a lowercase letter", subject)
	}
	if strings.HasSuffix(subject, ".") {
		return fmt.Errorf("subject %q must not end with a period", subject)
	}
	if len(header) > maxHeaderLen {
		return fmt.Errorf("header is %d characters, the limit is %d", len(header), maxHeaderLen)
	}
	if len(lines) < 2 || lines[1] != "" {
		return errors.New("header must be followed by a blank line")
	}

	last := len(lines) - 1
	if !strings.HasPrefix(lines[last], "Tests:") {
		return errors.New("the last line must start with \"Tests:\"")
	}
	if strings.TrimSpace(strings.TrimPrefix(lines[last], "Tests:")) == "" {
		return errors.New("the \"Tests:\" line must say what the tests cover")
	}
	bullets := 0
	for _, line := range lines[2:last] {
		if strings.HasPrefix(line, "- ") && strings.TrimSpace(line[2:]) != "" {
			bullets++
		}
	}
	if bullets == 0 {
		return errors.New("body needs at least one \"- \" bullet")
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
