package service

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
)

type AGViewState struct {
	LocalReport          core.AGLocalUsageReport
	OfficialReport       core.AGOfficialQuotaReport
	Execution            core.AGExecutionState
	BridgeStatus         core.AGBridgeStatus
	CLIBinaryPath        string
	Processes            []platform.ClaudeTask
	IsRefreshing         bool
	IsSyncing            bool
	AuthRequired         bool
	Feedback             string
	BrainDir             string
	HasBrainDir          bool
	CLIConfigDir         string
	SnapshotPath         string
	SavedFullWeekCostUSD float64
	SavedFullWeekTokens  int64
	HasMultipleAccounts  bool
	LocalAccountFiltered bool
}

func (s *Store) ensureAGLocked() {
	if s.agSyncer == nil {
		s.agSyncer = NewAGSilentSyncer()
	}
	brainDir := platform.AGBrainDir(s.prefs.AGBrainDir)
	if s.agIndex == nil {
		s.agIndex = core.NewAGTranscriptIndex(brainDir)
		s.agIndexDir = brainDir
	} else if !strings.EqualFold(s.agIndexDir, brainDir) {
		s.agIndex.SetBrainDir(brainDir)
		s.agIndexDir = brainDir
	}
}

func (s *Store) agBridgeInstallerLocked() core.AGBridgeInstaller {
	exe, _ := os.Executable()
	return core.AGBridgeInstaller{
		CLIConfigDir:   platform.AGCLIConfigDir(s.prefs.AGCLIConfigDir),
		CaptureExePath: exe,
	}
}

func (s *Store) agViewStateLocked() AGViewState {
	brainDir := platform.AGBrainDir(s.prefs.AGBrainDir)
	cliDir := platform.AGCLIConfigDir(s.prefs.AGCLIConfigDir)
	snapPath := filepath.Join(cliDir, "usage-monitor-snapshot.json")
	_, brainErr := os.Stat(brainDir)

	procs := make([]platform.ClaudeTask, len(s.agProcs))
	copy(procs, s.agProcs)

	savedCost, savedToks := s.agSavedFullWeekLocked()

	return AGViewState{
		LocalReport:          s.agLocal,
		OfficialReport:       s.agOfficial,
		Execution:            s.agExecution,
		BridgeStatus:         s.agBridge,
		CLIBinaryPath:        s.agCLIPath,
		Processes:            procs,
		IsRefreshing:         s.agRefreshing,
		IsSyncing:            s.agSyncing,
		AuthRequired:         s.agAuthRequired,
		Feedback:             s.agFeedback,
		BrainDir:             brainDir,
		HasBrainDir:          brainErr == nil,
		CLIConfigDir:         cliDir,
		SnapshotPath:         snapPath,
		SavedFullWeekCostUSD: savedCost,
		SavedFullWeekTokens:  savedToks,
		HasMultipleAccounts:  s.pstate.AGTimeline.HasMultipleAccounts(),
		LocalAccountFiltered: s.agLocalFiltered,
	}
}

func (s *Store) agActiveEmailLocked() string {
	if email := core.NormalizeAGEmail(s.agOfficial.Email); email != "" {
		return email
	}
	return core.NormalizeAGEmail(s.agActiveEmail)
}

func (s *Store) agSavedFullWeekLocked() (float64, int64) {
	email := s.agActiveEmailLocked()
	if email != "" {
		if acct, ok := s.pstate.AGAccounts[email]; ok {
			return acct.FullWeekCostUSD, acct.FullWeekTokens
		}
		return 0, 0
	}
	if len(s.pstate.AGAccounts) == 0 {
		return s.prefs.AGSavedFullWeekCostUSD, s.prefs.AGSavedFullWeekTokens
	}
	return 0, 0
}

func (s *Store) agCredScopePathLocked() string {
	if strings.TrimSpace(s.agCredScopePathOverride) != "" {
		return s.agCredScopePathOverride
	}
	return platform.AGCredentialScopeFile()
}

