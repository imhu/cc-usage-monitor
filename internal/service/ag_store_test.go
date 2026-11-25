package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccmonitor/internal/core"
)

func TestAGCaptureSnapshotStampsTimestamp(t *testing.T) {
	tmp := t.TempDir()
	snapPath := filepath.Join(tmp, "usage-monitor-snapshot.json")
	payload := `{"plan_tier":"Ultra","email":"u@example.com","quota":{"gemini-5h":{"remaining_fraction":0.75,"reset_in_seconds":7200}}}`

	stamped := stampAGSnapshotTimestamp([]byte(payload), time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC))
	if err := os.WriteFile(snapPath, stamped, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := core.LoadAGSnapshot(snapPath)
	if loaded == nil || loaded.PlanTier != "Ultra" || loaded.Email != "u@example.com" {
		t.Fatalf("unexpected loaded snapshot: %+v", loaded)
	}
	if loaded.CapturedTime() == nil || loaded.CapturedTime().Year() != 2026 {
		t.Fatalf("expected stamped _captured_at, got %+v", loaded.CapturedTime())
	}
}

func TestStoreAntigravityRefreshAndSwitchProvider(t *testing.T) {
	tmp := t.TempDir()
	brainDir := filepath.Join(tmp, "brain")
	cliDir := filepath.Join(tmp, "ag-cli")
	logsDir := filepath.Join(brainDir, "conv-1", ".system_generated", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	rec := `{"created_at":"` + now.Add(-2*time.Minute).Format(time.RFC3339) + `","model":"gemini-4-neon-xhigh","input_tokens":100000,"cache_read_tokens":400000,"output_tokens":10000}` + "\n"
	if err := os.WriteFile(filepath.Join(logsDir, "transcript.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	snapJSON := `{"plan_tier":"Pro","email":"ag@example.com","quota":{"gemini-5h":{"remaining_fraction":0.6,"reset_in_seconds":3600},"gemini-weekly":{"remaining_fraction":0.6,"reset_in_seconds":345600}},"_captured_at":"` + now.Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(cliDir, "usage-monitor-snapshot.json"), []byte(snapJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	prefs := core.DefaultPreferences()
	prefs.AGOfficialEnabled = false
	prefs.AGBrainDir = brainDir
	prefs.AGCLIConfigDir = cliDir

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     prefs,
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(now)},
	}

	s.SetActiveProvider(core.ProviderAntigravity)
	s.RefreshAntigravity(false)

	st := s.ViewState()
	if st.Prefs.ActiveProvider != core.ProviderAntigravity {
		t.Fatalf("expected active provider antigravity, got %v", st.Prefs.ActiveProvider)
	}
	if !st.AG.OfficialReport.HasSnapshot() || st.AG.OfficialReport.PrimaryFiveHour == nil || st.AG.OfficialReport.PrimaryFiveHour.UsedPercentInt() != 40 {
		t.Fatalf("expected AG 5h=40%%, got %+v", st.AG.OfficialReport)
	}
	if st.AG.LocalReport.Today.RequestCount != 1 || st.AG.LocalReport.Today.CacheReadTokens != 400000 {
		t.Fatalf("expected AG local record parsed, got %+v", st.AG.LocalReport.Today)
	}
	if st.Prefs.AGSavedFullWeekCostUSD <= 0 || st.Prefs.AGSavedFullWeekTokens <= 0 {
		t.Fatalf("expected persisted full-week basis when weekly >= 10%%, got cost=%v tokens=%d",
			st.Prefs.AGSavedFullWeekCostUSD, st.Prefs.AGSavedFullWeekTokens)
	}

	s.InstallAGBridge()
	st = s.ViewState()
	if !st.AG.BridgeStatus.IsInstalled() {
		t.Fatalf("expected AG bridge installed, got %+v", st.AG.BridgeStatus)
	}
	s.UninstallAGBridge()
	st = s.ViewState()
	if st.AG.BridgeStatus.IsInstalled() {
		t.Fatalf("expected AG bridge uninstalled, got %+v", st.AG.BridgeStatus)
	}
}

func TestShouldProbeExitByProvider(t *testing.T) {
	tmp := t.TempDir()
	emptyClaudeDir := filepath.Join(tmp, "claude-empty")
	prefs := core.DefaultPreferences()
	prefs.ClaudeConfigDir = emptyClaudeDir
	prefs.ActiveProvider = core.ProviderClaude

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     prefs,
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(time.Now())},
	}

	if s.shouldProbeExit() {
		t.Fatal("expected shouldProbeExit=false when Claude is active without credentials")
	}

	s.SetActiveProvider(core.ProviderAntigravity)
	if !s.shouldProbeExit() {
		t.Fatal("expected shouldProbeExit=true when Antigravity is active even without Claude credentials")
	}
	if s.exitPlace != nil {
		t.Fatalf("expected offline SetActiveProvider not to probe exit when ctx is nil, got %+v", s.exitPlace)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_AG_CAPTURE_HELPER") == "1" {
		for i := 0; i < len(os.Args)-1; i++ {
			if os.Args[i] == "--ag-capture-snapshot" {
				if err := RunAGCaptureSnapshot(os.Args[i+1], ""); err != nil {
					os.Exit(1)
				}
				os.Exit(0)
			}
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func TestAGCaptureScriptViaGoCmdExec(t *testing.T) {
	tmp := t.TempDir()
	spacedDir := filepath.Join(tmp, "ag-测试 空间")
	if err := os.MkdirAll(spacedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	snapPath := filepath.Join(spacedDir, "probe.json")
	scriptPath := filepath.Join(spacedDir, "probe.cmd")
	inst := core.AGBridgeInstaller{CaptureExePath: exe}
	if err := inst.WriteCaptureScript(scriptPath, snapPath, ""); err != nil {
		t.Fatalf("WriteCaptureScript failed: %v", err)
	}

	cmd := exec.Command("cmd", "/c", scriptPath)
	cmd.Env = append(os.Environ(), "GO_WANT_AG_CAPTURE_HELPER=1")
	cmd.Stdin = strings.NewReader(`{"plan_tier":"Pro","quota":{"gemini-5h":{"remaining_fraction":0.75,"reset_in_seconds":3600}}}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd /c script failed: %v\noutput: %s", err, string(out))
	}
	snap := core.LoadAGSnapshot(snapPath)
	if snap == nil || snap.PlanTier != "Pro" || snap.CapturedTime() == nil || len(snap.Quota) != 1 {
		t.Fatalf("unexpected captured snapshot: %+v", snap)
	}
}

func TestMergeAGSnapshotsPreservesRealContextOverEmptyProbe(t *testing.T) {
	prevPct := 34.0
	prevIn := int64(340000)
	prevSize := int64(1_048_576)
	prev := &core.AGStatusLineSnapshot{
		PlanTier: "Google AI Pro",
		ContextWindow: &core.AGStatusLineContextWindow{
			TotalInputTokens:  &prevIn,
			ContextWindowSize: &prevSize,
			UsedPercentage:    &prevPct,
		},
	}

	zeroPct := 0.0
	zeroTok := int64(0)
	captured := &core.AGStatusLineSnapshot{
		AgentState: "initializing",
		ContextWindow: &core.AGStatusLineContextWindow{
			TotalInputTokens:  &zeroTok,
			TotalOutputTokens: &zeroTok,
			ContextWindowSize: &prevSize,
			UsedPercentage:    &zeroPct,
		},
	}

	merged := mergeAGSnapshots(captured, prev, time.Date(2026, 11, 24, 14, 30, 0, 0, time.UTC))
	if merged.ContextWindow == nil || merged.ContextWindow.UsedPercentage == nil || *merged.ContextWindow.UsedPercentage != 34.0 {
		t.Fatalf("expected prev ContextWindow (34%%) preserved, got %+v", merged.ContextWindow)
	}
	if merged.PlanTier != "Google AI Pro" {
		t.Fatalf("expected PlanTier preserved, got %q", merged.PlanTier)
	}
}

func TestBuildAGProbeSettingsMergesUserFields(t *testing.T) {
	tmp := t.TempDir()
	userSettings := filepath.Join(tmp, "settings.json")
	orig := `{"model":"Gemini 4 Neon (High)","trustedWorkspaces":["D:\\work"],"statusLine":{"command":"old.cmd"}}`
	if err := os.WriteFile(userSettings, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := buildAGProbeSettings(userSettings, `C:\tmp\probe.cmd`)
	if err != nil {
		t.Fatalf("buildAGProbeSettings failed: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"Gemini 4 Neon (High)"`) || !strings.Contains(s, `"trustedWorkspaces"`) {
		t.Fatalf("expected user settings fields preserved, got: %s", s)
	}
	if !strings.Contains(s, `probe.cmd`) || strings.Contains(s, `old.cmd`) {
		t.Fatalf("expected statusLine replaced with probe.cmd, got: %s", s)
	}

	// Missing or invalid user settings falls back cleanly
	b2, err := buildAGProbeSettings(filepath.Join(tmp, "missing.json"), `C:\tmp\probe2.cmd`)
	if err != nil || !strings.Contains(string(b2), `probe2.cmd`) {
		t.Fatalf("expected clean fallback for missing file, err=%v out=%s", err, string(b2))
	}
}
