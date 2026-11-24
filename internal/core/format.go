package core

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	FXEndpoint        = "https://api.frankfurter.app/latest?from=USD"
	FXRefreshInterval = time.Hour
)

type CurrencyUnit string

const (
	CurrencyUSD CurrencyUnit = "USD"
	CurrencyCNY CurrencyUnit = "CNY"
	CurrencyJPY CurrencyUnit = "JPY"
	CurrencyGBP CurrencyUnit = "GBP"
	CurrencyEUR CurrencyUnit = "EUR"
	CurrencyHKD CurrencyUnit = "HKD"
	CurrencySGD CurrencyUnit = "SGD"
	CurrencyAUD CurrencyUnit = "AUD"
	CurrencyCAD CurrencyUnit = "CAD"
	CurrencyCHF CurrencyUnit = "CHF"
	CurrencyKRW CurrencyUnit = "KRW"
	CurrencyTWD CurrencyUnit = "TWD"
)

var AllCurrencies = []CurrencyUnit{
	CurrencyUSD, CurrencyCNY, CurrencyJPY, CurrencyGBP, CurrencyEUR,
	CurrencyHKD, CurrencySGD, CurrencyAUD, CurrencyCAD, CurrencyCHF,
	CurrencyKRW, CurrencyTWD,
}

func (u CurrencyUnit) Symbol() string {
	switch u {
	case CurrencyCNY:
		return "¥"
	case CurrencyJPY:
		return "JP¥"
	case CurrencyGBP:
		return "£"
	case CurrencyEUR:
		return "€"
	case CurrencyHKD:
		return "HK$"
	case CurrencySGD:
		return "S$"
	case CurrencyAUD:
		return "A$"
	case CurrencyCAD:
		return "C$"
	case CurrencyCHF:
		return "CHF "
	case CurrencyKRW:
		return "₩"
	case CurrencyTWD:
		return "NT$"
	default:
		return "$"
	}
}

func (u CurrencyUnit) DefaultRate() float64 {
	switch u {
	case CurrencyCNY:
		return 7.1
	case CurrencyJPY:
		return 149.0
	case CurrencyGBP:
		return 0.78
	case CurrencyEUR:
		return 0.92
	case CurrencyHKD:
		return 7.8
	case CurrencySGD:
		return 1.33
	case CurrencyAUD:
		return 1.52
	case CurrencyCAD:
		return 1.38
	case CurrencyCHF:
		return 0.88
	case CurrencyKRW:
		return 1380.0
	case CurrencyTWD:
		return 32.5
	default:
		return 1.0
	}
}

func (u CurrencyUnit) FractionDigits() int {
	switch u {
	case CurrencyJPY, CurrencyKRW, CurrencyTWD:
		return 0
	default:
		return 2
	}
}

