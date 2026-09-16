package ssr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mbvlabs/shadowfax/internal/config"
)

const (
	defaultStopTimeout        = 3 * time.Second
	defaultPortReleaseTimeout = 2 * time.Second
)

// Runner owns the project's cmd/ssr process during local development.
type Runner struct {
	Settings       config.SSRSettings
	PackageManager string
	AddProcess     func(*exec.Cmd)
	Verbose        bool
	BuildOut       io.Writer
	RuntimeOut     io.Writer
	Log            io.Writer

	mu          sync.Mutex
	command     *exec.Cmd
	binPath     string
	done        chan struct{}
	stopTimeout time.Duration
}

// Run ensures the JS SSR bundle exists, builds cmd/ssr, starts it, and restarts
// after frontend rebuilds.
func (runner *Runner) Run(ctx context.Context, rebuildChan <-chan struct{}) error {
	if err := runner.ensureBundle(ctx); err != nil {
		return err
	}
	if err := runner.buildAndStart(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			runner.stop()
			return nil
		case <-rebuildChan:
			if runner.Verbose {
				runner.logf("[shadowfax] Frontend changed, rebuilding SSR bundle\n")
			}
			if err := runner.rebuild(ctx); err != nil {
				runner.logf("[shadowfax] SSR rebuild error: %v\n", err)
				continue
			}
			runner.stop()
			if err := runner.buildAndStart(ctx); err != nil {
				runner.logf("[shadowfax] SSR restart error: %v\n", err)
			}
		}
	}
}

func (runner *Runner) ensureBundle(ctx context.Context) error {
	if _, err := os.Stat(runner.Settings.Bundle); err == nil {
		return nil
	}
	runner.logf(
		"[shadowfax] SSR bundle %s missing, running %s run build:ssr\n",
		runner.Settings.Bundle,
		runner.PackageManager,
	)
	return runner.runBuild(ctx)
}

func (runner *Runner) rebuild(ctx context.Context) error {
	return runner.runBuild(ctx)
}

func (runner *Runner) runBuild(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, runner.PackageManager, "run", "build:ssr")
	cmd.Dir = mustWorkingDir()
	cmd.Stdout = runner.buildWriter()
	cmd.Stderr = runner.buildWriter()
	return cmd.Run()
}

func (runner *Runner) buildAndStart(ctx context.Context) error {
	if err := runner.buildSSRBinary(ctx); err != nil {
		return err
	}
	return runner.start(ctx)
}

func (runner *Runner) buildSSRBinary(ctx context.Context) error {
	wd := mustWorkingDir()
	binDir := filepath.Join(wd, "tmp", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	binPath := filepath.Join(binDir, "ssr")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, "./cmd/ssr")
	cmd.Dir = wd
	cmd.Stdout = runner.buildWriter()
	cmd.Stderr = runner.buildWriter()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build cmd/ssr: %w", err)
	}
	runner.binPath = binPath
	return nil
}

func (runner *Runner) start(ctx context.Context) error {
	runner.mu.Lock()
	if runner.command != nil {
		runner.mu.Unlock()
		return nil
	}
	if runner.binPath == "" {
		runner.mu.Unlock()
		return fmt.Errorf("ssr binary path is empty")
	}

	cmd := exec.Command(runner.binPath)
	cmd.Dir = mustWorkingDir()
	cmd.Stdout = runner.runtimeWriter()
	cmd.Stderr = runner.runtimeWriter()
	// Put cmd/ssr in its own process group so a later kill reaches the Node
	// child that actually binds the SSR port.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		runner.mu.Unlock()
		return fmt.Errorf("start cmd/ssr: %w", err)
	}

	done := make(chan struct{})
	runner.command = cmd
	runner.done = done
	if runner.AddProcess != nil {
		runner.AddProcess(cmd)
	}
	runner.mu.Unlock()

	runner.logf("[shadowfax] Inertia SSR (cmd/ssr) listening on %s\n", runner.Settings.URL)

	go func() {
		waitErr := cmd.Wait()
		close(done)
		runner.mu.Lock()
		stale := runner.command != cmd
		if runner.command == cmd {
			runner.command = nil
			runner.done = nil
		}
		runner.mu.Unlock()
		if waitErr != nil && ctx.Err() == nil && !stale {
			runner.logf("[shadowfax] cmd/ssr exited: %v\n", waitErr)
		}
	}()

	if err := runner.waitForHealth(ctx, done); err != nil {
		runner.stop()
		return err
	}
	return nil
}

func (runner *Runner) waitForHealth(ctx context.Context, done <-chan struct{}) error {
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return fmt.Errorf("cmd/ssr exited before becoming healthy")
		default:
		}

		if err := checkHealth(ctx, runner.Settings.URL); err == nil {
			select {
			case <-done:
				return fmt.Errorf("cmd/ssr exited before becoming healthy")
			default:
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("inertia SSR health check timed out for %s", runner.Settings.URL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return fmt.Errorf("cmd/ssr exited before becoming healthy")
		case <-ticker.C:
		}
	}
}

func (runner *Runner) stop() {
	runner.mu.Lock()
	cmd := runner.command
	done := runner.done
	runner.command = nil
	runner.done = nil
	runner.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		runner.waitForPortRelease(context.Background())
		return
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)

	timer := time.NewTimer(runner.stopWaitTimeout())
	defer timer.Stop()

	if done != nil {
		select {
		case <-done:
			runner.waitForPortRelease(context.Background())
			return
		case <-timer.C:
		}
	}

	_ = signalProcessGroup(cmd, syscall.SIGKILL)
	if done != nil {
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}

	runner.waitForPortRelease(context.Background())
}

func (runner *Runner) stopWaitTimeout() time.Duration {
	if runner.stopTimeout > 0 {
		return runner.stopTimeout
	}
	return defaultStopTimeout
}

func (runner *Runner) waitForPortRelease(ctx context.Context) {
	addr, err := listenAddr(runner.Settings.URL)
	if err != nil {
		return
	}

	deadline := time.Now().Add(defaultPortReleaseTimeout)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func listenAddr(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" || port == "" {
		return "", fmt.Errorf("ssr url %q is missing host or port", rawURL)
	}
	return net.JoinHostPort(host, port), nil
}

func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return cmd.Process.Signal(sig)
	}

	err := syscall.Kill(-pid, sig)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return err
	}
	return cmd.Process.Signal(sig)
}

func mustWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func (runner *Runner) logf(format string, args ...any) {
	fmt.Fprintf(runner.logWriter(), format, args...)
}

func (runner *Runner) logWriter() io.Writer {
	if runner.Log != nil {
		return runner.Log
	}
	return os.Stdout
}

func (runner *Runner) buildWriter() io.Writer {
	if runner.BuildOut != nil {
		return runner.BuildOut
	}
	return os.Stdout
}

func (runner *Runner) runtimeWriter() io.Writer {
	if runner.RuntimeOut != nil {
		return runner.RuntimeOut
	}
	return os.Stdout
}
