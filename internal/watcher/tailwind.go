package watcher

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type TailwindConfig struct {
	Verbose    bool
	AddProcess func(*exec.Cmd)
}

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

	// Capture stdout to detect CSS rebuilds
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Println("Tailwind CLI not found. Run 'andurel sync' to download it.")
		return err
	}

	if cfg.AddProcess != nil {
		cfg.AddProcess(cmd)
	}

	// Parse tailwind output to detect rebuilds
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if cfg.Verbose {
				fmt.Printf("[tailwind] %s\n", line)
			}

			// Tailwind outputs "Done in" when it finishes rebuilding
			if strings.Contains(line, "Done in") {
				select {
				case cssRebuilt <- struct{}{}:
				default:
				}
			}
		}
	}()

	<-ctx.Done()
	if cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}