// ParseFrankfurterRates decodes a Frankfurter API response (`?from=USD`) into a per-USD rate table.
func ParseFrankfurterRates(data []byte) (map[string]float64, error) {
	var raw struct {
		Base  string             `json:"base"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Base != "USD" {
		return nil, fmt.Errorf("unexpected FX base currency: %q", raw.Base)
	}
	out := make(map[string]float64, len(raw.Rates)+1)
	for code, r := range raw.Rates {
		if !math.IsNaN(r) && !math.IsInf(r, 0) && r > 0 {
			out[strings.ToUpper(code)] = r
		}
	}
	if _, ok := out["EUR"]; !ok {
		return nil, fmt.Errorf("missing EUR rate in FX payload")
	}
	out["USD"] = 1.0
	return out, nil
}

type MoneyFormat struct {
	Unit CurrencyUnit
	Rate float64
}

func NewMoneyFormat(unit CurrencyUnit) MoneyFormat {
	return NewMoneyFormatWithRates(unit, nil)
}

func NewMoneyFormatWithRates(unit CurrencyUnit, perUSD map[string]float64) MoneyFormat {
	if unit == "" {
		unit = CurrencyUSD
	}
	rate := unit.DefaultRate()
	if unit == CurrencyUSD {
		rate = 1.0
	} else if r, ok := perUSD[string(unit)]; ok && !math.IsNaN(r) && !math.IsInf(r, 0) && r > 0 {
		rate = r
	}
	return MoneyFormat{Unit: unit, Rate: rate}
}

func (m MoneyFormat) Convert(usd float64) float64 {
	r := m.Rate
	if r <= 0 {
		r = 1
	}
	return usd * r
}

func (m MoneyFormat) FractionDigitsFor(usd float64) int {
	if m.Unit.FractionDigits() == 0 {
		return 0
	}
	if math.Abs(m.Convert(usd)) >= 10_000 {
		return 0
	}
	return 2
}

func (m MoneyFormat) Format(usd float64) string {
	return m.formatValue(m.Convert(usd), m.FractionDigitsFor(usd))
}

func (m MoneyFormat) FormatDigits(usd float64, digits int) string {
	if m.Unit.FractionDigits() == 0 {
		digits = 0
	}
	return m.formatValue(m.Convert(usd), digits)
}

func (m MoneyFormat) Whole(usd float64) string {
	v := m.Convert(usd)
	if m.Unit.FractionDigits() == 0 || math.Abs(v) >= 100 {
		return m.formatValue(math.Round(v), 0)
	}
	return m.formatValue(v, 2)
}

func (m MoneyFormat) Compact(usd float64) string {
	v := m.Convert(usd)
	abs := math.Abs(v)
	sign := ""
	if v < 0 {
		sign = "-"
	}
	switch {
	case abs >= 1_000_000:
		return sign + m.Unit.Symbol() + trimDecimal(fmt.Sprintf("%.2f", abs/1_000_000)) + "M"
	case abs >= 10_000:
		return sign + m.Unit.Symbol() + trimDecimal(fmt.Sprintf("%.1f", abs/1_000)) + "k"
	default:
		return m.Whole(usd)
	}
}

func (m MoneyFormat) formatValue(v float64, digits int) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	if digits == 0 {
		return sign + m.Unit.Symbol() + FormatGrouped(int64(math.Round(v)))
	}
	s := strconv.FormatFloat(v, 'f', digits, 64)
	parts := strings.SplitN(s, ".", 2)
	iv, _ := strconv.ParseInt(parts[0], 10, 64)
	return sign + m.Unit.Symbol() + FormatGrouped(iv) + "." + parts[1]
}

// Split apportions total USD across costs so displayed strings sum to Format(total).
func (m MoneyFormat) Split(total float64, costs []float64) []string {
	digits := m.FractionDigitsFor(total)
	scale := math.Pow10(digits)
	totalUnits := int(math.Round(m.Convert(total) * scale))
	quotas := make([]float64, len(costs))
	for i, c := range costs {
		quotas[i] = m.Convert(c) * scale
	}
	parts := RoundQuotas(quotas, totalUnits)
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = m.formatValue(float64(p)/scale, digits)
	}
	return out
}

func FormatGrouped(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		if i > rem {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// FormatMagnitude formats large token counts in Chinese (万/亿) or English (k/M/B).
func FormatMagnitude(n int64, zh bool) string {
	abs := float64(n)
	if abs < 0 {
		abs = -abs
	}
	if zh {
		switch {
		case abs >= 1e8:
			return trimDecimal(fmt.Sprintf("%.2f", float64(n)/1e8)) + " 亿"
		case abs >= 1e4:
			if abs >= 1e6 {
				return trimDecimal(fmt.Sprintf("%.1f", float64(n)/1e4)) + " 万"
			}
			return trimDecimal(fmt.Sprintf("%.1f", float64(n)/1e4)) + " 万"
		default:
			return FormatGrouped(n)
		}
	}
	switch {
	case abs >= 1e9:
		return trimDecimal(fmt.Sprintf("%.2f", float64(n)/1e9)) + "B"
	case abs >= 1e6:
		return trimDecimal(fmt.Sprintf("%.1f", float64(n)/1e6)) + "M"
	case abs >= 1e3:
		return trimDecimal(fmt.Sprintf("%.1f", float64(n)/1e3)) + "k"
	default:
		return FormatGrouped(n)
	}
}

func trimDecimal(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

func FormatPercent(fraction float64, digits int) string {
	pct := fraction * 100
	if digits <= 0 {
		return fmt.Sprintf("%d%%", int(math.Round(pct)))
	}
	return fmt.Sprintf("%.*f%%", digits, pct)
}

func FormatSafePercent(fraction float64) string {
	tenths := math.Round(fraction * 1000)
	if int(tenths)%10 == 0 {
		return fmt.Sprintf("%d%%", int(tenths/10))
	}
	return fmt.Sprintf("%.1f%%", tenths/10)
}

func FormatShares(costs []float64) []string {
	parts := LargestRemainder(costs, 100)
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = fmt.Sprintf("%d%%", p)
	}
	return out
}

func FormatRelative(t, now time.Time, zh bool) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	sec := int(d.Seconds())
	switch {
	case sec < 45:
		if zh {
			return "刚刚"
		}
		return "just now"
	case sec < 3600:
		m := max(1, int(math.Round(float64(sec)/60)))
		if zh {
			return fmt.Sprintf("%d 分钟前", m)
		}
		return fmt.Sprintf("%dm ago", m)
	case sec < 86400:
		h := int(math.Round(float64(sec) / 3600))
		if zh {
			return fmt.Sprintf("%d 小时前", h)
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(math.Round(float64(sec) / 86400))
		if zh {
			return fmt.Sprintf("%d 天前", days)
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

func FormatCountdown(d time.Duration, zh bool) string {
	if d <= 0 {
		if zh {
			return "0 分"
		}
		return "0m"
	}
	totalMin := int(math.Ceil(d.Minutes()))
	days := totalMin / (24 * 60)
	hours := (totalMin % (24 * 60)) / 60
	mins := totalMin % 60
	if zh {
		switch {
		case days > 0 && hours > 0:
			return fmt.Sprintf("%d 天 %d 小时", days, hours)
		case days > 0:
			return fmt.Sprintf("%d 天", days)
		case hours > 0 && mins > 0:
			return fmt.Sprintf("%d 小时 %d 分", hours, mins)
		case hours > 0:
			return fmt.Sprintf("%d 小时", hours)
		default:
			return fmt.Sprintf("%d 分", max(1, mins))
		}
	}
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", max(1, mins))
	}
}

func FormatClock(d time.Duration, zh bool) string {
	total := max(0, int(d.Seconds()))
	days := total / 86400
	hms := fmt.Sprintf("%02d:%02d:%02d", (total%86400)/3600, (total%3600)/60, total%60)
	if days > 0 {
		if zh {
			return fmt.Sprintf("%d 天 %s", days, hms)
		}
		return fmt.Sprintf("%dd %s", days, hms)
	}
	return hms
}

var zhWeekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
var enWeekdays = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

func RoundToMinute(t time.Time) time.Time {
	return time.Unix(int64(math.Round(float64(t.Unix())/60.0)*60), 0).In(t.Location())
}

func FormatResetMoment(t time.Time, fullDate bool, now time.Time, zh bool) string {
	r := RoundToMinute(t.In(now.Location()))
	if fullDate {
		if zh {
			return fmt.Sprintf("%d年%d月%d日 %02d:%02d", r.Year(), int(r.Month()), r.Day(), r.Hour(), r.Minute())
		}
		return r.Format("Jan 2, 2006 15:04")
	}
	hm := r.Format("15:04")
	ny, nm, nd := now.Date()
	ry, rm, rd := r.Date()
	if ry == ny && rm == nm && rd == nd {
		if zh {
			return "今天 " + hm
		}
		return "today " + hm
	}
	tom := now.AddDate(0, 0, 1)
	ty, tm, td := tom.Date()
	if ry == ty && rm == tm && rd == td {
		if zh {
			return "明天 " + hm
		}
		return "tomorrow " + hm
	}
	if zh {
		return zhWeekdays[r.Weekday()] + " " + hm
	}
	return enWeekdays[r.Weekday()] + " " + hm
}

func FormatPeriodRange(start, end time.Time, zh bool) string {
	s := start.Local()
	e := end.Add(-time.Second).Local()
	if zh {
		return fmt.Sprintf("%d月%d日 %02d:%02d:%02d – %d月%d日 %02d:%02d:%02d",
			int(s.Month()), s.Day(), s.Hour(), s.Minute(), s.Second(),
			int(e.Month()), e.Day(), e.Hour(), e.Minute(), e.Second(),
		)
	}
	return fmt.Sprintf("%s – %s", s.Format("Jan 2 15:04:05"), e.Format("Jan 2 15:04:05"))
}

func FormatDayWithWeekday(t time.Time, zh bool) string {
	l := t.Local()
	if zh {
		return fmt.Sprintf("%d月%d日 %s", int(l.Month()), l.Day(), zhWeekdays[l.Weekday()])
	}
	return fmt.Sprintf("%s, %s", enWeekdays[l.Weekday()], l.Format("Jan 2"))
}
