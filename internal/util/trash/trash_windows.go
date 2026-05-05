//go:build windows

package trash

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	foDelete          = 0x0003
	fofAllowUndo      = 0x0040
	fofNoConfirmation = 0x0010
	fofSilent         = 0x0004
)

type shfileopstruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procSHFileOperationW = shell32.NewProc("SHFileOperationW")
)

func moveToRecycleBinWindows(path string) error {
	// SHFileOperationW requires:
	//  - Backslash separators (forward slashes work in modern Windows but
	//    older drivers reject them).
	//  - Absolute path (relative paths reliably fail with DE_INVALIDFILES).
	//  - Path terminated with TWO NUL chars (the pFrom contract is a
	//    double-null-terminated list).
	abs, absErr := filepath.Abs(path)
	if absErr != nil {
		abs = path
	}
	abs = filepath.Clean(abs)

	// SHFileOperationW's pFrom contract requires the buffer to end with TWO
	// NUL chars (the string terminator + a list terminator).
	// `syscall.UTF16PtrFromString` rejects any input with an embedded NUL,
	// so concatenating "\x00" before encoding fails with "invalid argument"
	// — exactly the error users hit when deleting a series. Use
	// UTF16FromString (returns a []uint16 that's already singly
	// null-terminated) and append a second 0 to get the required double-
	// null terminator.
	utf16, err := syscall.UTF16FromString(abs)
	if err != nil {
		return fmt.Errorf("encoding path %q: %w", abs, err)
	}
	utf16 = append(utf16, 0)

	op := shfileopstruct{
		wFunc:  foDelete,
		pFrom:  &utf16[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent,
	}

	r1, _, callErr := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if r1 != 0 {
		// SHFileOperationW return codes are documented but the syscall errno
		// is often noise. Surface the numeric code with the path so the user
		// sees something actionable in the UI rather than "see server log."
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("SHFileOperationW(%q) failed: %d (%v)", abs, r1, callErr)
		}
		return fmt.Errorf("SHFileOperationW(%q) failed with code %d (%s)", abs, r1, shFileOpErrorMessage(int(r1)))
	}
	if op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("trash operation aborted for %q", abs)
	}
	return nil
}

// shFileOpErrorMessage maps a handful of common SHFileOperation return
// codes to human-readable text. Anything else falls through with just the
// code — better than nothing.
func shFileOpErrorMessage(code int) string {
	switch code {
	case 0x71:
		return "DE_SAMEFILE: source and destination are the same"
	case 0x7C:
		return "DE_INVALIDFILES: path is invalid (not absolute / bad characters?)"
	case 0x7E:
		return "DE_DESTSAMETREE: destination is a subtree of source"
	case 0x80:
		return "DE_FLDDESTISFILE: destination exists as a file, not a folder"
	case 0x82:
		return "DE_FILEDESTISFLD: destination is a folder where a file is expected"
	case 0x83:
		return "DE_FILENAMETOOLONG: path too long (>260 chars without long-path support)"
	case 0x84:
		return "DE_DEST_IS_CDROM: destination is read-only media"
	case 0x10000:
		return "ERRORONDEST: error on destination"
	case 1223: // ERROR_CANCELLED
		return "operation cancelled"
	}
	return fmt.Sprintf("code 0x%X", code)
}
