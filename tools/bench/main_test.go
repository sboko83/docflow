package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateTiny(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "bench", "testdata")
	var output bytes.Buffer
	err = validateCommand([]string{"--task", "tiny", "--tasks", filepath.Join(fixture, "tasks"), "--base", filepath.Join(fixture, "base")}, &output)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := output.String(); got != "tiny: base 0/1, solution 1/1, lint ok\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestValidateRejectsOraclePassingOnBase(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "bench", "testdata")
	tmp := t.TempDir()
	taskDir := filepath.Join(tmp, "tiny")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixture, "tasks", "tiny"), taskDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "oracle", "benchoracle", "value_test.go"), []byte("package benchoracle\nimport (\"testing\"; \"tinybase\")\nfunc TestValue(t *testing.T) { if tinybase.Value()!=1 { t.Fatal(\"wrong\") } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = validateCommand([]string{"--task", "tiny", "--tasks", tmp, "--base", filepath.Join(fixture, "base")}, &output)
	if err == nil {
		t.Fatal("expected oracle-on-base failure")
	}
}

func TestValidateRejectsBadSolution(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "bench", "testdata")
	tmp := t.TempDir()
	taskDir := filepath.Join(tmp, "tiny")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(fixture, "tasks", "tiny"), taskDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "solution", "value.go"), []byte("package tinybase\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = validateCommand([]string{"--task", "tiny", "--tasks", tmp, "--base", filepath.Join(fixture, "base")}, &output)
	if err == nil {
		t.Fatal("expected failing solution")
	}
}
