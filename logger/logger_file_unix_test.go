//go:build unix

package logger

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOpenLogFileCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gophish.log")

	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	assertFileMode(t, path, 0600)

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove log file: %v", err)
	}
	f, err = openLogFile(path)
	if err != nil {
		t.Fatalf("recreate log file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	assertFileMode(t, path, 0600)
}

func TestOpenLogFileUmask(t *testing.T) {
	if mask := os.Getenv("GOPHISH_LOG_UMASK_HELPER"); mask != "" {
		if mask == "000" {
			syscall.Umask(0)
		} else {
			syscall.Umask(0022)
		}
		f, err := openLogFile(os.Getenv("GOPHISH_LOG_UMASK_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	for _, mask := range []string{"022", "000"} {
		t.Run(mask, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gophish.log")
			cmd := exec.Command(os.Args[0], "-test.run=^TestOpenLogFileUmask$")
			cmd.Env = append(os.Environ(),
				"GOPHISH_LOG_UMASK_HELPER="+mask,
				"GOPHISH_LOG_UMASK_PATH="+path,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("umask helper failed: %v\n%s", err, output)
			}
			assertFileMode(t, path, 0600)
		})
	}
}

func TestOpenLogFileHardensExistingModes(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0644, 0666, 0777, 0640, 0604} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gophish.log")
			if err := os.WriteFile(path, []byte("old marker\n"), mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}

			f, err := openLogFile(path)
			if err != nil {
				t.Fatalf("open log file: %v", err)
			}
			if _, err := f.WriteString("new marker\n"); err != nil {
				t.Fatalf("append log file: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}

			assertFileMode(t, path, 0600)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != "old marker\nnew marker\n" {
				t.Fatalf("content was truncated or not appended: %q", content)
			}
		})
	}
}

func TestOpenLogFileDoesNotWidenRestrictiveMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gophish.log")
	if err := os.WriteFile(path, []byte("private marker"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}

	f, err := openLogFile(path)
	if err == nil {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	assertFileMode(t, path, 0400)
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "private marker" {
		t.Fatalf("restrictive file content changed: %q", content)
	}
}

func TestOpenLogFileRejectsUnsafePaths(t *testing.T) {
	t.Run("missing parent", func(t *testing.T) {
		const secretContent = "secret marker content"
		path := filepath.Join(t.TempDir(), "missing", "gophish.log")
		if _, err := openLogFile(path); err == nil {
			t.Fatal("expected missing parent to fail")
		} else if strings.Contains(err.Error(), secretContent) {
			t.Fatalf("error exposed file contents: %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		if _, err := openLogFile(t.TempDir()); err == nil ||
			!strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("expected regular-file error, got %v", err)
		}
	})

	t.Run("fifo", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "gophish.log")
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := openLogFile(path); err == nil ||
			!strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("expected regular-file error, got %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		path := filepath.Join(dir, "gophish.log")
		const marker = "target secret marker"
		if err := os.WriteFile(target, []byte(marker), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(target, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}

		if _, err := openLogFile(path); err == nil ||
			!strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink error, got %v", err)
		} else if strings.Contains(err.Error(), marker) {
			t.Fatalf("error exposed target contents: %v", err)
		}
		assertFileMode(t, target, 0644)
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != marker {
			t.Fatalf("symlink target content changed: %q", content)
		}
	})
}

func TestValidateLogFileIdentityRejectsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gophish.log")
	displaced := filepath.Join(dir, "displaced.log")
	if err := os.WriteFile(path, []byte("opened file"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close opened file: %v", err)
		}
	}()
	if err := os.Rename(path, displaced); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement file"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := validateLogFileIdentity(path, f); err == nil ||
		!strings.Contains(err.Error(), "changed while being opened") {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
}

func TestSetupKeepsStderrAndFileOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gophish.log")
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = stderrWriter
	t.Cleanup(func() {
		os.Stderr = originalStderr
		Logger.SetOutput(originalStderr)
		if openedLogFile != nil {
			_ = openedLogFile.Close()
			openedLogFile = nil
		}
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
	})

	if err := Setup(&Config{Filename: path}); err != nil {
		t.Fatalf("setup logger: %v", err)
	}
	Logger.Info("same logger record")
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if _, err := stderr.ReadFrom(stderrReader); err != nil {
		t.Fatal(err)
	}
	fileContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"stderr": stderr.String(),
		"file":   string(fileContent),
	} {
		if !strings.Contains(content, `level=info msg="same logger record"`) {
			t.Errorf("%s did not receive unchanged logger record: %q", name, content)
		}
	}
}

func assertFileMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("%s mode = %04o, want %04o", path, actual, expected)
	}
}
