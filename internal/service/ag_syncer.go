package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
)

type AGSyncResultKind int

const (
	AGSyncSynced AGSyncResultKind = iota
	AGSyncAuthRequired
	AGSyncCLINotFound
	AGSyncFailed
)

type AGSyncResult struct {
	Kind     AGSyncResultKind
	Snapshot *core.AGStatusLineSnapshot
	ErrorMsg string
}

type AGSilentSyncer struct {
	CaptureExePath string
	mu             sync.Mutex
	running        bool
}

func NewAGSilentSyncer() *AGSilentSyncer {
	return &AGSilentSyncer{}
}

func (s *AGSilentSyncer) Sync(ctx context.Context, agyPath, snapshotPath string, timeout time.Duration) AGSyncResult {
	if strings.TrimSpace(agyPath) == "" {
		return AGSyncResult{Kind: AGSyncCLINotFound}
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: "Sync already in progress"}
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	exePath := strings.TrimSpace(s.CaptureExePath)
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil || exePath == "" {
			return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: "无法定位当前可执行文件路径"}
		}
	}

	tempDir, err := os.MkdirTemp("", "ag-silent-sync-*")
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("无法创建临时目录：%v", err)}
	}
	defer os.RemoveAll(tempDir)

	probePath := filepath.Join(tempDir, "probe.json")
	probeScriptPath := filepath.Join(tempDir, "probe.cmd")
	installer := core.AGBridgeInstaller{CaptureExePath: exePath}
	if err := installer.WriteCaptureScript(probeScriptPath, probePath, ""); err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("写入临时捕获脚本失败：%v", err)}
	}

	settingsBytes, err := buildAGProbeSettings(filepath.Join(filepath.Dir(snapshotPath), "settings.json"), probeScriptPath)
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: "序列化临时配置失败"}
	}
	settingsPath := filepath.Join(tempDir, "settings.json")
	if err := os.WriteFile(settingsPath, settingsBytes, 0o644); err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("写入临时配置失败：%v", err)}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	proc, err := platform.StartConPTYCommand(runCtx, agyPath, []string{"--settings", settingsPath}, tempDir)
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("启动 agy 失败：%v", err)}
	}
	defer proc.Close()

	deadline := time.Now().Add(timeout)
	var firstQuotaAt, firstAuthAt time.Time
	var latest *core.AGStatusLineSnapshot
	sawAuthRequired := false

	for time.Now().Before(deadline) {
		if runCtx.Err() != nil {
			break
		}
		if parsed := core.LoadAGSnapshot(probePath); parsed != nil {
			latest = parsed
			if parsed.AgentState == "authenticating" && len(parsed.Quota) == 0 && strings.TrimSpace(parsed.Email) == "" {
				if firstAuthAt.IsZero() {
					firstAuthAt = time.Now()
				} else if time.Since(firstAuthAt) >= 5*time.Second {
					sawAuthRequired = true
					break
				}
			} else {
				firstAuthAt = time.Time{}
			}
			if len(parsed.Quota) > 0 {
				if strings.TrimSpace(parsed.PlanTier) != "" && strings.TrimSpace(parsed.Email) != "" {
					break
				}
				if firstQuotaAt.IsZero() {
					firstQuotaAt = time.Now()
				} else if time.Since(firstQuotaAt) >= 2500*time.Millisecond {
					break
				}
			}
		}
		if !proc.IsRunning() {
			break
		}
		select {
		case <-runCtx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}

	proc.Close()

	if sawAuthRequired {
		return AGSyncResult{Kind: AGSyncAuthRequired}
	}
	if latest == nil || len(latest.Quota) == 0 {
		if latest == nil {
			return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: "超时未收到 agy CLI 状态回报，请确认 agy CLI 可正常运行"}
		}
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("已连接 agy CLI（状态：%s），但超时未获取到官方额度", latest.AgentState)}
	}

	merged, err := saveMergedAGSnapshot(latest, snapshotPath)
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: err.Error()}
	}
	return AGSyncResult{Kind: AGSyncSynced, Snapshot: &merged}
}

func buildAGProbeSettings(userSettingsPath, probeScriptPath string) ([]byte, error) {
	root := map[string]any{}
	if strings.TrimSpace(userSettingsPath) != "" {
		if raw, err := os.ReadFile(userSettingsPath); err == nil {
			var parsed map[string]any
			if err := json.Unmarshal(raw, &parsed); err == nil && parsed != nil {
				root = parsed
			}
		}
	}
	root["statusLine"] = map[string]any{
		"type":    "command",
		"command": probeScriptPath,
		"enabled": true,
	}
	return json.Marshal(root)
}

