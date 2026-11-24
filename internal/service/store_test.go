package service

import (
	"os"
	"path/filepath"
	"strings"
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

func TestStoreMidweekResetDetectionAndPersistence(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	wStart := time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC)
	wEnd := wStart.Add(core.WeekWindow) // Nov 22 06:00
	tBefore := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tAfter := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	// Opus 5.5 input is $4/1M: 125M tokens = $500, 5M tokens = $20.
	lines := `{"type":"assistant","timestamp":"2026-11-16T10:00:00Z","requestId":"r1","sessionId":"s1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":125000000,"output_tokens":0}}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-11-18T10:00:00Z","requestId":"r2","sessionId":"s1","message":{"id":"m2","model":"claude-opus-5-5","usage":{"input_tokens":5000000,"output_tokens":0}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "session.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(tBefore)},
	}
	s.index.ScanRoots([]string{projDir}, nil)

	// Sync 1: 50.8% (floored to 50%) before reset.
	u1 := &core.OfficialUsage{
		SevenDay:  &core.OfficialLimit{Utilization: 50.8, ResetsAt: &wEnd},
		FetchedAt: tBefore,
	}
	s.mu.Lock()
	s.officialUsage = u1
	s.applyOfficialToStateLocked(u1)
	s.recomputeSnapshotLocked(tBefore)
	if s.pstate.WeeklyReading == nil || s.pstate.WeeklyReading.Utilization != 50 {
		s.mu.Unlock()
		t.Fatalf("expected floored WeeklyReading=50, got %+v", s.pstate.WeeklyReading)
	}
	if s.pstate.EarlierAllowance == nil || !s.pstate.EarlierAllowance.At.Equal(tBefore) {
		s.mu.Unlock()
		t.Fatalf("expected EarlierAllowance remembered at %v, got %+v", tBefore, s.pstate.EarlierAllowance)
	}
	if s.pstate.MidweekReset != nil {
		s.mu.Unlock()
		t.Fatalf("did not expect MidweekReset yet, got %+v", s.pstate.MidweekReset)
	}

	// Sync 2: drops to 2.9% (floored to 2%) within the same week -> detects mid-week reset!
	u2 := &core.OfficialUsage{
		SevenDay:  &core.OfficialLimit{Utilization: 2.9, ResetsAt: &wEnd},
		FetchedAt: tAfter,
	}
	s.officialUsage = u2
	s.applyOfficialToStateLocked(u2)
	s.recomputeSnapshotLocked(tAfter)
	if s.pstate.MidweekReset == nil || s.pstate.MidweekReset.Before.Utilization != 50 || s.pstate.MidweekReset.After.Utilization != 2 {
		s.mu.Unlock()
		t.Fatalf("expected MidweekReset 50->2, got %+v", s.pstate.MidweekReset)
	}
	if s.snapshot == nil || s.snapshot.WeeklyAllowance == nil {
		s.mu.Unlock()
		t.Fatal("expected WeeklyAllowance after midweek reset")
	}
	a := s.snapshot.WeeklyAllowance
	if !a.Borrowed || a.Forgiven < 499 || a.Forgiven > 501 {
		s.mu.Unlock()
		t.Fatalf("expected Borrowed=true and Forgiven=500, got %+v", a)
	}
	if !s.pstate.EarlierAllowance.At.Equal(tBefore) {
		s.mu.Unlock()
		t.Fatalf("borrowed post-reset allowance must not clobber EarlierAllowance.At, got %+v", s.pstate.EarlierAllowance)
	}

	// Sync 3: advancing into next week clears stale MidweekReset.
	nextWeekEnd := wEnd.Add(core.WeekWindow)
	tNext := wEnd.Add(time.Hour)
	u3 := &core.OfficialUsage{
		SevenDay:  &core.OfficialLimit{Utilization: 1.0, ResetsAt: &nextWeekEnd},
		FetchedAt: tNext,
	}
	s.officialUsage = u3
	s.applyOfficialToStateLocked(u3)
	s.recomputeSnapshotLocked(tNext)
	if s.pstate.MidweekReset != nil {
		s.mu.Unlock()
		t.Fatalf("expected stale MidweekReset cleared on new week, got %+v", s.pstate.MidweekReset)
	}
	s.mu.Unlock()
}

func TestStickySpeedDefaultAndCycle(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "projects")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	t0 := now.Add(-2 * time.Minute)
	sessionFile := filepath.Join(projDir, "s1.jsonl")

	// Start with Opus 5.5 having more output (1000 tokens) than Haiku 4.5 (200 tokens).
	part1 := `{"type":"api-request","id":"r1","timestamp":"` + t0.Format(time.RFC3339) + `","params":{"model":"claude-opus-5-5"}}` + "\n" +
		`{"type":"assistant","timestamp":"` + t0.Add(10*time.Second).Format(time.RFC3339) + `","requestId":"req1","requestRef":"r1","sessionId":"s1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":1000}}}` + "\n" +
		`{"type":"api-request","id":"r2","timestamp":"` + t0.Add(15*time.Second).Format(time.RFC3339) + `","params":{"model":"claude-haiku-4-5"}}` + "\n" +
		`{"type":"assistant","timestamp":"` + t0.Add(17*time.Second).Format(time.RFC3339) + `","requestId":"req2","requestRef":"r2","sessionId":"s1","message":{"id":"m2","model":"claude-haiku-4-5","usage":{"input_tokens":10,"output_tokens":200}}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(part1), 0o644); err != nil {
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
	}
	s.scanIndex([]string{projDir})

	st := s.ViewState()
	nowSec := float64(now.Unix()) + float64(now.Nanosecond())/1e9
	models := st.SpeedLog.Models(nowSec)
	if len(models) != 2 {
		t.Fatalf("expected 2 speed models, got %+v", models)
	}
	if active := st.ActiveSpeedModel(models); active == nil || active.ID() != "Opus 5.5" {
		t.Fatalf("expected initial sticky default Opus 5.5, got %+v", active)
	}

	// Now Haiku 4.5 outputs 3000 tokens (exceeding Opus 5.5's 1000) -> sticky default must STAY on Opus 5.5!
	part2 := part1 + `{"type":"api-request","id":"r3","timestamp":"` + t0.Add(30*time.Second).Format(time.RFC3339) + `","params":{"model":"claude-haiku-4-5"}}` + "\n" +
		`{"type":"assistant","timestamp":"` + t0.Add(40*time.Second).Format(time.RFC3339) + `","requestId":"req3","requestRef":"r3","sessionId":"s1","message":{"id":"m3","model":"claude-haiku-4-5","usage":{"input_tokens":10,"output_tokens":3000}}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(part2), 0o644); err != nil {
		t.Fatal(err)
	}
	s.scanIndex([]string{projDir})

	st = s.ViewState()
	models = st.SpeedLog.Models(nowSec)
	if models[0].ID() != "Haiku 4.5" {
		t.Fatalf("expected Haiku 4.5 ranked first by output, got %+v", models)
	}
	if active := st.ActiveSpeedModel(models); active == nil || active.ID() != "Opus 5.5" {
		t.Fatalf("expected sticky default to stay on Opus 5.5 even when Haiku leads, got %+v", active)
	}

	// Clicking CycleSpeedModel from sticky Opus 5.5 (index 1) must switch to Haiku 4.5 (index 0).
	s.CycleSpeedModel(models)
	st = s.ViewState()
	if active := st.ActiveSpeedModel(models); active == nil || active.ID() != "Haiku 4.5" {
		t.Fatalf("expected CycleSpeedModel to advance from Opus 5.5 to Haiku 4.5, got %+v", active)
	}
}

func TestExpiryRemindersAndDedup(t *testing.T) {
	tmp := t.TempDir()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.Local)

	type toast struct{ title, body string }
	var toasts []toast

	s := &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(now)},
		notifyToastFn: func(title, body string) {
			toasts = append(toasts, toast{title: title, body: body})
		},
	}
	s.prefs.BillingAnchorDay = 25 // 3 days left on Nov 22
	s.prefs.ExpiryNotifications = false

	// Disabled toggle must send nothing.
	s.mu.Lock()
	s.evaluateExpiryRemindersLocked(now)
	s.mu.Unlock()
	if len(toasts) != 0 {
		t.Fatalf("expected 0 toasts when disabled, got %+v", toasts)
	}

	// Enable -> 3-day reminder fires once, not twice.
	s.mu.Lock()
	s.prefs.ExpiryNotifications = true
	s.evaluateExpiryRemindersLocked(now)
	s.evaluateExpiryRemindersLocked(now.Add(10 * time.Minute))
	s.mu.Unlock()
	if len(toasts) != 1 || !strings.Contains(toasts[0].body, "3 天") {
		t.Fatalf("expected single 3-day reminder toast, got %+v", toasts)
	}

	// Advance to 1 day left (Nov 24) -> 1-day reminder fires once.
	nov24 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)
	s.mu.Lock()
	s.evaluateExpiryRemindersLocked(nov24)
	s.evaluateExpiryRemindersLocked(nov24.Add(time.Hour))
	s.mu.Unlock()
	if len(toasts) != 2 || !strings.Contains(toasts[1].body, "明天") {
		t.Fatalf("expected single 1-day reminder toast, got %+v", toasts)
	}

	// Health payment_failed notifies once, resets when back to active, notifies again on next failure.
	s.mu.Lock()
	s.profile = &core.AccountProfile{SubscriptionStatus: "past_due"}
	s.evaluateExpiryRemindersLocked(nov24)
	s.evaluateExpiryRemindersLocked(nov24)
	s.mu.Unlock()
	if len(toasts) != 3 || !strings.Contains(toasts[2].title, "扣款失败") {
		t.Fatalf("expected payment failed toast, got %+v", toasts)
	}
	s.mu.Lock()
	s.profile.SubscriptionStatus = "active"
	s.evaluateExpiryRemindersLocked(nov24)
	s.profile.SubscriptionStatus = "past_due"
	s.evaluateExpiryRemindersLocked(nov24)
	s.mu.Unlock()
	if len(toasts) != 4 {
		t.Fatalf("expected re-notification after recovery, got %d toasts", len(toasts))
	}

	// Login expiring in 12h (< 24h) notifies once.
	exp := nov24.Add(12 * time.Hour)
	s.mu.Lock()
	s.loginExpiresAt = &exp
	s.evaluateExpiryRemindersLocked(nov24)
	s.evaluateExpiryRemindersLocked(nov24.Add(15 * time.Minute))
	s.mu.Unlock()
	if len(toasts) != 5 || !strings.Contains(toasts[4].title, "登录即将过期") {
		t.Fatalf("expected single login-expiring toast, got %+v", toasts)
	}
}

func TestProfileIntervalDynamic1hVs6h(t *testing.T) {
	s := &Store{prefs: core.DefaultPreferences()}
	s.prefs.BillingAnchorDay = 25

	// Mid-term, healthy -> 6h
	mid := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	if got := s.profileIntervalLocked(mid); got != 6*time.Hour {
		t.Fatalf("expected 6h mid-term interval, got %v", got)
	}
	// Ending (<= 3 days left) -> 1h
	ending := time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)
	if got := s.profileIntervalLocked(ending); got != time.Hour {
		t.Fatalf("expected 1h ending interval, got %v", got)
	}
	// Just started (< 24h after renewal) -> 1h
	justStarted := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)
	if got := s.profileIntervalLocked(justStarted); got != time.Hour {
		t.Fatalf("expected 1h just-started interval, got %v", got)
	}
	// Troubled health -> 1h even mid-term
	s.profile = &core.AccountProfile{SubscriptionStatus: "past_due"}
	if got := s.profileIntervalLocked(mid); got != time.Hour {
		t.Fatalf("expected 1h troubled-health interval, got %v", got)
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
