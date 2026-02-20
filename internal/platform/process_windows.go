//go:build windows

package platform

import (
	"os"
	"syscall"
)

func signalStop(process *os.Process) error {
	if process == nil {
		return nil
	}
	// On Windows, signals are not supported.
	// Graceful shutdown is hard for general processes, so we kill.
	return process.Kill()
}

func isProcessAlive(process *os.Process) bool {
	if process == nil {
		return false
	}

	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.Pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var exitCode uint32
	err = syscall.GetExitCodeProcess(h, &exitCode)
	if err != nil {
		return false
	}

	return exitCode == 259 // STILL_ACTIVE
}

// terminationSignals returns the signals that should trigger a graceful shutdown.
func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
