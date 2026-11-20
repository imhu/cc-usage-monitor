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

func TestDayRolloverResetsTodayUsageWithoutNewLogs(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write one session record timestamped yesterday (local time).
	yesterday := time.Now().AddDate(0, 0, -1)
	line := `{"type":"assistant","timestamp":"` + yesterday.UTC().Format(time.RFC3339) + `","requestId":"r-yday","sessionId":"s1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":100000,"output_tokens":50000}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "session.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	notified := 0
	s := &Store{
		onChange:  func() { notified++ },
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(time.Now())},
	}

	// Initial scan ingests yesterday's record.
	s.scanIndex([]string{projDir})
	// Simulate that the cached snapshot was last computed yesterday (before midnight).
	s.mu.Lock()
	s.recomputeSnapshotLocked(yesterday)
	if s.snapshot == nil || s.snapshot.Day.Cost <= 0 || s.snapshot.Day.Requests != 1 {
		s.mu.Unlock()
		t.Fatalf("expected non-zero yesterday Day snapshot, got %+v", s.snapshot)
	}
	s.mu.Unlock()
	notified = 0

	// Now run scanIndex today with NO new log bytes written -> must detect midnight rollover and reset today to 0.
	s.scanIndex([]string{projDir})

	st := s.ViewState()
	if st.Snapshot == nil {
		t.Fatal("expected non-nil snapshot after rollover")
	}
	if st.Snapshot.Day.Cost != 0 || st.Snapshot.Day.Requests != 0 {
		t.Fatalf("expected today's cost/requests reset to 0 after midnight rollover, got cost=%v reqs=%d", st.Snapshot.Day.Cost, st.Snapshot.Day.Requests)
	}
	if today := st.Snapshot.Today(); today == nil || today.Cost != 0 {
		t.Fatalf("expected Daily Today() cost to be 0 after midnight rollover, got %+v", today)
	}
	if notified == 0 {
		t.Fatal("expected UI notify() to fire on midnight rollover")
	}
}

func TestCredentialsEventIgnoresUnrelatedFiles(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(claudeDir, ".credentials.json")
	credJSON := []byte(`{"claudeAiOauth":{"accessToken":"tok-1","refreshToken":"ref-1","expiresAt":4102444800000,"scopes":["user:profile","user:inference"]}}`)
	if err := os.WriteFile(credPath, credJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatal(err)
	}

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(time.Now())},
		// Simulate post-429 failed state where activeLoginID is empty
		officialState: OfficialFailed,
		lastCredMtime: info.ModTime(),
		lastCredSize:  info.Size(),
	}
	s.prefs.ClaudeConfigDir = claudeDir
	s.prefs.ExitGuard = false

	// Writing history.jsonl in ~/.claude must NOT trigger SyncOfficial.
	if err := os.WriteFile(filepath.Join(claudeDir, "history.jsonl"), []byte(`{"prompt":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.onCredentialsFileEvent()
	if s.lastReqAt != nil {
		t.Fatalf("unrelated file write in .claude must not trigger SyncOfficial, got lastReqAt=%v", s.lastReqAt)
	}
}

func TestTryReadClaudeCodeCacheAdoptsFreshUsageDuringCooldown(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	fetchedMs := now.Add(-10 * time.Second).UnixMilli()
	raw := []byte(`{"oauthAccount":{"accountUuid":"acct-1","emailAddress":"u@example.com"},"cachedUsageUtilization":{"fetchedAtMs":` +
		strconvFormatInt(fetchedMs) + `,"accountUuid":"acct-1","utilization":{"five_hour":{"utilization":15.2},"seven_day":{"utilization":48.0}}}}`)
	if err := os.WriteFile(filepath.Join(claudeDir, ".claude.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(now)},
		profile:   &core.AccountProfile{AccountUUID: "acct-1"},
		officialUsage: &core.OfficialUsage{
			FiveHour:  &core.OfficialLimit{Utilization: 2},
			FetchedAt: now.Add(-17 * time.Hour),
		},
		officialState:   OfficialFailed,
		officialFailMsg: "HTTP 429",
		isRateLimited:   true,
	}
	s.prefs.ClaudeConfigDir = claudeDir

	s.TryReadClaudeCodeCache()
	st := s.ViewState()
	if st.OfficialUsage == nil || st.OfficialUsage.FiveHour == nil || st.OfficialUsage.FiveHour.Percent() != 15 {
		t.Fatalf("expected Claude Code cache (15%%) to be adopted, got %+v", st.OfficialUsage)
	}
	if st.OfficialState != OfficialConnected {
		t.Fatalf("expected OfficialConnected after adopting Claude Code cache, got %v", st.OfficialState)
	}
	if !st.UsageFromClaudeCode {
		t.Fatal("expected UsageFromClaudeCode=true")
	}
}

func strconvFormatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
