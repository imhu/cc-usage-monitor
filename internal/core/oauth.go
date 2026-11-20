package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	OAuthTokenURL      = "https://platform.claude.com/v1/oauth/token"
	OAuthUsageURL      = "https://api.anthropic.com/api/oauth/usage"
	OAuthProfileURL    = "https://api.anthropic.com/api/oauth/profile"
	OAuthBetaHeader    = "oauth-2025-04-20"
	OAuthUserAgent     = "claude-code/2.1.304"
	DefaultClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	OAuthRefreshMargin = 5 * time.Minute
)

var (
	ClaudeAIScopes    = []string{"user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload"}
	PreservableScopes = []string{"user:projects:read", "user:projects:write"}
)

var (
	ErrInvalidGrant = errors.New("oauth invalid_grant")
	ErrInvalidScope = errors.New("oauth invalid_scope")
)

type Credentials struct {
	AccessToken           string
	RefreshToken          string
	ExpiresAt             *time.Time
	Scopes                []string
	SubscriptionType      string
	RateLimitTier         string
	ClientID              string
	RefreshTokenExpiresAt *time.Time
}

func (c Credentials) NeedsRefresh(now time.Time) bool {
	if c.ExpiresAt == nil {
		return false
	}
	return !now.Add(OAuthRefreshMargin).Before(*c.ExpiresAt)
}

func (c Credentials) IsExpired(now time.Time) bool {
	if c.ExpiresAt == nil {
		return false
	}
	return !c.ExpiresAt.After(now)
}

func (c Credentials) IsRefreshable() bool {
	if c.RefreshToken == "" {
		return false
	}
	return slices.Contains(c.Scopes, "user:inference") || c.SubscriptionType != ""
}

func (c Credentials) LoginID() string {
	if c.RefreshToken != "" {
		return c.RefreshToken
	}
	return c.AccessToken
}

func ParseCredentialsJSON(data []byte) (*Credentials, bool) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, false
	}
	oauth, ok := root["claudeAiOauth"].(map[string]any)
	if !ok {
		return nil, false
	}
	token, _ := oauth["accessToken"].(string)
	if token == "" {
		return nil, true // cleared login
	}
	parseMs := func(key string) *time.Time {
		v, ok := oauth[key].(float64)
		if !ok || v <= 0 {
			return nil
		}
		tm := time.UnixMilli(int64(v))
		return &tm
	}
	var scopes []string
	if rawScopes, ok := oauth["scopes"].([]any); ok {
		for _, item := range rawScopes {
			if s, ok := item.(string); ok {
				scopes = append(scopes, s)
			}
		}
	}
	refresh, _ := oauth["refreshToken"].(string)
	subType, _ := oauth["subscriptionType"].(string)
	tier, _ := oauth["rateLimitTier"].(string)
	clientID, _ := oauth["clientId"].(string)

	return &Credentials{
		AccessToken:           token,
		RefreshToken:          refresh,
		ExpiresAt:             parseMs("expiresAt"),
		Scopes:                scopes,
		SubscriptionType:      subType,
		RateLimitTier:         tier,
		ClientID:              clientID,
		RefreshTokenExpiresAt: parseMs("refreshTokenExpiresAt"),
	}, false
}

type OAuthGrant struct {
	AccessToken           string
	RefreshToken          string
	ExpiresAtMs           int64
	RefreshTokenExpiresMs *int64
	Scopes                []string
}

// IsClaudeAILogin mirrors Claude Code's rule: only persist tokens that carry `user:inference`.
func (g OAuthGrant) IsClaudeAILogin() bool {
	return slices.Contains(g.Scopes, "user:inference")
}

func RefreshScopesFor(c Credentials) ([]string, bool) {
	expanded := (slices.Contains(c.Scopes, "user:inference") || c.SubscriptionType != "") && c.ClientID == ""
	if !expanded {
		return c.Scopes, false
	}
	result := slices.Clone(ClaudeAIScopes)
	for _, s := range c.Scopes {
		if slices.Contains(PreservableScopes, s) && !slices.Contains(result, s) {
			result = append(result, s)
		}
	}
	return result, true
}

func RequestTokenRefresh(ctx context.Context, client *http.Client, refreshToken string, scopes []string, clientID string, now time.Time) (*OAuthGrant, error) {
	if clientID == "" {
		clientID = DefaultClientID
	}
	reqScopes := scopes
	if len(reqScopes) == 0 {
		reqScopes = ClaudeAIScopes
	}
	payload := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
		"scope":         strings.Join(reqScopes, " "),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "") // Match claude-usage-monitor: omit custom/default User-Agent
	req.Close = true

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return nil, classifyOAuthError(resp.StatusCode, resp.Header, respBody, now)
	}
	return parseGrant(respBody, refreshToken, reqScopes, now)
}

func ParseRetryAfter(hdr http.Header, now time.Time) time.Duration {
	if hdr == nil {
		return 0
	}
	ra := strings.TrimSpace(hdr.Get("Retry-After"))
	if ra == "" {
		return 0
	}
	if sec, err := strconv.Atoi(ra); err == nil {
		if sec > 0 {
			return time.Duration(sec) * time.Second
		}
		return 0
	}
	if t, err := http.ParseTime(ra); err == nil {
		if d := t.Sub(now); d > 0 {
			return d.Round(time.Second)
		}
	}
	return 0
}

