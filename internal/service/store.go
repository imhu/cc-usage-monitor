package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
)

type IndexPhase int

const (
	PhaseLaunching IndexPhase = iota
	PhaseIndexing
	PhaseReady
	PhaseEmpty
)

type OfficialStateKind int

const (
	OfficialConnecting OfficialStateKind = iota
	OfficialConnected
	OfficialDisabled
	OfficialNoCredentials
	OfficialExpired
	OfficialSignedOut
	OfficialFailed
)

type ViewState struct {
	Prefs core.Preferences

	Phase          IndexPhase
	IndexProgress  float64
	IndexSize      int64
	Snapshot       *core.UsageSnapshot
	SpeedLog       core.SpeedLog
	SpeedModelID   string
	SpeedDefaultID string

	// Official usage
	OfficialState       OfficialStateKind
	OfficialFailMsg     string
	OfficialUsage       *core.OfficialUsage
	UsageFromClaudeCode bool
	IsFetching          bool
	IsRateLimited       bool
	RetryWait           time.Duration
	LastUpdated         *time.Time

	// Account & subscription
	DetectedPlan       core.PlanKind
	SubscriptionHealth core.SubscriptionHealth
	SubscriptionTerm   *core.SubscriptionTerm
	AccountEmail       string
	AccountUUID        string
	LoginExpiresAt     *time.Time
	ProfileCheckedAt   *time.Time

	// Exit check
	ExitPlace     *core.PublicNetwork
	ExitIPv6      *core.PublicNetwork
	ExitFailed    bool
	ExitChecking  bool
	Shield        platform.ShieldState
	ExitBlocked   bool
	IPv6IsDirect  bool

	// Processes
	Processes []platform.ClaudeTask

	// Runtime data roots
	ClaudeConfigDir    string
	HasCredentialsFile bool
	HasProjectsDir     bool
	DataRoots          []string

	// Antigravity
	AG AGViewState
}

// ActiveSpeedModel returns the model currently displayed for output speed,
// preferring user choice, then sticky auto-default, then highest-output model.
func (st ViewState) ActiveSpeedModel(models []core.ModelSpeed) *core.ModelSpeed {
	for _, id := range []string{st.SpeedModelID, st.SpeedDefaultID} {
		if id == "" {
			continue
		}
		for i := range models {
			if models[i].ID() == id {
				return &models[i]
			}
		}
	}
	if len(models) > 0 {
		return &models[0]
	}
	return nil
}

