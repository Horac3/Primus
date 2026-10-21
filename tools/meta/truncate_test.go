package main

import (
	"strings"
	"testing"
)

func fileDiff(name string, bodyLines int) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + name + " b/" + name + "\n--- a/" + name + "\n+++ b/" + name + "\n@@ -1 +1 @@\n")
	for i := 0; i < bodyLines; i++ {
		b.WriteString("+line of added content number that takes some room\n")
	}
	return b.String()
}

func TestTruncateDiff_SmallDiffPassesThrough(t *testing.T) {
	diff := fileDiff("internal/engine/a.go", 3)
	got, truncated := TruncateDiff(diff, []FileChange{{Status: "M", Path: "internal/engine/a.go", Added: 3}}, 10000)
	if truncated {
		t.Error("truncated = true for a diff under the cap")
	}
	if got != diff {
		t.Error("diff under the cap was modified")
	}
}

func TestTruncateDiff_LargeDiffIsReducedToFitTheCap(t *testing.T) {
	diff := fileDiff("internal/engine/a.go", 200) + fileDiff("internal/store/b.go", 200)
	changes := []FileChange{
		{Status: "M", Path: "internal/engine/a.go", Added: 200},
		{Status: "A", Path: "internal/store/b.go", Added: 200},
	}
	const limit = 1500
	got, truncated := TruncateDiff(diff, changes, limit)
	if !truncated {
		t.Fatal("truncated = false for a diff over the cap")
	}
	if len(got) > limit {
		t.Errorf("len = %d, want <= %d", len(got), limit)
	}
	for _, want := range []string{"internal/engine/a.go (+200/-0)", "internal/store/b.go (+200/-0)", "truncated"} {
		if !strings.Contains(got, want) {
			t.Errorf("truncated diff missing %q", want)
		}
	}
}

func TestTruncateDiff_EveryFileKeepsSomeHunk(t *testing.T) {
	diff := fileDiff("internal/engine/a.go", 300) + fileDiff("internal/store/b.go", 300)
	changes := []FileChange{
		{Status: "M", Path: "internal/engine/a.go", Added: 300},
		{Status: "M", Path: "internal/store/b.go", Added: 300},
	}
	got, _ := TruncateDiff(diff, changes, 2000)
	for _, marker := range []string{"diff --git a/internal/engine/a.go", "diff --git a/internal/store/b.go"} {
		if !strings.Contains(got, marker) {
			t.Errorf("truncated diff lost the section %q", marker)
		}
	}
}

func TestTruncateDiff_ManyFilesStillFitTheCap(t *testing.T) {
	var diff strings.Builder
	var changes []FileChange
	for i := 0; i < 80; i++ {
		name := "internal/engine/file" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + ".go"
		diff.WriteString(fileDiff(name, 20))
		changes = append(changes, FileChange{Status: "M", Path: name, Added: 20})
	}
	const limit = 1200
	got, truncated := TruncateDiff(diff.String(), changes, limit)
	if !truncated || len(got) > limit {
		t.Errorf("truncated = %v, len = %d, want true and <= %d", truncated, len(got), limit)
	}
}
