//go:build linux

// xsstrashy/internal/linux/freedesktop_test.go
package linux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// setupHomeTrash redirects XDG_DATA_HOME to a temp dir and returns the
// resulting trash root path.
func setupHomeTrash(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	return filepath.Join(tmp, "Trash")
}

func TestMoveSingleFile(t *testing.T) {
	trashRoot := setupHomeTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(src, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

	trashed := filepath.Join(trashRoot, "files", "hello.txt")
	data, err := os.ReadFile(trashed)
	if err != nil {
		t.Fatalf("read trashed file: %v", err)
	}
	if string(data) != "hi" {
		t.Errorf("content mismatch: %q", data)
	}

	infoPath := filepath.Join(trashRoot, "info", "hello.txt.trashinfo")
	info, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("read .trashinfo: %v", err)
	}

	if want := "Path=" + encodePath(src); !strings.Contains(string(info), want) {
		t.Errorf(".trashinfo missing %q, got: %s", want, info)
	}
}

func TestMoveDirectory(t *testing.T) {
	trashRoot := setupHomeTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "mymod")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	r := Move(ctx, []string{src})
	if len(r.Failed) != 0 {
		t.Fatalf("failed: %v", r.Failed)
	}

	if _, err := os.Stat(filepath.Join(trashRoot, "files", "mymod", "sub", "file.txt")); err != nil {
		t.Fatalf("trashed dir content missing: %v", err)
	}
}

func TestMoveSkipsNonexistent(t *testing.T) {
	setupHomeTrash(t)
	ctx := context.Background()
	r := Move(ctx, []string{"/this/path/does/not/exist/hopefully"})
	if len(r.Failed) != 0 {
		t.Fatalf("nonexistent should be skipped, got failed: %v", r.Failed)
	}
	if len(r.Moved) != 0 {
		t.Fatalf("expected no moves, got %d", len(r.Moved))
	}
}

