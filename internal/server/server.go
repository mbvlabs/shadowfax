package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mbvlabs/shadowfax/internal/ctxrun"
	"github.com/mbvlabs/shadowfax/internal/reload"
	"github.com/mbvlabs/shadowfax/internal/state"
)

type AppServer struct {
	cmd                    *exec.Cmd
	buildCmd               string
	binPath                string
	binDir                 string
	templDir               string
	appPort                string
	broadcaster            *reload.Broadcaster
	addProcess             func(*exec.Cmd)
	readyChan              chan<- struct{}
	onRebuildStateChanged  func(bool)
	stateTracker           *state.Tracker
	clearLogs              func()
	healthMu               sync.Mutex
	healthCancel           context.CancelFunc
	buildRunner            *ctxrun.Runner
	cmdMu                  sync.Mutex
	runBuild               func(context.Context, string) error
	runtimeRestartChan     chan<- struct{}
	runtimeRestartDelay    time.Duration
	runtimeOutputThreshold int
	stdout                 io.Writer
	stderr                 io.Writer
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
	runtimeRestartChan := make(chan struct{}, 1)
	s.runtimeRestartChan = runtimeRestartChan

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
		case <-runtimeRestartChan:
			fmt.Println("[shadowfax] Repeated missing-file output detected, restarting app...")
			s.setRebuildState(true)
			s.buildRunner.Go(ctx, func(buildCtx context.Context) {
				if err := waitForRuntimeRestart(buildCtx, s.runtimeRestartBackoff()); err != nil {
					s.setRebuildState(false)
					return
				}
				if err := s.rebuild(buildCtx, ctx); err != nil {
					fmt.Printf("[shadowfax] Runtime restart failed: %v\n", err)
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

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(candidateBinPath)
		s.cmdMu.Unlock()
		return fmt.Errorf("stdout pipe failed: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		os.Remove(candidateBinPath)
		s.cmdMu.Unlock()
		return fmt.Errorf("stderr pipe failed: %w", err)
	}

	if err := cmd.Start(); err != nil {
		os.Remove(candidateBinPath)
		s.cmdMu.Unlock()
		return fmt.Errorf("start failed: %w", err)
	}

	guard := newRuntimeOutputGuard(s.runtimeMissingFileThreshold(), s.requestRuntimeRestart)
	go forwardRuntimeOutput(stdoutPipe, s.stdoutWriter(), guard)
	go forwardRuntimeOutput(stderrPipe, s.stderrWriter(), guard)

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

func waitForRuntimeRestart(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *AppServer) requestRuntimeRestart() {
	if s.runtimeRestartChan == nil {
		return
	}
	select {
	case s.runtimeRestartChan <- struct{}{}:
	default:
	}
}

func (s *AppServer) runtimeRestartBackoff() time.Duration {
	if s.runtimeRestartDelay > 0 {
		return s.runtimeRestartDelay
	}
	return time.Second
}

func (s *AppServer) runtimeMissingFileThreshold() int {
	if s.runtimeOutputThreshold > 0 {
		return s.runtimeOutputThreshold
	}
	return 5
}

func (s *AppServer) stdoutWriter() io.Writer {
	if s.stdout != nil {
		return s.stdout
	}
	return os.Stdout
}

func (s *AppServer) stderrWriter() io.Writer {
	if s.stderr != nil {
		return s.stderr
	}
	return os.Stderr
}

type runtimeOutputGuard struct {
	mu          sync.Mutex
	threshold   int
	consecutive int
	restartOnce sync.Once
	onThreshold func()
}

func newRuntimeOutputGuard(threshold int, onThreshold func()) *runtimeOutputGuard {
	if threshold <= 0 {
		threshold = 5
	}
	return &runtimeOutputGuard{
		threshold:   threshold,
		onThreshold: onThreshold,
	}
}

func forwardRuntimeOutput(reader io.Reader, writer io.Writer, guard *runtimeOutputGuard) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		emit, notice := guard.handleLine(line)
		if emit {
			fmt.Fprintln(writer, line)
		}
		if notice != "" {
			fmt.Fprintln(writer, notice)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(writer, "[shadowfax] error reading app output: %v\n", err)
	}
}

func (g *runtimeOutputGuard) handleLine(line string) (bool, string) {
	if !isMissingFileRuntimeLine(line) {
		g.mu.Lock()
		g.consecutive = 0
		g.mu.Unlock()
		return true, ""
	}

	var triggerRestart bool
	var emit bool
	var notice string

	g.mu.Lock()
	g.consecutive++
	switch {
	case g.consecutive < g.threshold:
		emit = true
	case g.consecutive == g.threshold:
		emit = true
		triggerRestart = true
		notice = fmt.Sprintf("[shadowfax] Suppressing repeated missing-file output after %d consecutive lines.", g.threshold)
	default:
		emit = false
	}
	g.mu.Unlock()

	if triggerRestart {
		g.restartOnce.Do(func() {
			if g.onThreshold != nil {
				g.onThreshold()
			}
		})
	}

	return emit, notice
}

func isMissingFileRuntimeLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "file not found") ||
		strings.Contains(lower, "no such file or directory") ||
		strings.Contains(lower, "cannot find the file") ||
		strings.Contains(lower, "file does not exist") ||
		strings.Contains(lower, "path does not exist") ||
		strings.Contains(lower, "directory does not exist")
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
