//go:build windows

package sysproxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	internetSettingsKeyPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	internetOptionSettings  = 39
	internetOptionRefresh   = 37
)

type windowsProxyBackup struct {
	ProxyEnable   uint32 `json:"proxyEnable"`
	HasEnable     bool   `json:"hasEnable"`
	ProxyServer   string `json:"proxyServer"`
	HasServer     bool   `json:"hasServer"`
	ProxyOverride string `json:"proxyOverride"`
	HasOverride   bool   `json:"hasOverride"`
}

func platformSupportInfo() (bool, string) {
	return true, ""
}

func platformSnapshot() (json.RawMessage, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open Windows Internet Settings registry key: %w", err)
	}
	defer k.Close()

	var b windowsProxyBackup
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		b.ProxyEnable = uint32(v)
		b.HasEnable = true
	}
	if v, _, err := k.GetStringValue("ProxyServer"); err == nil {
		b.ProxyServer = v
		b.HasServer = true
	}
	if v, _, err := k.GetStringValue("ProxyOverride"); err == nil {
		b.ProxyOverride = v
		b.HasOverride = true
	}
	return json.Marshal(b)
}

func platformEnable(httpHost string, httpPort int, socksHost string, socksPort int) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Windows Internet Settings registry key for write: %w", err)
	}
	defer k.Close()

	var parts []string
	if httpHost != "" && httpPort > 0 {
		parts = append(parts, fmt.Sprintf("http=%s:%d", httpHost, httpPort))
		parts = append(parts, fmt.Sprintf("https=%s:%d", httpHost, httpPort))
	}
	if socksHost != "" && socksPort > 0 {
		parts = append(parts, fmt.Sprintf("socks=%s:%d", socksHost, socksPort))
	}
	serverVal := strings.Join(parts, ";")
	if httpHost != "" && httpPort > 0 && socksHost == "" {
		serverVal = fmt.Sprintf("%s:%d", httpHost, httpPort)
	}

	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyServer", serverVal); err != nil {
		return err
	}
	// Only bypass loopback addresses; do NOT bypass 10.*/172.16.*/192.168.*/<local>
	// because corporate/campus VPN resources typically reside on private subnets and intranet hostnames.
	_ = k.SetStringValue("ProxyOverride", "localhost;127.*;[::1]")
	notifyWinInetRefresh()
	return nil
}

func platformRestore(raw json.RawMessage) error {
	var b windowsProxyBackup
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if b.HasEnable {
		_ = k.SetDWordValue("ProxyEnable", b.ProxyEnable)
	} else {
		_ = k.SetDWordValue("ProxyEnable", 0)
	}
	if b.HasServer {
		_ = k.SetStringValue("ProxyServer", b.ProxyServer)
	} else {
		_ = k.DeleteValue("ProxyServer")
	}
	if b.HasOverride {
		_ = k.SetStringValue("ProxyOverride", b.ProxyOverride)
	}
	notifyWinInetRefresh()
	return nil
}

func notifyWinInetRefresh() {
	wininet := syscall.NewLazyDLL("wininet.dll")
	proc := wininet.NewProc("InternetSetOptionW")
	_, _, _ = proc.Call(0, internetOptionSettings, 0, 0)
	_, _, _ = proc.Call(0, internetOptionRefresh, 0, 0)
}
