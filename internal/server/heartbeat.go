package server

type heartbeatState struct {
	failureThreshold int
	consecutiveFails int
}

func newHeartbeatState(failureThreshold int) *heartbeatState {
	if failureThreshold <= 0 {
		failureThreshold = 1
	}
	return &heartbeatState{
		failureThreshold: failureThreshold,
	}
}

// Observe returns:
// - restart=true when failure threshold is reached
// - recovered=true when heartbeat recovered from a prior failure streak
func (h *heartbeatState) Observe(healthy bool) (restart bool, recovered bool) {
	if healthy {
		recovered = h.consecutiveFails > 0
		h.consecutiveFails = 0
		return false, recovered
	}

	h.consecutiveFails++
	if h.consecutiveFails >= h.failureThreshold {
		h.consecutiveFails = 0
		return true, false
	}
	return false, false
}

func (h *heartbeatState) Reset() {
	h.consecutiveFails = 0
}
