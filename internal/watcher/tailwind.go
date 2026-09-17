package watcher

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

type TailwindConfig struct {
	Verbose    bool
	AddProcess func(*exec.Cmd)
	Log        io.Writer
}

const tailwindRebuildDebounce = 250 * time.Millisecond

func RunTailwindWatcher(ctx context.Context, cssRebuilt chan<- struct{}, cfg TailwindConfig) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, wd+"/bin/tailwindcli",
		"-i", "./css/base.css",
		"-o", "./assets/css/style.css",
		"--watch=always",
	)

	cmd.Dir = wd

	// Capture both stdout and stderr because Tailwind may print rebuild
	// completion lines ("Done in ...") to stderr.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		logf(cfg.Log, "Tailwind CLI not found. Run 'andurel sync' to download it.\n")
		return err
	}

	if cfg.AddProcess != nil {
		cfg.AddProcess(cmd)
	}

	// Parse tailwind output to detect rebuilds.
	var lastRebuildSignal atomic.Int64
	go scanTailwindOutput(stdout, cfg.Verbose, cssRebuilt, &lastRebuildSignal, cfg.Log)
	go scanTailwindOutput(stderr, cfg.Verbose, cssRebuilt, &lastRebuildSignal, cfg.Log)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
		}
		if err := <-done; err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	case err := <-done:
		if err != nil && ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func scanTailwindOutput(reader io.Reader, verbose bool, cssRebuilt chan<- struct{}, lastRebuildSignal *atomic.Int64, log io.Writer) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if verbose {
			logf(log, "[tailwind] %s\n", line)
		}

		if isTailwindRebuildDoneLine(line) && shouldEmitTailwindRebuild(lastRebuildSignal, tailwindRebuildDebounce) {
			select {
			case cssRebuilt <- struct{}{}:
			default:
			}
		}
	}
}

func isTailwindRebuildDoneLine(line string) bool {
	return strings.Contains(line, "Done in")
}

func shouldEmitTailwindRebuild(lastRebuildSignal *atomic.Int64, debounce time.Duration) bool {
	now := time.Now().UnixNano()
	for {
		last := lastRebuildSignal.Load()
		if last != 0 && time.Duration(now-last) < debounce {
			return false
		}
		if lastRebuildSignal.CompareAndSwap(last, now) {
			return true
		}
	}
}
