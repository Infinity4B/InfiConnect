//go:build !darwin

package rpc

import "errors"

// HelperStatus describes the state of the privileged Split-TUN helper daemon.
type HelperStatus struct {
	Supported   bool   `json:"supported"`
	Installed   bool   `json:"installed"`
	Running     bool   `json:"running"`
	NeedsUpdate bool   `json:"needsUpdate,omitempty"`
	Version     int    `json:"version,omitempty"`
	Message     string `json:"message,omitempty"`
}

func GetHelperStatus() HelperStatus {
	return HelperStatus{Supported: false}
}

func isHelperDaemonReady() bool {
	return false
}

func InstallPrivilegedHelper(_ string) error {
	return errors.New("privileged TUN helper daemon is only supported on macOS")
}

func UninstallPrivilegedHelper() error {
	return errors.New("privileged TUN helper daemon is only supported on macOS")
}

func RunHelperDaemon() error {
	return errors.New("privileged TUN helper daemon is only supported on macOS")
}
