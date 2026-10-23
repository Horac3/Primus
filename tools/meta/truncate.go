package main

import (
	"fmt"
	"strings"
)

const truncationMarker = "[... truncated]\n"

// TruncateDiff returns diff unchanged when it fits in limit bytes. Otherwise it
// returns a stat header listing every file with its line counts, followed by
// each file's section cut to an equal share of the remaining budget. The result
// never exceeds limit.
func TruncateDiff(diff string, changes []FileChange, limit int) (string, bool) {
	if len(diff) <= limit {
		return diff, false
	}

	var out strings.Builder
	out.WriteString("# diff truncated to fit the model input cap; files changed:\n")
	for _, c := range changes {
		fmt.Fprintf(&out, "# %s (+%d/-%d)\n", c.Path, c.Added, c.Deleted)
	}

	sections := splitSections(diff)
	budget := limit - out.Len()
	if budget > 0 && len(sections) > 0 {
		share := budget / len(sections)
		for _, section := range sections {
			out.WriteString(cutSection(section, share))
		}
	}
	result := out.String()
	if len(result) > limit {
		result = result[:limit]
	}
	return result, true
}

// splitSections cuts a unified diff into one chunk per file, each starting at
// its "diff --git" line.
func splitSections(diff string) []string {
	var sections []string
	for _, part := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(part, "diff --git ") || len(sections) == 0 {
			sections = append(sections, "")
		}
		sections[len(sections)-1] += part
	}
	return sections
}

// cutSection keeps whole lines of section up to size bytes and marks the cut.
func cutSection(section string, size int) string {
	if len(section) <= size {
		return section
	}
	room := size - len(truncationMarker)
	if room <= 0 {
		return ""
	}
	cut := strings.LastIndex(section[:room], "\n") + 1
	return section[:cut] + truncationMarker
}
