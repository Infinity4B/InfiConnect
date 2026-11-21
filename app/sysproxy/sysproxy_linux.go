//go:build linux

package sysproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type gnomeProxyBackup struct {
	Mode      string `json:"mode"`
	HTTPHost  string `json:"httpHost"`
	HTTPPort  string `json:"httpPort"`
	HTTPSHost string `json:"httpsHost"`
	HTTPSPort string `json:"httpsPort"`
	SocksHost string `json:"socksHost"`
	SocksPort string `json:"socksPort"`
}

func isGNOMEAvailable() bool {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return false
	}
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + " " + os.Getenv("DESKTOP_SESSION"))
	return strings.Contains(desktop, "gnome") || strings.Contains(desktop, "ubuntu") || strings.Contains(desktop, "unity") || strings.Contains(desktop, "cinnamon")
}

func platformSupportInfo() (bool, string) {
	if isGNOMEAvailable() {
		return true, ""
	}
	return false, "Automatic system proxy is currently supported on GNOME desktops; please configure HTTP/SOCKS5 proxy manually in your desktop environment settings."
}

func gsettingsGet(schema, key string) string {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(out)), "'")
}

func gsettingsSet(schema, key, value string) error {
	return exec.Command("gsettings", "set", schema, key, value).Run()
}

func platformSnapshot() (json.RawMessage, error) {
	if !isGNOMEAvailable() {
		_, msg := platformSupportInfo()
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, msg)
	}
	b := gnomeProxyBackup{
		Mode:      gsettingsGet("org.gnome.system.proxy", "mode"),
		HTTPHost:  gsettingsGet("org.gnome.system.proxy.http", "host"),
		HTTPPort:  gsettingsGet("org.gnome.system.proxy.http", "port"),
		HTTPSHost: gsettingsGet("org.gnome.system.proxy.https", "host"),
		HTTPSPort: gsettingsGet("org.gnome.system.proxy.https", "port"),
		SocksHost: gsettingsGet("org.gnome.system.proxy.socks", "host"),
		SocksPort: gsettingsGet("org.gnome.system.proxy.socks", "port"),
	}
	return json.Marshal(b)
}

func platformEnable(httpHost string, httpPort int, socksHost string, socksPort int) error {
	if !isGNOMEAvailable() {
		_, msg := platformSupportInfo()
		return fmt.Errorf("%w: %s", ErrUnsupported, msg)
	}
	if httpHost != "" && httpPort > 0 {
		p := strconv.Itoa(httpPort)
		if err := gsettingsSet("org.gnome.system.proxy.http", "host", httpHost); err != nil {
			return err
		}
		if err := gsettingsSet("org.gnome.system.proxy.http", "port", p); err != nil {
			return err
		}
		_ = gsettingsSet("org.gnome.system.proxy.https", "host", httpHost)
		_ = gsettingsSet("org.gnome.system.proxy.https", "port", p)
	}
	if socksHost != "" && socksPort > 0 {
		_ = gsettingsSet("org.gnome.system.proxy.socks", "host", socksHost)
		_ = gsettingsSet("org.gnome.system.proxy.socks", "port", strconv.Itoa(socksPort))
	}
	return gsettingsSet("org.gnome.system.proxy", "mode", "manual")
}

func platformRestore(raw json.RawMessage) error {
	if !isGNOMEAvailable() {
		return nil
	}
	var b gnomeProxyBackup
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	mode := b.Mode
	if mode == "" {
		mode = "none"
	}
	_ = gsettingsSet("org.gnome.system.proxy", "mode", mode)
	if b.HTTPHost != "" {
		_ = gsettingsSet("org.gnome.system.proxy.http", "host", b.HTTPHost)
	}
	if b.HTTPPort != "" {
		_ = gsettingsSet("org.gnome.system.proxy.http", "port", b.HTTPPort)
	}
	if b.HTTPSHost != "" {
		_ = gsettingsSet("org.gnome.system.proxy.https", "host", b.HTTPSHost)
	}
	if b.HTTPSPort != "" {
		_ = gsettingsSet("org.gnome.system.proxy.https", "port", b.HTTPSPort)
	}
	if b.SocksHost != "" {
		_ = gsettingsSet("org.gnome.system.proxy.socks", "host", b.SocksHost)
	}
	if b.SocksPort != "" {
		_ = gsettingsSet("org.gnome.system.proxy.socks", "port", b.SocksPort)
	}
	return nil
}
