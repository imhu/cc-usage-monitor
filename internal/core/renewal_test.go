package core

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func makeTestCredsJSON(t *testing.T, access, refresh string, expMs int64, scopes []string) []byte {
	t.Helper()
	root := map[string]any{
		"mcpOAuth": map[string]any{"srv": "keep-me"},
		"claudeAiOauth": map[string]any{
			"accessToken":  access,
			"refreshToken": refresh,
			"expiresAt":    expMs,
			"scopes":       scopes,
		},
	}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCredentialRenewalCanWriteProtectsRefreshToken(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	raw := makeTestCredsJSON(t, "acc-1", "ref-1", now.Add(time.Minute).UnixMilli(), []string{"user:profile", "user:inference"})
	c, _ := ParseCredentialsJSON(raw)

	sent := 0
	r := CredentialRenewal{
		Read: func() (*StoredCredentials, bool) {
			return &StoredCredentials{Credentials: *c, Raw: raw}, false
		},
		WriteAndVerify: func([]byte) bool { return false }, // simulate read-only/locked file
		SendRefresh: func(context.Context, string, []string, string) (*OAuthGrant, error) {
			sent++
			return nil, nil
		},
		AcquireRefreshLock: func() (func(), bool, error) { return func() {}, false, nil },
		AcquireWriteLock:   func() (func(), bool, error) { return func() {}, false, nil },
		Sleep:              func(context.Context, time.Duration) bool { return true },
		Now:                func() time.Time { return now },
	}

	res := r.Renew(context.Background(), "acc-1", false)
	if res.Kind != RenewFailed || res.Failure != RenewFailNotWritable {
		t.Fatalf("expected RenewFailNotWritable, got %+v", res)
	}
	if sent != 0 {
		t.Fatalf("expected 0 network refresh calls when file not writable, got %d", sent)
	}
}

func TestCredentialRenewalForceLockRetryMergeAndSaveRetry(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	// Token expires 2 hours later (NeedsRefresh == false), so only force=true should renew it.
	disk := makeTestCredsJSON(t, "acc-1", "ref-1", now.Add(2*time.Hour).UnixMilli(), []string{"user:profile", "user:inference"})

	var sleeps []time.Duration
	refreshLockTries := 0
	writeLockTries := 0
	writeCalls := 0
	scopeCalls := 0

	r := CredentialRenewal{
		Read: func() (*StoredCredentials, bool) {
			c, cleared := ParseCredentialsJSON(disk)
			if c == nil {
				return nil, cleared
			}
			return &StoredCredentials{Credentials: *c, Raw: disk}, false
		},
		WriteAndVerify: func(data []byte) bool {
			writeCalls++
			// Call 1 = canWrite pre-flight (succeed); Call 2 = first real save (fail once); Call 3 = retry save (succeed).
			if writeCalls == 2 {
				return false
			}
			disk = append([]byte(nil), data...)
			return true
		},
		SendRefresh: func(_ context.Context, rt string, scopes []string, _ string) (*OAuthGrant, error) {
			scopeCalls++
			if scopeCalls == 1 {
				// Simulate invalid_scope on expanded scopes -> should fall back to original scopes.
				return nil, ErrInvalidScope
			}
			if !slices.Equal(scopes, []string{"user:profile", "user:inference"}) {
				t.Fatalf("expected fallback to original scopes, got %v", scopes)
			}
			return &OAuthGrant{
				AccessToken:  "acc-2",
				RefreshToken: "ref-2",
				ExpiresAtMs:  now.Add(8 * time.Hour).UnixMilli(),
				Scopes:       scopes,
			}, nil
		},
		AcquireRefreshLock: func() (func(), bool, error) {
			refreshLockTries++
			if refreshLockTries < 3 {
				return nil, true, nil
			}
			return func() {}, false, nil
		},
		AcquireWriteLock: func() (func(), bool, error) {
			writeLockTries++
			if writeLockTries <= 2 {
				return nil, true, nil
			}
			return func() {}, false, nil
		},
		Sleep: func(_ context.Context, d time.Duration) bool {
			sleeps = append(sleeps, d)
			return true
		},
		RandFloat: func() float64 { return 0.5 },
		Now:       func() time.Time { return now },
	}

	res := r.Renew(context.Background(), "acc-1", true)
	if res.Kind != RenewRenewed || res.Credentials == nil || res.Credentials.AccessToken != "acc-2" {
		t.Fatalf("expected RenewRenewed with acc-2, got %+v", res)
	}
	if refreshLockTries != 3 || scopeCalls != 2 || writeCalls != 3 {
		t.Fatalf("unexpected counts: refreshLock=%d scopeCalls=%d writeCalls=%d", refreshLockTries, scopeCalls, writeCalls)
	}
	// Verify exponential write-lock backoff (100ms, 200ms) and save retry (1s) were recorded.
	if len(sleeps) < 5 || sleeps[2] != 100*time.Millisecond || sleeps[3] != 200*time.Millisecond || sleeps[len(sleeps)-1] != time.Second {
		t.Fatalf("unexpected sleep schedule: %v", sleeps)
	}
	// Verify merged JSON preserved mcpOAuth and explicitly included null subscriptionType/rateLimitTier.
	var root map[string]any
	if err := json.Unmarshal(disk, &root); err != nil {
		t.Fatal(err)
	}
	if mcp, ok := root["mcpOAuth"].(map[string]any); !ok || mcp["srv"] != "keep-me" {
		t.Fatalf("mcpOAuth key was not preserved: %v", root)
	}
	oauth := root["claudeAiOauth"].(map[string]any)
	if _, hasSub := oauth["subscriptionType"]; !hasSub {
		t.Fatalf("expected explicit subscriptionType key in merged JSON: %v", oauth)
	}
	if _, hasTier := oauth["rateLimitTier"]; !hasTier {
		t.Fatalf("expected explicit rateLimitTier key in merged JSON: %v", oauth)
	}
}

func TestCredentialRenewalAdoptOrOnConcurrentClaudeCodeRenewal(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	disk := makeTestCredsJSON(t, "acc-old", "ref-old", now.Add(time.Minute).UnixMilli(), []string{"user:profile", "user:inference"})

	r := CredentialRenewal{
		Read: func() (*StoredCredentials, bool) {
			c, cleared := ParseCredentialsJSON(disk)
			if c == nil {
				return nil, cleared
			}
			return &StoredCredentials{Credentials: *c, Raw: disk}, false
		},
		WriteAndVerify: func(data []byte) bool {
			disk = append([]byte(nil), data...)
			return true
		},
		SendRefresh: func(context.Context, string, []string, string) (*OAuthGrant, error) {
			// Simulate Claude Code concurrently renewing and writing acc-new while our call got invalid_grant.
			disk = makeTestCredsJSON(t, "acc-new", "ref-new", now.Add(8*time.Hour).UnixMilli(), []string{"user:profile", "user:inference"})
			return nil, ErrInvalidGrant
		},
		AcquireRefreshLock: func() (func(), bool, error) { return func() {}, false, nil },
		AcquireWriteLock:   func() (func(), bool, error) { return func() {}, false, nil },
		Sleep:              func(context.Context, time.Duration) bool { return true },
		Now:                func() time.Time { return now },
	}

	res := r.Renew(context.Background(), "acc-old", false)
	if res.Kind != RenewCurrent || res.Credentials == nil || res.Credentials.AccessToken != "acc-new" {
		t.Fatalf("expected adoptOr to return RenewCurrent(acc-new), got %+v", res)
	}
}

func TestCredentialRenewalSkipsPersistWithoutInferenceScope(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	original := makeTestCredsJSON(t, "acc-1", "ref-1", now.Add(time.Minute).UnixMilli(), []string{"user:profile", "user:inference"})
	disk := append([]byte(nil), original...)
	saveAttempts := 0

	r := CredentialRenewal{
		Read: func() (*StoredCredentials, bool) {
			c, cleared := ParseCredentialsJSON(disk)
			if c == nil {
				return nil, cleared
			}
			return &StoredCredentials{Credentials: *c, Raw: disk}, false
		},
		WriteAndVerify: func(data []byte) bool {
			saveAttempts++
			disk = append([]byte(nil), data...)
			return true
		},
		SendRefresh: func(context.Context, string, []string, string) (*OAuthGrant, error) {
			return &OAuthGrant{
				AccessToken:  "acc-profile-only",
				RefreshToken: "ref-2",
				ExpiresAtMs:  now.Add(8 * time.Hour).UnixMilli(),
				Scopes:       []string{"user:profile"}, // lacks user:inference
			}, nil
		},
		AcquireRefreshLock: func() (func(), bool, error) { return func() {}, false, nil },
		AcquireWriteLock:   func() (func(), bool, error) { return func() {}, false, nil },
		Sleep:              func(context.Context, time.Duration) bool { return true },
		Now:                func() time.Time { return now },
	}

	res := r.Renew(context.Background(), "acc-1", false)
	if res.Kind != RenewFailed || res.Failure != RenewFailSaveFailed || res.Credentials == nil || res.Credentials.AccessToken != "acc-profile-only" {
		t.Fatalf("expected RenewFailSaveFailed carrying renewed creds, got %+v", res)
	}
	// Only canWrite (1) should have written; save must NOT overwrite .credentials.json.
	if saveAttempts != 1 {
		t.Fatalf("expected only pre-flight canWrite (1 write), got %d writes", saveAttempts)
	}
}