func readAGCredScope(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var buf [256]byte
	n, _ := f.Read(buf[:])
	return strings.TrimSpace(string(buf[:n]))
}

func (s *Store) startAG() {
	s.RefreshAntigravity(false)
	s.mu.RLock()
	enabled := s.prefs.AGOfficialEnabled
	s.mu.RUnlock()
	if enabled {
		s.SyncAntigravityOfficial(false)
	}
	go s.runAGScheduler()
	go s.startAGWatchers()
}

func (s *Store) runAGScheduler() {
	for {
		s.mu.RLock()
		sec := s.prefs.AGSyncIntervalSec
		ctx := s.ctx
		s.mu.RUnlock()
		if sec < 10 {
			sec = 10
		}
		if ctx == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(sec) * time.Second):
			s.RefreshAntigravity(false)
		}
	}
}

func (s *Store) startAGWatchers() {
	s.mu.Lock()
	if s.ctx == nil {
		s.mu.Unlock()
		return
	}
	if s.agWatchCancel != nil {
		s.agWatchCancel()
	}
	wctx, wcancel := context.WithCancel(s.ctx)
	s.agWatchCancel = wcancel
	brainDir := platform.AGBrainDir(s.prefs.AGBrainDir)
	cliDir := platform.AGCLIConfigDir(s.prefs.AGCLIConfigDir)
	credScopePath := s.agCredScopePathLocked()
	s.agLastCredScope = readAGCredScope(credScopePath)
	s.agCredScopeInit = true
	s.mu.Unlock()

	if _, err := os.Stat(cliDir); err == nil {
		go s.watchDir(wctx, cliDir, false, func() {
			s.RefreshAntigravity(false)
		})
	}
	if _, err := os.Stat(brainDir); err == nil {
		go s.watchDir(wctx, brainDir, true, func() {
			s.RefreshAntigravity(false)
		})
	}
	if desktopDir := platform.AGDesktopConfigDir(); desktopDir != "" {
		if _, err := os.Stat(desktopDir); err == nil {
			go s.watchDir(wctx, desktopDir, false, func() {
				s.onAGCredentialScopeEvent()
			})
		}
	}
}

func (s *Store) onAGCredentialScopeEvent() bool {
	s.mu.Lock()
	cur := readAGCredScope(s.agCredScopePathLocked())
	if !s.agCredScopeInit {
		s.agLastCredScope = cur
		s.agCredScopeInit = true
		s.mu.Unlock()
		return false
	}
	if cur == s.agLastCredScope {
		s.mu.Unlock()
		return false
	}
	s.agLastCredScope = cur
	s.agCredSwitchAt = time.Now().UTC()
	enabled := s.prefs.AGOfficialEnabled
	syncing := s.agSyncing
	ctx := s.ctx
	s.mu.Unlock()

	if !enabled {
		return true
	}
	if !syncing {
		go s.SyncAntigravityOfficial(true)
	} else {
		go func() {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			} else {
				time.Sleep(2 * time.Second)
			}
			s.SyncAntigravityOfficial(true)
		}()
	}
	return true
}

