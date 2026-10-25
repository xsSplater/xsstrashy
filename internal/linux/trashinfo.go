//go:build linux

// Package linux implements the FreeDesktop.org Trash Specification 1.0.
//
// Spec: https://specifications.freedesktop.org/trash-spec/

// xsstrashy/internal/linux/trashinfo.go
package linux

import (
	"fmt"
	"strings"
	"time"
)

// hexUpper is the uppercase hexadecimal alphabet used by percent-encoding.
const hexUpper = "0123456789ABCDEF"

// isUnreserved reports whether c is one of the characters that RFC 2396
// defines as "unreserved". The trash spec explicitly refers to RFC 2396
// for the Path encoding.
func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	}
	return false
}

// encodePath percent-encodes an absolute path for the Path= field of a
// .trashinfo file.
//
// The path separator '/' is left as-is - it is structural, not data.
// Every non-unreserved byte (including all bytes of a UTF-8 sequence) is
// percent-encoded. This matches what GLib's g_uri_escape_string and other
// trash implementations do, so Nautilus, Dolphin, etc. decode our files
// correctly.
func encodePath(p string) string {
	var b strings.Builder
	b.Grow(len(p) + len(p)/4)
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' || isUnreserved(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0x0f])
		}
	}
	return b.String()
}

// decodePath reverses encodePath. Not used by Move, but exported for tests
// and for future Restore support.
func decodePath(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("truncated percent-encoding in %q", s)
		}
		hi := unhex(s[i+1])
		lo := unhex(s[i+2])
		if hi < 0 || lo < 0 {
			return "", fmt.Errorf("invalid percent-encoding at position %d in %q", i, s)
		}
		b.WriteByte(byte(hi<<4 | lo))
		i += 3
	}
	return b.String(), nil
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'F':
		return int(c - 'A' + 10)
	case c >= 'a' && c <= 'f':
		return int(c - 'a' + 10)
	}
	return -1
}

// writeTrashInfo formats the .trashinfo file contents for originalPath.
//
// Format per spec:
//
//	[Trash Info]
//	Path=/absolute/encoded/path
//	DeletionDate=YYYY-MM-DDThh:mm:ss
//
// DeletionDate is local time - the spec does not mandate UTC, and every
// real-world implementation uses local time so the trash manager can show
// "deleted at 14:32" without conversion.
func writeTrashInfo(originalPath string) string {
	return "[Trash Info]\n" +
		"Path=" + encodePath(originalPath) + "\n" +
		"DeletionDate=" + time.Now().Format("2006-01-02T15:04:05") + "\n"
}
