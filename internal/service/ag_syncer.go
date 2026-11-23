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
	mu      sync.Mutex
	running bool
}

func NewAGSilentSyncer() *AGSilentSyncer {
	return &AGSilentSyncer{}
}

func (s *AGSilentSyncer) Sync(ctx context.Context, agyPath, snapshotPath string, timeout time.Duration) AGSyncResult {
	if strings.TrimSpace(agyPath) == "" {
		return AGSyncResult{Kind: AGSyncCLINotFound}
	}
	if timeout <= 0 {
		timeout = 12 * time.Second
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

	exePath, err := os.Executable()
	if err != nil || exePath == "" {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: "无法定位当前可执行文件路径"}
	}

	tempDir, err := os.MkdirTemp("", "ag-silent-sync-*")
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("无法创建临时目录：%v", err)}
	}
	defer os.RemoveAll(tempDir)

	probePath := filepath.Join(tempDir, "probe.json")
	captureCmd := (core.AGBridgeInstaller{CaptureExePath: exePath}).BuildCaptureCommand(probePath, "")

	settingsBytes, err := json.Marshal(map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": captureCmd,
			"enabled": true,
		},
	})
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: "序列化临时配置失败"}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	proc, err := platform.StartConPTYCommand(runCtx, agyPath, []string{"--settings", string(settingsBytes)}, tempDir)
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("启动 agy 失败：%v", err)}
	}
	defer proc.Close()

	deadline := time.Now().Add(timeout)
	var firstQuotaAt time.Time
	var latest *core.AGStatusLineSnapshot
	sawAuthRequired := false

	for time.Now().Before(deadline) {
		if runCtx.Err() != nil {
			break
		}
		if parsed := core.LoadAGSnapshot(probePath); parsed != nil {
			latest = parsed
			if parsed.AgentState == "authenticating" {
				sawAuthRequired = true
				break
			}
			if len(parsed.Quota) > 0 {
				if strings.TrimSpace(parsed.PlanTier) != "" {
					break
				}
				if firstQuotaAt.IsZero() {
					firstQuotaAt = time.Now()
				} else if time.Since(firstQuotaAt) >= 800*time.Millisecond {
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
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: "超时未获取到官方额度，请确认 agy CLI 已登录"}
	}

	prev := core.LoadAGSnapshot(snapshotPath)
	merged := mergeAGSnapshots(latest, prev, time.Now().UTC())

	data, err := json.Marshal(merged)
	if err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("编码官方快照失败：%v", err)}
	}
	if err := platform.AtomicWriteFile(snapshotPath, data, 0o644); err != nil {
		return AGSyncResult{Kind: AGSyncFailed, ErrorMsg: fmt.Sprintf("保存官方快照失败：%v", err)}
	}

	return AGSyncResult{Kind: AGSyncSynced, Snapshot: &merged}
}

func mergeAGSnapshots(captured, prev *core.AGStatusLineSnapshot, capturedAt time.Time) core.AGStatusLineSnapshot {
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
	if captured.ContextWindow != nil {
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
			CmdLine: "cmd.exe /C " + chainCmd,
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

// LaunchAgyCLILogin opens a Windows terminal running agy for interactive sign-in.
func LaunchAgyCLILogin(agyPath string) {
	go func() {
		var inner string
		if strings.TrimSpace(agyPath) != "" {
			inner = fmt.Sprintf(`title Antigravity Usage Monitor - agy CLI Sign-in && echo Complete sign-in in agy below, then close this window. && call "%s"`, agyPath)
		} else {
			inner = `title Antigravity Usage Monitor - agy CLI && echo agy CLI not found in PATH. Please install Antigravity CLI first.`
		}
		cmd := exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CmdLine: `cmd.exe /C start "agy CLI" cmd.exe /K "` + inner + `"`,
		}
		_ = cmd.Start()
	}()
}
