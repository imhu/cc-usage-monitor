package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"ccmonitor/internal/core"
)

func buildTestReleaseZip(t *testing.T, payload []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("windows-" + runtime.GOARCH + "/ccmonitor.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func newTestUpdateStore(t *testing.T) *Store {
	t.Helper()
	tmp := t.TempDir()
	return &Store{
		dataDir:   tmp,
		prefsPath: filepath.Join(tmp, "prefs.json"),
		indexPath: filepath.Join(tmp, "index.gob"),
		statePath: filepath.Join(tmp, "state.json"),
		prefs:     core.DefaultPreferences(),
		index:     core.NewUsageIndex(),
		pstate:    persistedState{Budget: core.NewRequestBudget(time.Now())},
	}
}

func TestUpdateCheckETagSkipAndInstall(t *testing.T) {
	zipBytes, zipHash := buildTestReleaseZip(t, []byte("UPDATED_CCMONITOR_EXE"))
	assetName := "cc-usage-monitor-v9.9.9-windows-" + runtime.GOARCH + ".zip"
	sumsContent := zipHash + "  " + assetName + "\n"

	var apiCalls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			apiCalls.Add(1)
			if r.Header.Get("If-None-Match") == `"etag-v9"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"etag-v9"`)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name":     "v9.9.9",
				"name":         "Usage Monitor v9.9.9",
				"body":         "### Fixes\n- Faster sync",
				"html_url":     srv.URL + "/release",
				"draft":        false,
				"prerelease":   false,
				"published_at": "2026-11-25T02:00:00Z",
				"assets": []map[string]any{
					{"name": assetName, "browser_download_url": srv.URL + "/" + assetName, "size": len(zipBytes)},
					{"name": "SHA256SUMS.txt", "browser_download_url": srv.URL + "/SHA256SUMS.txt", "size": len(sumsContent)},
				},
			})
		case "/" + assetName:
			_, _ = w.Write(zipBytes)
		case "/SHA256SUMS.txt":
			_, _ = w.Write([]byte(sumsContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newTestUpdateStore(t)
	s.updateAPIURL = srv.URL + "/latest"

	// 1. First check fetches 200 OK
	s.CheckForUpdates(false)
	st := s.ViewState()
	if !st.Update.HasAvailable() || !st.Update.HasBadge() || st.Update.Release.TagName != "v9.9.9" {
		t.Fatalf("expected v9.9.9 available with badge, got %+v", st.Update)
	}

	// 2. Second check uses ETag -> 304 Not Modified
	s.CheckForUpdates(false)
	if apiCalls.Load() != 2 || s.ViewState().Update.Release.TagName != "v9.9.9" {
		t.Fatalf("expected ETag 304 check to keep cached release, calls=%d", apiCalls.Load())
	}

	// 3. Skipping version clears badge while keeping release visible
	s.SkipUpdateVersion("v9.9.9")
	st = s.ViewState()
	if !st.Update.HasAvailable() || st.Update.HasBadge() {
		t.Fatalf("expected skipped version to keep HasAvailable=true and HasBadge=false, got %+v", st.Update)
	}

	// 4. Install verifies SHA256, replaces targetExe, and calls relaunch handler
	targetExe := filepath.Join(t.TempDir(), "ccmonitor.exe")
	if err := os.WriteFile(targetExe, []byte("OLD_EXE"), 0o755); err != nil {
		t.Fatal(err)
	}
	var relaunched atomic.Bool
	s.targetExeOverride = targetExe
	s.SetRelaunchHandler(func() { relaunched.Store(true) })

	s.runInstallAvailableUpdate()
	st = s.ViewState()
	if st.Update.Status != UpdateReadyToRestart || !relaunched.Load() {
		t.Fatalf("expected UpdateReadyToRestart and relaunch called, status=%v relaunched=%v err=%q",
			st.Update.Status, relaunched.Load(), st.Update.ErrorMsg)
	}
	gotExe, err := os.ReadFile(targetExe)
	if err != nil || string(gotExe) != "UPDATED_CCMONITOR_EXE" {
		t.Fatalf("expected replaced exe bytes, got %q err=%v", string(gotExe), err)
	}
}

func TestUpdateInstallRejectsBadSHA256(t *testing.T) {
	zipBytes, _ := buildTestReleaseZip(t, []byte("TAMPERED_BYTES"))
	assetName := "cc-usage-monitor-v9.9.9-windows-" + runtime.GOARCH + ".zip"
	badSums := "0000000000000000000000000000000000000000000000000000000000000000  " + assetName + "\n"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(zipBytes)
		case "/SHA256SUMS.txt":
			_, _ = w.Write([]byte(badSums))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newTestUpdateStore(t)
	targetExe := filepath.Join(t.TempDir(), "ccmonitor.exe")
	if err := os.WriteFile(targetExe, []byte("ORIGINAL_EXE"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.targetExeOverride = targetExe
	s.SetUpdateReleaseForTest(&core.ReleaseInfo{
		TagName:      "v9.9.9",
		AssetName:    assetName,
		AssetURL:     srv.URL + "/" + assetName,
		AssetSize:    int64(len(zipBytes)),
		ChecksumsURL: srv.URL + "/SHA256SUMS.txt",
	})

	s.runInstallAvailableUpdate()
	st := s.ViewState()
	if st.Update.Status != UpdateError || st.Update.ErrorMsg == "" {
		t.Fatalf("expected UpdateError on SHA256 mismatch, got %+v", st.Update)
	}
	gotExe, _ := os.ReadFile(targetExe)
	if string(gotExe) != "ORIGINAL_EXE" {
		t.Fatalf("expected original exe untouched on SHA256 failure, got %q", string(gotExe))
	}
}
