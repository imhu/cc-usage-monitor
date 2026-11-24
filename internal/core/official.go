package core

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

type OfficialLimit struct {
	Utilization float64
	ResetsAt    *time.Time
}

// Percent floors utilization just like Claude Code `/usage` (71.9 -> 71).
func (l OfficialLimit) Percent() int {
	if math.IsNaN(l.Utilization) || math.IsInf(l.Utilization, 0) || l.Utilization <= 0 {
		return 0
	}
	return int(math.Floor(l.Utilization))
}

func (l OfficialLimit) Fraction() float64 {
	return float64(l.Percent()) / 100.0
}

type ScopedLimit struct {
	ModelName string
	Limit     OfficialLimit
}

type OfficialUsage struct {
	FiveHour          *OfficialLimit
	SevenDay          *OfficialLimit
	SevenDaySonnet    *OfficialLimit
	SevenDayOpus      *OfficialLimit
	Scoped            []ScopedLimit
	ExtraUsageEnabled *bool
	FetchedAt         time.Time
}

type flexibleTime struct {
	t *time.Time
}

func (ft *flexibleTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err == nil && str != "" {
			if ts, ok := ParseTimestamp(str); ok {
				tm := time.Unix(0, int64(ts*1e9)).UTC()
				ft.t = &tm
			}
		}
		return nil
	}
	var num float64
	if err := json.Unmarshal(b, &num); err == nil && num > 0 {
		if num > 1e12 {
			num /= 1000.0
		}
		tm := time.Unix(0, int64(num*1e9)).UTC()
		ft.t = &tm
	}
	return nil
}