func saveMergedAGSnapshot(captured *core.AGStatusLineSnapshot, snapshotPath string) (core.AGStatusLineSnapshot, error) {
	prev := core.LoadAGSnapshot(snapshotPath)
	merged := mergeAGSnapshots(captured, prev, time.Now().UTC())
	data, err := json.Marshal(merged)
	if err != nil {
		return core.AGStatusLineSnapshot{}, fmt.Errorf("编码官方快照失败：%v", err)
	}
	if err := platform.AtomicWriteFile(snapshotPath, data, 0o644); err != nil {
		return core.AGStatusLineSnapshot{}, fmt.Errorf("保存官方快照失败：%v", err)
	}
	return merged, nil
}

func mergeAGSnapshots(captured, prev *core.AGStatusLineSnapshot, capturedAt time.Time) core.AGStatusLineSnapshot {
	if prev != nil {
		capEmail := core.NormalizeAGEmail(captured.Email)
		prevEmail := core.NormalizeAGEmail(prev.Email)
		if capEmail != "" && prevEmail != "" && capEmail != prevEmail {
			// Never inherit context/plan/model/cost fields from a different account's snapshot.
			prev = nil
		}
	}
	merged := core.AGStatusLineSnapshot{
		Quota:      captured.Quota,
		AgentState: captured.AgentState,
		CapturedAt: &core.AGFlexibleTime{T: &capturedAt},
	}
	if captured.ConversationID != "" {
		merged.ConversationID = captured.ConversationID
	} else if prev != nil {
		merged.ConversationID = prev.ConversationID
	}
	if captured.ConversationTitle != "" {
		merged.ConversationTitle = captured.ConversationTitle
	} else if prev != nil {
		merged.ConversationTitle = prev.ConversationTitle
	}
	if captured.Model != nil {
		merged.Model = captured.Model
	} else if prev != nil {
		merged.Model = prev.Model
	}
	if captured.Version != "" {
		merged.Version = captured.Version
	} else if prev != nil {
		merged.Version = prev.Version
	}
	if agContextWindowHasUsage(captured.ContextWindow) {
		merged.ContextWindow = captured.ContextWindow
	} else if prev != nil && agContextWindowHasUsage(prev.ContextWindow) {
		merged.ContextWindow = prev.ContextWindow
	} else if captured.ContextWindow != nil {
		merged.ContextWindow = captured.ContextWindow
	} else if prev != nil {
		merged.ContextWindow = prev.ContextWindow
	}
	if strings.TrimSpace(captured.PlanTier) != "" {
		merged.PlanTier = captured.PlanTier
	} else if prev != nil {
		merged.PlanTier = prev.PlanTier
	}
	if strings.TrimSpace(captured.Email) != "" {
		merged.Email = captured.Email
	} else if prev != nil {
		merged.Email = prev.Email
	}
	if captured.Cost != nil {
		merged.Cost = captured.Cost
	} else if prev != nil {
		merged.Cost = prev.Cost
	}
	return merged
}

func agContextWindowHasUsage(ctx *core.AGStatusLineContextWindow) bool {
	if ctx == nil {
		return false
	}
	if ctx.UsedPercentage != nil && *ctx.UsedPercentage > 0 {
		return true
	}
	if ctx.TotalInputTokens != nil && *ctx.TotalInputTokens > 0 {
		return true
	}
	if ctx.TotalOutputTokens != nil && *ctx.TotalOutputTokens > 0 {
		return true
	}
	return false
}

// RunAGCaptureSnapshot reads statusLine JSON from stdin, stamps _captured_at,
// atomically writes snapshotPath, and optionally forwards stdin to chainCmd.
func RunAGCaptureSnapshot(snapshotPath, chainCmd string) error {
	if strings.TrimSpace(snapshotPath) == "" {
		return fmt.Errorf("missing snapshot output path")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 2<<20))
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	out := stampAGSnapshotTimestamp(raw, time.Now().UTC())
	if err := platform.AtomicWriteFile(snapshotPath, out, 0o644); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}

	if strings.TrimSpace(chainCmd) != "" {
		cmd := exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow: true,
			CmdLine:    "cmd.exe /C " + chainCmd,
		}
		cmd.Stdin = bytes.NewReader(raw)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("chain command: %w", err)
		}
	}
	return nil
}

