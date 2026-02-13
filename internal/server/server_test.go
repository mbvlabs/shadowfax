package server

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mbvlabs/shadowfax/internal/reload"
)

func TestSetRebuildStateInvokesCallback(t *testing.T) {
	var got atomic.Bool
	s := &AppServer{
		onRebuildStateChanged: func(inProgress bool) {
			got.Store(inProgress)
		},
	}

	s.setRebuildState(true)

	if !got.Load() {
		t.Fatal("expected onRebuildStateChanged callback to receive true")
	}
}

func TestStartHealthMonitorSignalsReadyAndClearsRebuildState(t *testing.T) {
	port, closeServer := startHealthyServer(t)
	defer closeServer()

	readyChan := make(chan struct{}, 1)
	rebuildState := make(chan bool, 2)

	s := &AppServer{
		appPort:     port,
		broadcaster: reload.NewBroadcaster(),
		readyChan:   readyChan,
		onRebuildStateChanged: func(inProgress bool) {
			rebuildState <- inProgress
		},
	}

	s.setRebuildState(true)
	select {
	case got := <-rebuildState:
		if !got {
			t.Fatal("expected initial rebuild state to be true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial rebuild state callback")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startHealthMonitor(ctx)
	defer s.cancelHealthMonitor()

	select {
	case <-readyChan:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for ready signal from health monitor")
	}

	select {
	case got := <-rebuildState:
		if got {
			t.Fatal("expected rebuild state to be cleared (false) after health check")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for rebuild state clear callback")
	}
}

func TestCancelHealthMonitorPreventsReadySignal(t *testing.T) {
	unhealthyPort := getUnusedPort(t)
	readyChan := make(chan struct{}, 1)
	rebuildState := make(chan bool, 1)

	s := &AppServer{
		appPort:     unhealthyPort,
		broadcaster: reload.NewBroadcaster(),
		readyChan:   readyChan,
		onRebuildStateChanged: func(inProgress bool) {
			rebuildState <- inProgress
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startHealthMonitor(ctx)
	s.cancelHealthMonitor()

	time.Sleep(250 * time.Millisecond)

	select {
	case <-readyChan:
		t.Fatal("did not expect ready signal after canceling health monitor")
	default:
	}

	select {
	case got := <-rebuildState:
		t.Fatalf("did not expect rebuild state callback after cancel, got %v", got)
	default:
	}
}

func startHealthyServer(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to open listener: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: handler}
	go func() {
		_ = srv.Serve(ln)
	}()

	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	closeFn := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}

	return port, closeFn
}

func getUnusedPort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to reserve port: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()
	return port
}
