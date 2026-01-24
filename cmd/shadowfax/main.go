package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
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
	if useTailwind {
		// Start tailwind watcher
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := watcher.TailwindConfig{
				Verbose:    verbose,
				AddProcess: addProcess,
			}
			if err := watcher.RunTailwindWatcher(ctx, broadcaster, cfg); err != nil {
				errChan <- fmt.Errorf("live-tailwind: %w", err)
			}
		}()
	} else if verbose {
		fmt.Println("[shadowfax] Tailwind watcher disabled")
	}

	// App server manager
	appServer := server.NewAppServer(server.Config{
		AppPort:     appPort,
		Broadcaster: broadcaster,
		AddProcess:  addProcess,
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
					fmt.Println("[shadowfax] Template changed, reloading browser")
					broadcaster.Broadcast()
				case watcher.TemplChangeNeedsRestart:
					fmt.Println("[shadowfax] Template Go code changed, rebuilding")
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
	runningProcesses = append(runningProcesses, cmd)
}

func cleanup() {
	fmt.Printf("\nCleaning up processes...\n")

	processMutex.Lock()
	processes := make([]*exec.Cmd, len(runningProcesses))
	copy(processes, runningProcesses)
	processMutex.Unlock()

	for _, cmd := range processes {
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
		}
	}

	wd, err := os.Getwd()
	if err == nil {
		exec.Command("pkill", "-f", wd+"/tmp/bin/main").Run()
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = DefaultAppPort
	}
	exec.Command("fuser", "-k", port+"/tcp").Run()

	time.Sleep(500 * time.Millisecond)
	fmt.Printf("Cleanup complete.\n")
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

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Proxy server error: %v\n", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
