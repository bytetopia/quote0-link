package plugin

import (
	"errors"
	"os"
	"path/filepath"
)

// AtomicWrite replaces path with data via a temporary file in the same
// directory, so readers never observe a partially written file.
func AtomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".quote0-link-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
