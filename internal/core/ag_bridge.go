package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type AGBridgeStatusKind int

const (
	AGBridgeNotInstalled AGBridgeStatusKind = iota
	AGBridgeInstalledDirect
	AGBridgeInstalledChained
)

type AGBridgeStatus struct {
	Kind            AGBridgeStatusKind
	ExistingCommand string
	OriginalCommand string
}

func (s AGBridgeStatus) IsInstalled() bool {
	return s.Kind == AGBridgeInstalledDirect || s.Kind == AGBridgeInstalledChained
}

type agBridgeMetadata struct {
	HadPreviousStatusLine    bool   `json:"hadPreviousStatusLine"`
	PreviousCommand          string `json:"previousCommand,omitempty"`
	PreviousType             string `json:"previousType,omitempty"`
	PreviousPadding          *int   `json:"previousPadding,omitempty"`
	PreviousEnabled          *bool  `json:"previousEnabled,omitempty"`
	PreviousStackWithDefault *bool  `json:"previousStackWithDefault,omitempty"`
}

// AGBridgeInstaller manages safe, reversible statusLine hook setup in ~/.gemini/antigravity-cli/settings.json.
type AGBridgeInstaller struct {
	CLIConfigDir   string
	CaptureExePath string
}

func (i AGBridgeInstaller) SettingsPath() string {
	return filepath.Join(i.CLIConfigDir, "settings.json")
}

func (i AGBridgeInstaller) BackupPath() string {
	return filepath.Join(i.CLIConfigDir, "settings.json.usage-monitor.bak")
}

func (i AGBridgeInstaller) SnapshotPath() string {
	return filepath.Join(i.CLIConfigDir, "usage-monitor-snapshot.json")
}

func (i AGBridgeInstaller) MetadataPath() string {
	return filepath.Join(i.CLIConfigDir, "usage-monitor-bridge.json")
}

func (i AGBridgeInstaller) CaptureScriptPath() string {
	return filepath.Join(i.CLIConfigDir, "usage-monitor-capture.cmd")
}

// BuildCaptureCommand returns a Windows-safe statusLine command pointing to ccmonitor.exe --ag-capture-snapshot.
func (i AGBridgeInstaller) BuildCaptureCommand(snapshotPath, chainedCmd string) string {
	exe := strings.TrimSpace(i.CaptureExePath)
	if exe == "" {
		if p, err := os.Executable(); err == nil && p != "" {
			exe = p
		} else {
			exe = "ccmonitor.exe"
		}
	}
	cmd := fmt.Sprintf(`call "%s" --ag-capture-snapshot "%s"`, quoteWinArgInner(exe), quoteWinArgInner(snapshotPath))
	if strings.TrimSpace(chainedCmd) != "" {
		cmd += fmt.Sprintf(` --chain "%s"`, quoteWinArgInner(chainedCmd))
	}
	return cmd
}

