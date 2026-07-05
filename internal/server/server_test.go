package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mbvlabs/shadowfax/internal/reload"
)

func TestClearLogsWiredFromConfig(t *testing.T) {
	var called bool
	cfg := Config{
		ClearLogs: func() { called = true },
	}
	s := NewAppServer(cfg)
	if s.clearLogs == nil {
		t.Fatal("expected clearLogs callback to be set")
	}
	s.clearLogs()
	if !called {
		t.Fatal("expected clearLogs callback to be invoked")
	}
}

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
	s.startHealthMonitor(ctx, "")
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
	s.startHealthMonitor(ctx, "")
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

func TestCanceledRebuildOnlyRemovesItsCandidateBinary(t *testing.T) {
	tmpDir := t.TempDir()

	oldActiveBin := filepath.Join(tmpDir, "server_old_active")
	if err := os.WriteFile(oldActiveBin, []byte("old active"), 0644); err != nil {
		t.Fatalf("failed to write old active binary: %v", err)
	}

	s := &AppServer{
		binDir:   tmpDir,
		templDir: filepath.Join(tmpDir, "templ"),
		binPath:  oldActiveBin,
	}

	buildCtx, cancelBuild := context.WithCancel(context.Background())
	defer cancelBuild()
	newActiveBin := filepath.Join(tmpDir, "server_new_active")

	s.runBuild = func(ctx context.Context, candidateBinPath string) error {
		if err := os.WriteFile(candidateBinPath, []byte("candidate"), 0644); err != nil {
			t.Fatalf("failed to write candidate binary: %v", err)
		}
		if err := os.WriteFile(newActiveBin, []byte("new active"), 0644); err != nil {
			t.Fatalf("failed to write new active binary: %v", err)
		}
		s.binPath = newActiveBin
		cancelBuild()
		return ctx.Err()
	}

	if err := s.rebuild(buildCtx, context.Background()); err == nil {
		t.Fatal("expected canceled rebuild to return an error")
	}

	if _, err := os.Stat(newActiveBin); err != nil {
		t.Fatalf("expected active binary to survive canceled rebuild cleanup: %v", err)
	}

	candidates, err := filepath.Glob(filepath.Join(tmpDir, "server_*"))
	if err != nil {
		t.Fatalf("failed to glob candidate binaries: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected only active binaries to remain, got %v", candidates)
	}
}

func TestStartHealthMonitorRemovesCapturedPreviousBinaryOnly(t *testing.T) {
	port, closeServer := startHealthyServer(t)
	defer closeServer()

	tmpDir := t.TempDir()
	previousBin := filepath.Join(tmpDir, "server_previous")
	currentBin := filepath.Join(tmpDir, "server_current")
	if err := os.WriteFile(previousBin, []byte("previous"), 0644); err != nil {
		t.Fatalf("failed to write previous binary: %v", err)
	}
	if err := os.WriteFile(currentBin, []byte("current"), 0644); err != nil {
		t.Fatalf("failed to write current binary: %v", err)
	}

	readyChan := make(chan struct{}, 1)
	s := &AppServer{
		appPort:     port,
		broadcaster: reload.NewBroadcaster(),
		readyChan:   readyChan,
		binPath:     currentBin,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startHealthMonitor(ctx, previousBin)
	defer s.cancelHealthMonitor()

	select {
	case <-readyChan:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for ready signal from health monitor")
	}

	if _, err := os.Stat(previousBin); !os.IsNotExist(err) {
		t.Fatalf("expected previous binary to be removed, stat err: %v", err)
	}
	if _, err := os.Stat(currentBin); err != nil {
		t.Fatalf("expected current binary to remain: %v", err)
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
