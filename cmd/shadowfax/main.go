package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/mbvlabs/shadowfax/internal/config"
	"github.com/mbvlabs/shadowfax/internal/proxy"
	"github.com/mbvlabs/shadowfax/internal/reload"
	"github.com/mbvlabs/shadowfax/internal/server"
	"github.com/mbvlabs/shadowfax/internal/watcher"
)

var Version = "dev"

const (
	DefaultProxyPort = "3000"
	DefaultAppPort   = "8080"
)

var (
	runningProcesses []*exec.Cmd
	processMutex     sync.Mutex
)

var verbose = os.Getenv("SHADOWFAX_VERBOSE") == "true"

func main() {
	// Handle --version flag
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("shadowfax version %s\n", Version)
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		cleanup()
	}()

	if err := godotenv.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load .env file: %v\n", err)
	}

	fmt.Printf("Starting shadowfax (version %s)\n", Version)

	proxyPort := os.Getenv("PROXY_PORT")
	if proxyPort == "" {
		proxyPort = DefaultProxyPort
	}
	appPort := os.Getenv("PORT")
	if appPort == "" {
		appPort = DefaultAppPort
	}

	broadcaster := reload.NewBroadcaster()
	rebuildChan := make(chan struct{}, 1)
	templChange := make(chan watcher.TemplChange, 64)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	errChan := make(chan error, 5)

	// Start proxy server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := runProxyServer(ctx, proxyPort, appPort, broadcaster); err != nil {
			errChan <- fmt.Errorf("proxy-server: %w", err)
		}
	}()

	// Start Go file watcher
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := watcher.RunGoWatcher(ctx, rebuildChan, verbose); err != nil {
			errChan <- fmt.Errorf("go-watcher: %w", err)
		}
	}()

	// Start templ watcher
	wg.Add(1)
	go func() {
		defer wg.Done()
		cfg := watcher.TemplWatcherConfig{
			Verbose:    verbose,
			AddProcess: addProcess,
		}
		if err := watcher.RunTemplWatcher(ctx, templChange, cfg); err != nil {
			errChan <- fmt.Errorf("live-templ: %w", err)
		}
	}()

	useTailwind, err := config.ShouldUseTailwind()
	if err != nil && verbose {
		fmt.Printf("[shadowfax] Tailwind detection error: %v\n", err)
	}

	var cssRebuilt chan struct{}
	var rebuildInProgress atomic.Bool

	if useTailwind {
		cssRebuilt = make(chan struct{}, 1)

		// Start tailwind watcher
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := watcher.TailwindConfig{
				Verbose:    verbose,
				AddProcess: addProcess,
			}
			if err := watcher.RunTailwindWatcher(ctx, cssRebuilt, cfg); err != nil {
				errChan <- fmt.Errorf("live-tailwind: %w", err)
			}
		}()

		// Handle CSS rebuild events from tailwind
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-cssRebuilt:
					if !rebuildInProgress.Load() {
						fmt.Println("[shadowfax] CSS rebuilt, broadcasting reload")
						broadcaster.Broadcast()
					} else if verbose {
						fmt.Println("[shadowfax] CSS rebuilt (server restart in progress, skipping broadcast)")
					}
				}
			}
		}()
	} else if verbose {
		fmt.Println("[shadowfax] Tailwind watcher disabled")
	}

	readyChan := make(chan struct{}, 1)

	// Clear rebuildInProgress when app server is ready
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-readyChan:
				rebuildInProgress.Store(false)
			}
		}
	}()

	// App server manager
	appServer := server.NewAppServer(server.Config{
		AppPort:     appPort,
		Broadcaster: broadcaster,
		AddProcess:  addProcess,
		ReadyChan:   readyChan,
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := appServer.Run(ctx, rebuildChan); err != nil {
			errChan <- fmt.Errorf("app-server: %w", err)
		}
	}()

	// Handle templ changes
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case change := <-templChange:
				switch change {
				case watcher.TemplChangeNeedsBrowserReload:
					if useTailwind {
						fmt.Println("[shadowfax] Template changed, triggering CSS rebuild")
						if err := touchFile("./css/base.css"); err != nil {
							fmt.Printf("[shadowfax] Warning: could not touch CSS file: %v\n", err)
							// Fall back to broadcasting directly
							broadcaster.Broadcast()
						}
					} else {
						fmt.Println("[shadowfax] Template changed, reloading browser")
						broadcaster.Broadcast()
					}
				case watcher.TemplChangeNeedsRestart:
					fmt.Println("[shadowfax] Template Go code changed, rebuilding")
					if useTailwind {
						rebuildInProgress.Store(true)
						if err := touchFile("./css/base.css"); err != nil && verbose {
							fmt.Printf("[shadowfax] Warning: could not touch CSS file: %v\n", err)
						}
					}
					select {
					case rebuildChan <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	fmt.Printf("\n  Proxy server: http://localhost:%s\n", proxyPort)
	fmt.Printf("  App server:   http://localhost:%s (internal)\n", appPort)
	fmt.Printf("  TEMPL_DEV_MODE: enabled (fast template reloads)\n\n")

	go func() {
		select {
		case sig := <-sigChan:
			fmt.Printf("\nReceived signal: %v\n", sig)
			cancel()
		case err := <-errChan:
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				fmt.Fprintf(os.Stderr, "Shutting down all processes...\n")
				cancel()
			}
		}
	}()

	wg.Wait()
	close(errChan)

	hasErrors := false
	for err := range errChan {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			hasErrors = true
		}
	}

	if hasErrors {
		os.Exit(1)
	}
}

