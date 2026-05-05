package trash

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// MoveToTrash moves the given file to the OS recycle bin / trash folder.
func MoveToTrash(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}

	switch runtime.GOOS {
	case "windows":
		return moveToRecycleBinWindows(path)
	case "darwin":
		return moveToTrashDarwin(path)
	default:
		return moveToTrashLinux(path)
	}
}

func moveToTrashDarwin(path string) error {
	trashDir := filepath.Join(os.Getenv("HOME"), ".Trash")
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return err
	}
	return moveWithUniqueName(path, trashDir)
}

func moveToTrashLinux(path string) error {
	trashDir := filepath.Join(os.Getenv("HOME"), ".local", "share", "Trash", "files")
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return err
	}
	return moveWithUniqueName(path, trashDir)
}

func moveWithUniqueName(path, destDir string) error {
	base := filepath.Base(path)
	dest := filepath.Join(destDir, base)
	if _, err := os.Stat(dest); err == nil {
		stamp := time.Now().Format("20060102_150405")
		dest = filepath.Join(destDir, fmt.Sprintf("%s_%s", stamp, base))
	}
	return os.Rename(path, dest)
}
