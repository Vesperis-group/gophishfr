package logger

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// Logger is the main logger that is abstracted in this package.
// It is exported here for use with gorm.
var Logger *logrus.Logger
var openedLogFile *os.File

// ErrInvalidLevel is returned when an invalid log level is given in the config
var ErrInvalidLevel = errors.New("invalid log level")

// Config represents configuration details for logging.
type Config struct {
	Filename string `json:"filename"`
	Level    string `json:"level"`
}

func init() {
	Logger = logrus.New()
	Logger.Formatter = &logrus.TextFormatter{DisableColors: true}
}

// Setup configures the logger based on options in the config.json.
func Setup(config *Config) error {
	var err error
	// Set up logging level
	level := logrus.InfoLevel
	if config.Level != "" {
		level, err = logrus.ParseLevel(config.Level)
		if err != nil {
			return err
		}
	}
	Logger.SetLevel(level)
	// Set up logging to a file if specified in the config
	logFile := config.Filename
	if logFile != "" {
		f, err := openLogFile(logFile)
		if err != nil {
			return err
		}
		mw := io.MultiWriter(os.Stderr, f)
		previousLogFile := openedLogFile
		Logger.Out = mw
		openedLogFile = f
		if previousLogFile != nil {
			_ = previousLogFile.Close()
		}
	}
	return nil
}

func openLogFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("log file %q is a symlink", path)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("log file %q is not a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect log file %q: %w", path, err)
	}

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open log directory for %q: %w", path, err)
	}
	f, openErr := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	closeErr := root.Close()
	if openErr != nil {
		return nil, fmt.Errorf("open log file %q: %w", path, openErr)
	}
	if closeErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("close log directory for %q: %w", path, closeErr)
	}
	closeWithError := func(err error) (*os.File, error) {
		_ = f.Close()
		return nil, err
	}

	info, err := validateLogFileIdentity(path, f)
	if err != nil {
		return closeWithError(err)
	}
	if err := secureLogFile(path, f, info); err != nil {
		return closeWithError(err)
	}

	if _, err := validateLogFileIdentity(path, f); err != nil {
		return closeWithError(err)
	}
	return f, nil
}

func validateLogFileIdentity(path string, f *os.File) (os.FileInfo, error) {
	openedInfo, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened log file %q: %w", path, err)
	}
	if !openedInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("log file %q is not a regular file", path)
	}

	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("reinspect log file %q: %w", path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("log file %q is a symlink", path)
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("log file %q is not a regular file", path)
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return nil, fmt.Errorf("log file %q changed while being opened", path)
	}
	return openedInfo, nil
}

// Debug logs a debug message
func Debug(args ...interface{}) {
	Logger.Debug(args...)
}

// Debugf logs a formatted debug messsage
func Debugf(format string, args ...interface{}) {
	Logger.Debugf(format, args...)
}

// Info logs an informational message
func Info(args ...interface{}) {
	Logger.Info(args...)
}

// Infof logs a formatted informational message
func Infof(format string, args ...interface{}) {
	Logger.Infof(format, args...)
}

// Error logs an error message
func Error(args ...interface{}) {
	Logger.Error(args...)
}

// Errorf logs a formatted error message
func Errorf(format string, args ...interface{}) {
	Logger.Errorf(format, args...)
}

// Warn logs a warning message
func Warn(args ...interface{}) {
	Logger.Warn(args...)
}

// Warnf logs a formatted warning message
func Warnf(format string, args ...interface{}) {
	Logger.Warnf(format, args...)
}

// Fatal logs a fatal error message
func Fatal(args ...interface{}) {
	Logger.Fatal(args...)
}

// Fatalf logs a formatted fatal error message
func Fatalf(format string, args ...interface{}) {
	Logger.Fatalf(format, args...)
}

// WithFields returns a new log enty with the provided fields
func WithFields(fields logrus.Fields) *logrus.Entry {
	return Logger.WithFields(fields)
}

// Writer returns the current logging writer
func Writer() *io.PipeWriter {
	return Logger.Writer()
}
