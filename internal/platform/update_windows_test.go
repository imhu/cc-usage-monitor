//go:build windows

package platform

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractExeFromZipAndReplaceInPlace(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "release.zip")

	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("windows-amd64/ccmonitor.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("NEW_BINARY_BYTES")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	extracted := filepath.Join(tmp, "extracted.exe")
	if err := ExtractExeFromZip(zipPath, extracted); err != nil {
		t.Fatalf("ExtractExeFromZip failed: %v", err)
	}

	target := filepath.Join(tmp, "ccmonitor.exe")
	if err := os.WriteFile(target, []byte("OLD_BINARY_BYTES"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceExecutableInPlace(target, extracted); err != nil {
		t.Fatalf("ReplaceExecutableInPlace failed: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil || string(got) != "NEW_BINARY_BYTES" {
		t.Fatalf("expected NEW_BINARY_BYTES, got %q err=%v", string(got), err)
	}
	oldBytes, err := os.ReadFile(target + ".old")
	if err != nil || string(oldBytes) != "OLD_BINARY_BYTES" {
		t.Fatalf("expected OLD_BINARY_BYTES in .old backup, got %q err=%v", string(oldBytes), err)
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Fatalf("expected staged .new file to be renamed away, stat err=%v", err)
	}
}
