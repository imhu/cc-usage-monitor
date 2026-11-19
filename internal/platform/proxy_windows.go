//go:build windows

package platform

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// SystemProxyURL returns the active Windows system proxy (or env fallback) as a URL string.
func SystemProxyURL() string {
	if p := readWindowsRegistryProxy(); p != "" {
		return p
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func readWindowsRegistryProxy() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()

	enabled, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return ""
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return ""
	}
	server = strings.TrimSpace(server)
	if !strings.Contains(server, "=") {
		return server
	}
	// Format like "http=127.0.0.1:7890;https=127.0.0.1:7890;socks=127.0.0.1:7891"
	var httpsVal, httpVal, socksVal string
	for _, part := range strings.Split(server, ";") {
		scheme, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(scheme)) {
		case "https":
			httpsVal = "http://" + strings.TrimSpace(addr)
		case "http":
			httpVal = "http://" + strings.TrimSpace(addr)
		case "socks":
			socksVal = "socks5://" + strings.TrimSpace(addr)
		}
	}
	if httpsVal != "" {
		return httpsVal
	}
	if httpVal != "" {
		return httpVal
	}
	return socksVal
}
