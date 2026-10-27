//go:build linux

// xsstrashy/internal/linux/freedesktop.go
package linux

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Result carries the outcome of Move. The caller (xsstrashy package)
// converts it to the appropriate public error type.
type Result struct {
	Moved  []string
	Failed map[string]error
}

// Move moves each path in paths to the trash. Paths that do not exist are
// skipped silently. If ctx is canceled mid-way, remaining paths are marked
// as failed with ctx.Err().
func Move(ctx context.Context, paths []string) Result {
	result := Result{Failed: make(map[string]error, len(paths))}

	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			result.Failed[p] = err
			continue
		}

		// Skip non-existent paths silently (idempotent behavior).
		// Any other Lstat error (permission denied on parent, etc.)
		// is reported to the caller - it indicates a real problem,
		// not a no-op.
		if _, err := os.Lstat(p); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			result.Failed[p] = err
			continue
		}

		abs, err := filepath.Abs(p)
		if err != nil {
			result.Failed[p] = err
			continue
		}

		if err := moveOne(ctx, abs); err != nil {
			result.Failed[p] = err
			continue
		}
		result.Moved = append(result.Moved, p)
	}
	return result
}

// trashDir describes one trash directory and its standard layout.
type trashDir struct {
	root  string // e.g. ~/.local/share/Trash or /mnt/usb/.Trash-1000
	files string // root/files
	info  string // root/info
}

// ensure creates root/files and root/info (mode 0700) if they are missing.
// It does not create root itself unless necessary - for the home trash
// we call it explicitly; for topdir trash the topdir itself is created by
// ensureTopTrashDir.
func (t trashDir) ensure() error {
	if err := os.MkdirAll(t.root, 0700); err != nil {
		return fmt.Errorf("create trash root %s: %w", t.root, err)
	}
	if err := os.MkdirAll(t.files, 0700); err != nil {
		return fmt.Errorf("create files dir %s: %w", t.files, err)
	}
	if err := os.MkdirAll(t.info, 0700); err != nil {
		return fmt.Errorf("create info dir %s: %w", t.info, err)
	}
	return nil
}

// homeTrashDir returns the user's home trash, per XDG spec.
//
// $XDG_DATA_HOME/Trash, with fallback to ~/.local/share/Trash.
func homeTrashDir() (trashDir, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return trashDir{}, fmt.Errorf("cannot determine home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	root := filepath.Join(dataHome, "Trash")
	return trashDir{
		root:  root,
		files: filepath.Join(root, "files"),
		info:  filepath.Join(root, "info"),
	}, nil
}

// deviceOf returns the st_dev of the filesystem containing path.
// path must exist.
func deviceOf(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unexpected Sys type %T", fi.Sys())
	}
	return uint64(st.Dev), nil
}

// findTopdir walks up from path until it reaches a directory on a
// different device. Returns the last directory that was on the same
// device - that is the mount point (topdir in trash-spec terms).
func findTopdir(path string) (string, error) {
	origDev, err := deviceOf(path)
	if err != nil {
		return "", err
	}

	dir := path
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		dir = filepath.Dir(dir)
	}

	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			return dir, nil
		}
		parentDev, err := deviceOf(parent)
		if err != nil {
			return "", err
		}
		if parentDev != origDev {
			return dir, nil
		}
		dir = parent
	}
}

// ensureTopTrashDir returns a writable per-user trash on the same device
// as absPath. Tries spec-preferred locations in order:
//
//  1. <topdir>/.Trash/$UID  (creating .Trash with 01777 if needed)
//  2. <topdir>/.Trash-$UID
//
// Returns an error if neither works, so the caller can fall back to
// copying into the home trash.
func ensureTopTrashDir(topdir string) (string, error) {
	uid := strconv.Itoa(os.Getuid())

	// (1) Try the shared .Trash directory.
	sharedTrash := filepath.Join(topdir, ".Trash")
	if _, err := os.Stat(sharedTrash); os.IsNotExist(err) {
		// Try to create it with the sticky bit. This normally only
		// succeeds if the user owns topdir. Chmod after MkdirAll
		// because umask may strip bits from the requested mode.
		if err := os.MkdirAll(sharedTrash, 01777); err == nil {
			_ = os.Chmod(sharedTrash, 01777)
		}
	}
	if st, err := os.Stat(sharedTrash); err == nil && st.IsDir() {
		userTrash := filepath.Join(sharedTrash, uid)
		if err := os.MkdirAll(userTrash, 0700); err == nil {
			return userTrash, nil
		}
	}

	// (2) Try .Trash-$UID.
	userTrash := filepath.Join(topdir, ".Trash-"+uid)
	if err := os.MkdirAll(userTrash, 0700); err == nil {
		return userTrash, nil
	}

	return "", fmt.Errorf("no writable trash directory on topdir %s", topdir)
}

