//go:build darwin

package sysproxy

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type darwinProxyEntry struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    int    `json:"port"`
}

type darwinServiceBackup struct {
	Service string           `json:"service"`
	Web     darwinProxyEntry `json:"web"`
	Secure  darwinProxyEntry `json:"secure"`
	Socks   darwinProxyEntry `json:"socks"`
}

func platformSupportInfo() (bool, string) {
	return true, ""
}

func listActiveDarwinServices() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("list macOS network services: %w", err)
	}
	var services []string
	for i, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("no active macOS network services found")
	}
	return services, nil
}

func readDarwinProxy(flag, service string) darwinProxyEntry {
	out, err := exec.Command("networksetup", flag, service).Output()
	if err != nil {
		return darwinProxyEntry{}
	}
	var entry darwinProxyEntry
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		switch k {
		case "Enabled":
			entry.Enabled = strings.EqualFold(v, "Yes")
		case "Server":
			entry.Server = v
		case "Port":
			entry.Port, _ = strconv.Atoi(v)
		}
	}
	return entry
}

func platformSnapshot() (json.RawMessage, error) {
	services, err := listActiveDarwinServices()
	if err != nil {
		return nil, err
	}
	backups := make([]darwinServiceBackup, 0, len(services))
	for _, svc := range services {
		backups = append(backups, darwinServiceBackup{
			Service: svc,
			Web:     readDarwinProxy("-getwebproxy", svc),
			Secure:  readDarwinProxy("-getsecurewebproxy", svc),
			Socks:   readDarwinProxy("-getsocksfirewallproxy", svc),
		})
	}
	return json.Marshal(backups)
}

func platformEnable(httpHost string, httpPort int, socksHost string, socksPort int) error {
	services, err := listActiveDarwinServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		if httpHost != "" && httpPort > 0 {
			p := strconv.Itoa(httpPort)
			if err := exec.Command("networksetup", "-setwebproxy", svc, httpHost, p).Run(); err != nil {
				return fmt.Errorf("set HTTP proxy on %s: %w", svc, err)
			}
			_ = exec.Command("networksetup", "-setwebproxystate", svc, "on").Run()
			if err := exec.Command("networksetup", "-setsecurewebproxy", svc, httpHost, p).Run(); err != nil {
				return fmt.Errorf("set HTTPS proxy on %s: %w", svc, err)
			}
			_ = exec.Command("networksetup", "-setsecurewebproxystate", svc, "on").Run()
		}
		if socksHost != "" && socksPort > 0 {
			p := strconv.Itoa(socksPort)
			if err := exec.Command("networksetup", "-setsocksfirewallproxy", svc, socksHost, p).Run(); err != nil {
				return fmt.Errorf("set SOCKS proxy on %s: %w", svc, err)
			}
			_ = exec.Command("networksetup", "-setsocksfirewallproxystate", svc, "on").Run()
		}
	}
	return nil
}

func platformRestore(raw json.RawMessage) error {
	var backups []darwinServiceBackup
	if err := json.Unmarshal(raw, &backups); err != nil {
		return err
	}
	for _, b := range backups {
		restoreOne := func(setFlag, stateFlag string, e darwinProxyEntry) {
			if e.Enabled && e.Server != "" && e.Port > 0 {
				_ = exec.Command("networksetup", setFlag, b.Service, e.Server, strconv.Itoa(e.Port)).Run()
				_ = exec.Command("networksetup", stateFlag, b.Service, "on").Run()
			} else {
				_ = exec.Command("networksetup", stateFlag, b.Service, "off").Run()
			}
		}
		restoreOne("-setwebproxy", "-setwebproxystate", b.Web)
		restoreOne("-setsecurewebproxy", "-setsecurewebproxystate", b.Secure)
		restoreOne("-setsocksfirewallproxy", "-setsocksfirewallproxystate", b.Socks)
	}
	return nil
}
