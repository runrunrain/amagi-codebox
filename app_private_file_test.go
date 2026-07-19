package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteClipboardImageCreatesPrivateTempFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits are validated on macOS/Linux")
	}
	raw := append([]byte{}, pngSignature...)
	raw = append(raw, 0x00)

	path, err := writeClipboardImage(raw)
	if err != nil {
		t.Fatalf("writeClipboardImage: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat temp image: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("temp image mode = %o, want 600", got)
	}
}

func TestWriteClipboardImageRejectsNonPNG(t *testing.T) {
	if _, err := writeClipboardImage([]byte("not an image")); err == nil {
		t.Fatal("expected non-PNG clipboard data to be rejected")
	}
}

func TestAtomicWriteFileCreatesPrivateExport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits are validated on macOS/Linux")
	}
	path := filepath.Join(t.TempDir(), "export.json")
	if err := atomicWriteFile(path, []byte(`{"apiKey":"secret"}`)); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat export: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("export mode = %o, want 600", got)
	}
}
