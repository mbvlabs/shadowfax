package platform

import "os"

// SignalStop attempts to gracefully stop a process.
func SignalStop(process *os.Process) error {
	return signalStop(process)
}

// IsProcessAlive checks if a process is still running.
func IsProcessAlive(process *os.Process) bool {
	return isProcessAlive(process)
}

// TerminationSignals returns the signals that should trigger a graceful shutdown.
func TerminationSignals() []os.Signal {
	return terminationSignals()
}
