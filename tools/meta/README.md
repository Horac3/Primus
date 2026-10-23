# meta — commit-message assistant

Developer tooling, not part of Primus. It reads `git diff --cached` and prints a draft commit message in the
project's format (`type(scope): subject`, bulleted body, final `Tests:` line). It never runs `git commit` and
never changes the index.

```
git add <files>
go run ./tools/meta                       # rules draft, or ollama when a model is configured
go run ./tools/meta --model qwen2.5-coder:7b
go run ./tools/meta | git commit -F -     # only if you have reviewed the draft
```

stdout is the draft only. stderr says which generator ran (`ollama` or `rules`, with the reason for a fallback)
and whether the diff was truncated.

| Flag | Env | Default |
|---|---|---|
| `--url` | `PRIMUS_LLM_URL` | `http://localhost:11434` |
| `--model` | `PRIMUS_LLM_MODEL` | empty (rules generator) |
| `--timeout` | | `30s` |
| `--max-bytes` | | `24000` (diff size sent to the model) |
| `--allow-remote` | | off; a non-loopback `--url` is refused (exit 2) because the staged diff would leave this machine |

The model is local Ollama only. A draft that fails the format check, a model that is absent, failing or slow all
fall back to the rules generator, which builds the message from the staged paths. Exit codes: `0` draft printed,
`1` no staged changes or git failed, `2` bad flags.
