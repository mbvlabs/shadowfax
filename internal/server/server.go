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
	templDir              string
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
	runBuild              func(context.Context, string) error
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
	templDir := wd + "/tmp/templ"
	os.MkdirAll(binDir, 0755)
	os.MkdirAll(templDir, 0755)
	return &AppServer{
		buildCmd:              "go build -o tmp/bin/main cmd/app/main.go",
		binDir:                binDir,
		templDir:              templDir,
		appPort:               cfg.AppPort,
		broadcaster:           cfg.Broadcaster,
		addProcess:            cfg.AddProcess,
		readyChan:             cfg.ReadyChan,
		onRebuildStateChanged: cfg.OnRebuildStateChanged,
		stateTracker:          cfg.StateTracker,
		clearLogs:             cfg.ClearLogs,
		buildRunner:           ctxrun.New(),
		runBuild:              runGoBuild,
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
	candidateBinPath := s.makeBinaryPath()

	if s.clearLogs != nil {
		s.clearLogs()
	}

	fmt.Println("[shadowfax] Building...")

	runBuild := s.runBuild
	if runBuild == nil {
		runBuild = runGoBuild
	}

	if err := runBuild(buildCtx, candidateBinPath); err != nil {
		os.Remove(candidateBinPath)
		if s.stateTracker != nil {
			s.stateTracker.SetError(state.IndexGoBuild, err.Error())
		}
		return fmt.Errorf("build failed: %w", err)
	}

	if buildCtx.Err() != nil {
		os.Remove(candidateBinPath)
		return buildCtx.Err()
	}

	if s.stateTracker != nil {
		s.stateTracker.SetError(state.IndexGoBuild, "")
	}

	s.cmdMu.Lock()

	if buildCtx.Err() != nil {
		os.Remove(candidateBinPath)
		s.cmdMu.Unlock()
		return buildCtx.Err()
	}

	previousBinPath := s.binPath
	s.stopLocked()

	fmt.Println("[shadowfax] Starting server...")
	cmd := exec.CommandContext(appCtx, candidateBinPath)
	cmd.Env = append(os.Environ(), "TEMPL_DEV_MODE=true", "TMPDIR="+s.templDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		os.Remove(candidateBinPath)
		s.cmdMu.Unlock()
		return fmt.Errorf("start failed: %w", err)
	}

	s.cmd = cmd
	s.binPath = candidateBinPath
	s.startHealthMonitor(appCtx, previousBinPath)
	s.cmdMu.Unlock()

	if s.addProcess != nil {
		s.addProcess(cmd)
	}

	return nil
}

func runGoBuild(ctx context.Context, outputPath string) error {
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", outputPath, "cmd/app/main.go")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	return buildCmd.Run()
}

func (s *AppServer) stop() {
	s.cancelHealthMonitor()
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	s.stopLocked()
}

func (s *AppServer) stopLocked() {
	s.cancelHealthMonitor()
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
	s.cmd = nil
}

func (s *AppServer) startHealthMonitor(ctx context.Context, previousBinPath string) {
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
		if previousBinPath != "" {
			os.Remove(previousBinPath)
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
