package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunTemplWatcherCancelUsesKillFallback(t *testing.T) {
	tmp := t.TempDir()
	createTemplScript(t, tmp, `#!/usr/bin/env sh
trap '' INT
while true; do
  sleep 1
done
`)

	oldTimeout := templShutdownTimeout
	templShutdownTimeout = 100 * time.Millisecond
	t.Cleanup(func() { templShutdownTimeout = oldTimeout })

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	start := time.Now()
	go func() {
		errCh <- RunTemplWatcher(ctx, make(chan TemplChange, 1), TemplWatcherConfig{})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil on cancel, got %v", err)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("shutdown took too long: %s", time.Since(start))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunTemplWatcher did not return after cancel")
	}
}

func TestRunTemplWatcherReturnsProcessError(t *testing.T) {
	tmp := t.TempDir()
	createTemplScript(t, tmp, `#!/usr/bin/env sh
echo "templ failed" 1>&2
exit 7
`)

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = RunTemplWatcher(ctx, make(chan TemplChange, 1), TemplWatcherConfig{})
	if err == nil {
		t.Fatal("expected process error, got nil")
	}
}

func createTemplScript(t *testing.T, root, content string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(binDir, "templ")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
