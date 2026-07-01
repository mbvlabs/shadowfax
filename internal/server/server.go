package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mbvlabs/shadowfax/internal/ctxrun"
	"github.com/mbvlabs/shadowfax/internal/reload"
	"github.com/mbvlabs/shadowfax/internal/state"
)

type AppServer struct {
	cmd                   *exec.Cmd
	buildCmd              string
	binPath               string
	binDir                string
	prevBinPath           string
	appPort               string
	broadcaster           *reload.Broadcaster
	addProcess            func(*exec.Cmd)
	readyChan             chan<- struct{}
	onRebuildStateChanged func(bool)
	stateTracker          *state.Tracker
	clearLogs             func()
	healthMu              sync.Mutex
	healthCancel          context.CancelFunc
	buildRunner           *ctxrun.Runner
	cmdMu                 sync.Mutex
}

type Config struct {
	AppPort               string
	Broadcaster           *reload.Broadcaster
	AddProcess            func(*exec.Cmd)
	ReadyChan             chan<- struct{}
	OnRebuildStateChanged func(bool)
	StateTracker          *state.Tracker
	ClearLogs             func()
}

func (s *AppServer) makeBinaryPath() string {
	return filepath.Join(s.binDir, "server_"+strconv.FormatInt(time.Now().UnixNano(), 16))
}

func NewAppServer(cfg Config) *AppServer {
	wd, _ := os.Getwd()
	binDir := wd + "/tmp/bin"
	return &AppServer{
		buildCmd:              "go build -o tmp/bin/main cmd/app/main.go",
		binPath:               filepath.Join(binDir, "server_"+strconv.FormatInt(time.Now().UnixNano(), 16)),
		binDir:                binDir,
		appPort:               cfg.AppPort,
		broadcaster:           cfg.Broadcaster,
		addProcess:            cfg.AddProcess,
		readyChan:             cfg.ReadyChan,
		onRebuildStateChanged: cfg.OnRebuildStateChanged,
		stateTracker:          cfg.StateTracker,
		clearLogs:             cfg.ClearLogs,
		buildRunner:           ctxrun.New(),
	}
}

func (s *AppServer) Run(ctx context.Context, rebuildChan <-chan struct{}) error {
	s.setRebuildState(true)
	s.buildRunner.Go(ctx, func(buildCtx context.Context) {
		if err := s.rebuild(buildCtx, ctx); err != nil {
			fmt.Printf("[shadowfax] Initial build failed: %v\n", err)
			s.setRebuildState(false)
		}
	})

	for {
		select {
		case <-ctx.Done():
			s.cancelHealthMonitor()
			return nil
		case <-rebuildChan:
			s.setRebuildState(true)
			s.buildRunner.Go(ctx, func(buildCtx context.Context) {
				if err := s.rebuild(buildCtx, ctx); err != nil {
					fmt.Printf("[shadowfax] Build failed: %v\n", err)
					s.setRebuildState(false)
				}
			})
		}
	}
}

func (s *AppServer) rebuild(buildCtx context.Context, appCtx context.Context) error {
	s.prevBinPath = s.binPath
	s.binPath = s.makeBinaryPath()

	if s.clearLogs != nil {
		s.clearLogs()
	}

	fmt.Println("[shadowfax] Building...")

	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", s.binPath, "cmd/app/main.go")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr

	if err := buildCmd.Run(); err != nil {
		os.Remove(s.binPath)
		s.binPath = s.prevBinPath
		s.prevBinPath = ""
		if s.stateTracker != nil {
			s.stateTracker.SetError(state.IndexGoBuild, err.Error())
		}
		return fmt.Errorf("build failed: %w", err)
	}

	if buildCtx.Err() != nil {
		os.Remove(s.binPath)
		s.binPath = s.prevBinPath
		s.prevBinPath = ""
		return buildCtx.Err()
	}

	if s.stateTracker != nil {
		s.stateTracker.SetError(state.IndexGoBuild, "")
	}

	s.stop()

	fmt.Println("[shadowfax] Starting server...")
	s.cmdMu.Lock()
	s.cmd = exec.CommandContext(appCtx, s.binPath)
	s.cmd.Env = append(os.Environ(), "TEMPL_DEV_MODE=true")
	s.cmd.Stdout = os.Stdout
	s.cmd.Stderr = os.Stderr

	if err := s.cmd.Start(); err != nil {
		s.cmdMu.Unlock()
		return fmt.Errorf("start failed: %w", err)
	}
	s.cmdMu.Unlock()

	if s.addProcess != nil {
		s.addProcess(s.cmd)
	}

	s.startHealthMonitor(appCtx)

	return nil
}

func (s *AppServer) stop() {
	s.cancelHealthMonitor()
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			s.cmd.Process.Kill()
		}
	}
}

func (s *AppServer) startHealthMonitor(ctx context.Context) {
	s.cancelHealthMonitor()
	healthCtx, cancel := context.WithCancel(ctx)
	s.healthMu.Lock()
	s.healthCancel = cancel
	s.healthMu.Unlock()

	go func() {
		healthURL := fmt.Sprintf("http://localhost:%s/", s.appPort)
		reload.BroadcastWhenHealthy(healthCtx, healthURL, s.broadcaster)
		if healthCtx.Err() != nil {
			return
		}
		if s.prevBinPath != "" {
			os.Remove(s.prevBinPath)
			s.prevBinPath = ""
		}
		s.setRebuildState(false)
		if s.readyChan != nil {
			select {
			case s.readyChan <- struct{}{}:
			default:
			}
		}
	}()
}

func (s *AppServer) cancelHealthMonitor() {
	s.healthMu.Lock()
	cancel := s.healthCancel
	s.healthCancel = nil
	s.healthMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *AppServer) setRebuildState(inProgress bool) {
	if s.onRebuildStateChanged != nil {
		s.onRebuildStateChanged(inProgress)
	}
}
