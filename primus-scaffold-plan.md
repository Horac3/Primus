# Primus — Phase 0: Scaffold Plan

## Overview

Stand up the complete directory skeleton for **Primus** — a self-hosted API testing toolkit
(load, burst, auth-probe) with an htmx-driven web UI and headless CLI mode.
This phase creates zero business logic; its only goal is to give every future phase
a clean, correctly-named home to fill in without restructuring.

Module path: `github.com/Horac3/Primus`  
Go version: 1.23+  
Storage: SQLite (wire-up deferred to a later phase)  
Binary: single `cmd/primus` entrypoint  

---

## Sub-Task 1 — Go module and minimal entrypoint

**Intent**  
Establish the Go module so `go run ./cmd/primus` works and proves the scaffold compiles.

**Expected Outcomes**  
- `go.mod` exists with module `github.com/Horac3/Primus` and `go 1.23`  
- `cmd/primus/main.go` prints `Primus scaffold OK` and exits with code 0  
- `go run ./cmd/primus` succeeds with no errors  

**Todo List**  
1. Create `go.mod` (module path + go directive, no dependencies yet)  
2. Create `cmd/primus/main.go` with a single `fmt.Println("Primus scaffold OK")` main  

**Relevant Context**  
- No existing module — workspace root is clean  
- Keep main.go a bare stub; real CLI wiring (cobra or flag) is a later phase  

**Status** `[x] done`

---

## Sub-Task 2 — Internal package stubs

**Intent**  
Create the four internal packages so Go can resolve them as import paths and
future phases can add files without changing the layout.

**Expected Outcomes**  
Each of the following packages exists as a `.go` file with only a `package` declaration:  
- `internal/engine`  
- `internal/importer`  
- `internal/store`  
- `internal/web`  
- `internal/model`  

**Todo List**  
1. Create `internal/engine/engine.go` — `package engine`  
2. Create `internal/importer/importer.go` — `package importer`  
3. Create `internal/store/store.go` — `package store`  
4. Create `internal/web/web.go` — `package web`  
5. Create `internal/model/model.go` — `package model`  

**Relevant Context**  
- Stub files need only the package declaration; no imports, no symbols  
- Package names match the directory name (Go convention)  

**Status** `[x] done`

---

## Sub-Task 3 — Web asset directories

**Intent**  
Create the `/web/templates` and `/web/static` directories so template and static
asset files have a home; include placeholder files so git tracks the directories.

**Expected Outcomes**  
- `web/templates/.gitkeep` exists  
- `web/static/.gitkeep` exists  
- A `web/static/README.md` note explains Chart.js is loaded via CDN (no bundler)  

**Todo List**  
1. Create `web/templates/.gitkeep`  
2. Create `web/static/.gitkeep`  
3. Create `web/static/README.md` with one-liner: Chart.js loaded via CDN, no local bundling  

**Relevant Context**  
- htmx will also be CDN-referenced; no node_modules, no bundler  

**Status** `[x] done`

---

## Sub-Task 4 — Deploy and sessions directories

**Intent**  
Create `/deploy` for container config and `/bob_sessions` for hackathon screenshots,
both empty but tracked by git.

**Expected Outcomes**  
- `deploy/.gitkeep` exists  
- `bob_sessions/.gitkeep` exists  

**Todo List**  
1. Create `deploy/.gitkeep`  
2. Create `bob_sessions/.gitkeep`  

**Status** `[x] done`

---

## Sub-Task 5 — Project meta-files

**Intent**  
Add `.gitignore`, `.bobignore`, and `README.md` so the repo is well-behaved from
the first commit and Bob's context window stays focused on code.

**Expected Outcomes**  
- `.gitignore` covers: `bin/`, `tmp/`, `*.db`, `*.env`, `.env`, `node_modules/`  
- `.bobignore` excludes: `bin/`, `tmp/`, `*.db`, `node_modules/`, `bob_sessions/`  
- `README.md` contains project name, one-line description, and a `## Status` section
  reading "Phase 0: scaffold only"  

**Todo List**  
1. Create `.gitignore`  
2. Create `.bobignore`  
3. Create `README.md`  

**Relevant Context**  
- `.bobignore` prevents screenshots and build artefacts from polluting Bob's context  
- README stays minimal — no architecture docs yet  

**Status** `[x] done`

---

## Final Validation

After all sub-tasks complete:  
- `go build ./...` must succeed with no errors or warnings  
- Directory tree must match the layout specified in the requirements  
- `.bobignore` and `.gitignore` entries must cover all listed patterns  
