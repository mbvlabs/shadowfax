package server

import "testing"

func TestHeartbeatStateTriggersRestartAtThreshold(t *testing.T) {
	hb := newHeartbeatState(3)

	if restart, _ := hb.Observe(false); restart {
		t.Fatal("restart should not trigger on first failure")
	}
	if restart, _ := hb.Observe(false); restart {
		t.Fatal("restart should not trigger on second failure")
	}
	if restart, _ := hb.Observe(false); !restart {
		t.Fatal("restart should trigger on third failure")
	}
}

func TestHeartbeatStateRecoverySignal(t *testing.T) {
	hb := newHeartbeatState(3)

	hb.Observe(false)
	restart, recovered := hb.Observe(true)

	if restart {
		t.Fatal("healthy signal should not trigger restart")
	}
	if !recovered {
		t.Fatal("healthy signal after failures should report recovery")
	}
}

func TestHeartbeatStateDefaultThreshold(t *testing.T) {
	hb := newHeartbeatState(0)

	restart, _ := hb.Observe(false)
	if !restart {
		t.Fatal("zero threshold should default to immediate restart on failure")
	}
}
