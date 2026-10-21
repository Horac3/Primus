package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const moduleRoot = "../.."

func TestBinaryDoesNotDependOnTools(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	cmd := exec.Command("go", "list", "-deps", "./cmd/primus")
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/primus: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.Contains(pkg, "/tools/") || strings.HasSuffix(pkg, "/tools") {
			t.Errorf("cmd/primus depends on %s", pkg)
		}
	}
}

func TestImageDoesNotShipTools(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join(moduleRoot, "deploy", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	stages := strings.Split(string(dockerfile), "\nFROM ")
	final := stages[len(stages)-1]
	if strings.Contains(final, "tools") {
		t.Error("the final image stage mentions tools")
	}

	ignore, err := os.ReadFile(filepath.Join(moduleRoot, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, line := range strings.Split(string(ignore), "\n") {
		if strings.TrimSpace(strings.TrimPrefix(line, string(rune(0xFEFF)))) == "tools/" {
			listed = true
		}
	}
	if !listed {
		t.Error(".dockerignore does not list tools/, so the build context still carries it")
	}
}
