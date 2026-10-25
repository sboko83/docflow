package dirlink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target & %PATH%")
	link := filepath.Join(root, "link & %PATH%")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(target, "keep")
	if err := os.WriteFile(file, []byte("target"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Create(target, link); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("link mode: %s", info.Mode())
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("Readlink = %q, %v; want %q", got, err, target)
	}
	got, err := filepath.EvalSymlinks(link)
	t.Logf("EvalSymlinks = %q, %v", got, err)
	if got, err := Resolve(link); err != nil || got != target {
		t.Fatalf("Resolve = %q, %v; want %q", got, err, target)
	}
	if got, err := Resolve(filepath.Join(link, "keep")); err != nil || got != file {
		t.Fatalf("Resolve child = %q, %v; want %q", got, err, file)
	}
	chain := filepath.Join(root, "chain")
	if err := Create(link, chain); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(filepath.Join(chain, "keep")); err != nil || got != file {
		t.Fatalf("Resolve chain = %q, %v; want %q", got, err, file)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "target" {
		t.Fatalf("removing link changed target: %q, %v", got, err)
	}
	// Junctions can also represent stale skill links after a release disappears.
	if err := Create(filepath.Join(root, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(link); err != nil {
		t.Fatalf("Readlink on dangling link: %v", err)
	}
	if _, err := Resolve(link); err == nil {
		t.Fatal("dangling link resolved")
	}
}

func TestResolveRejectsCycle(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := Create(b, a); err != nil {
		t.Fatal(err)
	}
	if err := Create(a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(a); err == nil {
		t.Fatal("link cycle resolved")
	}
}
