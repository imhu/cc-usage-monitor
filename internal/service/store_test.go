package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ccmonitor/internal/core"
)

func TestSwitchClaudeConfigDirClearsStaleQuota(t *testing.T) {
	tmp := t.TempDir()
	oldDir := filepath.Join(tmp, "claude-old")
	emptyDir := filepath.Join(tmp, "claude-empty")

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate: persistedState{
			ClaudeConfigDir: oldDir,
			Budget:          core.NewRequestBudget(time.Now()),
		},
		officialState: OfficialConnected,
	}
	s.prefs.ClaudeConfigDir = oldDir
	// Populate stale official usage and profile from the old directory
	now := time.Now()
	s.officialUsage = &core.OfficialUsage{
		FiveHour:  &core.OfficialLimit{Utilization: 42},
		FetchedAt: now,
	}
	s.profile = &core.AccountProfile{Email: "old@example.com"}
	s.pstate.LastUsage = s.officialUsage
	s.pstate.Profile = s.profile

	// Switch to an empty .claude directory (no .credentials.json, no .claude.json)
	s.UpdatePrefs(func(p *core.Preferences) {
		p.ClaudeConfigDir = emptyDir
	})

	// Immediately after UpdatePrefs, stale quota & profile must be cleared
	st := s.ViewState()
	if st.OfficialUsage != nil {
		t.Fatalf("expected OfficialUsage to be cleared on dir switch, got %+v", st.OfficialUsage)
	}
	if st.AccountEmail != "" {
		t.Fatalf("expected AccountEmail to be cleared on dir switch, got %q", st.AccountEmail)
	}

	// SyncOfficial with ExitGuard enabled & ExitPlace nil (exit blocked) must still
	// detect missing credentials locally and set OfficialNoCredentials with zero network calls.
	s.SyncOfficial(true, true)
	st = s.ViewState()
	if st.OfficialUsage != nil {
		t.Fatalf("expected OfficialUsage to remain nil, got %+v", st.OfficialUsage)
	}
	if st.OfficialState != OfficialNoCredentials {
		t.Fatalf("expected OfficialNoCredentials, got %v", st.OfficialState)
	}
}

func TestUnauthorizedNeverCallsClaudeAPI(t *testing.T) {
	tmp := t.TempDir()
	emptyDir := filepath.Join(tmp, "claude-empty")

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate: persistedState{
			Budget: core.NewRequestBudget(time.Now()),
		},
		officialState: OfficialConnecting,
	}
	s.prefs.ClaudeConfigDir = emptyDir
	s.prefs.ExitGuard = false

	// Without credentials, syncLocalAuthState / maybeAutoSync / SyncOfficial must not consume budget or call API.
	if s.syncLocalAuthState() {
		t.Fatal("expected syncLocalAuthState=false when no credentials exist")
	}
	s.maybeAutoSync()
	s.SyncOfficial(false, true)
	s.SyncOfficial(true, true)

	if s.lastReqAt != nil {
		t.Fatalf("expected zero Claude requests when unauthorized, got lastReqAt=%v", s.lastReqAt)
	}
	if s.exitPlace != nil {
		t.Fatalf("expected zero exit probes when unauthorized, got exitPlace=%+v", s.exitPlace)
	}
	if st := s.ViewState(); st.OfficialState != OfficialNoCredentials {
		t.Fatalf("expected OfficialNoCredentials, got %v", st.OfficialState)
	}

	// With a revoked/dead credential, auto-sync must also stay completely silent (zero retries).
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	credJSON := []byte(`{"claudeAiOauth":{"accessToken":"tok-dead","refreshToken":"ref-dead","expiresAt":4102444800000,"scopes":["user:profile","user:inference"]}}`)
	if err := os.WriteFile(filepath.Join(emptyDir, ".credentials.json"), credJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	s.deadLoginID = "ref-dead"
	if s.hasAuthorizedCredentials() {
		t.Fatal("expected hasAuthorizedCredentials=false for dead/revoked credential")
	}
	s.SyncOfficial(false, false)
	if s.lastReqAt != nil {
		t.Fatalf("expected zero retries for dead credential, got lastReqAt=%v", s.lastReqAt)
	}
}