// moveOne moves a single absolute path to the appropriate trash directory.
func moveOne(ctx context.Context, absPath string) error {
	home, err := homeTrashDir()
	if err != nil {
		return err
	}

	fileDev, err := deviceOf(absPath)
	if err != nil {
		return err
	}

	// Ensure home trash exists so deviceOf(home.root) succeeds below.
	if err := home.ensure(); err != nil {
		return err
	}
	homeDev, err := deviceOf(home.root)
	if err != nil {
		return err
	}

	if fileDev == homeDev {
		// Same device: atomic rename into home trash.
		return moveAtomic(ctx, absPath, home)
	}

	// Different device: try topdir trash first (keeps the file on the
	// same filesystem - cheap rename instead of a full copy).
	topdir, err := findTopdir(absPath)
	if err == nil {
		if tdRoot, tdErr := ensureTopTrashDir(topdir); tdErr == nil {
			td := trashDir{
				root:  tdRoot,
				files: filepath.Join(tdRoot, "files"),
				info:  filepath.Join(tdRoot, "info"),
			}
			if err := td.ensure(); err == nil {
				if err := moveAtomic(ctx, absPath, td); err == nil {
					return nil
				}
				// rename failed despite same device (rare - e.g. EACCES
				// after we passed all creation checks). Fall through to
				// copy.
			}
		}
	}

	// Last resort: copy into home trash. Non-atomic, but works on
	// read-only topdirs and any other pathological setup.
	return moveCopy(ctx, absPath, home)
}

// moveAtomic renames src into td via a reserved .trashinfo entry.
//
// Order of operations: reserve .trashinfo with O_EXCL (guarantees a
// unique name without TOCTOU), write its contents, then rename src.
// If the rename fails we remove the .trashinfo to avoid orphaned entries.
// If the process crashes between writing .trashinfo and renaming, the
// trash manager sees a broken entry for a still-existing file - but no
// data is lost. The reverse order (rename first) can leave a file stuck
// in trash with no metadata, which is worse.
func moveAtomic(_ context.Context, src string, td trashDir) error {
	base := filepath.Base(src)
	infoPath, infoFile, name, err := reserveInfo(td, base)
	if err != nil {
		return err
	}

	if _, err := infoFile.WriteString(writeTrashInfo(src)); err != nil {
		infoFile.Close()
		os.Remove(infoPath)
		return fmt.Errorf("write .trashinfo: %w", err)
	}
	if err := infoFile.Close(); err != nil {
		os.Remove(infoPath)
		return fmt.Errorf("close .trashinfo: %w", err)
	}

	dst := filepath.Join(td.files, name)
	if err := os.Rename(src, dst); err != nil {
		os.Remove(infoPath)
		return fmt.Errorf("rename into trash: %w", err)
	}

	return nil
}

// moveCopy copies src into td, then removes the original. Used only when
// an atomic rename is impossible (cross-device, no writable topdir trash).
func moveCopy(ctx context.Context, src string, td trashDir) error {
	base := filepath.Base(src)
	infoPath, infoFile, name, err := reserveInfo(td, base)
	if err != nil {
		return err
	}

	dst := filepath.Join(td.files, name)

	if err := copyTree(ctx, src, dst); err != nil {
		infoFile.Close()
		os.Remove(infoPath)
		os.RemoveAll(dst)
		return fmt.Errorf("copy into trash: %w", err)
	}

	if _, err := infoFile.WriteString(writeTrashInfo(src)); err != nil {
		infoFile.Close()
		os.Remove(infoPath)
		os.RemoveAll(dst)
		return fmt.Errorf("write .trashinfo: %w", err)
	}
	if err := infoFile.Close(); err != nil {
		os.Remove(infoPath)
		os.RemoveAll(dst)
		return fmt.Errorf("close .trashinfo: %w", err)
	}

	if err := os.RemoveAll(src); err != nil {
		// The file made it into trash, but we could not remove the
		// original. Report the error, but leave the copy in trash -
		// the user can recover the duplicate later.
		return fmt.Errorf("remove original after copy: %w", err)
	}
	return nil
}

// reserveInfo creates a uniquely named .trashinfo file in td.info and
// returns its path, the open *os.File (ready for writing), and the
// basename shared with the future files/ entry.
//
// Uniqueness is achieved via O_CREATE|O_EXCL - no TOCTOU window. On
// collision we append .1, .2, … to the basename.
func reserveInfo(td trashDir, base string) (string, *os.File, string, error) {
	for i := 0; i < 100000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		infoPath := filepath.Join(td.info, name+".trashinfo")
		f, err := os.OpenFile(infoPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			return infoPath, f, name, nil
		}
		if !os.IsExist(err) {
			return "", nil, "", fmt.Errorf("reserve .trashinfo: %w", err)
		}
	}
	return "", nil, "", fmt.Errorf("could not find unique name for %s after 100000 attempts", base)
}

// copyTree recursively copies src to dst, preserving regular files,
// directories, and symlinks. Special files (sockets, devices, FIFOs) are
// rejected with an error - they have no business being in a mod folder,
// and copying them is not straightforward.
func copyTree(ctx context.Context, src, dst string) error {
	return filepath.Walk(src, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0700)
		case info.Mode().IsRegular():
			return copyRegularFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported file type for %s (%s)", path, info.Mode())
		}
	})
}

// copyRegularFile copies a single regular file.
func copyRegularFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm|0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// IsAvailable reports whether the home trash is usable.
func IsAvailable() bool {
	home, err := homeTrashDir()
	if err != nil {
		return false
	}
	if err := home.ensure(); err != nil {
		return false
	}
	// Probe with a real open - os.Stat can succeed on read-only mounts.
	probe := filepath.Join(home.info, ".probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}
