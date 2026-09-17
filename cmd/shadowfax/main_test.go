package main

import (
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mbvlabs/shadowfax/internal/reload"
)

func TestTouchFileUpdatesMtime(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "touch-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	before, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Ensure clock advances
	time.Sleep(10 * time.Millisecond)

	if err := touchFile(f.Name()); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !after.ModTime().After(before.ModTime()) {
		t.Error("touchFile should update the file's modification time")
	}
}

func TestTouchFileErrorOnMissingFile(t *testing.T) {
	err := touchFile(t.TempDir() + "/nonexistent")
	if err == nil {
		t.Error("touchFile should return an error for a nonexistent file")
	}
}

func TestShouldBroadcastCSSRebuild(t *testing.T) {
	tests := []struct {
		name           string
		useInertia     bool
		templTriggered bool
		reloadBlocked  bool
		want           bool
		wantFlagAfter  bool
	}{
		{
			name:           "non-inertia idle rebuild",
			useInertia:     false,
			templTriggered: false,
			want:           true,
		},
		{
			name:           "non-inertia templ-triggered rebuild",
			useInertia:     false,
			templTriggered: true,
			want:           true,
		},
		{
			name:           "inertia js-driven rebuild stays silent",
			useInertia:     true,
			templTriggered: false,
			want:           false,
		},
		{
			name:           "inertia templ-triggered rebuild",
			useInertia:     true,
			templTriggered: true,
			want:           true,
		},
		{
			name:           "blocked rebuild never broadcasts",
			useInertia:     false,
			templTriggered: true,
			reloadBlocked:  true,
			want:           false,
		},
		{
			name:           "blocked inertia templ rebuild clears flag",
			useInertia:     true,
			templTriggered: true,
			reloadBlocked:  true,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pending atomic.Bool
			pending.Store(tt.templTriggered)

			got := shouldBroadcastCSSRebuild(tt.useInertia, &pending, tt.reloadBlocked)
			if got != tt.want {
				t.Fatalf("shouldBroadcastCSSRebuild() = %v, want %v", got, tt.want)
			}
			if pending.Load() != tt.wantFlagAfter {
				t.Fatalf("pending flag = %v, want %v", pending.Load(), tt.wantFlagAfter)
			}
		})
	}
}

func TestShouldBroadcastCSSRebuildClearsFlagBeforeLaterJSRebuild(t *testing.T) {
	var pending atomic.Bool
	pending.Store(true)

	if shouldBroadcastCSSRebuild(true, &pending, true) {
		t.Fatal("blocked rebuild should not broadcast")
	}
	if pending.Load() {
		t.Fatal("blocked rebuild should clear the pending templ flag")
	}
	if shouldBroadcastCSSRebuild(true, &pending, false) {
		t.Fatal("later inertia JS-driven rebuild should stay silent")
	}
}

// TestCSSRebuiltBroadcastsWhenIdle verifies that a CSS rebuild triggers a
// browser reload when no Go rebuild is in progress (the TemplChangeNeedsBrowserReload path).
func TestCSSRebuiltBroadcastsWhenIdle(t *testing.T) {
	broadcaster := reload.NewBroadcaster()
	listener := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(listener)

	var pending atomic.Bool
	handleCSSRebuild(broadcaster, false, &pending, false, false, io.Discard)

	select {
	case <-listener:
		// Expected: CSS rebuild with no Go rebuild in progress should broadcast.
	case <-time.After(time.Second):
		t.Fatal("expected broadcast after CSS rebuild when idle")
	}
}

