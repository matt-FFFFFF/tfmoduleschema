package tfmoduleschema

import (
	"io/fs"
	"os"
)

// Thin wrappers around os primitives so inspect.go can be unit-tested
// more easily and to keep imports localised.

func statDir(dir string) (fs.FileInfo, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "stat", Path: dir, Err: fs.ErrInvalid}
	}
	return info, nil
}

func readDir(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
func isNotExist(err error) bool                 { return os.IsNotExist(err) }
