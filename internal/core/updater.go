package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AppVersion is injected at build time via -ldflags "-X ccmonitor/internal/core.AppVersion=v1.x.y".
var AppVersion = "dev"

const (
	UpdateRepo          = "imhu/cc-usage-monitor"
	UpdateCheckInterval = 6 * time.Hour
	UpdateAPIURL        = "https://api.github.com/repos/imhu/cc-usage-monitor/releases/latest"
	UpdateReleasesURL   = "https://github.com/imhu/cc-usage-monitor/releases"
)

// ReleaseInfo describes an available GitHub release and its matching Windows archive.
type ReleaseInfo struct {
	TagName      string     `json:"tagName"`
	Name         string     `json:"name"`
	Body         string     `json:"body"`
	HTMLURL      string     `json:"htmlUrl"`
	PublishedAt  *time.Time `json:"publishedAt,omitempty"`
	AssetName    string     `json:"assetName"`
	AssetURL     string     `json:"assetUrl"`
	AssetSize    int64      `json:"assetSize"`
	ChecksumsURL string     `json:"checksumsUrl"`
}

// ParseSemver parses "v1.2.3", "1.2.3", or "1.2" (ignoring pre-release/build suffixes) into numeric components.
func ParseSemver(v string) (major, minor, patch int, ok bool) {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if s == "" {
		return 0, 0, 0, false
	}
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	minV, err2 := strconv.Atoi(parts[1])
	pat := 0
	var err3 error
	if len(parts) == 3 {
		pat, err3 = strconv.Atoi(parts[2])
	}
	if err1 != nil || err2 != nil || err3 != nil || maj < 0 || minV < 0 || pat < 0 {
		return 0, 0, 0, false
	}
	return maj, minV, pat, true
}

// IsNewerVersion reports whether latest is newer than current.
// Development builds ("dev" or empty) treat any valid semver release as newer so update detection can be tested.
func IsNewerVersion(current, latest string) bool {
	lMaj, lMin, lPat, ok := ParseSemver(latest)
	if !ok {
		return false
	}
	cur := strings.TrimSpace(current)
	if cur == "" || strings.EqualFold(cur, "dev") {
		return true
	}
	cMaj, cMin, cPat, ok := ParseSemver(cur)
	if !ok {
		return true
	}
	if lMaj != cMaj {
		return lMaj > cMaj
	}
	if lMin != cMin {
		return lMin > cMin
	}
	return lPat > cPat
}

// ParseGitHubLatestRelease decodes a GitHub `/releases/latest` JSON payload and selects
// the Windows zip asset matching goarch along with SHA256SUMS.txt.
func ParseGitHubLatestRelease(data []byte, goarch string) (*ReleaseInfo, error) {
	var raw struct {
		TagName     string     `json:"tag_name"`
		Name        string     `json:"name"`
		Body        string     `json:"body"`
		HTMLURL     string     `json:"html_url"`
		Draft       bool       `json:"draft"`
		Prerelease  bool       `json:"prerelease"`
		PublishedAt *time.Time `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse release json: %w", err)
	}
	if raw.Draft || raw.Prerelease {
		return nil, fmt.Errorf("release %q is draft or prerelease", raw.TagName)
	}
	if _, _, _, ok := ParseSemver(raw.TagName); !ok {
		return nil, fmt.Errorf("invalid release tag %q", raw.TagName)
	}
	suffix := strings.ToLower("-windows-" + strings.TrimSpace(goarch) + ".zip")
	rel := &ReleaseInfo{
		TagName:     raw.TagName,
		Name:        raw.Name,
		Body:        raw.Body,
		HTMLURL:     raw.HTMLURL,
		PublishedAt: raw.PublishedAt,
	}
	if rel.HTMLURL == "" {
		rel.HTMLURL = UpdateReleasesURL + "/tag/" + rel.TagName
	}
	for _, a := range raw.Assets {
		lower := strings.ToLower(strings.TrimSpace(a.Name))
		if strings.HasSuffix(lower, suffix) && rel.AssetURL == "" {
			rel.AssetName = a.Name
			rel.AssetURL = a.BrowserDownloadURL
			rel.AssetSize = a.Size
		} else if lower == "sha256sums.txt" && rel.ChecksumsURL == "" {
			rel.ChecksumsURL = a.BrowserDownloadURL
		}
	}
	return rel, nil
}

// ParseSHA256Sums extracts the lowercase 64-char hex digest for filename from a SHA256SUMS.txt file.
func ParseSHA256Sums(content, filename string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(filename))
	if want == "" {
		return "", false
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := strings.ToLower(fields[0])
		if len(hash) != 64 || !isHexString(hash) {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		name = strings.TrimPrefix(name, "./")
		if strings.EqualFold(name, want) {
			return hash, true
		}
	}
	return "", false
}

func isHexString(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return len(s) > 0
}

type MarkdownLineKind int

const (
	MDParagraph MarkdownLineKind = iota
	MDHeading
	MDBullet
)

type MarkdownLine struct {
	Kind MarkdownLineKind
	Text string
}

var mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)

// ParseReleaseNotesMarkdown simplifies GitHub Release Markdown into plain headings, bullets, and paragraphs.
func ParseReleaseNotesMarkdown(body string) []MarkdownLine {
	var out []MarkdownLine
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line == "---" || strings.HasPrefix(line, "```") {
			continue
		}
		kind := MDParagraph
		switch {
		case strings.HasPrefix(line, "#"):
			kind = MDHeading
			line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			kind = MDBullet
			line = strings.TrimSpace(line[2:])
		}
		line = mdLinkRe.ReplaceAllString(line, "$1")
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "__", "")
		line = strings.ReplaceAll(line, "`", "")
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, MarkdownLine{Kind: kind, Text: line})
		}
	}
	return out
}
