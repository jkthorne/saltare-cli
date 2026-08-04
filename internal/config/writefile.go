package config

import (
	"io"
	"os"
	"path/filepath"
)

// SafeWriteFile streams r into target via a temp sibling + rename, so an
// interrupted transfer never leaves a truncated file under the real name.
func SafeWriteFile(target string, r io.Reader) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".part-*")
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	return written, nil
}