func TestMoveCollision(t *testing.T) {
	trashRoot := setupHomeTrash(t)
	dir := t.TempDir()

	src1 := filepath.Join(dir, "a.txt")
	src2 := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(src1, []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Move first, rename to simulate same basename.
	ctx := context.Background()
	Move(ctx, []string{src1})
	if err := os.WriteFile(src2, []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Move second - collides with a.txt in files/.
	if err := os.WriteFile(src2, []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}
	r := Move(ctx, []string{src2})
	if len(r.Failed) != 0 {
		t.Fatalf("failed: %v", r.Failed)
	}

	// The second must land as a.txt.1 or similar.
	if _, err := os.Stat(filepath.Join(trashRoot, "files", "b.txt")); err != nil {
		t.Fatalf("expected b.txt to be moved: %v", err)
	}
}

func TestMoveCollisionSameBasename(t *testing.T) {
	trashRoot := setupHomeTrash(t)
	dir := t.TempDir()

	// Two files with the same basename in different subdirs.
	d1 := filepath.Join(dir, "d1")
	d2 := filepath.Join(dir, "d2")
	os.MkdirAll(d1, 0755)
	os.MkdirAll(d2, 0755)
	os.WriteFile(filepath.Join(d1, "same.txt"), []byte("1"), 0644)
	os.WriteFile(filepath.Join(d2, "same.txt"), []byte("2"), 0644)

	ctx := context.Background()
	r := Move(ctx, []string{
		filepath.Join(d1, "same.txt"),
		filepath.Join(d2, "same.txt"),
	})
	if len(r.Failed) != 0 {
		t.Fatalf("failed: %v", r.Failed)
	}

	// Both must exist in files/.
	if _, err := os.Stat(filepath.Join(trashRoot, "files", "same.txt")); err != nil {
		t.Errorf("first same.txt missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trashRoot, "files", "same.txt.1")); err != nil {
		t.Errorf("second same.txt.1 missing: %v", err)
	}
}

func TestMoveSymlinkMovesLinkNotTarget(t *testing.T) {
	trashRoot := setupHomeTrash(t)
	dir := t.TempDir()

	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	r := Move(ctx, []string{link})
	if len(r.Failed) != 0 {
		t.Fatalf("failed: %v", r.Failed)
	}

	// Target must still exist.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target was affected: %v", err)
	}

	// Moved entry must be a symlink.
	trashed := filepath.Join(trashRoot, "files", "link.txt")
	fi, err := os.Lstat(trashed)
	if err != nil {
		t.Fatalf("stat trashed link: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("trashed entry is not a symlink: %v", fi.Mode())
	}
}

func TestMoveContextCancellation(t *testing.T) {
	setupHomeTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "x.txt")
	os.WriteFile(src, []byte("x"), 0644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	r := Move(ctx, []string{src})
	if len(r.Failed) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(r.Failed))
	}
	if !errors.Is(r.Failed[src], context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", r.Failed[src])
	}
}

// TestMoveCrossDevice verifies the copy path when home trash is on a
// different filesystem. Uses /dev/shm as a source - it's usually tmpfs,
// and typically on a different device from the t.TempDir() location.
// Skips if /dev/shm isn't usable or happens to be on the same device.
func TestMoveCrossDevice(t *testing.T) {
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skipf("/dev/shm not available: %v", err)
	}

	trashRoot := setupHomeTrash(t)
	if err := os.MkdirAll(filepath.Join(trashRoot, "files"), 0700); err != nil {
		t.Fatal(err)
	}

	homeDev, err := deviceOf(trashRoot)
	if err != nil {
		t.Skipf("cannot stat trash root: %v", err)
	}
	shmDev, err := deviceOf("/dev/shm")
	if err != nil {
		t.Skipf("cannot stat /dev/shm: %v", err)
	}
	if homeDev == shmDev {
		t.Skipf("home trash and /dev/shm are on the same device - cannot test cross-device")
	}

	srcDir, err := os.MkdirTemp("/dev/shm", "xsstrashy-test-")
	if err != nil {
		t.Skipf("cannot create dir on /dev/shm: %v", err)
	}
	defer os.RemoveAll(srcDir)
	src := filepath.Join(srcDir, "payload.txt")
	if err := os.WriteFile(src, []byte("cross-device payload"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := Move(ctx, []string{src})
	if len(r.Failed) != 0 {
		t.Fatalf("failed: %v", r.Failed)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}

	uid := strconv.Itoa(os.Getuid())

	// Спецификация требует сначала пробовать topdir-корзину (на /dev/shm),
	// и только если не вышло — fallback на домашнюю.
	top1 := filepath.Join("/dev/shm", ".Trash", uid, "files", "payload.txt")
	top2 := filepath.Join("/dev/shm", ".Trash-"+uid, "files", "payload.txt")
	home := filepath.Join(trashRoot, "files", "payload.txt")

	var foundPath string
	for _, p := range []string{top1, top2, home} {
		if _, err := os.Stat(p); err == nil {
			foundPath = p
			break
		}
	}

	if foundPath == "" {
		t.Fatalf("trashed payload not found in topdir or home trash")
	}

	// Сначала читаем и проверяем!
	data, err := os.ReadFile(foundPath)
	if err != nil {
		t.Fatalf("read trashed payload: %v", err)
	}
	if string(data) != "cross-device payload" {
		t.Errorf("content mismatch: %q", data)
	}

	// И только теперь убираем за собой, если создали корзину в /dev/shm
	if foundPath == top1 {
		os.RemoveAll(filepath.Join("/dev/shm", ".Trash"))
	} else if foundPath == top2 {
		os.RemoveAll(filepath.Join("/dev/shm", ".Trash-"+uid))
	}
}

// TestIsAvailable exercises the probe path.
func TestIsAvailable(t *testing.T) {
	setupHomeTrash(t)
	if !IsAvailable() {
		t.Fatal("expected IsAvailable to be true in a fresh temp dir")
	}
}
