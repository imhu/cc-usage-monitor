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

func TestConPTYEnvDisablesAgyAutoUpdate(t *testing.T) {
	t.Setenv("AGY_TEST_STRIPPED", "should-not-leak")
	t.Setenv("AGY_CLI_DISABLE_AUTO_UPDATE", "false")

	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "env.txt")
	batFile := filepath.Join(tmp, "check_env.bat")
	script := "@echo off\r\n(\r\n  echo AUTO=%AGY_CLI_DISABLE_AUTO_UPDATE%\r\n  echo STRIP=%AGY_TEST_STRIPPED%\r\n) > \"" + outFile + "\"\r\n"
	if err := os.WriteFile(batFile, []byte(script), 0o644); err != nil {
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
	if !bytes.Contains(b, []byte("AUTO=true")) {
		t.Fatalf("expected AUTO=true in output, got:\n%s", string(b))
	}
	if bytes.Contains(b, []byte("should-not-leak")) || !bytes.Contains(b, []byte("STRIP=")) {
		t.Fatalf("expected stripped AGY_TEST_STRIPPED env var, got:\n%s", string(b))
	}
}

func TestCollectDescendantPIDs(t *testing.T) {
	entries := []procEntry{
		{pid: 100, ppid: 1, exe: "ccmonitor.exe"},
		{pid: 200, ppid: 100, exe: "conhost.exe"},
		{pid: 201, ppid: 100, exe: "agy.exe"},
		{pid: 301, ppid: 201, exe: "language_server.exe"},
		{pid: 302, ppid: 201, exe: "cmd.exe"},
		{pid: 400, ppid: 302, exe: "ccmonitor.exe"},
		{pid: 500, ppid: 999, exe: "agy.exe"},      // detached / unrelated user CLI
		{pid: 600, ppid: 600, exe: "selfcycle.exe"}, // self-parent edge case
	}
	got := collectDescendantPIDs([]uint32{100}, entries)
	for _, want := range []uint32{100, 200, 201, 301, 302, 400} {
		if !got[want] {
			t.Fatalf("expected PID %d in descendant tree, got %v", want, got)
		}
	}
	if got[500] || got[600] {
		t.Fatalf("did not expect unrelated PIDs 500/600 in descendant tree: %v", got)
	}
}
