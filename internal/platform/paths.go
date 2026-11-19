package platform

import (
	"os"
	"path/filepath"
	"strings"
)

func HomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func AppDataDir() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "ccmonitor")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "ccmonitor")
	}
	return filepath.Join(HomeDir(), ".ccmonitor")
}

func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" {
		return HomeDir()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(HomeDir(), p[2:])
	}
	return filepath.Clean(p)
}

// DefaultClaudeConfigDir returns the default .claude directory (from CLAUDE_CONFIG_DIR env or ~/.claude).
func DefaultClaudeConfigDir() string {
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		parts := strings.Split(env, ",")
		if p := expandPath(parts[0]); p != "" {
			return p
		}
	}
	return filepath.Join(HomeDir(), ".claude")
}

// ClaudeConfigDir resolves the effective .claude directory, preferring the user's custom setting.
func ClaudeConfigDir(custom string) string {
	if p := expandPath(custom); p != "" {
		return p
	}
	return DefaultClaudeConfigDir()
}

func ClaudeCredentialsFile(customConfigDir string) string {
	return filepath.Join(ClaudeConfigDir(customConfigDir), ".credentials.json")
}

func ClaudeGlobalConfigFiles(customConfigDir string) []string {
	var files []string
	seen := map[string]bool{}
	add := func(p string) {
		p = expandPath(p)
		if p == "" {
			return
		}
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	if custom := expandPath(customConfigDir); custom != "" {
		// Custom .claude directory: only inspect that directory's .claude.json so we never
		// leak another account's cache from ~/.claude.json or CLAUDE_CONFIG_DIR.
		add(filepath.Join(custom, ".claude.json"))
		if strings.EqualFold(custom, filepath.Join(HomeDir(), ".claude")) {
			add(filepath.Join(HomeDir(), ".claude.json"))
		}
		return files
	}
	if env := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); env != "" {
		for _, part := range strings.Split(env, ",") {
			if p := expandPath(part); p != "" {
				add(filepath.Join(p, ".claude.json"))
			}
		}
	}
	add(filepath.Join(HomeDir(), ".claude", ".claude.json"))
	add(filepath.Join(HomeDir(), ".claude.json"))
	return files
}

func ClaudeProjectRoots(customConfigDir, customDataDir string) []string {
	var roots []string
	seen := map[string]bool{}
	add := func(p string) {
		p = expandPath(p)
		if p == "" {
			return
		}
		if !seen[p] {
			seen[p] = true
			roots = append(roots, p)
		}
	}
	if customDataDir != "" {
		add(customDataDir)
	}
	cfgDir := ClaudeConfigDir(customConfigDir)
	add(filepath.Join(cfgDir, "projects"))
	if strings.TrimSpace(customConfigDir) == "" {
		add(filepath.Join(HomeDir(), ".claude", "projects"))
	}
	return roots
}
