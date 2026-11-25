package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
)

type UpdateStatusKind int

const (
	UpdateIdle UpdateStatusKind = iota
	UpdateChecking
	UpdateAvailable
	UpdateUpToDate
	UpdateDownloading
	UpdateVerifying
	UpdateInstalling
	UpdateReadyToRestart
	UpdateError
)

type UpdateViewState struct {
	CurrentVersion string
	Status         UpdateStatusKind
	Release        *core.ReleaseInfo
	SkippedVersion string
	CheckedAt      *time.Time
	Progress       float64
	Downloaded     int64
	Total          int64
	ErrorMsg       string
}

func (u UpdateViewState) HasAvailable() bool {
	return u.Release != nil && core.IsNewerVersion(u.CurrentVersion, u.Release.TagName)
}

func (u UpdateViewState) HasBadge() bool {
	return u.HasAvailable() && !strings.EqualFold(strings.TrimSpace(u.SkippedVersion), strings.TrimSpace(u.Release.TagName))
}

func (s *Store) SetRelaunchHandler(fn func()) {
	s.mu.Lock()
	s.onRelaunch = fn
	s.mu.Unlock()
}

func (s *Store) SetUpdateReleaseForTest(rel *core.ReleaseInfo) {
	s.mu.Lock()
	s.updateRelease = rel
	if rel != nil && core.IsNewerVersion(core.AppVersion, rel.TagName) {
		s.updateStatus = UpdateAvailable
	} else {
		s.updateStatus = UpdateIdle
	}
	s.updateError = ""
	s.mu.Unlock()
	s.notify()
}

func (s *Store) updateViewStateLocked() UpdateViewState {
	var relCopy *core.ReleaseInfo
	if s.updateRelease != nil {
		cp := *s.updateRelease
		relCopy = &cp
	}
	var checkedCopy *time.Time
	if s.pstate.UpdateCheckedAt != nil {
		t := *s.pstate.UpdateCheckedAt
		checkedCopy = &t
	}
	return UpdateViewState{
		CurrentVersion: core.AppVersion,
		Status:         s.updateStatus,
		Release:        relCopy,
		SkippedVersion: s.prefs.SkippedVersion,
		CheckedAt:      checkedCopy,
		Progress:       s.updateProgress,
		Downloaded:     s.updateDownloaded,
		Total:          s.updateTotal,
		ErrorMsg:       s.updateError,
	}
}

func (s *Store) startUpdateScheduler() {
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()
	if ctx == nil {
		return
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(4 * time.Second):
	}

	s.mu.RLock()
	checkedAt := s.pstate.UpdateCheckedAt
	s.mu.RUnlock()
	if checkedAt == nil || time.Since(*checkedAt) >= core.UpdateCheckInterval {
		s.CheckForUpdates(false)
	}

	ticker := time.NewTicker(core.UpdateCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.CheckForUpdates(false)
		}
	}
}

func (s *Store) SkipUpdateVersion(tag string) {
	s.UpdatePrefs(func(p *core.Preferences) {
		p.SkippedVersion = strings.TrimSpace(tag)
	})
}

func (s *Store) ClearSkippedVersion() {
	s.UpdatePrefs(func(p *core.Preferences) {
		p.SkippedVersion = ""
	})
}

// CheckForUpdates queries GitHub Releases (using ETag caching) for a newer version.
func (s *Store) CheckForUpdates(manual bool) {
	s.mu.Lock()
	if s.updateStatus == UpdateChecking || s.updateStatus == UpdateDownloading ||
		s.updateStatus == UpdateVerifying || s.updateStatus == UpdateInstalling {
		s.mu.Unlock()
		return
	}
	prevStatus := s.updateStatus
	s.updateStatus = UpdateChecking
	if manual {
		s.updateError = ""
	}
	proxyURL := s.effectiveProxyLocked()
	apiURL := s.updateAPIURL
	if apiURL == "" {
		apiURL = core.UpdateAPIURL
	}
	etag := s.pstate.UpdateETag
	cached := s.pstate.CachedRelease
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Unlock()
	s.notify()

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		s.finishUpdateCheckError(manual, prevStatus, err)
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cc-usage-monitor/"+core.AppVersion)
	if etag != "" && cached != nil {
		req.Header.Set("If-None-Match", etag)
	}

	client := core.BuildHTTPClient(proxyURL, "tcp4", 15*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		s.finishUpdateCheckError(manual, prevStatus, err)
		return
	}
	defer resp.Body.Close()

	now := time.Now()
	if resp.StatusCode == http.StatusNotModified && cached != nil {
		s.applyCheckedReleaseLockedAndNotify(cached, etag, now, manual)
		return
	}
	if resp.StatusCode != http.StatusOK {
		s.finishUpdateCheckError(manual, prevStatus, fmt.Errorf("HTTP %d", resp.StatusCode))
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		s.finishUpdateCheckError(manual, prevStatus, err)
		return
	}
	rel, err := core.ParseGitHubLatestRelease(body, runtime.GOARCH)
	if err != nil {
		s.finishUpdateCheckError(manual, prevStatus, err)
		return
	}

	newETag := strings.TrimSpace(resp.Header.Get("ETag"))
	s.applyCheckedReleaseLockedAndNotify(rel, newETag, now, manual)
}

