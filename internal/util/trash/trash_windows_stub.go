//go:build !windows

package trash

import "fmt"

func moveToRecycleBinWindows(path string) error {
	return fmt.Errorf("windows recycle bin is only available on Windows builds")
}
