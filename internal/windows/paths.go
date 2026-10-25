//go:build windows

// xsstrashy/internal/windows/paths.go
package windows

import (
	"path/filepath"
	"strings"
)

const longPathPrefix = `\\?\`

// normalizePath returns an absolute path suitable for
// SHCreateItemFromParsingName. Adds the \\?\ prefix for paths longer than
// MAX_PATH (260), so they work on Windows 10 1607+ with LongPathsEnabled.
func normalizePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(abs, longPathPrefix) {
		return abs, nil
	}
	if strings.HasPrefix(abs, `\\`) {
		if len(abs) > 260 {
			return longPathPrefix + `UNC` + abs[1:], nil
		}
		return abs, nil
	}
	if len(abs) > 260 {
		return longPathPrefix + abs, nil
	}
	return abs, nil
}
