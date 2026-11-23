package service

import (
	"os"
	"path/filepath"
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

	snapJSON := `{"plan_tier":"Pro","email":"ag@example.com","quota":{"gemini-5h":{"remaining_fraction":0.6,"reset_in_seconds":3600}},"_captured_at":"` + now.Format(time.RFC3339) + `"}`
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
