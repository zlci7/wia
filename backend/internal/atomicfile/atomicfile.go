// Package atomicfile replaces a file's contents without a reader ever observing a
// partial write.
//
// Callers update configuration and metadata while the application is running, so
// the new content is written beside the destination and renamed over it.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Mode is the permission of the files written here. They are not credentials, so
// they stay readable; credentials are written by the secret package, which keeps a
// stricter guarantee.
const Mode = 0o644

// Write replaces a file atomically, creating its directory when needed.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create a temporary file in %s: %w", dir, err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()

	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write %s: %w", temporary, err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync %s: %w", temporary, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", temporary, err)
	}
	if err := os.Chmod(temporary, Mode); err != nil {
		return fmt.Errorf("set permissions on %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
