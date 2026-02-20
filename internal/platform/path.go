package platform

import (
	"path/filepath"
	"runtime"
)

// BinaryPath returns the correct path for a binary based on GOOS.
// It appends .exe on Windows and ensures path separators are correct.
func BinaryPath(path string) string {
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	return filepath.FromSlash(path)
}
