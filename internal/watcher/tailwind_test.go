package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsTailwindRebuildDoneLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "done line",
			line: "Done in 40ms",
			want: true,
		},
		{
			name: "done line with microseconds",
			line: "Done in 44µs",
			want: true,
		},
		{
			name: "non-done line",
			line: "Rebuilding...",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTailwindRebuildDoneLine(tt.line)
			if got != tt.want {
				t.Fatalf("isTailwindRebuildDoneLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestShouldEmitTailwindRebuildDebounces(t *testing.T) {
	var last atomic.Int64

	if !shouldEmitTailwindRebuild(&last, 100*time.Millisecond) {
		t.Fatal("first signal should emit")
	}
	if shouldEmitTailwindRebuild(&last, 100*time.Millisecond) {
		t.Fatal("second immediate signal should be debounced")
	}

	time.Sleep(120 * time.Millisecond)

	if !shouldEmitTailwindRebuild(&last, 100*time.Millisecond) {
		t.Fatal("signal after debounce window should emit")
	}
}

func TestRunTailwindWatcherCancelReturnsNil(t *testing.T) {
	tmp := t.TempDir()
	createTailwindScript(t, tmp, `#!/usr/bin/env sh
trap '' TERM
while true; do
  echo "Done in 10ms"
  sleep 1
done
`)

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
	go func() {
		errCh <- RunTailwindWatcher(ctx, make(chan struct{}, 1), TailwindConfig{})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil on cancel, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunTailwindWatcher did not return after cancel")
	}
}

func TestRunTailwindWatcherReturnsProcessError(t *testing.T) {
	tmp := t.TempDir()
	createTailwindScript(t, tmp, `#!/usr/bin/env sh
echo "boom" 1>&2
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

	err = RunTailwindWatcher(ctx, make(chan struct{}, 1), TailwindConfig{})
	if err == nil {
		t.Fatal("expected process error, got nil")
	}
}

func createTailwindScript(t *testing.T, root, content string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(binDir, "tailwindcli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
