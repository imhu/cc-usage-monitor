//go:build windows

package platform

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConPTYCommandExecutionAndCleanup(t *testing.T) {
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "out.txt")
	batFile := filepath.Join(tmp, "write.bat")
	if err := os.WriteFile(batFile, []byte("@echo off\r\necho CONPTY_OK > \""+outFile+"\"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	proc, err := StartConPTYCommand(ctx, batFile, nil, tmp)
	if err != nil {
		t.Fatalf("StartConPTYCommand failed: %v", err)
	}
	defer proc.Close()

	select {
	case <-proc.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("ConPTY command timed out")
	}

	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("expected output file from ConPTY child: %v", err)
	}
	if !bytes.Contains(b, []byte("CONPTY_OK")) {
		t.Fatalf("unexpected output file contents: %q", string(b))
	}
}

func TestRenderAntigravityTrayIcon(t *testing.T) {
	pngBytes := RenderAntigravityTrayIcon(42, true, false, true)
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("failed to decode Antigravity tray icon PNG: %v", err)
	}
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Fatalf("expected 32x32 icon, got %v", img.Bounds())
	}
	// Verify center pixel is non-transparent (star body)
	_, _, _, a := img.At(14, 14).RGBA()
	if a == 0 {
		t.Fatal("expected non-transparent star pixel near center")
	}
}

func TestLocateAgyBinaryCustomPath(t *testing.T) {
	tmp := t.TempDir()
	fakeExe := filepath.Join(tmp, "agy.exe")
	if err := os.WriteFile(fakeExe, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LocateAgyBinary(fakeExe); got != fakeExe {
		t.Fatalf("expected custom path %q, got %q", fakeExe, got)
	}
}
