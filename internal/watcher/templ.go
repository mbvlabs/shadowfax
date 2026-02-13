package watcher

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type TemplChange int8

const (
	TemplChangeNone               TemplChange = iota
	TemplChangeNeedsRestart                   // Full server restart needed (e.g., _templ.go changed)
	TemplChangeNeedsBrowserReload             // Just browser reload needed (e.g., template content changed)
)

var (
	bytesPrefixWarning      = []byte(`(!)`)
	bytesPrefixErr          = []byte(`(✗)`)
	bytesPrefixErrCleared   = []byte(`(✓) Error cleared`)
	bytesPrefixPostGenEvent = []byte(`(✓) Post-generation event received, processing...`)
	bytesNeedsRestart       = []byte(`needsRestart=true`)
	bytesNeedsBrowserReload = []byte(`needsBrowserReload=true`)
)

var templShutdownTimeout = 2 * time.Second

type TemplWatcherConfig struct {
	Verbose     bool
	AddProcess  func(*exec.Cmd)
}

func RunTemplWatcher(ctx context.Context, templChange chan<- TemplChange, cfg TemplWatcherConfig) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	cmd := exec.Command(
		wd+"/bin/templ", "generate",
		"--watch",
		"--log-level", "debug",
		// Only watch .templ files - the Go watcher handles .go files
		"--watch-pattern", `(.+\.templ$)`,
	)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("obtaining stderr pipe: %w", err)
	}

	fmt.Println("[shadowfax] Starting templ generate --watch")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting templ: %w", err)
	}

	if cfg.AddProcess != nil {
		cfg.AddProcess(cmd)
	}

	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			b := scanner.Bytes()
			line := scanner.Text()

			// Print templ output for debugging
			if cfg.Verbose {
				fmt.Printf("[templ] %s\n", line)
			}

			switch {
			case bytes.HasPrefix(b, bytesPrefixWarning):
				fmt.Printf("[shadowfax] templ warning: %s\n", line)
			case bytes.HasPrefix(b, bytesPrefixErr):
				fmt.Printf("[shadowfax] templ error: %s\n", line)
			case bytes.HasPrefix(b, bytesPrefixErrCleared):
				fmt.Println("[shadowfax] templ error cleared")
			}

			if after, found := bytes.CutPrefix(b, bytesPrefixPostGenEvent); found {
				switch {
				case bytes.Contains(after, bytesNeedsRestart):
					fmt.Println("[shadowfax] templ: needs restart (Go code changed)")
					select {
					case templChange <- TemplChangeNeedsRestart:
					default:
					}
				case bytes.Contains(after, bytesNeedsBrowserReload):
					fmt.Println("[shadowfax] templ: needs browser reload (template content changed)")
					select {
					case templChange <- TemplChangeNeedsBrowserReload:
					default:
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Printf("[shadowfax] error scanning templ output: %v\n", err)
		}
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		stopTemplProcess(cmd, done)
		return nil
	case err := <-done:
		return err
	}
}

func stopTemplProcess(cmd *exec.Cmd, done <-chan error) {
	if cmd.Process == nil {
		return
	}

	_ = cmd.Process.Signal(os.Interrupt)

	timer := time.NewTimer(templShutdownTimeout)
	defer timer.Stop()

	select {
	case <-done:
		return
	case <-timer.C:
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		fmt.Printf("[shadowfax] templ kill fallback error: %v\n", err)
	}

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		fmt.Println("[shadowfax] templ did not exit after kill fallback")
	}
}
