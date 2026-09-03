//go:build unix

package logger

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func secureLogFile(path string, f *os.File, info os.FileInfo) error {
	if err := verifyLogFileOwner(path, info); err != nil {
		return err
	}

	// Remove every permission outside the owner's read/write set. In
	// particular, do not add owner permissions to a more restrictive file.
	hardenedMode := info.Mode().Perm() & 0600
	if info.Mode().Perm() != hardenedMode {
		if err := f.Chmod(hardenedMode); err != nil {
			return fmt.Errorf("secure log file %q: %w", path, err)
		}
	}

	securedInfo, err := f.Stat()
	if err != nil {
		return fmt.Errorf("verify secured log file %q: %w", path, err)
	}
	if err := verifyLogFileOwner(path, securedInfo); err != nil {
		return err
	}
	if securedInfo.Mode().Perm() != hardenedMode {
		return fmt.Errorf("log file %q permissions could not be secured", path)
	}
	return nil
}

func verifyLogFileOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("log file %q ownership could not be verified", path)
	}
	effectiveUID := os.Geteuid()
	effectiveUIDValue, err := strconv.ParseUint(strconv.Itoa(effectiveUID), 10, 32)
	if err != nil {
		return fmt.Errorf("log file %q effective ownership could not be verified", path)
	}
	if uint64(stat.Uid) != effectiveUIDValue {
		return fmt.Errorf("log file %q is not owned by the effective service user", path)
	}
	return nil
}
