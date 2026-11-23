package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type DisplayMode string

const (
	DisplayDetailed DisplayMode = "detailed"
	DisplayCompact  DisplayMode = "compact"
	DisplayHidden   DisplayMode = "hidden"
)

type ConditionalDisplay string

const (
	ShowAlways ConditionalDisplay = "always"
	ShowOnWarn ConditionalDisplay = "issue"
)

type CostSpan string

const (
	SpanMonth CostSpan = "month"
	SpanWeek  CostSpan = "week"
	SpanDay   CostSpan = "day"
)

var AllCostSpans = []CostSpan{SpanMonth, SpanWeek, SpanDay}

type IconStyle string

const (
	IconStyleClawd IconStyle = "clawd"
	IconStyleLogo  IconStyle = "logo"
)

type LanguagePref string

const (
	LangAuto LanguagePref = "auto"
	LangZH   LanguagePref = "zh"
	LangEN   LanguagePref = "en"
)

type Preferences struct {
	// Usage tab
	OfficialEnabled    bool   `json:"officialEnabled"`
	CheckOnOpen        bool   `json:"checkOnOpen"`
	SyncIntervalSec    int    `json:"syncIntervalSec"`
	ProxyURL           string `json:"proxyUrl"`
	AutoRenewLogin     bool   `json:"autoRenewLogin"`
	BillingAnchorDay   int    `json:"billingAnchorDay"`
	ManualPlan         PlanKind `json:"manualPlan"`

	// Menubar / Tray & Floating Pill tab
	IconStyle          IconStyle `json:"iconStyle"`
	ClawdAnimation     bool      `json:"clawdAnimation"`
	ShowFloatingPill   bool      `json:"showFloatingPill"`
	PillAlwaysOnTop    bool      `json:"pillAlwaysOnTop"`
	PillPosX           *int      `json:"pillPosX,omitempty"`
	PillPosY           *int      `json:"pillPosY,omitempty"`
	PillShowFiveHour   bool      `json:"pillShowFiveHour"`
	PillShowWeek       bool      `json:"pillShowWeek"`
	PillShowScoped     bool      `json:"pillShowScoped"`
	PillShowTodayCost  bool      `json:"pillShowTodayCost"`
	PillShowWeekCost   bool      `json:"pillShowWeekCost"`
	PillShowMonthCost  bool      `json:"pillShowMonthCost"`
	PillShowSpeed      bool      `json:"pillShowSpeed"`
	ExitGuard          bool      `json:"exitGuard"`
	AlertEnabled       bool      `json:"alertEnabled"`
	AlertThresholdPct  int       `json:"alertThresholdPct"`

	// Panel tab
	ExitDisplay         ConditionalDisplay `json:"exitDisplay"`
	SubscriptionDisplay ConditionalDisplay `json:"subscriptionDisplay"`
	ShowProcesses       bool               `json:"showProcesses"`
	LimitsDisplay       DisplayMode        `json:"limitsDisplay"`
	SpeedDisplay        DisplayMode        `json:"speedDisplay"`
	SpendDisplay        DisplayMode        `json:"spendDisplay"`
	FullDateReset       bool               `json:"fullDateReset"`
	ShowResetCountdown  bool               `json:"showResetCountdown"`
	Currency            CurrencyUnit       `json:"currency"`
	CostSpan            CostSpan           `json:"costSpan"`
	CollapsedLimits     bool               `json:"collapsedLimits"`
	CollapsedSpeed      bool               `json:"collapsedSpeed"`
	CollapsedSpend      bool               `json:"collapsedSpend"`

	// Data tab
	ClaudeConfigDir string `json:"claudeConfigDir"`
	CustomDataDir   string `json:"customDataDir"`

	// General tab
	Language            LanguagePref `json:"language"`
	ExpiryNotifications bool         `json:"expiryNotifications"`
}

func DefaultPreferences() Preferences {
	return Preferences{
		OfficialEnabled:     true,
		CheckOnOpen:         true,
		SyncIntervalSec:     30,
		AutoRenewLogin:      true,
		BillingAnchorDay:    0,
		IconStyle:           IconStyleClawd,
		ClawdAnimation:      true,
		ShowFloatingPill:    false,
		PillAlwaysOnTop:     true,
		PillShowFiveHour:    true,
		PillShowWeek:        true,
		PillShowTodayCost:   true,
		ExitGuard:           true,
		AlertEnabled:        true,
		AlertThresholdPct:   80,
		ExitDisplay:         ShowAlways,
		SubscriptionDisplay: ShowAlways,
		ShowProcesses:       true,
		LimitsDisplay:       DisplayDetailed,
		SpeedDisplay:        DisplayCompact,
		SpendDisplay:        DisplayDetailed,
		FullDateReset:       false,
		ShowResetCountdown:  false,
		Currency:            CurrencyUSD,
		CostSpan:            SpanWeek,
		Language:            LangZH,
		ExpiryNotifications: true,
	}
}

func (p Preferences) IsChinese() bool {
	if p.Language == LangEN {
		return false
	}
	return true
}

func LoadPreferences(path string) Preferences {
	p := DefaultPreferences()
	b, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(b, &p)
	if p.SyncIntervalSec < 10 {
		p.SyncIntervalSec = 10
	}
	if p.SyncIntervalSec > 3600 {
		p.SyncIntervalSec = 3600
	}
	if p.AlertThresholdPct < 50 || p.AlertThresholdPct > 95 {
		p.AlertThresholdPct = 80
	}
	if p.Currency == "" {
		p.Currency = CurrencyUSD
	}
	if p.CostSpan == "" {
		p.CostSpan = SpanWeek
	}
	return p
}

func SavePreferences(path string, p Preferences) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