func TestCSSRebuiltInertiaSilentWithoutTempl(t *testing.T) {
	broadcaster := reload.NewBroadcaster()
	listener := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(listener)

	var pending atomic.Bool
	handleCSSRebuild(broadcaster, true, &pending, false, false, io.Discard)

	select {
	case <-listener:
		t.Fatal("inertia CSS rebuild without templ trigger should not broadcast")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCSSRebuiltInertiaBroadcastsOnTempl(t *testing.T) {
	broadcaster := reload.NewBroadcaster()
	listener := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(listener)

	var pending atomic.Bool
	pending.Store(true)
	handleCSSRebuild(broadcaster, true, &pending, false, false, io.Discard)

	select {
	case <-listener:
	case <-time.After(time.Second):
		t.Fatal("expected broadcast after templ-triggered CSS rebuild in inertia")
	}
	if pending.Load() {
		t.Fatal("pending templ flag should be cleared after broadcast")
	}
}

// TestCSSRebuiltSuppressedDuringRestart verifies that a CSS rebuild does NOT
// trigger a browser reload while a Go rebuild is in progress
// (the TemplChangeNeedsRestart path). The app server's health-check broadcast
// handles the reload instead.
func TestCSSRebuiltSuppressedDuringRestart(t *testing.T) {
	broadcaster := reload.NewBroadcaster()
	listener := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(listener)

	var pending atomic.Bool
	pending.Store(true)
	handleCSSRebuild(broadcaster, false, &pending, true, false, io.Discard)

	select {
	case <-listener:
		t.Fatal("should not broadcast CSS rebuild while Go rebuild is in progress")
	case <-time.After(100 * time.Millisecond):
		// Expected: broadcast suppressed.
	}
	if pending.Load() {
		t.Fatal("pending templ flag should be cleared when rebuild is blocked")
	}
}

// TestReadyChanClearsRebuildInProgress verifies that the app server's ready
// signal allows subsequent CSS rebuilds to broadcast again.
func TestReadyChanClearsRebuildInProgress(t *testing.T) {
	var rebuildInProgress atomic.Bool
	rebuildInProgress.Store(true)

	readyChan := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-readyChan
		rebuildInProgress.Store(false)
	}()

	readyChan <- struct{}{}
	<-done

	if rebuildInProgress.Load() {
		t.Fatal("rebuildInProgress should be cleared after readyChan signal")
	}
}

// TestFullRestartCycle verifies the complete TemplChangeNeedsRestart flow:
// 1. rebuildInProgress is set
// 2. CSS rebuild during Go rebuild is suppressed
// 3. readyChan clears rebuildInProgress
// 4. A subsequent CSS rebuild broadcasts normally
func TestFullRestartCycle(t *testing.T) {
	broadcaster := reload.NewBroadcaster()
	listener := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(listener)

	cssRebuilt := make(chan struct{}, 1)
	readyChan := make(chan struct{}, 1)
	var rebuildInProgress atomic.Bool

	var templPending atomic.Bool

	// Start the CSS rebuild handler (mirrors main.go goroutine)
	go func() {
		for range cssRebuilt {
			handleCSSRebuild(broadcaster, false, &templPending, rebuildInProgress.Load(), false, io.Discard)
		}
	}()

	// Start the ready handler (mirrors main.go goroutine)
	go func() {
		for range readyChan {
			rebuildInProgress.Store(false)
		}
	}()

	// Step 1: Simulate TemplChangeNeedsRestart — set flag
	rebuildInProgress.Store(true)

	// Step 2: Tailwind finishes CSS rebuild during Go rebuild — should be suppressed
	cssRebuilt <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	select {
	case <-listener:
		t.Fatal("CSS rebuild during Go restart should not broadcast")
	default:
	}

	// Step 3: App server becomes healthy — clears flag
	readyChan <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	if rebuildInProgress.Load() {
		t.Fatal("rebuildInProgress should be cleared after ready signal")
	}

	// Step 4: Next CSS rebuild should broadcast (e.g. from a subsequent templ change)
	// Wait for broadcaster debounce to expire
	time.Sleep(60 * time.Millisecond)
	cssRebuilt <- struct{}{}

	select {
	case <-listener:
		// Expected: broadcast succeeds after rebuild cycle completes.
	case <-time.After(time.Second):
		t.Fatal("CSS rebuild after restart cycle should broadcast")
	}
}

func TestRunProxyServerFailsFastWhenPortInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	start := time.Now()
	err = runProxyServer(ctx, port, "8080", reload.NewBroadcaster(), nil)
	if err == nil {
		t.Fatal("expected bind error when proxy port is already in use")
	}

	if time.Since(start) > time.Second {
		t.Fatalf("expected startup failure to return quickly, took %s", time.Since(start))
	}
}
