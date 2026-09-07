package ssr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mbvlabs/shadowfax/internal/config"
)

// Runner owns the project's cmd/ssr process during local development.
type Runner struct {
	Settings       config.SSRSettings
	PackageManager string
	AddProcess     func(*exec.Cmd)
	Verbose        bool

	mu      sync.Mutex
	command *exec.Cmd
	binPath string
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
				fmt.Println("[shadowfax] Frontend changed, rebuilding SSR bundle")
			}
			if err := runner.rebuild(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "[shadowfax] SSR rebuild error: %v\n", err)
				continue
			}
			runner.stop()
			if err := runner.buildAndStart(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "[shadowfax] SSR restart error: %v\n", err)
			}
		}
	}
}

func (runner *Runner) ensureBundle(ctx context.Context) error {
	if _, err := os.Stat(runner.Settings.Bundle); err == nil {
		return nil
	}
	fmt.Printf(
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build cmd/ssr: %w", err)
	}
	runner.binPath = binPath
	return nil
}

func (runner *Runner) start(ctx context.Context) error {
	runner.mu.Lock()
	defer runner.mu.Unlock()

	if runner.command != nil {
		return nil
	}
	if runner.binPath == "" {
		return fmt.Errorf("ssr binary path is empty")
	}

	cmd := exec.Command(runner.binPath)
	cmd.Dir = mustWorkingDir()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cmd/ssr: %w", err)
	}

	runner.command = cmd
	if runner.AddProcess != nil {
		runner.AddProcess(cmd)
	}

	fmt.Printf("[shadowfax] Inertia SSR (cmd/ssr) listening on %s\n", runner.Settings.URL)

	go func() {
		waitErr := cmd.Wait()
		runner.mu.Lock()
		if runner.command == cmd {
			runner.command = nil
		}
		runner.mu.Unlock()
		if waitErr != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "[shadowfax] cmd/ssr exited: %v\n", waitErr)
		}
	}()

	return runner.waitForHealth(ctx)
}

func (runner *Runner) waitForHealth(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := checkHealth(ctx, runner.Settings.URL); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("inertia SSR health check timed out for %s", runner.Settings.URL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (runner *Runner) stop() {
	runner.mu.Lock()
	cmd := runner.command
	runner.command = nil
	runner.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

func mustWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