func (s *Store) applyAGAccountStateLocked(official core.AGOfficialQuotaReport, now time.Time) (activeEmail string, switched bool) {
	email := core.NormalizeAGEmail(official.Email)
	if email == "" {
		return core.NormalizeAGEmail(s.agActiveEmail), false
	}
	observeAt := now.UTC()
	if s.agActiveEmail != "" && s.agActiveEmail != email {
		if !s.agCredSwitchAt.IsZero() && now.Sub(s.agCredSwitchAt) <= 2*time.Minute {
			observeAt = s.agCredSwitchAt.UTC()
		} else if official.SnapshotCapturedAt != nil && !official.SnapshotCapturedAt.After(now) {
			observeAt = official.SnapshotCapturedAt.UTC()
		}
	} else if s.agActiveEmail == "" && len(s.pstate.AGTimeline.Segments) > 0 {
		if official.SnapshotCapturedAt != nil && !official.SnapshotCapturedAt.After(now) {
			observeAt = official.SnapshotCapturedAt.UTC()
		}
	}

	changed, tlSwitched := s.pstate.AGTimeline.Observe(email, observeAt)
	memSwitched := s.agActiveEmail != "" && s.agActiveEmail != email
	if tlSwitched || memSwitched {
		s.agLastAlertKey = ""
		s.agLastSeenTokens = 0
		s.agCredSwitchAt = time.Time{}
		switched = true
	}
	s.agActiveEmail = email
	if changed {
		s.savePersistedStateLocked()
	}
	return email, switched
}

func (s *Store) computeAGReports(now time.Time, idx *core.AGTranscriptIndex, snapPath string, billingDay int) (
	official core.AGOfficialQuotaReport,
	local core.AGLocalUsageReport,
	execState core.AGExecutionState,
	activeEmail string,
	filtered bool,
) {
	snap := core.LoadAGSnapshot(snapPath)
	official = core.EvaluateAGQuota(snap, now)
	var weeklyReset *time.Time
	if official.PrimaryWeekly != nil {
		weeklyReset = official.PrimaryWeekly.ResetTime
	}
	recs, convCount := idx.Refresh()
	localExec := idx.ExecutionState(now)
	execState = core.EvaluateAGExecution(localExec, official, now)

	s.mu.Lock()
	activeEmail, _ = s.applyAGAccountStateLocked(official, now)
	includeAll := s.prefs.AGSpendAllAccounts
	filteredRecs, filteredConvCount, isFiltered := s.pstate.AGTimeline.FilterRecords(recs, activeEmail, includeAll)
	if !isFiltered {
		filteredConvCount = convCount
	}
	s.mu.Unlock()

	local = core.AggregateAGUsage(filteredRecs, filteredConvCount, now, weeklyReset, billingDay)
	return official, local, execState, activeEmail, isFiltered
}

func (s *Store) syncAGFullWeekBasisLocked(official core.AGOfficialQuotaReport, local core.AGLocalUsageReport, activeEmail string) {
	if !s.prefs.AGSpendAllAccounts && activeEmail != "" &&
		official.PrimaryWeekly != nil &&
		!official.PrimaryWeekly.IsResetElapsed &&
		official.PrimaryWeekly.UsedFraction >= 0.10 &&
		local.Week.EstimatedCostUSD > 0 {
		fullCost := local.Week.EstimatedCostUSD / official.PrimaryWeekly.UsedFraction
		fullToks := int64(float64(local.Week.TotalTokens()) / official.PrimaryWeekly.UsedFraction)
		if s.pstate.AGAccounts == nil {
			s.pstate.AGAccounts = map[string]core.AGAccountPersistedState{}
		}
		prev := s.pstate.AGAccounts[activeEmail]
		if math.Abs(prev.FullWeekCostUSD-fullCost) > 0.01 || prev.FullWeekTokens != fullToks {
			s.pstate.AGAccounts[activeEmail] = core.AGAccountPersistedState{
				FullWeekCostUSD: fullCost,
				FullWeekTokens:  fullToks,
			}
			s.savePersistedStateLocked()
		}
	}

	var wantCost float64
	var wantToks int64
	if activeEmail != "" {
		if acct, ok := s.pstate.AGAccounts[activeEmail]; ok {
			wantCost = acct.FullWeekCostUSD
			wantToks = acct.FullWeekTokens
		}
	} else if len(s.pstate.AGAccounts) == 0 {
		wantCost = s.prefs.AGSavedFullWeekCostUSD
		wantToks = s.prefs.AGSavedFullWeekTokens
	}
	if math.Abs(s.prefs.AGSavedFullWeekCostUSD-wantCost) > 0.01 || s.prefs.AGSavedFullWeekTokens != wantToks {
		s.prefs.AGSavedFullWeekCostUSD = wantCost
		s.prefs.AGSavedFullWeekTokens = wantToks
		if s.prefsPath != "" {
			_ = core.SavePreferences(s.prefsPath, s.prefs)
		}
	}
}

