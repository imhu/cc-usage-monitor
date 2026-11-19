package core

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"time"
)

// StoredCredentials pairs parsed OAuth credentials with the exact raw JSON bytes
// so write-back merges into the original file without losing unrelated keys.
type StoredCredentials struct {
	Credentials Credentials
	Raw         []byte
}

type RenewResultKind int

const (
	RenewRenewed RenewResultKind = iota // Renewed and persisted back to disk
	RenewCurrent                        // Already renewed elsewhere or not yet due
	RenewFailed                         // Failed; inspect FailureKind / Err / Credentials
)

type RenewFailureKind int

const (
	RenewFailNone RenewFailureKind = iota
	RenewFailLocked
	RenewFailNoCredentials
	RenewFailNotRenewable
	RenewFailNotWritable
	RenewFailSignedOut
	RenewFailRateLimited
	RenewFailNetwork
	RenewFailServer
	RenewFailSaveFailed
)

type RenewResult struct {
	Kind             RenewResultKind
	Failure          RenewFailureKind
	Credentials      *Credentials // Non-nil for RenewRenewed, RenewCurrent, and RenewFailSaveFailed
	Cleared          bool         // True if .credentials.json had claudeAiOauth with empty accessToken
	UsedRefreshToken string       // Populated on RenewFailSignedOut
	RetryAfter       time.Duration
	Err              error
}

// CredentialRenewal implements Claude Code / claude-usage-monitor's 5-step renewal flow:
//  1. Acquire refresh locks (.oauth_refresh.lock + <dir>.lock), retrying 1-2s up to 5 times.
//  2. Re-read credentials: if accessToken already changed or (!force && !needsRefresh), adopt current.
//  3. Pre-flight writability check (canWrite) inside write lock BEFORE calling /v1/oauth/token,
//     because Anthropic rotates & invalidates the old refresh_token on refresh.
//  4. Call /v1/oauth/token (falling back to original scopes once on invalid_scope if expanded);
//     on error, re-read disk once (adoptOr) in case Claude Code beat us to renewal.
//  5. Acquire write lock (100ms exponential backoff up to 1s, max 20 attempts), verify refresh_token
//     unchanged, merge JSON, and write+verify (retrying once after 1s on failure).
type CredentialRenewal struct {
	Read               func() (*StoredCredentials, bool)
	WriteAndVerify     func(data []byte) bool
	SendRefresh        func(ctx context.Context, refreshToken string, scopes []string, clientID string) (*OAuthGrant, error)
	AcquireRefreshLock func() (release func(), locked bool, err error)
	AcquireWriteLock   func() (release func(), locked bool, err error)
	Sleep              func(ctx context.Context, d time.Duration) bool
	RandFloat          func() float64
	Now                func() time.Time
}

func (r CredentialRenewal) sleep(ctx context.Context, d time.Duration) bool {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (r CredentialRenewal) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r CredentialRenewal) randFloat() float64 {
	if r.RandFloat != nil {
		return r.RandFloat()
	}
	return rand.Float64()
}

