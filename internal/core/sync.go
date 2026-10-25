package core

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	tmpSuffix = ".playswap-tmp"
	oldSuffix = ".playswap-old"
)

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// Mirror makes dst an exact copy of src (file or folder). If src does not
// exist, dst is removed. The new copy is assembled next to dst first and
// swapped in with renames, so a failed copy never leaves dst half-written.
func Mirror(src, dst string) error {
	st, err := os.Stat(src)
	if errors.Is(err, os.ErrNotExist) {
		return removeAll(dst)
	}
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(src); err == nil {
		src = real // the save folder itself may be a link to another drive
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + tmpSuffix
	old := dst + oldSuffix
	_ = removeAll(tmp)
	_ = removeAll(old)

	if st.IsDir() {
		err = copyTree(src, tmp)
	} else {
		err = copyFile(src, tmp, st)
	}
	if err != nil {
		_ = removeAll(tmp)
		return fmt.Errorf("copying %s: %w", src, err)
	}
	if exists(dst) {
		if err := os.Rename(dst, old); err != nil {
			_ = removeAll(tmp)
			return fmt.Errorf("%s is in use; close the game and try again (%w)", dst, err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		// put the original back
		if exists(old) {
			_ = os.Rename(old, dst)
		}
		_ = removeAll(tmp)
		return err
	}
	_ = removeAll(old)
	return nil
}

func removeAll(p string) error {
	err := os.RemoveAll(p)
	if err != nil {
		// Read-only files block deletion on Windows: clear the flag and retry.
		_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o777)
			}
			return nil
		})
		err = os.RemoveAll(p)
	}
	return err
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 || d.Type()&fs.ModeIrregular != 0 {
			return nil // skip links/junctions: never follow them out of the save folder
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, target, info)
	})
}

func copyFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	return nil
}

// Size returns the total size of a file or folder (0 if missing).
func Size(p string) int64 {
	var total int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
