package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGit struct {
	diff    string
	changes []FileChange
	err     error
}

func (f fakeGit) StagedDiff(context.Context) (string, error)    { return f.diff, f.err }
func (f fakeGit) Changes(context.Context) ([]FileChange, error) { return f.changes, f.err }

var engineGit = fakeGit{
	diff:    fileDiff("internal/engine/ramp.go", 5),
	changes: []FileChange{{Status: "A", Path: "internal/engine/ramp.go", Added: 5}},
}

func noEnv(string) string { return "" }

func runMeta(t *testing.T, git Git, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, noEnv, &out, &errOut, git)
	return code, out.String(), errOut.String()
}

// ollamaServer answers /api/chat with content as the model message and counts requests.
func ollamaServer(t *testing.T, content string, hits *atomic.Int32, lastBody *[]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if lastBody != nil {
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(r.Body)
			*lastBody = buf.Bytes()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": content}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

const goodModelJSON = `{"type":"feat","scope":"engine","subject":"add burst ramp","bullets":["add ramp to the burst loop","cap ramp at concurrency"],"tests":"TestRamp covers the ramp."}`

func TestRun_UsesOllamaDraftWhenValid(t *testing.T) {
	var hits atomic.Int32
	var body []byte
	srv := ollamaServer(t, goodModelJSON, &hits, &body)

	code, stdout, stderr := runMeta(t, engineGit, "--url", srv.URL, "--model", "tiny")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if err := Validate(stdout); err != nil {
		t.Fatalf("stdout is not a valid draft: %v\n%s", err, stdout)
	}
	if !strings.HasPrefix(stdout, "feat(engine): add burst ramp\n") {
		t.Errorf("stdout = %q, want the model's draft", stdout)
	}
	if !strings.Contains(stderr, "generator: ollama") {
		t.Errorf("stderr = %q, want it to name the ollama generator", stderr)
	}

	var req struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if req.Model != "tiny" || req.Stream {
		t.Errorf("request model = %q, stream = %v, want tiny and false", req.Model, req.Stream)
	}
	if !strings.Contains(string(body), "internal/engine/ramp.go") {
		t.Error("request does not carry the staged diff")
	}
}

func TestRun_FallsBackToRules(t *testing.T) {
	var hits atomic.Int32
	wantRules := RulesDraft(engineGit.changes)

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	tests := []struct {
		name string
		url  string
		args []string
	}{
		{"ollama absent", closedURL, nil},
		{"server error", failing.URL, nil},
		{"slower than the time cap", slow.URL, []string{"--timeout", "100ms"}},
		{"not json", ollamaServer(t, "just some prose", &hits, nil).URL, nil},
		{"unknown scope", ollamaServer(t, strings.Replace(goodModelJSON, `"engine"`, `"nope"`, 1), &hits, nil).URL, nil},
		{"no bullets", ollamaServer(t, `{"type":"feat","scope":"engine","subject":"add thing","bullets":[],"tests":"x"}`, &hits, nil).URL, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--url", tt.url, "--model", "tiny"}, tt.args...)
			code, stdout, stderr := runMeta(t, engineGit, args...)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr)
			}
			if stdout != wantRules+"\n" {
				t.Errorf("stdout = %q, want the rules draft", stdout)
			}
			if err := Validate(stdout); err != nil {
				t.Errorf("printed draft is invalid: %v", err)
			}
			if !strings.Contains(stderr, "generator: rules") {
				t.Errorf("stderr = %q, want it to name the rules generator", stderr)
			}
		})
	}
}

func TestRun_NoModelUsesRulesWithoutNetwork(t *testing.T) {
	code, stdout, stderr := runMeta(t, engineGit)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if stdout != RulesDraft(engineGit.changes)+"\n" {
		t.Errorf("stdout = %q, want the rules draft", stdout)
	}
	if !strings.Contains(stderr, "generator: rules") {
		t.Errorf("stderr = %q, want the rules generator", stderr)
	}
}

func TestRun_EmptyDiffExitsNonZeroAndCallsNoModel(t *testing.T) {
	var hits atomic.Int32
	srv := ollamaServer(t, goodModelJSON, &hits, nil)

	code, stdout, stderr := runMeta(t, fakeGit{}, "--url", srv.URL, "--model", "tiny")
	if code == 0 {
		t.Error("exit = 0 for an empty staged diff")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if !strings.Contains(stderr, "no staged changes") {
		t.Errorf("stderr = %q, want a no staged changes message", stderr)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("model received %d requests, want 0", n)
	}
}

func TestRun_ReportsTruncation(t *testing.T) {
	var hits atomic.Int32
	var body []byte
	srv := ollamaServer(t, goodModelJSON, &hits, &body)
	big := fakeGit{
		diff:    fileDiff("internal/engine/ramp.go", 500),
		changes: []FileChange{{Status: "A", Path: "internal/engine/ramp.go", Added: 500}},
	}

	code, _, stderr := runMeta(t, big, "--url", srv.URL, "--model", "tiny", "--max-bytes", "1000")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "truncated") {
		t.Errorf("stderr = %q, want a truncation note", stderr)
	}
	if !strings.Contains(string(body), "truncated") {
		t.Error("request body does not carry the truncated diff")
	}
}

func TestRun_GitFailureExitsNonZero(t *testing.T) {
	code, _, stderr := runMeta(t, fakeGit{err: errors.New("not a git repository")})
	if code == 0 {
		t.Error("exit = 0 when git fails")
	}
	if !strings.Contains(stderr, "not a git repository") {
		t.Errorf("stderr = %q, want the git error", stderr)
	}
}

func TestRun_UnknownFlagExitsTwo(t *testing.T) {
	if code, _, _ := runMeta(t, engineGit, "--nope"); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestRun_EnvSuppliesModelAndURL(t *testing.T) {
	var hits atomic.Int32
	srv := ollamaServer(t, goodModelJSON, &hits, nil)
	env := map[string]string{"PRIMUS_LLM_URL": srv.URL, "PRIMUS_LLM_MODEL": "tiny"}

	var out, errOut bytes.Buffer
	code := run(context.Background(), nil, func(k string) string { return env[k] }, &out, &errOut, engineGit)
	if code != 0 || hits.Load() != 1 {
		t.Fatalf("exit = %d, hits = %d, stderr = %s", code, hits.Load(), errOut.String())
	}
}

func TestRealGit_ReadsStagedChangesWithoutChangingTheIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "core.autocrlf=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	writeFile(t, dir, "docs/a.md", "one\n")
	git("add", "docs/a.md")
	git("commit", "-q", "-m", "init")
	writeFile(t, dir, "docs/a.md", "one\ntwo\n")
	writeFile(t, dir, "internal/engine/ramp.go", "package engine\n")
	git("add", "docs/a.md", "internal/engine/ramp.go")

	indexBefore, headBefore := git("diff", "--cached"), git("rev-parse", "HEAD")

	var out, errOut bytes.Buffer
	code := run(context.Background(), nil, noEnv, &out, &errOut, RealGit{Dir: dir})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if err := Validate(out.String()); err != nil {
		t.Fatalf("draft invalid: %v\n%s", err, out.String())
	}
	for _, want := range []string{"add internal/engine/ramp.go (+1/-0)", "update docs/a.md (+1/-0)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("draft missing %q:\n%s", want, out.String())
		}
	}
	if git("diff", "--cached") != indexBefore || git("rev-parse", "HEAD") != headBefore {
		t.Error("the index or HEAD changed")
	}
}
