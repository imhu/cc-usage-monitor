package core

import (
	"encoding/json"
	"math"
	"time"
)

const ClaudeCodeCacheFreshness = 60 * time.Second

type ClaudeCodeAccount struct {
	UUID             string
	Email            string
	OrganizationType string
	RateLimitTier    string
}

type ClaudeCodeUsageCache struct {
	Usage       OfficialUsage
	AccountUUID string
}

// Replaces matches ClaudeCodeUsageCache.replaces in claude-usage-monitor:
// same account, not >60s in the future, and at least 1s newer than shown.
func (c ClaudeCodeUsageCache) Replaces(shown *time.Time, accountUUID string, now time.Time) bool {
	if accountUUID != "" && c.AccountUUID != "" && c.AccountUUID != accountUUID {
		return false
	}
	if c.Usage.FetchedAt.Sub(now) >= 60*time.Second {
		return false
	}
	if shown == nil {
		return true
	}
	return c.Usage.FetchedAt.Sub(*shown) > time.Second
}

// IsFresh returns true when the cached reading is within 60s of now.
func (c ClaudeCodeUsageCache) IsFresh(now time.Time) bool {
	d := now.Sub(c.Usage.FetchedAt)
	if d < 0 {
		d = -d
	}
	return d < ClaudeCodeCacheFreshness
}

type ClaudeCodeConfig struct {
	Account *ClaudeCodeAccount
	Usage   *ClaudeCodeUsageCache
}

// ParseClaudeCodeConfig parses ~/.claude.json to extract the active account and cached usage,
// matching Claude Code's `oauthAccount` and `cachedUsageUtilization` schema.
func ParseClaudeCodeConfig(data []byte) *ClaudeCodeConfig {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	cfg := &ClaudeCodeConfig{}

	if acctRaw, ok := raw["oauthAccount"]; ok {
		var a struct {
			AccountUUID               string `json:"accountUuid"`
			EmailAddress              string `json:"emailAddress"`
			OrganizationType          string `json:"organizationType"`
			OrganizationRateLimitTier string `json:"organizationRateLimitTier"`
		}
		if json.Unmarshal(acctRaw, &a) == nil && a.AccountUUID != "" {
			cfg.Account = &ClaudeCodeAccount{
				UUID:             a.AccountUUID,
				Email:            a.EmailAddress,
				OrganizationType: a.OrganizationType,
				RateLimitTier:    a.OrganizationRateLimitTier,
			}
		}
	}

	if cacheRaw, ok := raw["cachedUsageUtilization"]; ok {
		var c struct {
			FetchedAtMs float64         `json:"fetchedAtMs"`
			Timestamp   float64         `json:"timestamp"`
			FetchedAt   float64         `json:"fetchedAt"`
			AccountUUID string          `json:"accountUuid"`
			Utilization json.RawMessage `json:"utilization"`
			Data        json.RawMessage `json:"data"`
			Usage       json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(cacheRaw, &c) == nil {
			ms := c.FetchedAtMs
			if ms <= 0 {
				ts := c.Timestamp
				if ts <= 0 {
					ts = c.FetchedAt
				}
				if ts > 0 {
					if ts > 1e12 {
						ms = ts
					} else {
						ms = ts * 1000.0
					}
				}
			}
			payload := c.Utilization
			if len(payload) == 0 {
				payload = c.Data
			}
			if len(payload) == 0 {
				payload = c.Usage
			}
			uuid := c.AccountUUID
			if uuid == "" && cfg.Account != nil {
				uuid = cfg.Account.UUID
			}
			// Match Claude Code / claude-usage-monitor: discard cache if oauthAccount switched away.
			accountMatches := cfg.Account == nil || uuid == "" || cfg.Account.UUID == uuid
			if accountMatches && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms > 0 && len(payload) > 0 {
				fetchedAt := time.UnixMilli(int64(ms))
				if usage, err := DecodeOfficialUsage(payload, fetchedAt); err == nil &&
					(usage.FiveHour != nil || usage.SevenDay != nil || usage.SevenDaySonnet != nil || usage.SevenDayOpus != nil || len(usage.Scoped) > 0) {
					cfg.Usage = &ClaudeCodeUsageCache{
						Usage:       *usage,
						AccountUUID: uuid,
					}
				}
			}
		}
	}

	return cfg
}
