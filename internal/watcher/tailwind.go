package watcher

import (
	"bufio"
	"context"
	"fmt"
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
		fmt.Println("Tailwind CLI not found. Run 'andurel sync' to download it.")
		return err
	}

	if cfg.AddProcess != nil {
		cfg.AddProcess(cmd)
	}

	// Parse tailwind output to detect rebuilds.
	var lastRebuildSignal atomic.Int64
	go scanTailwindOutput(stdout, cfg.Verbose, cssRebuilt, &lastRebuildSignal)
	go scanTailwindOutput(stderr, cfg.Verbose, cssRebuilt, &lastRebuildSignal)

	<-ctx.Done()
	if cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}

func scanTailwindOutput(reader io.Reader, verbose bool, cssRebuilt chan<- struct{}, lastRebuildSignal *atomic.Int64) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if verbose {
			fmt.Printf("[tailwind] %s\n", line)
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