// SummarizeAPIError extracts a short, credential-free error summary from an Anthropic JSON/text response.
func SummarizeAPIError(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) == nil {
		if m, ok := raw["error"].(map[string]any); ok {
			typ, _ := m["type"].(string)
			msg, _ := m["message"].(string)
			if len(msg) > 120 {
				msg = msg[:120]
			}
			if typ != "" && msg != "" {
				return typ + ": " + msg
			}
			if msg != "" {
				return msg
			}
			if typ != "" {
				return typ
			}
		} else if s, ok := raw["error"].(string); ok && s != "" {
			desc, _ := raw["error_description"].(string)
			if desc != "" {
				return s + ": " + desc
			}
			return s
		}
	}
	s := strings.Join(strings.Fields(string(body)), " ")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func formatRetryWait(d time.Duration) string {
	sec := int((d + time.Second - 1) / time.Second)
	if sec < 60 {
		return fmt.Sprintf("%d 秒", sec)
	}
	min := sec / 60
	rem := sec % 60
	if rem == 0 {
		return fmt.Sprintf("%d 分钟（%ds）", min, sec)
	}
	return fmt.Sprintf("%d 分 %d 秒（%ds）", min, rem, sec)
}

type OAuthRateLimitError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *OAuthRateLimitError) Error() string {
	return e.Message
}

func classifyOAuthError(status int, hdr http.Header, body []byte, now time.Time) error {
	var raw map[string]any
	var code, msg string
	if json.Unmarshal(body, &raw) == nil {
		if s, ok := raw["error"].(string); ok {
			code = s
			msg, _ = raw["error_description"].(string)
		} else if m, ok := raw["error"].(map[string]any); ok {
			code, _ = m["type"].(string)
			msg, _ = m["message"].(string)
		}
	}
	if (status == 400 || status == 401) && code == "invalid_grant" {
		return ErrInvalidGrant
	}
	if status == 400 && code == "invalid_scope" {
		return ErrInvalidScope
	}
	base := fmt.Sprintf("oauth token HTTP %d (%s)", status, code)
	if msg != "" {
		base = fmt.Sprintf("oauth token HTTP %d (%s: %s)", status, code, msg)
	}
	if status == http.StatusTooManyRequests {
		wait := ParseRetryAfter(hdr, now)
		if wait > 0 {
			return &OAuthRateLimitError{
				RetryAfter: wait,
				Message:    fmt.Sprintf("%s — 请约 %s后重试", base, formatRetryWait(wait)),
			}
		}
		return &OAuthRateLimitError{
			Message: fmt.Sprintf("%s — 服务端未返回 Retry-After，建议 5~15 分钟后重试或切换代理节点", base),
		}
	}
	return errors.New(base)
}

func parseGrant(data []byte, previousRefresh string, requestedScopes []string, now time.Time) (*OAuthGrant, error) {
	var raw struct {
		AccessToken           string  `json:"access_token"`
		RefreshToken          string  `json:"refresh_token"`
		ExpiresIn             float64 `json:"expires_in"`
		RefreshTokenExpiresIn float64 `json:"refresh_token_expires_in"`
		Scope                 string  `json:"scope"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.AccessToken == "" || raw.ExpiresIn <= 0 {
		return nil, errors.New("invalid oauth grant payload")
	}
	nowMs := now.UnixMilli()
	refresh := raw.RefreshToken
	if refresh == "" {
		refresh = previousRefresh
	}
	var rtExp *int64
	if raw.RefreshTokenExpiresIn > 0 {
		v := nowMs + int64(raw.RefreshTokenExpiresIn*1000)
		rtExp = &v
	}
	scopes := requestedScopes
	if raw.Scope != "" {
		scopes = strings.Fields(raw.Scope)
	}
	return &OAuthGrant{
		AccessToken:           raw.AccessToken,
		RefreshToken:          refresh,
		ExpiresAtMs:           nowMs + int64(raw.ExpiresIn*1000),
		RefreshTokenExpiresMs: rtExp,
		Scopes:                scopes,
	}, nil
}

// MergeCredentialsJSON updates only `claudeAiOauth` while keeping all other keys (e.g. MCP tokens).
func MergeCredentialsJSON(original []byte, grant OAuthGrant, clientID string) ([]byte, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(original)) > 0 {
		if err := json.Unmarshal(original, &root); err != nil {
			return nil, err
		}
	}
	old, _ := root["claudeAiOauth"].(map[string]any)
	kept := func(key string) any {
		if old == nil {
			return nil
		}
		v, ok := old[key]
		if !ok || v == nil {
			return nil
		}
		return v
	}
	scopes := grant.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	oauth := map[string]any{
		"accessToken":      grant.AccessToken,
		"refreshToken":     grant.RefreshToken,
		"expiresAt":        grant.ExpiresAtMs,
		"scopes":           scopes,
		"subscriptionType": kept("subscriptionType"),
		"rateLimitTier":    kept("rateLimitTier"),
	}
	if grant.RefreshTokenExpiresMs != nil {
		oauth["refreshTokenExpiresAt"] = *grant.RefreshTokenExpiresMs
	} else if v := kept("refreshTokenExpiresAt"); v != nil {
		oauth["refreshTokenExpiresAt"] = v
	}
	if clientID != "" {
		oauth["clientId"] = clientID
	}
	root["claudeAiOauth"] = oauth
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func CredentialsFromGrant(grant OAuthGrant, prev Credentials) Credentials {
	exp := time.UnixMilli(grant.ExpiresAtMs)
	rtExp := prev.RefreshTokenExpiresAt
	if grant.RefreshTokenExpiresMs != nil {
		t := time.UnixMilli(*grant.RefreshTokenExpiresMs)
		rtExp = &t
	}
	return Credentials{
		AccessToken:           grant.AccessToken,
		RefreshToken:          grant.RefreshToken,
		ExpiresAt:             &exp,
		Scopes:                grant.Scopes,
		SubscriptionType:      prev.SubscriptionType,
		RateLimitTier:         prev.RateLimitTier,
		ClientID:              prev.ClientID,
		RefreshTokenExpiresAt: rtExp,
	}
}
