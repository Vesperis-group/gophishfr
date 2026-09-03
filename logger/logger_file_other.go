//go:build !unix && !windows

package logger

import "os"

// Non-Unix platforms retain writable-open and descriptor/path identity checks
// without asserting POSIX ownership or permission semantics.
func secureLogFile(_ string, _ *os.File, _ os.FileInfo) error {
	return nil
}
