package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const systemPrompt = `You write git commit messages for the Primus Go project. Reply with JSON only.
type is one of: feat, fix, test, refactor, docs, chore, build, ci, perf, style.
scope is one of: engine, model, store, web, ui, insight, author, auth, importer, cli, docs, docker, ci, tools.
subject is imperative, lowercase, no trailing period, and keeps the whole header under 72 characters.
bullets has one short line per specific change, without a leading dash.
tests says what the tests cover, or "none changed".
Describe only what the diff shows.`

// OllamaGenerator drafts a commit message with a local Ollama model.
type OllamaGenerator struct {
	URL    string
	Model  string
	Client *http.Client
}

type modelDraft struct {
	Type    string   `json:"type"`
	Scope   string   `json:"scope"`
	Subject string   `json:"subject"`
	Bullets []string `json:"bullets"`
	Tests   string   `json:"tests"`
}

func draftSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":    str,
			"scope":   str,
			"subject": str,
			"bullets": map[string]any{"type": "array", "items": str},
			"tests":   str,
		},
		"required": []string{"type", "scope", "subject", "bullets", "tests"},
	}
}

// Generate asks the model for a draft and renders it as a commit message. The
// result is not validated; the caller decides whether to trust it.
func (g OllamaGenerator) Generate(ctx context.Context, diff string, changes []FileChange) (string, error) {
	var files strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&files, "%s %s (+%d/-%d)\n", c.Status, c.Path, c.Added, c.Deleted)
	}
	body, err := json.Marshal(map[string]any{
		"model": g.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": "Files:\n" + files.String() + "\nDiff:\n" + diff},
		},
		"stream":     false,
		"format":     draftSchema(),
		"options":    map[string]any{"temperature": 0},
		"keep_alive": "2m",
	})
	if err != nil {
		return "", fmt.Errorf("marshal Ollama request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.URL, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create Ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call Ollama: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read Ollama response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama returned %s", resp.Status)
	}

	var chat struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &chat); err != nil {
		return "", fmt.Errorf("decode Ollama response: %w", err)
	}
	var draft modelDraft
	if err := json.Unmarshal([]byte(chat.Message.Content), &draft); err != nil {
		return "", fmt.Errorf("model reply is not the requested JSON: %w", err)
	}
	return renderDraft(draft), nil
}

func renderDraft(d modelDraft) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s(%s): %s\n\n", strings.TrimSpace(d.Type), strings.TrimSpace(d.Scope), strings.TrimSpace(d.Subject))
	for _, bullet := range d.Bullets {
		fmt.Fprintf(&b, "- %s\n", strings.TrimPrefix(strings.TrimSpace(bullet), "- "))
	}
	fmt.Fprintf(&b, "\nTests: %s", strings.TrimSpace(d.Tests))
	return b.String()
}
