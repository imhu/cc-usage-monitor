//go:build windows

package platform

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CleanupStaleUpdatedBinary removes leftover .old / .new files from a previous in-place update.
func CleanupStaleUpdatedBinary() {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return
	}
	_ = os.Remove(exe + ".old")
	_ = os.Remove(exe + ".new")
}

// ExtractExeFromZip extracts ccmonitor.exe from zipPath into destPath.
func ExtractExeFromZip(zipPath, destPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var target *zip.File
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(f.Name))
		if strings.HasPrefix(clean, "../") || clean == ".." {
			continue
		}
		if strings.EqualFold(filepath.Base(clean), "ccmonitor.exe") {
			target = f
			break
		}
	}
	if target == nil {
		return fmt.Errorf("ccmonitor.exe not found in archive")
	}

	rc, err := target.Open()
	if err != nil {
		return fmt.Errorf("open zip entry: %w", err)
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create extracted binary: %w", err)
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 256<<20)); err != nil {
		_ = out.Close()
		_ = os.Remove(destPath)
		return fmt.Errorf("extract binary: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(destPath)
		return err
	}
	return nil
}

// ReplaceExecutableInPlace atomically swaps targetExe with newExePath on Windows
// by renaming the running executable to .old and moving the staged binary into place.
func ReplaceExecutableInPlace(targetExe, newExePath string) error {
	if strings.TrimSpace(targetExe) == "" {
		return fmt.Errorf("empty target executable path")
	}
	staged := targetExe + ".new"
	backup := targetExe + ".old"

	if err := copyFileToPath(newExePath, staged); err != nil {
		return fmt.Errorf("stage update binary: %w", err)
	}
	_ = os.Remove(backup)

	hadTarget := false
	if _, err := os.Stat(targetExe); err == nil {
		hadTarget = true
		if err := os.Rename(targetExe, backup); err != nil {
			_ = os.Remove(staged)
			return fmt.Errorf("backup running binary (check directory permissions): %w", err)
		}
	}

	if err := os.Rename(staged, targetExe); err != nil {
		if hadTarget {
			_ = os.Rename(backup, targetExe)
		}
		_ = os.Remove(staged)
		return fmt.Errorf("install updated binary: %w", err)
	}
	return nil
}

func copyFileToPath(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
