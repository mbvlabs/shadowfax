package ssr

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mbvlabs/shadowfax/internal/config"
	"github.com/mbvlabs/shadowfax/internal/proc"
)

const (
	defaultStopTimeout        = 3 * time.Second
	defaultPortReleaseTimeout = 5 * time.Second
)

// Runner owns the project's cmd/ssr process during local development.
type Runner struct {
	Settings            config.SSRSettings
	PackageManager      string
	AddProcess          func(*exec.Cmd)
	Verbose             bool
	BuildOut            io.Writer
	RuntimeOut          io.Writer
	Log                 io.Writer
	OnReadyStateChanged func(ready bool)

	mu          sync.Mutex
	command     *exec.Cmd
	binPath     string
	done        chan struct{}
	stopTimeout time.Duration
	ready       bool
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
			if err := runner.stop(); err != nil {
				runner.logf("[shadowfax] SSR stop error: %v\n", err)
			}
			return nil
		case <-rebuildChan:
			if runner.Verbose {
				runner.logf("[shadowfax] Frontend changed, rebuilding SSR bundle\n")
			}
			if err := runner.rebuild(ctx); err != nil {
				runner.logf("[shadowfax] SSR rebuild error: %v\n", err)
				continue
			}
			// Build the replacement binary before stopping the old process so
			// /render stays available during the (slow) go build.
			if err := runner.buildSSRBinary(ctx); err != nil {
				runner.logf("[shadowfax] SSR restart error: %v\n", err)
				continue
			}
			if err := runner.stop(); err != nil {
				runner.logf("[shadowfax] SSR stop error: %v\n", err)
				continue
			}
			if err := runner.start(ctx); err != nil {
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
	proc.ConfigureProcessGroup(cmd)

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
		_ = runner.stop()
		return err
	}

	runner.logf("[shadowfax] Inertia SSR (cmd/ssr) listening on %s\n", runner.Settings.URL)
	runner.setReady(true)
	return nil
}

func (runner *Runner) setReady(ready bool) {
	runner.mu.Lock()
	if runner.ready == ready {
		runner.mu.Unlock()
		return
	}
	runner.ready = ready
	callback := runner.OnReadyStateChanged
	runner.mu.Unlock()
	if callback != nil {
		callback(ready)
	}
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

func (runner *Runner) stop() error {
	runner.setReady(false)

	runner.mu.Lock()
	cmd := runner.command
	done := runner.done
	runner.command = nil
	runner.done = nil
	runner.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		proc.Stop(cmd, proc.StopOptions{
			Done:      done,
			TermWait:  runner.stopWaitTimeout(),
			KillWait:  time.Second,
			GroupKill: true,
		})
	}

	return runner.waitForPortRelease(context.Background())
}

func (runner *Runner) stopWaitTimeout() time.Duration {
	if runner.stopTimeout > 0 {
		return runner.stopTimeout
	}
	return defaultStopTimeout
}

func (runner *Runner) waitForPortRelease(ctx context.Context) error {
	addr, err := listenAddr(runner.Settings.URL)
	if err != nil {
		return nil
	}
	timeout := defaultPortReleaseTimeout
	if runner.stopTimeout > 0 && runner.stopTimeout < timeout {
		// Tests that shrink stopTimeout should also get a snappy port wait.
		timeout = 2 * time.Second
	}
	if err := proc.WaitForPortRelease(ctx, addr, timeout); err != nil {
		return fmt.Errorf("inertia SSR listen address still busy after stop: %w", err)
	}
	return nil
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
