package core

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const AnthropicTraceURL = "https://api.anthropic.com/cdn-cgi/trace"

type PublicNetwork struct {
	IP          string
	CountryCode string
	Region      string
}

func (p PublicNetwork) RestrictsUsage() bool {
	switch strings.ToUpper(p.CountryCode) {
	case "CN", "HK", "MO":
		return true
	default:
		return false
	}
}

func (p PublicNetwork) IsUnitedStates() bool {
	return strings.EqualFold(p.CountryCode, "US")
}

func (p PublicNetwork) IsIPv6() bool {
	return strings.Contains(p.IP, ":")
}

func (p PublicNetwork) CountryName(zh bool) string {
	code := strings.ToUpper(p.CountryCode)
	if zh {
		names := map[string]string{
			"US": "美国", "JP": "日本", "SG": "新加坡", "GB": "英国",
			"DE": "德国", "FR": "法国", "NL": "荷兰", "CA": "加拿大",
			"AU": "澳大利亚", "KR": "韩国", "TW": "中国台湾",
			"CN": "中国大陆", "HK": "中国香港", "MO": "中国澳门",
			"IE": "爱尔兰", "SE": "瑞典", "CH": "瑞士", "IN": "印度",
		}
		if n, ok := names[code]; ok {
			return n
		}
		return code
	}
	names := map[string]string{
		"US": "United States", "JP": "Japan", "SG": "Singapore", "GB": "United Kingdom",
		"DE": "Germany", "FR": "France", "NL": "Netherlands", "CA": "Canada",
		"AU": "Australia", "KR": "South Korea", "TW": "Taiwan",
		"CN": "Mainland China", "HK": "Hong Kong", "MO": "Macau",
		"IE": "Ireland", "SE": "Sweden", "CH": "Switzerland", "IN": "India",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return code
}

// MaskIP hides the last two IPv4 octets (or all but first IPv6 group) with equal-width bullets.
func MaskIP(ip string) string {
	sep := byte('.')
	keep := 2
	if strings.Contains(ip, ":") {
		sep = ':'
		keep = 1
	}
	var b strings.Builder
	groups := 0
	for i := 0; i < len(ip); i++ {
		if ip[i] == sep {
			groups++
			b.WriteByte(sep)
			continue
		}
		if groups < keep {
			b.WriteByte(ip[i])
		} else {
			b.WriteString("•")
		}
	}
	return b.String()
}

func DecodeTrace(text string) (*PublicNetwork, bool) {
	fields := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if ok && k != "" && v != "" {
			fields[k] = v
		}
	}
	ip := fields["ip"]
	loc := strings.ToUpper(fields["loc"])
	if ip == "" || len(loc) != 2 {
		return nil, false
	}
	return &PublicNetwork{
		IP:          ip,
		CountryCode: loc,
		Region:      fields["colo"],
	}, true
}

// NormalizeProxyURL normalizes user input like "127.0.0.1:7890" -> "http://127.0.0.1:7890".
func NormalizeProxyURL(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return url.Parse(s)
}

// BuildHTTPClient creates an HTTP client forcing IPv4 ("tcp4") or IPv6 ("tcp6") with optional proxy.
func BuildHTTPClient(proxyRaw string, network string, timeout time.Duration) *http.Client {
	if network == "" {
		network = "tcp4"
	}
	dialer := &net.Dialer{
		Timeout:   8 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   false,
		TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
		MaxIdleConns:        10,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 8 * time.Second,
	}

	proxyURL, err := NormalizeProxyURL(proxyRaw)
	if err == nil && proxyURL != nil {
		if strings.HasPrefix(strings.ToLower(proxyURL.Scheme), "socks5") {
			auth := (*proxy.Auth)(nil)
			if proxyURL.User != nil {
				pwd, _ := proxyURL.User.Password()
				auth = &proxy.Auth{User: proxyURL.User.Username(), Password: pwd}
			}
			if sd, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, dialer); err == nil {
				tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
					return sd.Dial(network, addr)
				}
			}
		} else {
			tr.Proxy = http.ProxyURL(proxyURL)
			tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			}
		}
	} else {
		tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		}
	}

	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
}

func ProbeTrace(ctx context.Context, proxyRaw string, network string) (*PublicNetwork, error) {
	client := BuildHTTPClient(proxyRaw, network, 6*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AnthropicTraceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "claude-usage-monitor/windows")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return nil, err
	}
	pn, ok := DecodeTrace(string(body))
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return pn, nil
}
