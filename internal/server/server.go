package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/mbvlabs/shadowfax/internal/reload"
)

type AppServer struct {
	cmd                   *exec.Cmd
	buildCmd              string
	binPath               string
	appPort               string
	broadcaster           *reload.Broadcaster
	addProcess            func(*exec.Cmd)
	readyChan             chan<- struct{}
	onRebuildStateChanged func(bool)
	healthMu              sync.Mutex
	healthCancel          context.CancelFunc
}

type Config struct {
	AppPort               string
	Broadcaster           *reload.Broadcaster
	AddProcess            func(*exec.Cmd)
	ReadyChan             chan<- struct{}
	OnRebuildStateChanged func(bool)
}

func NewAppServer(cfg Config) *AppServer {
	wd, _ := os.Getwd()
	return &AppServer{
		buildCmd:              "go build -o tmp/bin/main cmd/app/main.go",
		binPath:               wd + "/tmp/bin/main",
		appPort:               cfg.AppPort,
		broadcaster:           cfg.Broadcaster,
		addProcess:            cfg.AddProcess,
		readyChan:             cfg.ReadyChan,
		onRebuildStateChanged: cfg.OnRebuildStateChanged,
	}
}

func (s *AppServer) Run(ctx context.Context, rebuildChan <-chan struct{}) error {
	// Initial build and start
	s.setRebuildState(true)
	if err := s.rebuild(ctx); err != nil {
		fmt.Printf("[shadowfax] Initial build failed: %v\n", err)
	}

	for {
		select {
		case <-ctx.Done():
			s.stop()
			s.cancelHealthMonitor()
			return nil
		case <-rebuildChan:
			s.setRebuildState(true)
			s.stop()
			if err := s.rebuild(ctx); err != nil {
				fmt.Printf("[shadowfax] Build failed: %v\n", err)
				continue
			}
		}
	}
}

func (s *AppServer) rebuild(ctx context.Context) error {
	fmt.Println("[shadowfax] Building...")

	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", "tmp/bin/main", "cmd/app/main.go")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr

	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	fmt.Println("[shadowfax] Starting server...")
	s.cmd = exec.CommandContext(ctx, s.binPath)
	s.cmd.Env = append(os.Environ(), "TEMPL_DEV_MODE=true")
	s.cmd.Stdout = os.Stdout
	s.cmd.Stderr = os.Stderr

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start failed: %w", err)
	}

	if s.addProcess != nil {
		s.addProcess(s.cmd)
	}

	s.startHealthMonitor(ctx)

	return nil
}

func (s *AppServer) stop() {
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
