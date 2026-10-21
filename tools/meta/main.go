// Command meta drafts a commit message from the staged diff. It is developer
// tooling: it prints a draft and never commits or changes the index.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	defaultURL      = "http://localhost:11434"
	defaultTimeout  = 30 * time.Second
	defaultMaxBytes = 24000
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr, RealGit{}))
}

// run prints a draft commit message for the staged changes to stdout and says
// which generator produced it on stderr. It returns the process exit code.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, git Git) int {
	fs := flag.NewFlagSet("meta", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", orDefault(getenv("PRIMUS_LLM_URL"), defaultURL), "Ollama base URL")
	model := fs.String("model", getenv("PRIMUS_LLM_MODEL"), "Ollama model; empty uses the rules generator")
	timeout := fs.Duration("timeout", defaultTimeout, "time cap for the Ollama call")
	maxBytes := fs.Int("max-bytes", defaultMaxBytes, "diff size cap sent to the model")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	diff, err := git.StagedDiff(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if diff == "" {
		fmt.Fprintln(stderr, "error: no staged changes (git add the files first)")
		return 1
	}
	changes, err := git.Changes(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	draft, reason := "", "no model configured (set --model or PRIMUS_LLM_MODEL)"
	if *model != "" {
		modelDiff, truncated := TruncateDiff(diff, changes, *maxBytes)
		if truncated {
			fmt.Fprintf(stderr, "note: diff truncated to %d bytes for the model\n", *maxBytes)
		}
		gen := OllamaGenerator{URL: *url, Model: *model, Client: &http.Client{Timeout: *timeout}}
		draft, err = gen.Generate(ctx, modelDiff, changes)
		if err == nil {
			err = Validate(draft)
		}
		if err == nil {
			fmt.Fprintf(stderr, "generator: ollama (%s)\n", *model)
			fmt.Fprintln(stdout, draft)
			return 0
		}
		reason = "ollama draft unusable: " + err.Error()
	}

	fmt.Fprintf(stderr, "generator: rules (%s)\n", reason)
	fmt.Fprintln(stdout, RulesDraft(changes))
	return 0
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
