//go:build !darwin && !windows && !linux

package sysproxy

import "encoding/json"

func platformSupportInfo() (bool, string) {
	return false, "Automatic system proxy is not supported on this operating system."
}

func platformSnapshot() (json.RawMessage, error) {
	return nil, ErrUnsupported
}

func platformEnable(string, int, string, int) error {
	return ErrUnsupported
}

func platformRestore(json.RawMessage) error {
	return nil
}