type rawOfficialUsage struct {
	FiveHour       *rawLimit `json:"five_hour"`
	SevenDay       *rawLimit `json:"seven_day"`
	SevenDaySonnet *rawLimit `json:"seven_day_sonnet"`
	SevenDayOpus   *rawLimit `json:"seven_day_opus"`
	Limits         []struct {
		Kind     string        `json:"kind"`
		Percent  *float64      `json:"percent"`
		ResetsAt *flexibleTime `json:"resets_at"`
		Scope    *struct {
			Model *struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	} `json:"limits"`
	ExtraUsage *struct {
		IsEnabled *bool `json:"is_enabled"`
	} `json:"extra_usage"`
}

type rawLimit struct {
	Utilization *float64      `json:"utilization"`
	ResetsAt    *flexibleTime `json:"resets_at"`
}

func (r *rawLimit) toLimit() *OfficialLimit {
	if r == nil || r.Utilization == nil {
		return nil
	}
	var reset *time.Time
	if r.ResetsAt != nil {
		reset = r.ResetsAt.t
	}
	return &OfficialLimit{
		Utilization: *r.Utilization,
		ResetsAt:    reset,
	}
}

func DecodeOfficialUsage(data []byte, fetchedAt time.Time) (*OfficialUsage, error) {
	var raw rawOfficialUsage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	u := &OfficialUsage{
		FiveHour:       raw.FiveHour.toLimit(),
		SevenDay:       raw.SevenDay.toLimit(),
		SevenDaySonnet: raw.SevenDaySonnet.toLimit(),
		SevenDayOpus:   raw.SevenDayOpus.toLimit(),
		FetchedAt:      fetchedAt,
	}
	if raw.ExtraUsage != nil {
		u.ExtraUsageEnabled = raw.ExtraUsage.IsEnabled
	}
	seen := map[string]bool{}
	for _, sc := range raw.Limits {
		if sc.Kind != "weekly_scoped" || sc.Percent == nil || sc.Scope == nil || sc.Scope.Model == nil || sc.Scope.Model.DisplayName == "" {
			continue
		}
		name := sc.Scope.Model.DisplayName
		if seen[name] {
			continue
		}
		seen[name] = true
		var reset *time.Time
		if sc.ResetsAt != nil {
			reset = sc.ResetsAt.t
		}
		u.Scoped = append(u.Scoped, ScopedLimit{
			ModelName: name,
			Limit:     OfficialLimit{Utilization: *sc.Percent, ResetsAt: reset},
		})
	}
	return u, nil
}

// EffectiveAt returns a view of OfficialUsage where elapsed windows reset locally to 0.
func (u OfficialUsage) EffectiveAt(now time.Time) OfficialUsage {
	res := u
	if u.FiveHour != nil {
		c := *u.FiveHour
		if c.ResetsAt != nil && !c.ResetsAt.After(now) {
			c.Utilization = 0
		}
		res.FiveHour = &c
	}
	if u.SevenDay != nil {
		c := *u.SevenDay
		if c.ResetsAt != nil && !c.ResetsAt.After(now) {
			c.Utilization = 0
		}
		res.SevenDay = &c
	}
	if u.SevenDaySonnet != nil {
		c := *u.SevenDaySonnet
		if c.ResetsAt != nil && !c.ResetsAt.After(now) {
			c.Utilization = 0
		}
		res.SevenDaySonnet = &c
	}
	if u.SevenDayOpus != nil {
		c := *u.SevenDayOpus
		if c.ResetsAt != nil && !c.ResetsAt.After(now) {
			c.Utilization = 0
		}
		res.SevenDayOpus = &c
	}
	if len(u.Scoped) > 0 {
		res.Scoped = make([]ScopedLimit, len(u.Scoped))
		for i, s := range u.Scoped {
			if s.Limit.ResetsAt != nil && !s.Limit.ResetsAt.After(now) {
				s.Limit.Utilization = 0
			}
			res.Scoped[i] = s
		}
	}
	return res
}

type PlanKind string

const (
	PlanNone       PlanKind = ""
	PlanPro        PlanKind = "pro"
	PlanMax5x      PlanKind = "max5x"
	PlanMax20x     PlanKind = "max20x"
	PlanUltra      PlanKind = "ultra"
	PlanTeam       PlanKind = "team"
	PlanEnterprise PlanKind = "enterprise"
)

func (p PlanKind) Title() string {
	switch p {
	case PlanPro:
		return "Pro"
	case PlanMax5x:
		return "Max"
	case PlanMax20x:
		return "Ultra"
	case PlanUltra:
		return "Ultra"
	case PlanTeam:
		return "Team"
	case PlanEnterprise:
		return "Enterprise"
	default:
		return ""
	}
}

func DetectPlan(orgType, rateLimitTier string) PlanKind {
	tier := strings.ToLower(rateLimitTier)
	org := strings.ToLower(orgType)
	switch {
	case strings.Contains(tier, "ultra"):
		return PlanUltra
	case strings.Contains(tier, "max_20x") || strings.Contains(tier, "20x"):
		return PlanMax20x
	case strings.Contains(tier, "max_5x") || strings.Contains(tier, "5x") || strings.Contains(org, "max"):
		return PlanMax5x
	case strings.Contains(org, "enterprise") || strings.Contains(tier, "enterprise"):
		return PlanEnterprise
	case strings.Contains(org, "team") || strings.Contains(tier, "team"):
		return PlanTeam
	case strings.Contains(org, "pro") || strings.Contains(tier, "pro"):
		return PlanPro
	default:
		return PlanNone
	}
}

type SubscriptionHealth string

const (
	HealthActive         SubscriptionHealth = "active"
	HealthPaymentFailed  SubscriptionHealth = "payment_failed"
	HealthActionRequired SubscriptionHealth = "action_required"
	HealthInactive       SubscriptionHealth = "inactive"
)

type AccountProfile struct {
	OrganizationType   string
	RateLimitTier      string
	SubscriptionStatus string
	AccountUUID        string
	Email              string
}

func DecodeAccountProfile(data []byte) (*AccountProfile, error) {
	var raw struct {
		Account *struct {
			UUID  string `json:"uuid"`
			Email string `json:"email"`
		} `json:"account"`
		Organization *struct {
			OrganizationType   string `json:"organization_type"`
			RateLimitTier      string `json:"rate_limit_tier"`
			SubscriptionStatus string `json:"subscription_status"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	p := &AccountProfile{}
	if raw.Account != nil {
		p.AccountUUID = raw.Account.UUID
		p.Email = raw.Account.Email
	}
	if raw.Organization != nil {
		p.OrganizationType = raw.Organization.OrganizationType
		p.RateLimitTier = raw.Organization.RateLimitTier
		p.SubscriptionStatus = raw.Organization.SubscriptionStatus
	}
	return p, nil
}

func (p *AccountProfile) Health() SubscriptionHealth {
	if p == nil {
		return ""
	}
	switch strings.ToLower(p.SubscriptionStatus) {
	case "past_due", "unpaid":
		return HealthPaymentFailed
	case "incomplete":
		return HealthActionRequired
	case "canceled", "incomplete_expired":
		if DetectPlan(p.OrganizationType, p.RateLimitTier) == PlanNone {
			return HealthInactive
		}
		return HealthActive
	case "active", "trialing":
		return HealthActive
	default:
		if DetectPlan(p.OrganizationType, p.RateLimitTier) != PlanNone {
			return HealthActive
		}
		return ""
	}
}

type SubscriptionTerm struct {
	Start    time.Time
	End      time.Time
	DaysLeft int
}

func (t SubscriptionTerm) IsEnding() bool {
	return t.DaysLeft <= 3
}

// JustStarted reports whether the current billing period began less than 24 hours ago.
func (t SubscriptionTerm) JustStarted(now time.Time) bool {
	d := now.Sub(t.Start)
	return d >= 0 && d < 24*time.Hour
}

func ComputeSubscriptionTerm(anchorDay int, hour, minute, second int, now time.Time) *SubscriptionTerm {
	if anchorDay < 1 || anchorDay > 31 {
		return nil
	}
	start, end := BillingInterval(now, anchorDay, hour, minute, second)
	left := int(math.Ceil(end.Sub(now).Hours() / 24.0))
	if left < 0 {
		left = 0
	}
	return &SubscriptionTerm{
		Start:    start,
		End:      end,
		DaysLeft: left,
	}
}

type ResetKind int

const (
	ResetNone ResetKind = iota
	ResetCountdown
	ResetWeekday
	ResetElapsed
	ResetIdle
)

type LimitRow struct {
	ID         string
	Title      string
	ShortTitle string
	Percent    int
	Fraction   float64
	ResetKind  ResetKind
	ResetsAt   *time.Time
	IsFiveHour bool
}

func (r LimitRow) Pace(now time.Time) *PaceLine {
	if r.IsFiveHour {
		return FiveHourPace(r.ResetsAt, now)
	}
	return WeeklyPace(r.ResetsAt, now)
}

func BuildLimitRows(u *OfficialUsage, now time.Time, zh bool) []LimitRow {
	if u == nil {
		return nil
	}
	eff := u.EffectiveAt(now)
	var rows []LimitRow
	if eff.FiveHour != nil {
		rk := ResetIdle
		if eff.FiveHour.ResetsAt != nil {
			if eff.FiveHour.ResetsAt.After(now) {
				rk = ResetCountdown
			} else {
				rk = ResetElapsed
			}
		}
		rows = append(rows, LimitRow{
			ID:         "five_hour",
			Title:      l10n(zh, "5 小时", "5-hour"),
			ShortTitle: l10n(zh, "5小时", "5h"),
			Percent:    eff.FiveHour.Percent(),
			Fraction:   eff.FiveHour.Fraction(),
			ResetKind:  rk,
			ResetsAt:   eff.FiveHour.ResetsAt,
			IsFiveHour: true,
		})
	}
	if eff.SevenDay != nil {
		rk := ResetNone
		if eff.SevenDay.ResetsAt != nil {
			if eff.SevenDay.ResetsAt.After(now) {
				rk = ResetWeekday
			} else {
				rk = ResetElapsed
			}
		}
		rows = append(rows, LimitRow{
			ID:         "seven_day",
			Title:      l10n(zh, "本周 · 全部模型", "Weekly · All models"),
			ShortTitle: l10n(zh, "本周", "Week"),
			Percent:    eff.SevenDay.Percent(),
			Fraction:   eff.SevenDay.Fraction(),
			ResetKind:  rk,
			ResetsAt:   eff.SevenDay.ResetsAt,
		})
	}
	for _, sc := range eff.Scoped {
		rk := ResetNone
		if sc.Limit.ResetsAt != nil {
			if sc.Limit.ResetsAt.After(now) {
				rk = ResetWeekday
			} else {
				rk = ResetElapsed
			}
		}
		rows = append(rows, LimitRow{
			ID:         "scoped_" + sc.ModelName,
			Title:      l10n(zh, "本周 · "+sc.ModelName, "Weekly · "+sc.ModelName),
			ShortTitle: sc.ModelName,
			Percent:    sc.Limit.Percent(),
			Fraction:   sc.Limit.Fraction(),
			ResetKind:  rk,
			ResetsAt:   sc.Limit.ResetsAt,
		})
	}
	if len(eff.Scoped) == 0 && eff.SevenDaySonnet != nil {
		rk := ResetNone
		if eff.SevenDaySonnet.ResetsAt != nil {
			if eff.SevenDaySonnet.ResetsAt.After(now) {
				rk = ResetWeekday
			} else {
				rk = ResetElapsed
			}
		}
		rows = append(rows, LimitRow{
			ID:         "seven_day_sonnet",
			Title:      l10n(zh, "本周 · Sonnet", "Weekly · Sonnet"),
			ShortTitle: "Sonnet",
			Percent:    eff.SevenDaySonnet.Percent(),
			Fraction:   eff.SevenDaySonnet.Fraction(),
			ResetKind:  rk,
			ResetsAt:   eff.SevenDaySonnet.ResetsAt,
		})
	}
	if len(eff.Scoped) == 0 && eff.SevenDayOpus != nil {
		rk := ResetNone
		if eff.SevenDayOpus.ResetsAt != nil {
			if eff.SevenDayOpus.ResetsAt.After(now) {
				rk = ResetWeekday
			} else {
				rk = ResetElapsed
			}
		}
		rows = append(rows, LimitRow{
			ID:         "seven_day_opus",
			Title:      l10n(zh, "本周 · Opus", "Weekly · Opus"),
			ShortTitle: "Opus",
			Percent:    eff.SevenDayOpus.Percent(),
			Fraction:   eff.SevenDayOpus.Fraction(),
			ResetKind:  rk,
			ResetsAt:   eff.SevenDayOpus.ResetsAt,
		})
	}
	return rows
}

func l10n(zh bool, cn, en string) string {
	if zh {
		return cn
	}
	return en
}