func (s *Store) SetActiveProvider(p core.ProviderKind) {
	norm := core.NormalizeProvider(p)
	s.UpdatePrefs(func(pr *core.Preferences) {
		pr.ActiveProvider = norm
	})
	if norm == core.ProviderAntigravity {
		s.mu.RLock()
		needExit := s.ctx != nil && (s.exitPlace == nil || s.exitFailed)
		s.mu.RUnlock()
		if needExit {
			go s.CheckExit(false)
		}
	}
}

func (s *Store) RefreshAntigravity(forceSync bool) {
	s.mu.Lock()
	s.ensureAGLocked()
	idx := s.agIndex
	inst := s.agBridgeInstallerLocked()
	customCLI := s.prefs.AGAgyCLIPath
	billingDay := s.prefs.AGBillingAnchorDay
	enabled := s.prefs.AGOfficialEnabled
	s.agRefreshing = true
	s.mu.Unlock()
	s.notify()

	now := time.Now()
	bridgeStatus := inst.Status()
	cliPath := platform.LocateAgyBinary(customCLI)
	official, local, execState, activeEmail, isFiltered := s.computeAGReports(now, idx, inst.SnapshotPath(), billingDay)
	procs := platform.ListAntigravityProcesses()

	s.mu.Lock()
	s.agBridge = bridgeStatus
	s.agCLIPath = cliPath
	s.agOfficial = official
	s.agLocal = local
	s.agExecution = execState
	s.agLocalFiltered = isFiltered
	s.syncAGFullWeekBasisLocked(official, local, activeEmail)
	s.agProcs = procs
	s.agRefreshing = false
	if official.AgentState == "authenticating" && len(official.Buckets) == 0 {
		s.agAuthRequired = true
	} else if official.HasSnapshot() && len(official.Buckets) > 0 {
		s.agAuthRequired = false
	}

	curTokens := local.Week.TotalTokens()
	tokensIncreased := curTokens > s.agLastSeenTokens
	s.agLastSeenTokens = curTokens

	alreadySyncing := s.agSyncing
	sinceLastAttempt := now.Sub(s.agLastAutoSync)
	alertEnabled := s.prefs.AGAlertEnabled
	zh := s.prefs.IsChinese()
	lastAlert := s.agLastAlertKey
	alertKey, alertTitle, alertBody := evaluateAGAlert(official, alertEnabled, zh)
	if alertKey != "" && alertKey != lastAlert {
		s.agLastAlertKey = alertKey
	} else {
		alertKey = ""
	}
	s.mu.Unlock()
	s.notify()

	if alertKey != "" {
		go func(t, b string) {
			_ = mygo.NewNotification(mygo.NotificationOptions{Title: t, Body: b}).Show()
		}(alertTitle, alertBody)
	}

	if !enabled || cliPath == "" {
		return
	}

	if forceSync {
		go s.SyncAntigravityOfficial(true)
		return
	}

	if !alreadySyncing && sinceLastAttempt >= 90*time.Second {
		age := official.StaleAge
		if !official.HasSnapshot() || age >= 5*time.Minute || (tokensIncreased && age >= 2*time.Minute) {
			go s.SyncAntigravityOfficial(false)
		}
	}
}

