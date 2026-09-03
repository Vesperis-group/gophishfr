//go:build windows

package logger

import "os"

// secureLogFile intentionally does not apply or verify POSIX permission bits.
// On Windows, os.FileMode maps only to the read-only attribute and exact access
// control is determined by the file's ACL. The successful writable open plus
// the common descriptor/path identity checks are the portable safety boundary.
func secureLogFile(_ string, _ *os.File, _ os.FileInfo) error {
	return nil
}
