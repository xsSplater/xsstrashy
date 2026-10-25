//go:build windows

// xsstrashy/internal/windows/move_test.go
package windows

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMoveSingleFile — end-to-end on Windows.
//
// We cannot query the recycle bin from the test without additional
// shell calls, but we can verify the source file is gone — the absence
// of the original is the contract.
func TestMoveSingleFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "xsstrashy-test-file.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := Move(ctx, []string{src})
	if len(r.Failed) != 0 {
		t.Fatalf("failed: %v", r.Failed)
	}
	if len(r.Moved) != 1 {
		t.Fatalf("expected 1 moved, got %d", len(r.Moved))
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
}

// TestMoveSkipsNonexistent — non-existent paths must be treated as
// no-op, not as failure.
func TestMoveSkipsNonexistent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := Move(ctx, []string{`Z:\this\does\not\exist\xsstrashy.txt`})
	if len(r.Failed) != 0 {
		t.Fatalf("non-existent path should not appear in Failed: %v", r.Failed)
	}
	if len(r.Moved) != 0 {
		t.Fatalf("expected no moves, got %v", r.Moved)
	}
}

// TestMoveDirectory — dirs are moved as a whole.
func TestMoveDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "xsstrashy-test-dir")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "x.txt"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := Move(ctx, []string{dir})
	if len(r.Failed) != 0 {
		t.Fatalf("failed: %v", r.Failed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}