func (s *Store) applyCheckedReleaseLockedAndNotify(rel *core.ReleaseInfo, etag string, now time.Time, manual bool) {
	s.mu.Lock()
	s.pstate.UpdateCheckedAt = &now
	if etag != "" {
		s.pstate.UpdateETag = etag
	}
	cp := *rel
	s.pstate.CachedRelease = &cp
	s.savePersistedStateLocked()

	s.updateError = ""
	if core.IsNewerVersion(core.AppVersion, rel.TagName) {
		s.updateRelease = &cp
		s.updateStatus = UpdateAvailable
		// If the user previously skipped an older release, a newer tag surfaces normally.
		if manual && strings.EqualFold(strings.TrimSpace(s.prefs.SkippedVersion), strings.TrimSpace(rel.TagName)) {
			s.prefs.SkippedVersion = ""
			_ = core.SavePreferences(s.prefsPath, s.prefs)
		}
	} else {
		s.updateRelease = nil
		s.updateStatus = UpdateUpToDate
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Store) finishUpdateCheckError(manual bool, prevStatus UpdateStatusKind, err error) {
	s.mu.Lock()
	if manual {
		s.updateStatus = UpdateError
		if s.prefs.IsChinese() {
			s.updateError = fmt.Sprintf("检查更新失败：%v", err)
		} else {
			s.updateError = fmt.Sprintf("Update check failed: %v", err)
		}
	} else {
		if s.updateRelease != nil && core.IsNewerVersion(core.AppVersion, s.updateRelease.TagName) {
			s.updateStatus = UpdateAvailable
		} else {
			s.updateStatus = prevStatus
			if s.updateStatus == UpdateChecking {
				s.updateStatus = UpdateIdle
			}
		}
	}
	s.mu.Unlock()
	s.notify()
}

// InstallAvailableUpdate downloads the Windows zip asset, verifies SHA256SUMS.txt,
// extracts ccmonitor.exe, replaces the running executable in place, and relaunches.
func (s *Store) InstallAvailableUpdate() {
	go s.runInstallAvailableUpdate()
}

func (s *Store) runInstallAvailableUpdate() {
	s.mu.Lock()
	if s.updateStatus == UpdateDownloading || s.updateStatus == UpdateVerifying || s.updateStatus == UpdateInstalling {
		s.mu.Unlock()
		return
	}
	rel := s.updateRelease
	if rel == nil || !core.IsNewerVersion(core.AppVersion, rel.TagName) {
		s.mu.Unlock()
		return
	}
	if strings.TrimSpace(rel.AssetURL) == "" || strings.TrimSpace(rel.ChecksumsURL) == "" {
		s.updateStatus = UpdateError
		if s.prefs.IsChinese() {
			s.updateError = "该版本缺少 Windows 安装包或 SHA256 校验文件，请点击「前往下载」手动更新。"
		} else {
			s.updateError = "Missing Windows archive or SHA256SUMS.txt. Please use Open Release."
		}
		s.mu.Unlock()
		s.notify()
		return
	}

	parentCtx := s.ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if s.updateCancel != nil {
		s.updateCancel()
	}
	uctx, ucancel := context.WithTimeout(parentCtx, 5*time.Minute)
	s.updateCancel = ucancel
	proxyURL := s.effectiveProxyLocked()
	zh := s.prefs.IsChinese()
	targetExe := s.targetExeOverride
	relCopy := *rel
	s.updateStatus = UpdateDownloading
	s.updateProgress = 0
	s.updateDownloaded = 0
	s.updateTotal = relCopy.AssetSize
	s.updateError = ""
	s.mu.Unlock()
	s.notify()

	defer func() {
		ucancel()
		s.mu.Lock()
		s.updateCancel = nil
		s.mu.Unlock()
	}()

	fail := func(cn, en string, err error) {
		msg := fmt.Sprintf("%s: %v", en, err)
		if zh {
			msg = fmt.Sprintf("%s：%v", cn, err)
		}
		s.mu.Lock()
		s.updateStatus = UpdateError
		s.updateError = msg
		s.mu.Unlock()
		s.notify()
	}

	tmpDir, err := os.MkdirTemp("", "ccmonitor-update-*")
	if err != nil {
		fail("创建临时目录失败", "Create temp dir failed", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, relCopy.AssetName)
	client := core.BuildHTTPClient(proxyURL, "tcp4", 5*time.Minute)
	gotHash, err := s.downloadUpdateZip(uctx, client, relCopy.AssetURL, zipPath, relCopy.AssetSize)
	if err != nil {
		fail("下载更新包失败", "Download failed", err)
		return
	}

	s.mu.Lock()
	s.updateStatus = UpdateVerifying
	s.updateProgress = 1
	s.mu.Unlock()
	s.notify()

	sumsReq, err := http.NewRequestWithContext(uctx, http.MethodGet, relCopy.ChecksumsURL, nil)
	if err != nil {
		fail("构建校验请求失败", "Build checksum request failed", err)
		return
	}
	sumsReq.Header.Set("User-Agent", "cc-usage-monitor/"+core.AppVersion)
	sumsResp, err := client.Do(sumsReq)
	if err != nil {
		fail("下载 SHA256 校验文件失败", "Download SHA256SUMS failed", err)
		return
	}
	sumsBytes, readErr := io.ReadAll(io.LimitReader(sumsResp.Body, 1<<20))
	_ = sumsResp.Body.Close()
	if readErr != nil || sumsResp.StatusCode != http.StatusOK {
		if readErr == nil {
			readErr = fmt.Errorf("HTTP %d", sumsResp.StatusCode)
		}
		fail("读取 SHA256 校验文件失败", "Read SHA256SUMS failed", readErr)
		return
	}

	wantHash, ok := core.ParseSHA256Sums(string(sumsBytes), relCopy.AssetName)
	if !ok {
		fail("校验文件中未找到对应安装包摘要", "Checksum entry missing", fmt.Errorf("%s", relCopy.AssetName))
		return
	}
	if !strings.EqualFold(gotHash, wantHash) {
		fail("安装包 SHA256 校验不匹配", "SHA256 mismatch", fmt.Errorf("got %s want %s", gotHash, wantHash))
		return
	}

	s.mu.Lock()
	s.updateStatus = UpdateInstalling
	s.mu.Unlock()
	s.notify()

	extractedExe := filepath.Join(tmpDir, "ccmonitor.exe")
	if err := platform.ExtractExeFromZip(zipPath, extractedExe); err != nil {
		fail("解压安装包失败", "Extract update failed", err)
		return
	}

	if strings.TrimSpace(targetExe) == "" {
		exe, exeErr := os.Executable()
		if exeErr != nil || exe == "" {
			fail("定位当前程序路径失败", "Resolve executable failed", exeErr)
			return
		}
		targetExe = exe
	}
	if err := platform.ReplaceExecutableInPlace(targetExe, extractedExe); err != nil {
		fail("替换程序文件失败（请尝试「前往下载」手动替换）", "Install failed (try Open Release)", err)
		return
	}

	s.mu.Lock()
	s.updateStatus = UpdateReadyToRestart
	relaunchFn := s.onRelaunch
	s.mu.Unlock()
	s.notify()

	if relaunchFn != nil {
		relaunchFn()
	}
}

func (s *Store) downloadUpdateZip(ctx context.Context, client *http.Client, urlStr, destPath string, expectedTotal int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cc-usage-monitor/"+core.AppVersion)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	if total <= 0 {
		total = expectedTotal
	}
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	defer out.Close()

	hasher := sha256.New()
	buf := make([]byte, 32*1024)
	var downloaded int64
	lastNotify := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return "", werr
			}
			_, _ = hasher.Write(buf[:n])
			downloaded += int64(n)
			if time.Since(lastNotify) >= 100*time.Millisecond {
				lastNotify = time.Now()
				s.mu.Lock()
				s.updateDownloaded = downloaded
				s.updateTotal = total
				if total > 0 {
					s.updateProgress = min(1.0, float64(downloaded)/float64(total))
				}
				s.mu.Unlock()
				s.notify()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	s.mu.Lock()
	s.updateDownloaded = downloaded
	s.updateTotal = max(total, downloaded)
	s.updateProgress = 1.0
	s.mu.Unlock()
	s.notify()

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
