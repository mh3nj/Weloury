package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultMaxSizeBytes is the size a log file is allowed to reach before
// rotation, when not overridden.
const DefaultMaxSizeBytes = 10 * 1024 * 1024 // 10 MiB

// DefaultMaxBackups is the number of rotated files kept alongside the
// active log file, when not overridden.
const DefaultMaxBackups = 5

// RotatingWriter is a minimal, dependency-free io.WriteCloser that rotates
// its target file once it exceeds maxSize bytes, keeping at most
// maxBackups previous files (path.1 newest to path.maxBackups oldest;
// the oldest is dropped on the next rotation).
//
// It is safe for concurrent use, though in practice log/slog's built-in
// handlers already serialize calls to Write, so contention here is rare.
type RotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxSize    int64
	maxBackups int
	file       *os.File
	size       int64
}

// NewRotatingWriter opens (creating if necessary) the log file at path.
func NewRotatingWriter(path string, maxSize int64, maxBackups int) (*RotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat log file: %w", err)
	}
	return &RotatingWriter{
		path:       path,
		maxSize:    maxSize,
		maxBackups: maxBackups,
		file:       f,
		size:       info.Size(),
	}, nil
}

// Write implements io.Writer, rotating first if p would push the file
// past maxSize.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateLocked(); err != nil {
			// A rotation failure should not lose the log line; fall back
			// to writing to the current (oversized) file rather than
			// returning an error the caller likely can't act on.
			n, werr := w.file.Write(p)
			w.size += int64(n)
			return n, werr
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotateLocked() error {
	if err := w.file.Close(); err != nil {
		return err
	}

	for i := w.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.path, i)
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	if w.maxBackups > 0 {
		if _, err := os.Stat(w.path); err == nil {
			_ = os.Rename(w.path, fmt.Sprintf("%s.1", w.path))
		}
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open rotated log file: %w", err)
	}
	w.file = f
	w.size = 0
	return nil
}

// Close closes the underlying file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
