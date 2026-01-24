package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

var excludeDirs = map[string]bool{
	"tmp": true, "bin": true, "node_modules": true,
	".git": true, "assets": true, "vendor": true,
}

func RunGoWatcher(ctx context.Context, rebuildChan chan<- struct{}, verbose bool) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	wd, _ := os.Getwd()

	// Recursively add directories
	filepath.WalkDir(wd, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		name := d.Name()
		if excludeDirs[name] || strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		watcher.Add(path)
		return nil
	})

	// Debounce timer
	var debounceTimer *time.Timer
	debounceDelay := 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-watcher.Events:
			if !isGoFile(event.Name) || isTemplGenerated(event.Name) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			// Debounce
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(debounceDelay, func() {
				fmt.Printf("[shadowfax] Go file changed: %s\n", filepath.Base(event.Name))
				select {
				case rebuildChan <- struct{}{}:
				default:
				}
			})
		case err := <-watcher.Errors:
			if verbose {
				fmt.Printf("[shadowfax] watcher error: %v\n", err)
			}
		}
	}
}

func isGoFile(path string) bool {
	return strings.HasSuffix(path, ".go")
}

func isTemplGenerated(path string) bool {
	return strings.HasSuffix(path, "_templ.go")
}
