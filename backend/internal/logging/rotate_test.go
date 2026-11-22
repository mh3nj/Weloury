package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterWritesToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weloury.log")
	w, err := NewRotatingWriter(path, DefaultMaxSizeBytes, DefaultMaxBackups)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, []byte("hello\n")) {
		t.Fatalf("file contents = %q, want %q", data, "hello\n")
	}
}

func TestRotatingWriterRotatesAtMaxSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weloury.log")
	// A tiny max size forces rotation on the second write.
	w, err := NewRotatingWriter(path, 10, 3)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer w.Close()

	line := []byte(strings.Repeat("x", 8) + "\n") // 9 bytes
	if _, err := w.Write(line); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if _, err := w.Write(line); err != nil { // pushes size over 10 -> rotates first
		t.Fatalf("Write 2: %v", err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup %s.1 to exist: %v", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, line) {
		t.Fatalf("active file after rotation = %q, want only the most recent write %q", data, line)
	}
}

func TestRotatingWriterPrunesOldestBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weloury.log")
	w, err := NewRotatingWriter(path, 5, 2) // keep only 2 backups
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer w.Close()

	line := []byte("123456") // 6 bytes, exceeds maxSize=5 every time -> rotates every write after the first
	for i := 0; i < 5; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatal("expected at most 2 backups to be kept, found a 3rd")
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("expected backup .2 to exist: %v", err)
	}
}
