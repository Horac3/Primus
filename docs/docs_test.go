package docs_test

import (
	"strings"
	"testing"

	"github.com/Horac3/Primus/docs"
)

// ============================================================
// List — every reference doc, titled
// ============================================================

func TestList_CoversAllModes(t *testing.T) {
	// Arrange — nothing to set up; the embedded docs are the fixture

	// Act
	list := docs.List()

	// Assert — all four modes plus the three guides, each with a title
	want := []string{"load", "burst", "soak", "auth-probe", "global-auth", "assertions", "cli"}
	if len(list) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(list), len(want), list)
	}
	seen := map[string]string{}
	for _, d := range list {
		seen[d.Slug] = d.Title
	}
	for _, slug := range want {
		title, ok := seen[slug]
		if !ok {
			t.Errorf("slug %q missing from list", slug)
			continue
		}
		if title == "" || title == slug {
			t.Errorf("slug %q has no real title (got %q)", slug, title)
		}
	}
}

// ============================================================
// Render — markdown becomes HTML (tables included)
// ============================================================

func TestRender_RealDoc_RendersHeadingAndTables(t *testing.T) {
	// Arrange — load.md is known to contain a heading and tables

	// Act
	html, err := docs.Render("load")

	// Assert
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(html), "<h1") {
		t.Errorf("rendered doc missing <h1>; got %.200s…", html)
	}
	if !strings.Contains(string(html), "<table") {
		t.Errorf("rendered doc missing <table> (pipe tables must render); got %.200s…", html)
	}
}

func TestRender_UnknownSlug_ReturnsError(t *testing.T) {
	for _, slug := range []string{"nope", "../load", "a/b", ""} {
		// Arrange — slug above

		// Act
		_, err := docs.Render(slug)

		// Assert — error, never a panic or empty page
		if err == nil {
			t.Errorf("Render(%q) = nil error, want error", slug)
		}
	}
}

func TestRender_Twice_ReturnsSameOutput(t *testing.T) {
	// Arrange — prime the cache

	// Act
	first, err1 := docs.Render("burst")
	second, err2 := docs.Render("burst")

	// Assert
	if err1 != nil || err2 != nil {
		t.Fatalf("Render errors: %v, %v", err1, err2)
	}
	if first != second {
		t.Error("second render differs — cache must return identical output")
	}
}