func (s *Store) SyncAntigravityOfficial(force bool) {
	s.mu.Lock()
	s.ensureAGLocked()
	if s.agSyncing {
		s.mu.Unlock()
		return
	}
	if !force && !s.agLastAutoSync.IsZero() && time.Since(s.agLastAutoSync) < 90*time.Second {
		s.mu.Unlock()
		return
	}
	customCLI := s.prefs.AGAgyCLIPath
	inst := s.agBridgeInstallerLocked()
	syncer := s.agSyncer
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	zh := s.prefs.IsChinese()
	s.agSyncing = true
	s.agLastAutoSync = time.Now()
	s.mu.Unlock()
	s.notify()

	cliPath := platform.LocateAgyBinary(customCLI)
	res := syncer.Sync(ctx, cliPath, inst.SnapshotPath(), 20*time.Second)

	s.mu.Lock()
	s.agSyncing = false
	s.agCLIPath = cliPath
	switch res.Kind {
	case AGSyncSynced:
		s.agAuthRequired = false
		s.agFeedback = ""
	case AGSyncAuthRequired:
		s.agAuthRequired = true
		if zh {
			s.agFeedback = "agy CLI 需要重新登录，点击按钮在终端完成登录后即可关闭终端。"
		} else {
			s.agFeedback = "agy CLI requires sign-in. Complete login in terminal once, then close it."
		}
	case AGSyncCLINotFound:
		if force {
			if zh {
				s.agFeedback = "未找到 agy CLI，请先安装 Antigravity CLI 或在设置中指定路径。"
			} else {
				s.agFeedback = "agy CLI not found. Install Antigravity CLI or set its path in Settings."
			}
		}
	case AGSyncFailed:
		if force || !s.agOfficial.HasSnapshot() {
			s.agFeedback = res.ErrorMsg
		}
	}
	s.mu.Unlock()
	s.notify()

	if res.Kind == AGSyncSynced {
		s.RefreshAntigravity(false)
	}
}