func (r CredentialRenewal) Renew(ctx context.Context, expectedAccessToken string, force bool) RenewResult {
	var releaseRefresh func()
	for attempt := 0; attempt <= 5; attempt++ {
		rel, locked, err := r.AcquireRefreshLock()
		if err == nil && !locked {
			releaseRefresh = rel
			break
		}
		if locked {
			if attempt == 5 {
				return RenewResult{Kind: RenewFailed, Failure: RenewFailLocked, Err: errors.New("refresh lock held by Claude Code")}
			}
			delay := time.Second + time.Duration(r.randFloat()*float64(time.Second))
			if !r.sleep(ctx, delay) {
				return RenewResult{Kind: RenewFailed, Failure: RenewFailLocked, Err: ctx.Err()}
			}
			continue
		}
		return RenewResult{Kind: RenewFailed, Failure: RenewFailLocked, Err: err}
	}
	defer releaseRefresh()

	stored, cleared := r.Read()
	if stored == nil {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailNoCredentials, Cleared: cleared}
	}
	current := stored.Credentials
	if current.AccessToken != expectedAccessToken {
		return RenewResult{Kind: RenewCurrent, Credentials: &current}
	}
	if !force && !current.NeedsRefresh(r.now()) {
		return RenewResult{Kind: RenewCurrent, Credentials: &current}
	}
	if !current.IsRefreshable() {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailNotRenewable}
	}
	if !r.canWrite(ctx) {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailNotWritable, Err: errors.New("credentials file is not writable")}
	}

	refreshToken := current.RefreshToken
	scopes, expanded := RefreshScopesFor(current)
	grant, err := r.SendRefresh(ctx, refreshToken, scopes, current.ClientID)
	if err != nil && expanded && errors.Is(err, ErrInvalidScope) && slices.Contains(current.Scopes, "user:inference") {
		scopes = current.Scopes
		grant, err = r.SendRefresh(ctx, refreshToken, scopes, current.ClientID)
	}
	if err != nil {
		var rlErr *OAuthRateLimitError
		if errors.As(err, &rlErr) {
			return r.adoptOr(RenewResult{
				Kind:       RenewFailed,
				Failure:    RenewFailRateLimited,
				RetryAfter: rlErr.RetryAfter,
				Err:        rlErr,
			}, expectedAccessToken)
		}
		if errors.Is(err, ErrInvalidGrant) {
			return r.adoptOr(RenewResult{
				Kind:             RenewFailed,
				Failure:          RenewFailSignedOut,
				UsedRefreshToken: refreshToken,
				Err:              err,
			}, expectedAccessToken)
		}
		if errors.Is(err, ErrInvalidScope) {
			return r.adoptOr(RenewResult{
				Kind:    RenewFailed,
				Failure: RenewFailServer,
				Err:     err,
			}, expectedAccessToken)
		}
		return r.adoptOr(RenewResult{
			Kind:    RenewFailed,
			Failure: RenewFailNetwork,
			Err:     err,
		}, expectedAccessToken)
	}
	if grant == nil {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailServer, Err: errors.New("empty oauth grant")}
	}

	renewed := CredentialsFromGrant(*grant, current)
	// Mirror Claude Code: only persist claude.ai logins that carry user:inference.
	if !grant.IsClaudeAILogin() {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailSaveFailed, Credentials: &renewed}
	}
	return r.save(ctx, *grant, renewed, refreshToken, current.ClientID)
}

func (r CredentialRenewal) canWrite(ctx context.Context) bool {
	release := r.acquireWriteLockWaiting(ctx)
	if release == nil {
		return false
	}
	defer release()
	latest, _ := r.Read()
	if latest == nil {
		return false
	}
	return r.WriteAndVerify(latest.Raw)
}

// acquireWriteLockWaiting matches Claude Code / claude-usage-monitor:
// 100ms initial delay, doubling each time up to 1s, max 20 attempts.
func (r CredentialRenewal) acquireWriteLockWaiting(ctx context.Context) func() {
	delay := 100 * time.Millisecond
	for i := 0; i < 20; i++ {
		rel, locked, err := r.AcquireWriteLock()
		if err == nil && !locked {
			return rel
		}
		if i == 19 {
			return nil
		}
		if !r.sleep(ctx, delay) {
			return nil
		}
		delay *= 2
		if delay > time.Second {
			delay = time.Second
		}
	}
	return nil
}

// adoptOr re-reads credentials after a refresh error: if Claude Code already renewed
// while our request failed, adopt the fresh credentials instead of surfacing failure.
func (r CredentialRenewal) adoptOr(fail RenewResult, expectedAccessToken string) RenewResult {
	if latest, _ := r.Read(); latest != nil && latest.Credentials.AccessToken != expectedAccessToken {
		c := latest.Credentials
		return RenewResult{Kind: RenewCurrent, Credentials: &c}
	}
	return fail
}

func (r CredentialRenewal) save(ctx context.Context, grant OAuthGrant, renewed Credentials, usedRefreshToken, clientID string) RenewResult {
	release := r.acquireWriteLockWaiting(ctx)
	if release == nil {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailSaveFailed, Credentials: &renewed}
	}
	defer release()

	latest, _ := r.Read()
	if latest == nil {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailSaveFailed, Credentials: &renewed}
	}
	if latest.Credentials.RefreshToken != usedRefreshToken {
		c := latest.Credentials
		return RenewResult{Kind: RenewCurrent, Credentials: &c}
	}
	merged, err := MergeCredentialsJSON(latest.Raw, grant, clientID)
	if err != nil {
		return RenewResult{Kind: RenewFailed, Failure: RenewFailSaveFailed, Credentials: &renewed, Err: err}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if r.WriteAndVerify(merged) {
			return RenewResult{Kind: RenewRenewed, Credentials: &renewed}
		}
		if attempt == 0 {
			if !r.sleep(ctx, time.Second) {
				break
			}
		}
	}
	return RenewResult{Kind: RenewFailed, Failure: RenewFailSaveFailed, Credentials: &renewed}
}
