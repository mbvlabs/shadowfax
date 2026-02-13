package main

import (
	"context"
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

// TestCSSRebuiltBroadcastsWhenIdle verifies that a CSS rebuild triggers a
// browser reload when no Go rebuild is in progress (the TemplChangeNeedsBrowserReload path).
func TestCSSRebuiltBroadcastsWhenIdle(t *testing.T) {
	broadcaster := reload.NewBroadcaster()
	listener := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(listener)

	cssRebuilt := make(chan struct{}, 1)
	var rebuildInProgress atomic.Bool

	go func() {
		<-cssRebuilt
		if !rebuildInProgress.Load() {
			broadcaster.Broadcast()
		}
	}()

	cssRebuilt <- struct{}{}

	select {
	case <-listener:
		// Expected: CSS rebuild with no Go rebuild in progress should broadcast.
	case <-time.After(time.Second):
		t.Fatal("expected broadcast after CSS rebuild when idle")
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

	cssRebuilt := make(chan struct{}, 1)
	var rebuildInProgress atomic.Bool
	rebuildInProgress.Store(true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-cssRebuilt
		if !rebuildInProgress.Load() {
			broadcaster.Broadcast()
		}
	}()

	cssRebuilt <- struct{}{}
	<-done

	select {
	case <-listener:
		t.Fatal("should not broadcast CSS rebuild while Go rebuild is in progress")
	case <-time.After(100 * time.Millisecond):
		// Expected: broadcast suppressed.
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

	// Start the CSS rebuild handler (mirrors main.go goroutine)
	go func() {
		for range cssRebuilt {
			if !rebuildInProgress.Load() {
				broadcaster.Broadcast()
			}
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	err = runProxyServer(ctx, port, "8080", reload.NewBroadcaster())
	if err == nil {
		t.Fatal("expected bind error when proxy port is already in use")
	}

	if time.Since(start) > time.Second {
		t.Fatalf("expected startup failure to return quickly, took %s", time.Since(start))
	}
}
