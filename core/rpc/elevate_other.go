//go:build !darwin

package rpc

import (
	"errors"
	"os"
	"runtime"
)

func isElevatedProcess() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return os.Geteuid() == 0
}

func (e *SubprocessEngine) spawnElevatedWorker(_ string) (*workerProc, error) {
	return nil, errors.New("TUN mode requires administrator/root privileges; please launch InfiConnect with elevated privileges or disable TUN mode")
}