func stampAGSnapshotTimestamp(raw []byte, now time.Time) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err != nil || obj == nil {
		return raw
	}
	obj["_captured_at"] = now.UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return b
}

var (
	agLoginMu     sync.Mutex
	agLoginCancel context.CancelFunc
)

// LaunchAgyCLILogin opens a visible Windows terminal running agy for interactive sign-in,
// captures the snapshot once authenticated, and closes the terminal automatically.
func LaunchAgyCLILogin(ctx context.Context, agyPath, snapshotPath string, onSynced func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	agLoginMu.Lock()
	if agLoginCancel != nil {
		agLoginCancel()
	}
	loginCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	agLoginCancel = cancel
	agLoginMu.Unlock()

	go func() {
		defer cancel()
		if strings.TrimSpace(agyPath) == "" {
			inner := `title Antigravity Usage Monitor - agy CLI && echo agy CLI not found in PATH. Please install Antigravity CLI first.`
			cmd := exec.Command("cmd.exe")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				CmdLine: `cmd.exe /C start "agy CLI" cmd.exe /K "` + inner + `"`,
			}
			_ = cmd.Start()
			return
		}

		exePath, err := os.Executable()
		if err != nil || exePath == "" || strings.TrimSpace(snapshotPath) == "" {
			inner := fmt.Sprintf(`title Antigravity Usage Monitor - agy CLI Sign-in && echo Complete sign-in in agy below, then close this window. && call "%s"`, agyPath)
			cmd := exec.Command("cmd.exe")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				CmdLine: `cmd.exe /C start "agy CLI" cmd.exe /K "` + inner + `"`,
			}
			_ = cmd.Start()
			return
		}

		tempDir, err := os.MkdirTemp("", "ag-login-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(tempDir)

		probePath := filepath.Join(tempDir, "probe.json")
		probeScriptPath := filepath.Join(tempDir, "probe.cmd")
		installer := core.AGBridgeInstaller{CaptureExePath: exePath}
		if err := installer.WriteCaptureScript(probeScriptPath, probePath, ""); err != nil {
			return
		}
		settingsBytes, err := buildAGProbeSettings(filepath.Join(filepath.Dir(snapshotPath), "settings.json"), probeScriptPath)
		if err != nil {
			return
		}
		settingsPath := filepath.Join(tempDir, "settings.json")
		if err := os.WriteFile(settingsPath, settingsBytes, 0o644); err != nil {
			return
		}

		esc := func(s string) string {
			s = strings.ReplaceAll(s, `%`, `%%`)
			return strings.ReplaceAll(s, `"`, `\"`)
		}
		loginScriptPath := filepath.Join(tempDir, "login.cmd")
		loginScript := fmt.Sprintf(
			"@echo off\r\nchcp 65001 >nul 2>nul\r\ntitle Antigravity Usage Monitor - agy CLI Sign-in\r\necho Complete sign-in in agy below. This window closes automatically once synced.\r\n\"%s\" --settings \"%s\"\r\n",
			esc(agyPath), esc(settingsPath),
		)
		if err := os.WriteFile(loginScriptPath, []byte(loginScript), 0o644); err != nil {
			return
		}

		proc, err := platform.StartVisibleConsoleCommand(loginCtx, loginScriptPath, nil, tempDir)
		if err != nil {
			return
		}
		defer proc.Close()

		var firstQuotaAt time.Time
		var readySnap *core.AGStatusLineSnapshot
		for {
			if parsed := core.LoadAGSnapshot(probePath); parsed != nil && len(parsed.Quota) > 0 {
				readySnap = parsed
				if strings.TrimSpace(parsed.Email) != "" && strings.TrimSpace(parsed.PlanTier) != "" {
					break
				}
				if firstQuotaAt.IsZero() {
					firstQuotaAt = time.Now()
				} else if time.Since(firstQuotaAt) >= 2500*time.Millisecond {
					break
				}
			}
			select {
			case <-loginCtx.Done():
				return
			case <-proc.Done():
				if readySnap != nil {
					if _, err := saveMergedAGSnapshot(readySnap, snapshotPath); err == nil && onSynced != nil {
						onSynced()
					}
				}
				return
			case <-time.After(300 * time.Millisecond):
			}
		}

		if readySnap != nil {
			if _, err := saveMergedAGSnapshot(readySnap, snapshotPath); err == nil {
				time.Sleep(400 * time.Millisecond)
				proc.Close()
				if onSynced != nil {
					onSynced()
				}
			}
		}
	}()
}