func addProcess(cmd *exec.Cmd) {
	processMutex.Lock()
	defer processMutex.Unlock()
	compactRunningProcessesLocked()
	runningProcesses = append(runningProcesses, cmd)
}

func cleanup() {
	fmt.Printf("\nCleaning up processes...\n")

	processMutex.Lock()
	compactRunningProcessesLocked()
	processes := make([]*exec.Cmd, len(runningProcesses))
	copy(processes, runningProcesses)
	runningProcesses = nil
	processMutex.Unlock()

	// Ask tracked child processes to stop first.
	for _, cmd := range processes {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	}

	// Fallback to force kill only tracked child processes that are still alive.
	time.Sleep(500 * time.Millisecond)
	for _, cmd := range processes {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if processAlive(cmd.Process) {
			_ = cmd.Process.Kill()
		}
	}

	fmt.Printf("Cleanup complete.\n")
}

func compactRunningProcessesLocked() {
	if len(runningProcesses) == 0 {
		return
	}

	compacted := runningProcesses[:0]
	for _, cmd := range runningProcesses {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			continue
		}
		if !processAlive(cmd.Process) {
			continue
		}
		compacted = append(compacted, cmd)
	}
	runningProcesses = compacted
}

func processAlive(process *os.Process) bool {
	if process == nil {
		return false
	}

	err := process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func runProxyServer(
	ctx context.Context,
	proxyPort, appPort string,
	broadcaster *reload.Broadcaster,
) error {
	targetURL := fmt.Sprintf("http://localhost:%s", appPort)

	proxyServer, err := proxy.NewServer(targetURL, reload.WebSocketPath)
	if err != nil {
		return err
	}

	wsHandler := reload.NewWebSocketHandler(broadcaster)
	handler := proxyServer.Handler(wsHandler)

	server := &http.Server{
		Addr:    ":" + proxyPort,
		Handler: handler,
	}

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.Addr, err)
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErr:
		return fmt.Errorf("serve proxy on %s: %w", server.Addr, err)
	}
}

// touchFile updates the modification time of a file to trigger file watchers.
func touchFile(path string) error {
	now := time.Now()
	return os.Chtimes(path, now, now)
}
