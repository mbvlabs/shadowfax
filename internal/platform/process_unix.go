//go:build !windows

package platform

import (
	"errors"
	"os"
	"syscall"
)

func signalStop(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Signal(syscall.SIGTERM)
}

func isProcessAlive(process *os.Process) bool {
	if process == nil {
		return false
	}

	err := process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
