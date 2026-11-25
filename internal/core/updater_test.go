package core

import (
	"testing"
)

func TestSemverAndIsNewerVersion(t *testing.T) {
	if maj, minV, pat, ok := ParseSemver("v1.10.2"); !ok || maj != 1 || minV != 10 || pat != 2 {
		t.Fatalf("unexpected ParseSemver: %d.%d.%d ok=%v", maj, minV, pat, ok)
	}
	if _, _, _, ok := ParseSemver("dev"); ok {
		t.Fatal("expected dev not to parse as semver")
	}

	if !IsNewerVersion("dev", "v1.0.2") {
		t.Fatal("expected dev build to treat v1.0.2 as newer")
	}
	if !IsNewerVersion("", "v1.0.0") {
		t.Fatal("expected empty version to treat v1.0.0 as newer")
	}
	if !IsNewerVersion("v1.0.2", "v1.0.10") {
		t.Fatal("expected v1.0.10 > v1.0.2")
	}
	if !IsNewerVersion("v1.0.2", "v1.1.0") {
		t.Fatal("expected v1.1.0 > v1.0.2")
	}
	if IsNewerVersion("v1.0.2", "v1.0.2") {
		t.Fatal("expected equal versions not to be newer")
	}
	if IsNewerVersion("v1.2.0", "v1.0.9") {
		t.Fatal("expected older version not to be newer")
	}
}

func TestParseGitHubLatestReleaseAndSHA256Sums(t *testing.T) {
	payload := `{
		"tag_name": "v1.0.3",
		"name": "Usage Monitor v1.0.3",
		"body": "### What's Changed\n- Fix **bug** in [updater](https://example.com) using ` + "`SHA256`" + `\n",
		"html_url": "https://github.com/imhu/cc-usage-monitor/releases/tag/v1.0.3",
		"draft": false,
		"prerelease": false,
		"published_at": "2026-11-25T02:00:00Z",
		"assets": [
			{"name": "cc-usage-monitor-v1.0.3-windows-amd64.zip", "browser_download_url": "https://example.com/amd64.zip", "size": 12345},
			{"name": "cc-usage-monitor-v1.0.3-windows-arm64.zip", "browser_download_url": "https://example.com/arm64.zip", "size": 12300},
			{"name": "SHA256SUMS.txt", "browser_download_url": "https://example.com/SHA256SUMS.txt", "size": 256}
		]
	}`

	rel, err := ParseGitHubLatestRelease([]byte(payload), "amd64")
	if err != nil {
		t.Fatalf("ParseGitHubLatestRelease failed: %v", err)
	}
	if rel.TagName != "v1.0.3" || rel.AssetName != "cc-usage-monitor-v1.0.3-windows-amd64.zip" || rel.ChecksumsURL == "" {
		t.Fatalf("unexpected ReleaseInfo: %+v", rel)
	}

	relArm, err := ParseGitHubLatestRelease([]byte(payload), "arm64")
	if err != nil || relArm.AssetName != "cc-usage-monitor-v1.0.3-windows-arm64.zip" {
		t.Fatalf("expected arm64 asset, got %+v err=%v", relArm, err)
	}

	draftPayload := `{"tag_name":"v1.0.4","draft":true,"prerelease":false}`
	if _, err := ParseGitHubLatestRelease([]byte(draftPayload), "amd64"); err == nil {
		t.Fatal("expected draft release to be rejected")
	}

	sums := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  cc-usage-monitor-v1.0.3-windows-amd64.zip\n" +
		"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB *cc-usage-monitor-v1.0.3-windows-arm64.zip\n"
	h1, ok1 := ParseSHA256Sums(sums, "cc-usage-monitor-v1.0.3-windows-amd64.zip")
	if !ok1 || h1 != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected amd64 checksum: %q ok=%v", h1, ok1)
	}
	h2, ok2 := ParseSHA256Sums(sums, "cc-usage-monitor-v1.0.3-windows-arm64.zip")
	if !ok2 || h2 != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("unexpected arm64 checksum: %q ok=%v", h2, ok2)
	}
	if _, ok3 := ParseSHA256Sums(sums, "missing.zip"); ok3 {
		t.Fatal("expected missing filename to return false")
	}

	lines := ParseReleaseNotesMarkdown(rel.Body)
	if len(lines) != 2 || lines[0].Kind != MDHeading || lines[0].Text != "What's Changed" ||
		lines[1].Kind != MDBullet || lines[1].Text != "Fix bug in updater using SHA256" {
		t.Fatalf("unexpected parsed markdown lines: %+v", lines)
	}
}