func (s *Store) InstallAGBridge() {
	s.mu.Lock()
	inst := s.agBridgeInstallerLocked()
	zh := s.prefs.IsChinese()
	s.mu.Unlock()

	st, err := inst.Install()
	s.mu.Lock()
	if err != nil {
		if zh {
			s.agFeedback = fmt.Sprintf("安装桥接配置失败：%v", err)
		} else {
			s.agFeedback = fmt.Sprintf("Install bridge failed: %v", err)
		}
	} else {
		s.agBridge = st
		if st.Kind == core.AGBridgeInstalledChained {
			if zh {
				s.agFeedback = "已安装 statusLine 链式桥接，保留原有自定义脚本。"
			} else {
				s.agFeedback = "Installed chained statusLine bridge preserving your script."
			}
		} else {
			if zh {
				s.agFeedback = "已安装 statusLine 桥接配置，下次运行 agy 时自动同步快照。"
			} else {
				s.agFeedback = "Installed statusLine bridge. Next agy session updates snapshot."
			}
		}
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Store) UninstallAGBridge() {
	s.mu.Lock()
	inst := s.agBridgeInstallerLocked()
	zh := s.prefs.IsChinese()
	s.mu.Unlock()

	err := inst.Uninstall()
	s.mu.Lock()
	if err != nil {
		if zh {
			s.agFeedback = fmt.Sprintf("卸载失败：%v", err)
		} else {
			s.agFeedback = fmt.Sprintf("Uninstall failed: %v", err)
		}
	} else {
		s.agBridge = inst.Status()
		if zh {
			s.agFeedback = "已移除桥接配置并还原 settings.json。"
		} else {
			s.agFeedback = "Removed statusLine bridge and restored settings.json."
		}
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Store) RebuildAGIndex() {
	go func() {
		s.mu.Lock()
		s.ensureAGLocked()
		s.agIndex.Reset()
		s.mu.Unlock()
		s.RefreshAntigravity(false)
	}()
}

func (s *Store) ForceQuitAntigravityProcess(task platform.ClaudeTask) {
	go func() {
		_ = platform.ForceQuitTask(task)
		time.Sleep(300 * time.Millisecond)
		procs := platform.ListAntigravityProcesses()
		s.mu.Lock()
		s.agProcs = procs
		s.mu.Unlock()
		s.notify()
	}()
}

// RefreshAntigravityLocal performs a lightweight local-only refresh (transcript index + snapshot file + processes)
// without triggering background CLI sync, used while the popover panel is open.
func (s *Store) RefreshAntigravityLocal() {
	s.mu.Lock()
	s.ensureAGLocked()
	idx := s.agIndex
	inst := s.agBridgeInstallerLocked()
	billingDay := s.prefs.AGBillingAnchorDay
	s.mu.Unlock()

	now := time.Now()
	official, local, execState, activeEmail, isFiltered := s.computeAGReports(now, idx, inst.SnapshotPath(), billingDay)
	procs := platform.ListAntigravityProcesses()

	s.mu.Lock()
	s.agOfficial = official
	s.agLocal = local
	s.agExecution = execState
	s.agLocalFiltered = isFiltered
	s.syncAGFullWeekBasisLocked(official, local, activeEmail)
	s.agProcs = procs
	s.mu.Unlock()
	s.notify()
}

func (s *Store) SetAGExecutionForTest(exec core.AGExecutionState) {
	s.mu.Lock()
	s.agExecution = exec
	s.mu.Unlock()
	s.notify()
}

func (s *Store) LaunchAgyLogin() {
	s.mu.RLock()
	customCLI := s.prefs.AGAgyCLIPath
	inst := s.agBridgeInstallerLocked()
	ctx := s.ctx
	zh := s.prefs.IsChinese()
	s.mu.RUnlock()
	if ctx == nil {
		ctx = context.Background()
	}

	cliPath := platform.LocateAgyBinary(customCLI)
	LaunchAgyCLILogin(ctx, cliPath, inst.SnapshotPath(), func() {
		s.RefreshAntigravity(false)
	})

	s.mu.Lock()
	if cliPath != "" {
		if zh {
			s.agFeedback = "已打开终端启动 agy。完成登录后窗口将自动关闭并同步额度。"
		} else {
			s.agFeedback = "Opened terminal with agy. Once signed in, it closes and syncs automatically."
		}
	} else {
		if zh {
			s.agFeedback = "未检测到 agy CLI，请在终端提示中确认安装路径或在设置中指定。"
		} else {
			s.agFeedback = "agy CLI not found in PATH. Check installation or set path in Settings."
		}
	}
	s.mu.Unlock()
	s.notify()
}

func evaluateAGAlert(rep core.AGOfficialQuotaReport, enabled bool, zh bool) (key, title, body string) {
	if !enabled || !rep.HasSnapshot() || rep.IsStale {
		return "", "", ""
	}
	var target *core.AGQuotaBucketEval
	if rep.PrimaryFiveHour != nil && !rep.PrimaryFiveHour.IsResetElapsed {
		target = rep.PrimaryFiveHour
	} else if rep.PrimaryWeekly != nil && !rep.PrimaryWeekly.IsResetElapsed {
		target = rep.PrimaryWeekly
	}
	if target == nil {
		return "", "", ""
	}
	pct := target.UsedPercentInt()
	if pct >= 95 {
		key = target.BucketID + ":95"
		if zh {
			return key, "Antigravity 额度即将耗尽", fmt.Sprintf("%s 已使用 %d%%", core.AGBucketDisplayName(target.BucketID, target.WindowKind, true), pct)
		}
		return key, "Antigravity Quota Critical", fmt.Sprintf("%s reached %d%%", target.DisplayName, pct)
	}
	if target.IsOverPace() {
		key = target.BucketID + ":pace"
		if zh {
			return key, "Antigravity 用量超出安全线", fmt.Sprintf("%s 当前 %d%%（已超过安全配速）", core.AGBucketDisplayName(target.BucketID, target.WindowKind, true), pct)
		}
		return key, "Antigravity Over Safe Pace", fmt.Sprintf("%s is at %d%% (over pace)", target.DisplayName, pct)
	}
	return "", "", ""
}
