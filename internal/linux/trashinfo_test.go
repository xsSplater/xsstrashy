//go:build linux

// xsstrashy/internal/linux/trashinfo_test.go
package linux

import (
	"strings"
	"testing"
)

func TestEncodePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/home/user/foo.txt", "/home/user/foo.txt"},
		{"/home/user/foo bar.txt", "/home/user/foo%20bar.txt"},
		{"/home/user/foo/bar/baz", "/home/user/foo/bar/baz"},
		{"/tmp/файл.txt", "/tmp/%D1%84%D0%B0%D0%B9%D0%BB.txt"},
		{"/tmp/a#b?c&d", "/tmp/a%23b%3Fc%26d"},
		{"/tmp/a(b)c!d~e*f", "/tmp/a(b)c!d~e*f"}, // unreserved per RFC 2396
		{"/tmp/a'b", "/tmp/a'b"},
	}
	for _, c := range cases {
		if got := encodePath(c.in); got != c.want {
			t.Errorf("encodePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	paths := []string{
		"/home/user/foo.txt",
		"/tmp/с пробелами и юникодом.txt",
		"/tmp/a#b?c&d=e/f",
		"/very/deep/path/with/many/components/file.mod",
	}
	for _, p := range paths {
		enc := encodePath(p)
		dec, err := decodePath(enc)
		if err != nil {
			t.Fatalf("decodePath(%q) error: %v", enc, err)
		}
		if dec != p {
			t.Errorf("round-trip failed: %q → %q → %q", p, enc, dec)
		}
	}
}

func TestDecodePathRejectsTruncated(t *testing.T) {
	if _, err := decodePath("/tmp/a%2"); err == nil {
		t.Fatal("expected error for truncated encoding")
	}
	if _, err := decodePath("/tmp/a%ZZ"); err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestWriteTrashInfoFormat(t *testing.T) {
	got := writeTrashInfo("/home/user/foo bar.txt")
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "[Trash Info]" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "Path=/home/user/foo%20bar.txt" {
		t.Errorf("line 1 = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "DeletionDate=") {
		t.Errorf("line 2 = %q", lines[2])
	}
}