func escapeBatchQuotedArg(s string) string {
	s = strings.ReplaceAll(s, `%`, `%%`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// BuildCaptureScript generates a quote-free UTF-8 .cmd wrapper for agy statusLine execution.
func (i AGBridgeInstaller) BuildCaptureScript(snapshotPath, chainedCmd string) string {
	exe := strings.TrimSpace(i.CaptureExePath)
	if exe == "" {
		if p, err := os.Executable(); err == nil && p != "" {
			exe = p
		} else {
			exe = "ccmonitor.exe"
		}
	}
	line := fmt.Sprintf(`"%s" --ag-capture-snapshot "%s"`, escapeBatchQuotedArg(exe), escapeBatchQuotedArg(snapshotPath))
	if strings.TrimSpace(chainedCmd) != "" {
		line += fmt.Sprintf(` --chain "%s"`, escapeBatchQuotedArg(chainedCmd))
	}
	return "@echo off\r\nchcp 65001 >nul 2>nul\r\n" + line + "\r\n"
}

func (i AGBridgeInstaller) WriteCaptureScript(scriptPath, snapshotPath, chainedCmd string) error {
	return atomicWriteFileCore(scriptPath, []byte(i.BuildCaptureScript(snapshotPath, chainedCmd)), 0o644)
}

func quoteWinArgInner(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

func (i AGBridgeInstaller) isOurCommand(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "usage-monitor-capture.cmd") ||
		(strings.Contains(command, "--ag-capture-snapshot") && strings.Contains(command, i.SnapshotPath()))
}

func (i AGBridgeInstaller) Status() AGBridgeStatus {
	root, err := i.readSettingsObject()
	if err != nil {
		return AGBridgeStatus{Kind: AGBridgeNotInstalled}
	}
	sl, ok := root["statusLine"].(map[string]any)
	if !ok {
		return AGBridgeStatus{Kind: AGBridgeNotInstalled}
	}
	cmd, _ := sl["command"].(string)
	if cmd == "" {
		return AGBridgeStatus{Kind: AGBridgeNotInstalled}
	}
	if i.isOurCommand(cmd) {
		if meta := i.readMetadata(); meta != nil && strings.TrimSpace(meta.PreviousCommand) != "" {
			return AGBridgeStatus{Kind: AGBridgeInstalledChained, OriginalCommand: meta.PreviousCommand}
		}
		return AGBridgeStatus{Kind: AGBridgeInstalledDirect}
	}
	return AGBridgeStatus{Kind: AGBridgeNotInstalled, ExistingCommand: cmd}
}

func (i AGBridgeInstaller) Install() (AGBridgeStatus, error) {
	if err := os.MkdirAll(i.CLIConfigDir, 0o755); err != nil {
		return AGBridgeStatus{}, fmt.Errorf("create config directory: %w", err)
	}

	var root map[string]any
	if raw, err := os.ReadFile(i.SettingsPath()); err == nil {
		if err := json.Unmarshal(raw, &root); err != nil || root == nil {
			return AGBridgeStatus{}, fmt.Errorf("settings.json 根节点必须是有效的 JSON 对象")
		}
		if err := atomicWriteFileCore(i.BackupPath(), raw, 0o644); err != nil {
			return AGBridgeStatus{}, fmt.Errorf("backup settings.json: %w", err)
		}
	} else if os.IsNotExist(err) {
		root = map[string]any{}
	} else {
		return AGBridgeStatus{}, fmt.Errorf("read settings.json: %w", err)
	}

	existingSL, _ := root["statusLine"].(map[string]any)
	existingCmd, _ := existingSL["command"].(string)

	var chainedCmd string
	if existingCmd != "" && i.isOurCommand(existingCmd) {
		if meta := i.readMetadata(); meta != nil {
			chainedCmd = meta.PreviousCommand
		}
	} else {
		meta := agBridgeMetadata{
			HadPreviousStatusLine: existingSL != nil,
			PreviousCommand:       existingCmd,
		}
		if t, ok := existingSL["type"].(string); ok {
			meta.PreviousType = t
		}
		if pad, ok := existingSL["padding"].(float64); ok {
			v := int(pad)
			meta.PreviousPadding = &v
		}
		if en, ok := existingSL["enabled"].(bool); ok {
			meta.PreviousEnabled = &en
		}
		if swd, ok := existingSL["stack_with_default"].(bool); ok {
			meta.PreviousStackWithDefault = &swd
		}
		if err := i.writeMetadata(meta); err != nil {
			return AGBridgeStatus{}, err
		}
		chainedCmd = existingCmd
	}

	if err := i.WriteCaptureScript(i.CaptureScriptPath(), i.SnapshotPath(), chainedCmd); err != nil {
		return AGBridgeStatus{}, fmt.Errorf("write capture script: %w", err)
	}

	newSL := map[string]any{
		"type":    "command",
		"command": i.CaptureScriptPath(),
		"enabled": true,
	}
	if existingSL != nil {
		if pad, ok := existingSL["padding"].(float64); ok {
			newSL["padding"] = int(pad)
		}
	}
	if strings.TrimSpace(chainedCmd) != "" {
		if existingSL != nil {
			if swd, ok := existingSL["stack_with_default"].(bool); ok {
				newSL["stack_with_default"] = swd
			}
		}
	} else {
		newSL["stack_with_default"] = true
	}

	root["statusLine"] = newSL
	if err := i.writeSettingsObject(root); err != nil {
		return AGBridgeStatus{}, err
	}

	if strings.TrimSpace(chainedCmd) != "" {
		return AGBridgeStatus{Kind: AGBridgeInstalledChained, OriginalCommand: chainedCmd}, nil
	}
	return AGBridgeStatus{Kind: AGBridgeInstalledDirect}, nil
}

func (i AGBridgeInstaller) Uninstall() error {
	if _, err := os.Stat(i.SettingsPath()); err == nil {
		root, err := i.readSettingsObject()
		if err != nil {
			return err
		}
		if meta := i.readMetadata(); meta != nil {
			if meta.HadPreviousStatusLine {
				restored := map[string]any{}
				if meta.PreviousCommand != "" {
					restored["command"] = meta.PreviousCommand
				}
				if meta.PreviousType != "" {
					restored["type"] = meta.PreviousType
				}
				if meta.PreviousPadding != nil {
					restored["padding"] = *meta.PreviousPadding
				}
				if meta.PreviousEnabled != nil {
					restored["enabled"] = *meta.PreviousEnabled
				}
				if meta.PreviousStackWithDefault != nil {
					restored["stack_with_default"] = *meta.PreviousStackWithDefault
				}
				root["statusLine"] = restored
			} else {
				delete(root, "statusLine")
			}
			if err := i.writeSettingsObject(root); err != nil {
				return err
			}
		} else if sl, ok := root["statusLine"].(map[string]any); ok {
			if cmd, _ := sl["command"].(string); i.isOurCommand(cmd) {
				delete(root, "statusLine")
				if err := i.writeSettingsObject(root); err != nil {
					return err
				}
			}
		}
	}
	_ = os.Remove(i.CaptureScriptPath())
	_ = os.Remove(i.MetadataPath())
	return nil
}

func (i AGBridgeInstaller) readSettingsObject() (map[string]any, error) {
	raw, err := os.ReadFile(i.SettingsPath())
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		return nil, fmt.Errorf("invalid settings.json object")
	}
	return root, nil
}

func (i AGBridgeInstaller) writeSettingsObject(root map[string]any) error {
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return atomicWriteFileCore(i.SettingsPath(), b, 0o644)
}

func (i AGBridgeInstaller) readMetadata() *agBridgeMetadata {
	raw, err := os.ReadFile(i.MetadataPath())
	if err != nil {
		return nil
	}
	var m agBridgeMetadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

func (i AGBridgeInstaller) writeMetadata(m agBridgeMetadata) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return atomicWriteFileCore(i.MetadataPath(), b, 0o644)
}

func atomicWriteFileCore(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".ccmonitor-ag-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, perm)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
