package platform

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeGlobalConfigFilesIsolation(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom-claude")
	files := ClaudeGlobalConfigFiles(custom)
	if len(files) != 1 {
		t.Fatalf("expected only 1 config file for custom dir, got %v", files)
	}
	want := filepath.Join(custom, ".claude.json")
	if !strings.EqualFold(files[0], want) {
		t.Fatalf("expected %q, got %q", want, files[0])
	}
}