type persistedState struct {
	ClaudeConfigDir  string               `json:"claudeConfigDir,omitempty"`
	Budget           core.RequestBudget   `json:"budget"`
	WeeklyReset      *time.Time           `json:"weeklyReset,omitempty"`
	WeeklyReading    *core.WeeklyReading  `json:"weeklyReading,omitempty"`
	EarlierAllowance *core.AllowanceBasis `json:"earlierAllowance,omitempty"`
	MidweekReset     *core.MidweekReset   `json:"midweekReset,omitempty"`
	LastUsage        *core.OfficialUsage  `json:"lastUsage,omitempty"`
	Profile          *core.AccountProfile `json:"profile,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	onChange func()

	dataDir   string
	prefsPath string
	indexPath string
	statePath string

	prefs core.Preferences
	index *core.UsageIndex
	pstate persistedState

	phase          IndexPhase
	indexProgress  float64
	snapshot       *core.UsageSnapshot
	speedLog       core.SpeedLog
	speedModelID   string
	speedDefaultID string

	officialState       OfficialStateKind
	officialFailMsg     string
	officialUsage       *core.OfficialUsage
	usageFromClaudeCode  bool
	isFetching           bool
	isRateLimited        bool
	lastReqAt            *time.Time
	lastSyncLifetimeCost float64
	lastSyncWindowCost   float64
	lastSyncFiveHourPct  int
	lastSyncWeekCost     float64
	lastSyncSevenDayPct  int

	profile          *core.AccountProfile
	profileCheckedAt *time.Time
	loginExpiresAt   *time.Time
	deadLoginID      string
	activeLoginID    string
	lastCredMtime    time.Time
	lastCredSize     int64

	exitPlace    *core.PublicNetwork
	exitIPv6     *core.PublicNetwork
	exitFailed   bool
	exitChecking bool
	exitAt       time.Time

	processes []platform.ClaudeTask

	lastAlertPct int

	ctx         context.Context
	cancel      context.CancelFunc
	watchCancel context.CancelFunc

	// Antigravity state
	agIndex          *core.AGTranscriptIndex
	agIndexDir       string
	agSyncer         *AGSilentSyncer
	agLocal          core.AGLocalUsageReport
	agOfficial       core.AGOfficialQuotaReport
	agBridge         core.AGBridgeStatus
	agCLIPath        string
	agProcs          []platform.ClaudeTask
	agRefreshing     bool
	agSyncing        bool
	agAuthRequired   bool
	agFeedback       string
	agLastAutoSync   time.Time
	agLastSeenTokens int64
	agLastAlertKey   string
	agWatchCancel    context.CancelFunc
}

func NewStore(onChange func()) *Store {
	dataDir := platform.AppDataDir()
	_ = os.MkdirAll(dataDir, 0o755)
	prefsPath := filepath.Join(dataDir, "prefs.json")
	indexPath := filepath.Join(dataDir, "index.gob")
	statePath := filepath.Join(dataDir, "state.json")

	prefs := core.LoadPreferences(prefsPath)
	s := &Store{
		onChange:      onChange,
		dataDir:       dataDir,
		prefsPath:     prefsPath,
		indexPath:     indexPath,
		statePath:     statePath,
		prefs:         prefs,
		index:         core.NewUsageIndex(),
		pstate:        persistedState{Budget: core.NewRequestBudget(time.Now())},
		phase:         PhaseLaunching,
		officialState: OfficialConnecting,
	}
	s.loadPersistedState()
	curClaudeDir := platform.ClaudeConfigDir(prefs.ClaudeConfigDir)
	dirMismatch := s.pstate.ClaudeConfigDir != "" && !strings.EqualFold(s.pstate.ClaudeConfigDir, curClaudeDir)
	if dirMismatch || !s.hasAnyAuthSource(prefs.ClaudeConfigDir) {
		s.resetOfficialAccountLocked()
	}
	if s.pstate.LastUsage != nil {
		s.officialUsage = s.pstate.LastUsage
		s.officialState = OfficialConnected
	}
	if s.pstate.Profile != nil {
		s.profile = s.pstate.Profile
	}
	if !prefs.OfficialEnabled {
		s.officialState = OfficialDisabled
	} else if !s.hasAuthorizedCredentials() {
		s.officialState = OfficialNoCredentials
	} else if s.pstate.Budget.InCooldown(time.Now()) {
		s.isRateLimited = true
		if s.officialUsage == nil {
			s.officialState = OfficialFailed
			s.officialFailMsg = "429"
		}
	}
	return s
}

func (s *Store) hasAuthorizedCredentials() bool {
	creds, _, _ := s.loadCredentials()
	if creds == nil || creds.AccessToken == "" {
		return false
	}
	s.mu.RLock()
	dead := s.deadLoginID != "" && s.deadLoginID == creds.LoginID()
	s.mu.RUnlock()
	return !dead
}

// syncLocalAuthState checks credentials purely on disk (zero network requests)
// and updates OfficialState when unauthorized. Returns true iff callable credentials exist.
func (s *Store) syncLocalAuthState() bool {
	creds, cleared, _ := s.loadCredentials()
	if creds == nil || creds.AccessToken == "" {
		s.mu.Lock()
		if !s.usageFromClaudeCode {
			s.resetOfficialAccountLocked()
			s.recomputeSnapshotLocked(time.Now())
		}
		if s.prefs.OfficialEnabled {
			if cleared {
				s.officialState = OfficialSignedOut
			} else {
				s.officialState = OfficialNoCredentials
			}
		} else {
			s.officialState = OfficialDisabled
		}
		s.mu.Unlock()
		s.notify()
		return false
	}
	s.mu.RLock()
	dead := s.deadLoginID != "" && s.deadLoginID == creds.LoginID()
	s.mu.RUnlock()
	return !dead
}

func (s *Store) hasAnyAuthSource(customClaudeDir string) bool {
	if data, err := os.ReadFile(platform.ClaudeCredentialsFile(customClaudeDir)); err == nil {
		if creds, _ := core.ParseCredentialsJSON(data); creds != nil {
			return true
		}
	}
	for _, cfg := range platform.ClaudeGlobalConfigFiles(customClaudeDir) {
		if data, err := os.ReadFile(cfg); err == nil {
			if parsed := core.ParseClaudeCodeConfig(data); parsed != nil && (parsed.Usage != nil || parsed.Account != nil) {
				return true
			}
		}
	}
	return false
}

func (s *Store) resetOfficialAccountLocked() {
	s.officialUsage = nil
	s.usageFromClaudeCode = false
	s.officialFailMsg = ""
	s.profile = nil
	s.profileCheckedAt = nil
	s.loginExpiresAt = nil
	s.deadLoginID = ""
	s.activeLoginID = ""
	s.lastCredMtime = time.Time{}
	s.lastCredSize = 0
	s.lastSyncLifetimeCost = 0
	s.lastSyncWindowCost = 0
	s.lastSyncFiveHourPct = 0
	s.lastSyncWeekCost = 0
	s.lastSyncSevenDayPct = 0
	s.lastAlertPct = 0
	s.pstate.LastUsage = nil
	s.pstate.Profile = nil
	s.pstate.WeeklyReset = nil
	s.pstate.WeeklyReading = nil
	s.pstate.EarlierAllowance = nil
	s.pstate.MidweekReset = nil
	s.pstate.ClaudeConfigDir = platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir)
	s.savePersistedStateLocked()
}

func (s *Store) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// 1. Load local index + initial incremental scan
	go s.bootIndex()

	// 2. Local cache check; only probe exit & official API when authorized credentials exist
	go func() {
		s.RefreshProcesses()
		s.TryReadClaudeCodeCache()
		if !s.syncLocalAuthState() {
			return
		}
		s.CheckExit(false)
		s.mu.RLock()
		enabled := s.prefs.OfficialEnabled
		s.mu.RUnlock()
		if enabled {
			s.SyncOfficial(false, true)
		}
	}()

	// 3. Background periodic scheduler
	go s.runScheduler()

	// 4. Watch ~/.claude/projects and ~/.claude.json via mygo.WatchFiles
	go s.startFileWatchers()

	// 5. Start Antigravity local scanner, watchers, and background CLI syncer
	go s.startAG()
}

func (s *Store) Stop() {
	s.mu.Lock()
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	if s.agWatchCancel != nil {
		s.agWatchCancel()
		s.agWatchCancel = nil
	}
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	if s.index != nil && s.index.Dirty {
		_ = s.index.Save(s.indexPath)
	}
	s.savePersistedStateLocked()
	s.mu.Unlock()
}

func (s *Store) notify() {
	if s.onChange != nil {
		s.onChange()
	}
}

func (s *Store) bootIndex() {
	idx := core.LoadUsageIndex(s.indexPath)
	s.mu.Lock()
	s.index = idx
	if len(idx.Records) > 0 {
		s.phase = PhaseReady
		s.recomputeSnapshotLocked(time.Now())
	} else {
		s.phase = PhaseIndexing
	}
	roots := platform.ClaudeProjectRoots(s.prefs.ClaudeConfigDir, s.prefs.CustomDataDir)
	s.mu.Unlock()
	s.notify()

	s.scanIndex(roots)
}

func (s *Store) scanIndex(roots []string) {
	s.mu.RLock()
	idx := s.index
	s.mu.RUnlock()

	// Clone? ScanRoots mutates index; serialize via a scan lock or directly under mu for incremental speed.
	// To avoid blocking UI during initial multi-GB scan, scan into a separate pass if launching.
	s.mu.Lock()
	isInitial := len(s.index.Records) == 0
	s.mu.Unlock()

	if isInitial {
		fresh := core.NewUsageIndex()
		fresh.ScanRoots(roots, func(p float64) {
			s.mu.Lock()
			s.indexProgress = p
			s.mu.Unlock()
			s.notify()
		})
		s.mu.Lock()
		s.index = fresh
		if len(fresh.Records) > 0 {
			s.phase = PhaseReady
		} else {
			s.phase = PhaseEmpty
		}
		s.recomputeSnapshotLocked(time.Now())
		_ = s.index.Save(s.indexPath)
		s.mu.Unlock()
		s.notify()
		return
	}

	s.mu.Lock()
	stats := idx.ScanRoots(roots, nil)
	if len(idx.Records) > 0 {
		s.phase = PhaseReady
	} else {
		s.phase = PhaseEmpty
	}
	now := time.Now()
	rolled := s.snapshotRolledOverLocked(now)
	cooldownCleared := false
	if s.isRateLimited && !s.pstate.Budget.InCooldown(now) {
		s.isRateLimited = false
		if s.officialState == OfficialFailed && s.officialUsage != nil {
			s.officialState = OfficialConnected
			s.officialFailMsg = ""
		}
		cooldownCleared = true
	}
	if stats.RecordsChanged || s.snapshot == nil || rolled {
		s.recomputeSnapshotLocked(now)
		if stats.RecordsChanged && idx.Dirty {
			_ = idx.Save(s.indexPath)
		}
	} else if stats.TimingsChanged {
		s.updateSpeedLogLocked(now)
	}
	s.mu.Unlock()
	if stats.RecordsChanged || stats.TimingsChanged || rolled || cooldownCleared {
		s.notify()
	}
}

// snapshotRolledOverLocked reports whether the cached snapshot has crossed a day,
// weekly, billing-cycle, or official limit reset boundary since it was generated.
func (s *Store) snapshotRolledOverLocked(now time.Time) bool {
	if s.snapshot == nil {
		return false
	}
	if !now.Before(s.snapshot.Day.End) || now.Before(s.snapshot.Day.Start) {
		return true
	}
	if !now.Before(s.snapshot.Billing.End) || now.Before(s.snapshot.Billing.Start) {
		return true
	}
	if s.snapshot.Week != nil && (!now.Before(s.snapshot.Week.End) || now.Before(s.snapshot.Week.Start)) {
		return true
	}
	if s.officialUsage != nil {
		gen := s.snapshot.GeneratedAt
		crossed := func(lim *core.OfficialLimit) bool {
			return lim != nil && lim.ResetsAt != nil && lim.ResetsAt.After(gen) && !lim.ResetsAt.After(now)
		}
		if crossed(s.officialUsage.FiveHour) || crossed(s.officialUsage.SevenDay) ||
			crossed(s.officialUsage.SevenDaySonnet) || crossed(s.officialUsage.SevenDayOpus) {
			return true
		}
		for i := range s.officialUsage.Scoped {
			if crossed(&s.officialUsage.Scoped[i].Limit) {
				return true
			}
		}
	}
	return false
}

func (s *Store) RebuildIndex() {
	go func() {
		s.mu.Lock()
		s.index = core.NewUsageIndex()
		s.phase = PhaseIndexing
		s.indexProgress = 0
		roots := platform.ClaudeProjectRoots(s.prefs.ClaudeConfigDir, s.prefs.CustomDataDir)
		s.mu.Unlock()
		s.notify()
		s.scanIndex(roots)
	}()
}

func (s *Store) startFileWatchers() {
	s.mu.Lock()
	if s.ctx == nil {
		s.mu.Unlock()
		return
	}
	if s.watchCancel != nil {
		s.watchCancel()
	}
	wctx, wcancel := context.WithCancel(s.ctx)
	s.watchCancel = wcancel
	claudeDir := s.prefs.ClaudeConfigDir
	customDir := s.prefs.CustomDataDir
	s.mu.Unlock()

	roots := platform.ClaudeProjectRoots(claudeDir, customDir)
	for _, root := range roots {
		if _, err := os.Stat(root); err == nil {
			go s.watchDir(wctx, root, true, func() {
				s.mu.RLock()
				r := platform.ClaudeProjectRoots(s.prefs.ClaudeConfigDir, s.prefs.CustomDataDir)
				s.mu.RUnlock()
				s.scanIndex(r)
			})
		}
	}
	// Watch .claude.json config cache files
	for _, cfg := range platform.ClaudeGlobalConfigFiles(claudeDir) {
		if _, err := os.Stat(cfg); err == nil {
			go s.watchDir(wctx, cfg, false, func() {
				s.TryReadClaudeCodeCache()
			})
		}
	}
	// Watch configured .claude directory so CLI login (.credentials.json creation/update)
	// automatically wakes up syncing once authorized. Snapshot current .credentials.json
	// stat first so unrelated files (history.jsonl, settings.json, etc.) never trigger syncs.
	resolvedClaudeDir := platform.ClaudeConfigDir(claudeDir)
	credFile := platform.ClaudeCredentialsFile(claudeDir)
	if info, err := os.Stat(credFile); err == nil {
		s.mu.Lock()
		s.lastCredMtime = info.ModTime()
		s.lastCredSize = info.Size()
		s.mu.Unlock()
	}
	if _, err := os.Stat(resolvedClaudeDir); err == nil {
		go s.watchDir(wctx, resolvedClaudeDir, false, func() {
			s.onCredentialsFileEvent()
		})
	}
}

func (s *Store) onCredentialsFileEvent() {
	s.mu.RLock()
	claudeDir := s.prefs.ClaudeConfigDir
	prevMtime := s.lastCredMtime
	prevSize := s.lastCredSize
	s.mu.RUnlock()

	credFile := platform.ClaudeCredentialsFile(claudeDir)
	info, err := os.Stat(credFile)
	if err != nil {
		s.mu.Lock()
		hadFile := !s.lastCredMtime.IsZero()
		s.lastCredMtime = time.Time{}
		s.lastCredSize = 0
		s.mu.Unlock()
		if hadFile {
			s.syncLocalAuthState()
		}
		return
	}
	// Ignore all unrelated writes in ~/.claude (e.g. history.jsonl, debug, todos).
	if info.ModTime().Equal(prevMtime) && info.Size() == prevSize {
		return
	}
	s.mu.Lock()
	s.lastCredMtime = info.ModTime()
	s.lastCredSize = info.Size()
	s.mu.Unlock()

	creds, _, _ := s.loadCredentials()
	if creds == nil || creds.AccessToken == "" {
		s.syncLocalAuthState()
		return
	}
	id := creds.LoginID()
	s.mu.RLock()
	enabled := s.prefs.OfficialEnabled
	state := s.officialState
	prevActive := s.activeLoginID
	sameDead := s.deadLoginID != "" && s.deadLoginID == id
	needExit := s.exitPlace == nil
	s.mu.RUnlock()

	awaitingLogin := state == OfficialNoCredentials || state == OfficialSignedOut || state == OfficialExpired
	loginSwitched := prevActive != "" && prevActive != id
	if sameDead || (!awaitingLogin && !loginSwitched) {
		return
	}
	if needExit {
		s.CheckExit(false)
	}
	if enabled {
		s.SyncOfficial(false, true)
	}
}

func (s *Store) watchDir(ctx context.Context, target string, recursive bool, onHit func()) {
	w, err := mygo.WatchFiles(ctx, target, mygo.FileWatchOptions{
		Recursive: recursive,
		Debounce:  250 * time.Millisecond,
		MaxDelay:  1500 * time.Millisecond,
	})
	if err != nil {
		return
	}
	defer w.Close()
	for {
		events, err := w.Next(ctx)
		if err != nil {
			return
		}
		if len(events) > 0 {
			onHit()
		}
	}
}

func (s *Store) runScheduler() {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	exitTick := time.NewTicker(5 * time.Minute)
	defer exitTick.Stop()
	scanTick := time.NewTicker(15 * time.Second)
	defer scanTick.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-scanTick.C:
			s.mu.RLock()
			roots := platform.ClaudeProjectRoots(s.prefs.ClaudeConfigDir, s.prefs.CustomDataDir)
			s.mu.RUnlock()
			s.scanIndex(roots)
			s.TryReadClaudeCodeCache()
		case <-exitTick.C:
			if s.hasAuthorizedCredentials() {
				s.CheckExit(false)
			}
		case <-tick.C:
			s.maybeAutoSync()
		}
	}
}

func (s *Store) maybeAutoSync() {
	s.mu.RLock()
	enabled := s.prefs.OfficialEnabled
	interval := time.Duration(s.prefs.SyncIntervalSec) * time.Second
	act := s.currentActivityLocked(time.Now())
	lastReq := s.lastReqAt
	s.mu.RUnlock()

	if !enabled || !s.hasAuthorizedCredentials() {
		return
	}
	now := time.Now()
	if core.ShouldAutoSync(interval, core.OccasionTick, act, lastReq, now) {
		go s.SyncOfficial(false, false)
	}
}

func (s *Store) OnPanelOpened() {
	go func() {
		s.mu.RLock()
		activeProvider := core.NormalizeProvider(s.prefs.ActiveProvider)
		s.mu.RUnlock()
		if activeProvider == core.ProviderAntigravity {
			s.RefreshAntigravity(false)
			return
		}

		s.RefreshProcesses()
		s.mu.RLock()
		roots := platform.ClaudeProjectRoots(s.prefs.ClaudeConfigDir, s.prefs.CustomDataDir)
		checkOnOpen := s.prefs.OfficialEnabled && s.prefs.CheckOnOpen
		act := s.currentActivityLocked(time.Now())
		lastReq := s.lastReqAt
		hasUsage := s.officialUsage != nil
		s.mu.RUnlock()

		s.scanIndex(roots)
		s.TryReadClaudeCodeCache()

		now := time.Now()
		spacingOK := lastReq == nil || now.Sub(*lastReq) >= core.AutoSyncMinSpacing
		if checkOnOpen && spacingOK && s.hasAuthorizedCredentials() && (!hasUsage || core.ShouldCheckOnOpen(act, lastReq, now)) {
			s.SyncOfficial(false, false)
		}
	}()
}

func (s *Store) RefreshNow() {
	go func() {
		s.mu.RLock()
		activeProvider := core.NormalizeProvider(s.prefs.ActiveProvider)
		s.mu.RUnlock()
		if activeProvider == core.ProviderAntigravity {
			s.RefreshAntigravity(true)
			return
		}

		s.RefreshProcesses()
		s.mu.RLock()
		roots := platform.ClaudeProjectRoots(s.prefs.ClaudeConfigDir, s.prefs.CustomDataDir)
		enabled := s.prefs.OfficialEnabled
		s.mu.RUnlock()
		s.scanIndex(roots)
		s.TryReadClaudeCodeCache()

		creds, _, _ := s.loadCredentials()
		if creds == nil || creds.AccessToken == "" {
			s.syncLocalAuthState()
			return
		}
		// Manual refresh with existing credentials: allow retrying a previously rejected credential once.
		s.mu.Lock()
		s.deadLoginID = ""
		s.mu.Unlock()
		s.CheckExit(true)
		if enabled {
			s.SyncOfficial(true, true)
		}
	}()
}

func (s *Store) RefreshProcesses() {
	tasks := platform.ListClaudeProcesses()
	s.mu.Lock()
	s.processes = tasks
	s.mu.Unlock()
	s.notify()
}

func (s *Store) ForceQuitProcess(pid uint32) {
	go func() {
		_ = platform.ForceQuitProcess(pid)
		time.Sleep(300 * time.Millisecond)
		s.RefreshProcesses()
	}()
}

func (s *Store) effectiveProxyLocked() string {
	if s.prefs.ProxyURL != "" {
		return s.prefs.ProxyURL
	}
	return platform.SystemProxyURL()
}

func (s *Store) CheckExit(force bool) {
	s.mu.Lock()
	if !force && s.exitPlace != nil && !s.exitFailed && time.Since(s.exitAt) < 10*time.Minute {
		s.mu.Unlock()
		return
	}
	if s.exitChecking {
		s.mu.Unlock()
		return
	}
	s.exitChecking = true
	proxyRaw := s.effectiveProxyLocked()
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var v4, v6 *core.PublicNetwork
	var err4 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		v4, err4 = core.ProbeTrace(ctx, proxyRaw, "tcp4")
	}()
	go func() {
		defer wg.Done()
		// Probe direct IPv6 leak (no proxy) just like macOS version
		v6, _ = core.ProbeTrace(ctx, "", "tcp6")
	}()
	wg.Wait()

	s.mu.Lock()
	s.exitChecking = false
	s.exitAt = time.Now()
	if err4 == nil && v4 != nil {
		s.exitPlace = v4
		s.exitFailed = false
	} else {
		s.exitFailed = true
	}
	s.exitIPv6 = v6
	s.mu.Unlock()
	s.notify()
}

func (s *Store) TryReadClaudeCodeCache() {
	s.mu.RLock()
	claudeDir := s.prefs.ClaudeConfigDir
	s.mu.RUnlock()

	var bestAccount *core.ClaudeCodeAccount
	var newestUsage *core.ClaudeCodeUsageCache

	for _, file := range platform.ClaudeGlobalConfigFiles(claudeDir) {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		cfg := core.ParseClaudeCodeConfig(data)
		if cfg == nil {
			continue
		}
		if cfg.Account != nil && bestAccount == nil {
			bestAccount = cfg.Account
		}
		if cfg.Usage != nil {
			if newestUsage == nil || cfg.Usage.Usage.FetchedAt.After(newestUsage.Usage.FetchedAt) {
				u := *cfg.Usage
				newestUsage = &u
			}
		}
	}

	if bestAccount == nil && newestUsage == nil {
		return
	}

	changed := false
	s.mu.Lock()
	if bestAccount != nil && s.profile == nil {
		s.profile = &core.AccountProfile{
			AccountUUID:      bestAccount.UUID,
			Email:            bestAccount.Email,
			OrganizationType: bestAccount.OrganizationType,
			RateLimitTier:    bestAccount.RateLimitTier,
		}
		changed = true
	}
	if newestUsage != nil {
		var shownAt *time.Time
		if s.officialUsage != nil {
			t := s.officialUsage.FetchedAt
			shownAt = &t
		}
		acctUUID := ""
		if s.profile != nil {
			acctUUID = s.profile.AccountUUID
		}
		now := time.Now()
		if newestUsage.Replaces(shownAt, acctUUID, now) {
			u := newestUsage.Usage
			s.officialUsage = &u
			s.usageFromClaudeCode = true
			if s.officialState == OfficialConnecting || s.officialState == OfficialFailed {
				s.officialState = OfficialConnected
				s.officialFailMsg = ""
			}
			s.applyOfficialToStateLocked(&u)
			s.recomputeSnapshotLocked(now)
			s.savePersistedStateLocked()
			changed = true
		}
	}
	s.mu.Unlock()
	if changed {
		s.notify()
	}
}

func (s *Store) loadCredentials() (*core.Credentials, bool, string) {
	s.mu.RLock()
	claudeDir := s.prefs.ClaudeConfigDir
	s.mu.RUnlock()
	credFile := platform.ClaudeCredentialsFile(claudeDir)
	data, err := os.ReadFile(credFile)
	if err != nil {
		return nil, false, credFile
	}
	creds, cleared := core.ParseCredentialsJSON(data)
	if creds != nil {
		return creds, false, credFile
	}
	return nil, cleared, credFile
}

func (s *Store) SyncOfficial(manual bool, fetchProfile bool) {
	now := time.Now()
	s.mu.Lock()
	if !s.prefs.OfficialEnabled {
		s.officialState = OfficialDisabled
		s.mu.Unlock()
		s.notify()
		return
	}
	if s.isFetching {
		s.mu.Unlock()
		return
	}
	reqClaudeDir := platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir)
	s.mu.Unlock()

	// Check local credentials BEFORE ExitGuard / Budget so switching to a directory
	// without credentials immediately clears stale quota without any network request.
	creds, cleared, credFile := s.loadCredentials()
	if creds == nil || creds.AccessToken == "" {
		s.mu.Lock()
		if !s.usageFromClaudeCode {
			s.resetOfficialAccountLocked()
			s.recomputeSnapshotLocked(now)
		}
		if cleared {
			s.officialState = OfficialSignedOut
		} else {
			s.officialState = OfficialNoCredentials
		}
		s.mu.Unlock()
		s.notify()
		return
	}

	s.mu.Lock()
	if !s.prefs.OfficialEnabled || s.isFetching {
		s.mu.Unlock()
		return
	}
	// Enforce minimum 10s spacing between any two official requests (auto and manual).
	if s.lastReqAt != nil && now.Sub(*s.lastReqAt) < core.AutoSyncMinSpacing {
		s.mu.Unlock()
		return
	}
	// Never keep hammering Claude endpoints with a credential already known to be revoked/expired.
	if !manual && s.deadLoginID != "" && s.deadLoginID == creds.LoginID() {
		s.mu.Unlock()
		return
	}
	// Enforce exit guard
	if s.prefs.ExitGuard && s.isExitBlockedLocked() {
		s.mu.Unlock()
		return
	}
	// Check fresh Claude Code cache first if not manual (within 60s of now)
	if !manual && s.officialUsage != nil && s.usageFromClaudeCode && (core.ClaudeCodeUsageCache{Usage: *s.officialUsage}).IsFresh(now) {
		s.mu.Unlock()
		return
	}
	// Check request budget
	if manual {
		if !s.pstate.Budget.CanManual(now) {
			s.isRateLimited = s.pstate.Budget.InCooldown(now)
			s.mu.Unlock()
			s.notify()
			return
		}
	} else {
		if !s.pstate.Budget.CanAuto(now) {
			s.isRateLimited = s.pstate.Budget.InCooldown(now)
			s.mu.Unlock()
			return
		}
	}
	s.isFetching = true
	proxyRaw := s.effectiveProxyLocked()
	autoRenew := s.prefs.AutoRenewLogin
	if creds.RefreshTokenExpiresAt != nil {
		s.loginExpiresAt = creds.RefreshTokenExpiresAt
	}
	s.mu.Unlock()
	s.notify()

	defer func() {
		s.mu.Lock()
		s.isFetching = false
		s.mu.Unlock()
		s.notify()
	}()

	client := core.BuildHTTPClient(proxyRaw, "tcp4", 15*time.Second)

	// Consume budget before any network request to Claude (including token refresh)
	// so failed refreshes cannot spin in a tight retry loop and trigger 429.
	consumed := false
	consumeBudget := func() {
		if consumed {
			return
		}
		consumed = true
		s.mu.Lock()
		reqNow := time.Now()
		s.pstate.Budget.Consume(reqNow)
		s.lastReqAt = &reqNow
		s.savePersistedStateLocked()
		s.mu.Unlock()
	}

	applyRenewFailure := func(res core.RenewResult, cur core.Credentials) {
		s.mu.Lock()
		defer s.mu.Unlock()
		zh := s.prefs.IsChinese()
		switch res.Failure {
		case core.RenewFailRateLimited:
			s.pstate.Budget.RecordRateLimit(time.Now(), res.RetryAfter)
			s.isRateLimited = true
			s.officialState = OfficialFailed
			if res.Err != nil {
				s.officialFailMsg = res.Err.Error()
			} else {
				s.officialFailMsg = "429"
			}
			s.savePersistedStateLocked()
		case core.RenewFailSignedOut:
			if res.UsedRefreshToken != "" {
				s.deadLoginID = res.UsedRefreshToken
			} else {
				s.deadLoginID = cur.LoginID()
			}
			s.officialState = OfficialSignedOut
		case core.RenewFailNoCredentials:
			if res.Cleared {
				s.officialState = OfficialSignedOut
			} else {
				s.officialState = OfficialNoCredentials
			}
		case core.RenewFailNotRenewable:
			s.deadLoginID = cur.LoginID()
			s.officialState = OfficialExpired
		case core.RenewFailLocked:
			s.officialState = OfficialFailed
			if zh {
				s.officialFailMsg = "Claude Code 正在续期"
			} else {
				s.officialFailMsg = "Claude Code is renewing"
			}
		case core.RenewFailNotWritable:
			s.officialState = OfficialFailed
			if zh {
				s.officialFailMsg = "无法写入凭据文件"
			} else {
				s.officialFailMsg = "Could not write credentials file"
			}
		default:
			s.officialState = OfficialFailed
			if res.Err != nil {
				s.officialFailMsg = res.Err.Error()
			}
		}
	}

	// Renew if expired or within 5 minutes of expiring (matches Claude Code / claude-usage-monitor)
	if creds.NeedsRefresh(now) {
		if !autoRenew || !creds.IsRefreshable() {
			if creds.IsExpired(now) {
				s.mu.Lock()
				s.deadLoginID = creds.LoginID()
				s.officialState = OfficialExpired
				s.mu.Unlock()
				return
			}
		} else {
			res := s.renewCredentials(client, *creds, credFile, false, consumeBudget)
			switch res.Kind {
			case core.RenewRenewed, core.RenewCurrent:
				if res.Credentials != nil {
					creds = res.Credentials
				}
			case core.RenewFailed:
				if res.Failure == core.RenewFailSaveFailed && res.Credentials != nil {
					// Token renewed in memory even though disk write failed: still use it for this call.
					creds = res.Credentials
				} else if res.Failure == core.RenewFailRateLimited || creds.IsExpired(now) {
					applyRenewFailure(res, *creds)
					return
				}
				// Not yet expired: continue with current accessToken and retry renewal next sync.
			}
		}
	}

	consumeBudget()

	usage, status, retryAfter, err := s.fetchUsageAPI(client, creds.AccessToken)
	if status == http.StatusUnauthorized {
		// Match claude-usage-monitor: first re-read disk in case Claude Code already refreshed the token.
		if latest, _, _ := s.loadCredentials(); latest != nil && latest.AccessToken != creds.AccessToken {
			creds = latest
			usage, status, retryAfter, err = s.fetchUsageAPI(client, creds.AccessToken)
		} else if autoRenew && creds.IsRefreshable() {
			// Force renewal even if expiresAt hasn't reached the 5-minute window yet.
			res := s.renewCredentials(client, *creds, credFile, true, consumeBudget)
			switch res.Kind {
			case core.RenewRenewed:
				if res.Credentials != nil {
					creds = res.Credentials
					usage, status, retryAfter, err = s.fetchUsageAPI(client, creds.AccessToken)
				}
			case core.RenewCurrent:
				if res.Credentials != nil && res.Credentials.AccessToken != creds.AccessToken {
					creds = res.Credentials
					usage, status, retryAfter, err = s.fetchUsageAPI(client, creds.AccessToken)
				}
			case core.RenewFailed:
				if res.Failure == core.RenewFailSaveFailed && res.Credentials != nil {
					creds = res.Credentials
					usage, status, retryAfter, err = s.fetchUsageAPI(client, creds.AccessToken)
				} else {
					applyRenewFailure(res, *creds)
					return
				}
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.EqualFold(reqClaudeDir, platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir)) {
		return
	}
	doneNow := time.Now()

	if status == http.StatusTooManyRequests {
		s.pstate.Budget.RecordRateLimit(doneNow, retryAfter)
		s.isRateLimited = true
		s.officialState = OfficialFailed
		if err != nil {
			s.officialFailMsg = err.Error()
		} else {
			s.officialFailMsg = "429"
		}
		s.savePersistedStateLocked()
		return
	}
	if status == http.StatusUnauthorized {
		s.deadLoginID = creds.LoginID()
		s.officialState = OfficialSignedOut
		return
	}
	if err != nil || usage == nil {
		s.officialState = OfficialFailed
		if err != nil {
			s.officialFailMsg = err.Error()
		} else {
			s.officialFailMsg = fmt.Sprintf("HTTP %d", status)
		}
		return
	}

	s.pstate.Budget.RecordSuccess(doneNow)
	s.isRateLimited = false
	s.deadLoginID = ""
	s.activeLoginID = creds.LoginID()
	s.officialUsage = usage
	s.usageFromClaudeCode = false
	s.officialState = OfficialConnected
	s.officialFailMsg = ""
	s.applyOfficialToStateLocked(usage)
	s.recomputeSnapshotLocked(doneNow)
	s.savePersistedStateLocked()

	// Usage alert notification
	if s.prefs.AlertEnabled && usage.FiveHour != nil {
		pct := usage.FiveHour.Percent()
		thresh := s.prefs.AlertThresholdPct
		if (pct >= thresh && s.lastAlertPct < thresh) || (pct >= 95 && s.lastAlertPct < 95) {
			go s.sendUsageNotification(pct)
		}
		s.lastAlertPct = pct
	}

	// Fetch profile periodically (every 6h or on first sync)
	needProfile := fetchProfile || s.profile == nil || s.profileCheckedAt == nil || time.Since(*s.profileCheckedAt) > 6*time.Hour
	token := creds.AccessToken
	if needProfile {
		go s.fetchProfile(client, token, reqClaudeDir)
	}
}

func (s *Store) sendUsageNotification(pct int) {
	zh := s.prefs.IsChinese()
	title := "Claude 用量提醒"
	body := fmt.Sprintf("5 小时额度已使用 %d%%", pct)
	if !zh {
		title = "Claude Usage Alert"
		body = fmt.Sprintf("5-hour limit reached %d%%", pct)
	}
	_ = mygo.NewNotification(mygo.NotificationOptions{
		Title: title,
		Body:  body,
	}).Show()
}

func (s *Store) fetchUsageAPI(client *http.Client, accessToken string) (*core.OfficialUsage, int, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, core.OAuthUsageURL, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	// Match claude-usage-monitor's OfficialUsageService.swift exactly:
	// Content-Type + Accept + anthropic-beta, no custom/default User-Agent, HTTP/1.1 Connection: close.
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", core.OAuthBetaHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "")
	req.Close = true

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, 0, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		retry := core.ParseRetryAfter(resp.Header, time.Now())
		if summary := core.SummarizeAPIError(body); summary != "" {
			return nil, resp.StatusCode, retry, fmt.Errorf("HTTP 429 (%s)", summary)
		}
		return nil, resp.StatusCode, retry, fmt.Errorf("HTTP 429")
	}
	if resp.StatusCode != http.StatusOK {
		if summary := core.SummarizeAPIError(body); summary != "" {
			return nil, resp.StatusCode, 0, fmt.Errorf("HTTP %d (%s)", resp.StatusCode, summary)
		}
		return nil, resp.StatusCode, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	u, err := core.DecodeOfficialUsage(body, time.Now())
	return u, resp.StatusCode, 0, err
}

func (s *Store) fetchProfile(client *http.Client, accessToken string, reqClaudeDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, core.OAuthProfileURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "")
	req.Close = true

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}
	prof, err := core.DecodeAccountProfile(body)
	if err != nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	if !strings.EqualFold(reqClaudeDir, platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir)) {
		s.mu.Unlock()
		return
	}
	s.profile = prof
	s.profileCheckedAt = &now
	s.pstate.Profile = prof
	s.pstate.ClaudeConfigDir = reqClaudeDir
	s.savePersistedStateLocked()
	s.mu.Unlock()
	s.notify()
}

func (s *Store) renewCredentials(client *http.Client, creds core.Credentials, credFile string, force bool, onNetwork func()) core.RenewResult {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cfgDir := filepath.Dir(credFile)
	renewal := core.CredentialRenewal{
		Read: func() (*core.StoredCredentials, bool) {
			raw, err := os.ReadFile(credFile)
			if err != nil {
				return nil, false
			}
			c, cleared := core.ParseCredentialsJSON(raw)
			if c == nil {
				return nil, cleared
			}
			return &core.StoredCredentials{Credentials: *c, Raw: raw}, false
		},
		WriteAndVerify: func(data []byte) bool {
			return platform.WriteAndVerifyFile(credFile, data, 0o600)
		},
		SendRefresh: func(rctx context.Context, refreshToken string, scopes []string, clientID string) (*core.OAuthGrant, error) {
			if onNetwork != nil {
				onNetwork()
			}
			return core.RequestTokenRefresh(rctx, client, refreshToken, scopes, clientID, time.Now())
		},
		AcquireRefreshLock: func() (func(), bool, error) {
			locks, err := platform.AcquireClaudeRefreshLocks(cfgDir)
			if err != nil {
				return nil, errors.Is(err, platform.ErrLocked), err
			}
			return func() {
				for _, l := range locks {
					l.Release()
				}
			}, false, nil
		},
		AcquireWriteLock: func() (func(), bool, error) {
			l, err := platform.AcquireClaudeWriteLock(cfgDir)
			if err != nil {
				return nil, errors.Is(err, platform.ErrLocked), err
			}
			return l.Release, false, nil
		},
	}
	return renewal.Renew(ctx, creds.AccessToken, force)
}

// LaunchClaudeCodeLogin opens a terminal running `claude auth login`.
func (s *Store) LaunchClaudeCodeLogin() {
	go func() {
		cmd := exec.Command("cmd.exe", "/C", "start", "cmd", "/K", "claude auth login")
		_ = cmd.Start()
	}()
}

func (s *Store) applyOfficialToStateLocked(u *core.OfficialUsage) {
	s.pstate.ClaudeConfigDir = platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir)
	s.pstate.LastUsage = u
	if u.SevenDay != nil && u.SevenDay.ResetsAt != nil {
		newEnd := *u.SevenDay.ResetsAt
		// Use the floored integer percentage shown on the panel, matching claude-usage-monitor.
		newReading := core.WeeklyReading{
			Utilization: float64(u.SevenDay.Percent()),
			At:          u.FetchedAt,
		}
		if s.pstate.WeeklyReading != nil && s.pstate.WeeklyReset != nil {
			if reset := core.MidweekResetBetween(*s.pstate.WeeklyReading, *s.pstate.WeeklyReset, newReading, newEnd); reset != nil {
				s.pstate.MidweekReset = reset
			}
		}
		if s.pstate.MidweekReset != nil && !s.pstate.MidweekReset.BelongsToWeekEnding(newEnd) {
			s.pstate.MidweekReset = nil
		}
		s.pstate.WeeklyReset = &newEnd
		s.pstate.WeeklyReading = &newReading
	}
	if s.snapshot != nil {
		s.lastSyncLifetimeCost = s.snapshot.LifetimeCost
		if u.FiveHour != nil && s.snapshot.FiveHourCost != nil {
			s.lastSyncWindowCost = *s.snapshot.FiveHourCost
			s.lastSyncFiveHourPct = u.FiveHour.Percent()
		}
		if u.SevenDay != nil && s.snapshot.Week != nil {
			s.lastSyncWeekCost = s.snapshot.Week.Cost
			s.lastSyncSevenDayPct = u.SevenDay.Percent()
		}
	}
}

func (s *Store) recomputeSnapshotLocked(now time.Time) {
	if s.index == nil {
		return
	}
	var fiveHourReset *time.Time
	if s.officialUsage != nil && s.officialUsage.FiveHour != nil {
		fiveHourReset = s.officialUsage.FiveHour.ResetsAt
	}
	weeklyReading := s.pstate.WeeklyReading
	if s.officialUsage != nil && s.officialUsage.SevenDay != nil {
		r := core.WeeklyReading{
			Utilization: float64(s.officialUsage.SevenDay.Percent()),
			At:          s.officialUsage.FetchedAt,
		}
		weeklyReading = &r
	}
	h, m, sec := 0, 0, 0
	if s.pstate.WeeklyReset != nil {
		loc := s.pstate.WeeklyReset.Local()
		h, m, sec = loc.Hour(), loc.Minute(), loc.Second()
	}
	settings := core.UsageSettings{
		BillingAnchorDay:    max(1, s.prefs.BillingAnchorDay),
		BillingAnchorHour:   h,
		BillingAnchorMinute: m,
		BillingAnchorSecond: sec,
		WeeklyReset:         s.pstate.WeeklyReset,
		FiveHourReset:       fiveHourReset,
		WeeklyReading:       weeklyReading,
		EarlierAllowance:    s.pstate.EarlierAllowance,
		MidweekReset:        s.pstate.MidweekReset,
	}
	snap := core.ComputeSnapshot(s.index.MakeSnapshot(), settings, now)
	s.snapshot = &snap
	s.updateSpeedLogLocked(now)
	if s.rememberAllowanceLocked(snap) {
		s.savePersistedStateLocked()
	}
}

func (s *Store) updateSpeedLogLocked(now time.Time) {
	if s.index == nil {
		return
	}
	s.speedLog = s.index.SpeedLog()
	nowSec := float64(now.Unix()) + float64(now.Nanosecond())/1e9
	models := s.speedLog.Models(nowSec)
	stillPresent := false
	if s.speedDefaultID != "" {
		for _, m := range models {
			if m.ID() == s.speedDefaultID {
				stillPresent = true
				break
			}
		}
	}
	if !stillPresent {
		if len(models) > 0 {
			s.speedDefaultID = models[0].ID()
		} else {
			s.speedDefaultID = ""
		}
	}
}

func (s *Store) rememberAllowanceLocked(snap core.UsageSnapshot) bool {
	dirty := false
	if snap.WeeklyAllowance != nil && snap.WeeklyAllowance.Reset != nil {
		if !s.pstate.MidweekReset.Equal(snap.WeeklyAllowance.Reset) {
			r := *snap.WeeklyAllowance.Reset
			s.pstate.MidweekReset = &r
			dirty = true
		}
	}
	if a := snap.WeeklyAllowance; a != nil && !a.Borrowed &&
		a.Utilization >= core.MinWeeklyUtilizationForBasis && a.SyncedAt != nil {
		basis := core.AllowanceBasis{
			Utilization: a.Utilization,
			SyncedCost:  a.SyncedCost,
			At:          *a.SyncedAt,
		}
		if s.pstate.EarlierAllowance == nil ||
			s.pstate.EarlierAllowance.Utilization != basis.Utilization ||
			s.pstate.EarlierAllowance.SyncedCost != basis.SyncedCost ||
			!s.pstate.EarlierAllowance.At.Equal(basis.At) {
			s.pstate.EarlierAllowance = &basis
			dirty = true
		}
	}
	return dirty
}

func (s *Store) currentActivityLocked(now time.Time) core.SyncActivity {
	act := core.SyncActivity{
		ClaudeCodeRunning: len(s.processes) > 0,
	}
	if s.snapshot != nil {
		act.LastConsumption = s.snapshot.LastRecord
		if s.snapshot.LastRecord != nil && (s.officialUsage == nil || s.snapshot.LastRecord.After(s.officialUsage.FetchedAt)) {
			act.HasNewUsage = true
		}
		unsynced := max(0, s.snapshot.LifetimeCost-s.lastSyncLifetimeCost)
		var windows []core.SyncWindow
		if s.officialUsage != nil && s.officialUsage.FiveHour != nil && s.lastSyncWindowCost > 0 {
			windows = append(windows, core.SyncWindow{Cost: s.lastSyncWindowCost, Percent: s.lastSyncFiveHourPct})
		}
		if s.officialUsage != nil && s.officialUsage.SevenDay != nil && s.lastSyncWeekCost > 0 {
			windows = append(windows, core.SyncWindow{Cost: s.lastSyncWeekCost, Percent: s.lastSyncSevenDayPct})
		}
		act.Progress = core.ComputeMultiWindowProgress(unsynced, windows)
	}
	return act
}

func (s *Store) isExitBlockedLocked() bool {
	if s.exitFailed || s.exitPlace == nil {
		return true
	}
	return s.exitPlace.RestrictsUsage()
}

func (s *Store) shieldLocked() platform.ShieldState {
	if !s.prefs.ExitGuard {
		return platform.ShieldNone
	}
	if s.exitIPv6 != nil && s.exitIPv6.RestrictsUsage() {
		return platform.ShieldSevere
	}
	if s.exitChecking && s.exitPlace != nil && !s.exitPlace.RestrictsUsage() {
		return platform.ShieldLoading
	}
	if s.exitPlace != nil {
		if s.exitFailed || s.exitPlace.RestrictsUsage() {
			return platform.ShieldRisk
		}
		return platform.ShieldSafe
	}
	if s.exitFailed {
		return platform.ShieldRisk
	}
	return platform.ShieldNone
}

func (s *Store) UpdatePrefs(fn func(*core.Preferences)) {
	var proxyChanged, officialToggledOn, claudeDirChanged, dirChanged, officialEnabled bool
	var agWatchChanged, agRefreshNeeded, agToggledOn bool
	s.mu.Lock()
	oldProxy := s.prefs.ProxyURL
	oldOfficial := s.prefs.OfficialEnabled
	oldClaudeDir := platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir)
	oldDir := s.prefs.CustomDataDir
	oldProvider := s.prefs.ActiveProvider
	oldAGBrain := platform.AGBrainDir(s.prefs.AGBrainDir)
	oldAGCLIDir := platform.AGCLIConfigDir(s.prefs.AGCLIConfigDir)
	oldAGCLIPath := s.prefs.AGAgyCLIPath
	oldAGBilling := s.prefs.AGBillingAnchorDay
	oldAGOfficial := s.prefs.AGOfficialEnabled

	fn(&s.prefs)
	s.prefs.ActiveProvider = core.NormalizeProvider(s.prefs.ActiveProvider)

	proxyChanged = s.prefs.ProxyURL != oldProxy
	officialToggledOn = s.prefs.OfficialEnabled && !oldOfficial
	claudeDirChanged = !strings.EqualFold(platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir), oldClaudeDir)
	dirChanged = s.prefs.CustomDataDir != oldDir
	officialEnabled = s.prefs.OfficialEnabled

	newAGBrain := platform.AGBrainDir(s.prefs.AGBrainDir)
	newAGCLIDir := platform.AGCLIConfigDir(s.prefs.AGCLIConfigDir)
	agBrainChanged := !strings.EqualFold(newAGBrain, oldAGBrain)
	agCLIConfigChanged := !strings.EqualFold(newAGCLIDir, oldAGCLIDir)
	agWatchChanged = agBrainChanged || agCLIConfigChanged
	agRefreshNeeded = agWatchChanged || s.prefs.AGAgyCLIPath != oldAGCLIPath || s.prefs.AGBillingAnchorDay != oldAGBilling || (s.prefs.ActiveProvider != oldProvider && s.prefs.ActiveProvider == core.ProviderAntigravity)
	agToggledOn = s.prefs.AGOfficialEnabled && !oldAGOfficial
	if agBrainChanged && s.agIndex != nil {
		s.agIndex.SetBrainDir(newAGBrain)
		s.agIndexDir = newAGBrain
	}

	if claudeDirChanged {
		s.resetOfficialAccountLocked()
	}
	if !s.prefs.OfficialEnabled {
		s.officialState = OfficialDisabled
	} else if claudeDirChanged || s.officialState == OfficialDisabled {
		s.officialState = OfficialConnecting
	}
	s.recomputeSnapshotLocked(time.Now())
	_ = core.SavePreferences(s.prefsPath, s.prefs)
	s.mu.Unlock()
	s.notify()

	if agWatchChanged {
		go s.startAGWatchers()
	}
	if agToggledOn {
		go s.RefreshAntigravity(true)
	} else if agRefreshNeeded {
		go s.RefreshAntigravity(false)
	}

	if proxyChanged {
		go func() {
			if s.hasAuthorizedCredentials() {
				s.CheckExit(true)
			}
		}()
	}
	if claudeDirChanged || dirChanged {
		go s.startFileWatchers()
		s.RebuildIndex()
	}
	if claudeDirChanged {
		go func() {
			s.TryReadClaudeCodeCache()
			if !s.syncLocalAuthState() {
				return
			}
			s.mu.RLock()
			needExit := s.exitPlace == nil
			s.mu.RUnlock()
			if needExit {
				s.CheckExit(false)
			}
			if officialEnabled {
				s.SyncOfficial(true, true)
			}
		}()
	} else if officialToggledOn {
		go func() {
			if !s.syncLocalAuthState() {
				return
			}
			s.mu.RLock()
			needExit := s.exitPlace == nil
			s.mu.RUnlock()
			if needExit {
				s.CheckExit(false)
			}
			s.SyncOfficial(true, true)
		}()
	}
}

func (s *Store) CycleSpeedModel(models []core.ModelSpeed) {
	if len(models) <= 1 {
		return
	}
	s.mu.Lock()
	idx := 0
	found := false
	for _, id := range []string{s.speedModelID, s.speedDefaultID} {
		if id == "" {
			continue
		}
		for i, m := range models {
			if m.ID() == id {
				idx = i
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	s.speedModelID = models[(idx+1)%len(models)].ID()
	s.mu.Unlock()
	s.notify()
}

func (s *Store) ViewState() ViewState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var plan core.PlanKind
	var health core.SubscriptionHealth
	var email, uuid string
	if s.profile != nil {
		plan = core.DetectPlan(s.profile.OrganizationType, s.profile.RateLimitTier)
		health = s.profile.Health()
		email = s.profile.Email
		uuid = s.profile.AccountUUID
	}
	if plan == core.PlanNone {
		plan = s.prefs.ManualPlan
	}

	var term *core.SubscriptionTerm
	if s.prefs.BillingAnchorDay >= 1 && s.prefs.BillingAnchorDay <= 31 {
		h, m, sec := 0, 0, 0
		if s.pstate.WeeklyReset != nil {
			loc := s.pstate.WeeklyReset.Local()
			h, m, sec = loc.Hour(), loc.Minute(), loc.Second()
		}
		term = core.ComputeSubscriptionTerm(s.prefs.BillingAnchorDay, h, m, sec, now)
	}

	var lastUp *time.Time
	if s.officialUsage != nil {
		t := s.officialUsage.FetchedAt
		lastUp = &t
	}

	var idxSize int64
	if info, err := os.Stat(s.indexPath); err == nil {
		idxSize = info.Size()
	}

	procs := make([]platform.ClaudeTask, len(s.processes))
	copy(procs, s.processes)

	claudeDir := platform.ClaudeConfigDir(s.prefs.ClaudeConfigDir)
	_, credErr := os.Stat(platform.ClaudeCredentialsFile(s.prefs.ClaudeConfigDir))
	_, projErr := os.Stat(filepath.Join(claudeDir, "projects"))

	inCooldown := s.pstate.Budget.InCooldown(now)
	var retryWait time.Duration
	if inCooldown {
		retryWait = s.pstate.Budget.RetryWait(now)
	}

	return ViewState{
		Prefs:               s.prefs,
		Phase:               s.phase,
		IndexProgress:       s.indexProgress,
		IndexSize:           idxSize,
		Snapshot:            s.snapshot,
		SpeedLog:            s.speedLog,
		SpeedModelID:        s.speedModelID,
		SpeedDefaultID:      s.speedDefaultID,
		OfficialState:       s.officialState,
		OfficialFailMsg:     s.officialFailMsg,
		OfficialUsage:       s.officialUsage,
		UsageFromClaudeCode: s.usageFromClaudeCode,
		IsFetching:          s.isFetching,
		IsRateLimited:       inCooldown,
		RetryWait:           retryWait,
		LastUpdated:         lastUp,
		DetectedPlan:        plan,
		SubscriptionHealth:  health,
		SubscriptionTerm:    term,
		AccountEmail:        email,
		AccountUUID:         uuid,
		LoginExpiresAt:      s.loginExpiresAt,
		ProfileCheckedAt:    s.profileCheckedAt,
		ExitPlace:           s.exitPlace,
		ExitIPv6:            s.exitIPv6,
		ExitFailed:          s.exitFailed,
		ExitChecking:        s.exitChecking,
		Shield:              s.shieldLocked(),
		ExitBlocked:         s.isExitBlockedLocked(),
		IPv6IsDirect:        s.exitIPv6 != nil && s.exitIPv6.RestrictsUsage(),
		Processes:           procs,
		ClaudeConfigDir:     claudeDir,
		HasCredentialsFile:  credErr == nil,
		HasProjectsDir:      projErr == nil,
		DataRoots:           platform.ClaudeProjectRoots(s.prefs.ClaudeConfigDir, s.prefs.CustomDataDir),
		AG:                  s.agViewStateLocked(),
	}
}

func (s *Store) loadPersistedState() {
	b, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.pstate)
	if s.pstate.Budget.Slowdown < 1 {
		s.pstate.Budget.Slowdown = 1
	} else if s.pstate.Budget.Slowdown > core.BudgetMaxSlowdown {
		s.pstate.Budget.Slowdown = core.BudgetMaxSlowdown
	}
	if s.pstate.Budget.InCooldown(time.Now()) && s.pstate.Budget.Tokens < core.BudgetManualReserve {
		s.pstate.Budget.Tokens = core.BudgetManualReserve
	}
}

func (s *Store) savePersistedStateLocked() {
	if s.statePath == "" {
		return
	}
	b, err := json.Marshal(s.pstate)
	if err != nil {
		return
	}
	_ = platform.AtomicWriteFile(s.statePath, b, 0o644)
}
