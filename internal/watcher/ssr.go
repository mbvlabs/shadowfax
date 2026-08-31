package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// RunSSRSourceWatcher watches frontend sources and signals SSR bundle rebuilds.
func RunSSRSourceWatcher(ctx context.Context, rebuildChan chan<- struct{}, verbose bool) error {
	root := filepath.Join(mustWorkingDir(), "resources", "js")
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := addWatchRecursive(watcher, root); err != nil {
		return err
	}

	var debounceTimer *time.Timer
	debounceDelay := 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if event.Op&fsnotify.Create != 0 {
				if stat, statErr := os.Stat(event.Name); statErr == nil && stat.IsDir() {
					if err := addWatchRecursive(watcher, event.Name); err != nil && verbose {
						fmt.Printf("[shadowfax] failed to watch SSR source directory %s: %v\n", event.Name, err)
					}
					continue
				}
			}

			if !isSSRSourceFile(event.Name) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) == 0 {
				continue
			}

			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(debounceDelay, func() {
				if verbose {
					fmt.Printf("[shadowfax] SSR source changed: %s\n", filepath.Base(event.Name))
				}
				select {
				case rebuildChan <- struct{}{}:
				default:
				}
			})
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			if verbose {
				fmt.Printf("[shadowfax] SSR source watcher error: %v\n", err)
			}
		}
	}
}

func isSSRSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".vue", ".svelte":
		return true
	default:
		return false
	}
}

func mustWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
