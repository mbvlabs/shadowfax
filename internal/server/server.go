package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/mbvlabs/shadowfax/internal/reload"
)

type AppServer struct {
	cmd         *exec.Cmd
	buildCmd    string
	binPath     string
	appPort     string
	healthURL   string
	broadcaster *reload.Broadcaster
	addProcess  func(*exec.Cmd)
	readyChan   chan<- struct{}
	heartbeat   heartbeatConfig
}

type Config struct {
	AppPort     string
	Broadcaster *reload.Broadcaster
	AddProcess  func(*exec.Cmd)
	ReadyChan   chan<- struct{}
}

type heartbeatConfig struct {
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold int
	StartupGrace     time.Duration
}

func defaultHeartbeatConfig() heartbeatConfig {
	return heartbeatConfig{
		Interval:         3 * time.Second,
		Timeout:          700 * time.Millisecond,
		FailureThreshold: 3,
		StartupGrace:     4 * time.Second,
	}
}

func NewAppServer(cfg Config) *AppServer {
	wd, _ := os.Getwd()
	return &AppServer{
		buildCmd:    "go build -o tmp/bin/main cmd/app/main.go",
		binPath:     wd + "/tmp/bin/main",
		appPort:     cfg.AppPort,
		healthURL:   fmt.Sprintf("http://localhost:%s/", cfg.AppPort),
		broadcaster: cfg.Broadcaster,
		addProcess:  cfg.AddProcess,
		readyChan:   cfg.ReadyChan,
		heartbeat:   defaultHeartbeatConfig(),
	}
}

func (s *AppServer) Run(ctx context.Context, rebuildChan <-chan struct{}) error {
	hb := newHeartbeatState(s.heartbeat.FailureThreshold)
	lastStartedAt := time.Time{}

	// Initial build and start
	if err := s.rebuild(ctx); err != nil {
		fmt.Printf("[shadowfax] Initial build failed: %v\n", err)
	} else {
		lastStartedAt = time.Now()
	}

	ticker := time.NewTicker(s.heartbeat.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.stop()
			return nil
		case <-rebuildChan:
			s.stop()
			if err := s.rebuild(ctx); err != nil {
				fmt.Printf("[shadowfax] Build failed: %v\n", err)
				continue
			}
			lastStartedAt = time.Now()
			hb.Reset()
		case <-ticker.C:
			if s.cmd == nil || s.cmd.Process == nil {
				continue
			}
			if !lastStartedAt.IsZero() && time.Since(lastStartedAt) < s.heartbeat.StartupGrace {
				continue
			}

			healthy := s.isHealthy(ctx)
			restart, recovered := hb.Observe(healthy)
			if recovered {
				fmt.Println("[shadowfax] Heartbeat recovered")
			}
			if !restart {
				continue
			}

			fmt.Printf(
				"[shadowfax] Heartbeat failed %d consecutive checks, restarting app server...\n",
				s.heartbeat.FailureThreshold,
			)
			s.stop()
			if err := s.rebuild(ctx); err != nil {
				fmt.Printf("[shadowfax] Build failed during heartbeat recovery: %v\n", err)
				continue
			}
			lastStartedAt = time.Now()
			hb.Reset()
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

	// Wait for healthy, then broadcast
	go func() {
		healthURL := fmt.Sprintf("http://localhost:%s/", s.appPort)
		reload.BroadcastWhenHealthy(ctx, healthURL, s.broadcaster)
		if s.readyChan != nil {
			select {
			case s.readyChan <- struct{}{}:
			default:
			}
		}
	}()

	return nil
}

func (s *AppServer) stop() {
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

func (s *AppServer) isHealthy(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(ctx, s.heartbeat.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, http.MethodHead, s.healthURL, nil)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: s.heartbeat.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode < http.StatusInternalServerError
}
