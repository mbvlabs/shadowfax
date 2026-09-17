package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/mbvlabs/shadowfax/internal/ctxrun"
	"github.com/mbvlabs/shadowfax/internal/proxy"
	"github.com/mbvlabs/shadowfax/internal/queue"
	"github.com/mbvlabs/shadowfax/internal/reload"
	"github.com/mbvlabs/shadowfax/internal/server"
	"github.com/mbvlabs/shadowfax/internal/state"
	"github.com/mbvlabs/shadowfax/internal/tui"
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

	runOpts, err := config.ParseRunOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "shadowfax: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		cleanup()
	}()

	if err := godotenv.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load .env file: %v\n", err)
	}

	if err := queue.RequireEntrypoint(); err != nil {
		fmt.Fprintf(os.Stderr, "shadowfax: %v\n", err)
		os.Exit(1)
	}

	hub := tui.NewHub()
	useTUI := tui.UseTUI(runOpts.Inline, os.Stdin, os.Stdout)
	buildLog := hub.Writer(tui.StreamBuild)
	appLog := hub.Writer(tui.StreamApp)
	queueLog := hub.Writer(tui.StreamQueue)
	if !useTUI {
		hub.SetFallback(os.Stdout)
	}

	var clearLogs func()
	if !useTUI && os.Getenv("SHADOWFAX_CLEAR_LOGS") != "" {
		clearLogs = func() { fmt.Fprint(os.Stdout, "\033[2J\033[H") }
	}

	fmt.Fprintf(buildLog, "Starting shadowfax (version %s)\n", Version)

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
	appRebuild := make(chan struct{}, 1)
	queueRebuild := make(chan struct{}, 1)
	templChange := make(chan watcher.TemplChange, 64)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	trk := state.New()
	var wg sync.WaitGroup
	errChan := make(chan error, 8)
	var rebuildInProgress atomic.Bool

	useInertia := runOpts.Inertia
	jsRuntime := runOpts.PackageManager

	reloadBlocked := func() bool {
		return rebuildInProgress.Load()
	}

	go ctxrun.Fanout(ctx, rebuildChan, appRebuild, queueRebuild)

	// Start proxy server
	wg.Go(func() {
		if err := runProxyServer(ctx, proxyPort, appPort, broadcaster, reloadBlocked); err != nil {
			errChan <- fmt.Errorf("proxy-server: %w", err)
		}
	})

	// Start Go file watcher
	wg.Go(func() {
		if err := watcher.RunGoWatcher(ctx, rebuildChan, verbose, buildLog); err != nil {
			errChan <- fmt.Errorf("go-watcher: %w", err)
		}
	})

	// Start templ watcher
	wg.Go(func() {
		cfg := watcher.TemplWatcherConfig{
			Verbose:    verbose,
			AddProcess: addProcess,
			Log:        buildLog,
			OnTemplErr: func(msg string) {
				trk.SetError(state.IndexTempl, msg)
				if msg != "" {
					hub.SetStatus(tui.StreamBuild, tui.StatusError)
				}
			},
		}
		if err := watcher.RunTemplWatcher(ctx, templChange, cfg); err != nil {
			errChan <- fmt.Errorf("live-templ: %w", err)
		}
	})

	useTailwind, err := config.ShouldUseTailwind()
	if err != nil && verbose {
		fmt.Fprintf(buildLog, "[shadowfax] Tailwind detection error: %v\n", err)
	}
	// Always compile assets/css/style.css for Templ pages. Inertia JS still
	// uses @tailwindcss/vite; broadcasts are gated so Vite HMR is left alone.
	useTailwindCLI := useTailwind
	var templPendingCSSReload atomic.Bool

	var cssRebuilt chan struct{}

	if useTailwindCLI {
		cssRebuilt = make(chan struct{}, 1)

		// Start tailwind watcher
		wg.Go(func() {
			cfg := watcher.TailwindConfig{
				Verbose:    verbose,
				AddProcess: addProcess,
				Log:        buildLog,
			}
			if err := watcher.RunTailwindWatcher(ctx, cssRebuilt, cfg); err != nil {
				errChan <- fmt.Errorf("live-tailwind: %w", err)
			}
		})

		// Handle CSS rebuild events from tailwind
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-cssRebuilt:
					handleCSSRebuild(broadcaster, useInertia, &templPendingCSSReload, reloadBlocked(), verbose, buildLog)
				}
			}
		}()
	} else if verbose {
		fmt.Fprintln(buildLog, "[shadowfax] Tailwind watcher disabled")
	}

	if useInertia {
		fmt.Fprintf(buildLog, "[shadowfax] Starting %s run dev (Inertia frontend)\n", jsRuntime)
		wg.Go(func() {
			if err := runJsDev(ctx, jsRuntime, buildLog); err != nil {
				fmt.Fprintf(buildLog, "[shadowfax] %s run dev: %v\n", jsRuntime, err)
				<-ctx.Done()
			}
		})
	} else if verbose {
		fmt.Fprintln(buildLog, "[shadowfax] Inertia frontend disabled")
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
		AppPort:      appPort,
		Broadcaster:  broadcaster,
		AddProcess:   addProcess,
		ReadyChan:    readyChan,
		StateTracker: trk,
		ClearLogs:    clearLogs,
		Stdout:       appLog,
		Stderr:       appLog,
		BuildOut:     buildLog,
		Log:          appLog,
		OnStatus: func(status string) {
			switch status {
			case "starting":
				hub.SetStatus(tui.StreamApp, tui.StatusStarting)
			case "ready":
				hub.SetStatus(tui.StreamApp, tui.StatusReady)
			case "error":
				hub.SetStatus(tui.StreamApp, tui.StatusError)
			}
		},
		OnRebuildStateChanged: func(inProgress bool) {
			rebuildInProgress.Store(inProgress)
			if inProgress {
				hub.SetStatus(tui.StreamBuild, tui.StatusStarting)
			} else if !trk.HasErrorAt(state.IndexGoBuild) && !trk.HasErrorAt(state.IndexTempl) {
				hub.SetStatus(tui.StreamBuild, tui.StatusReady)
			}
		},
	})
	wg.Go(func() {
		if err := appServer.Run(ctx, appRebuild); err != nil {
			errChan <- fmt.Errorf("app-server: %w", err)
		}
	})

	queueRunner := queue.NewRunner()
	queueRunner.AddProcess = addProcess
	queueRunner.BuildOut = buildLog
	queueRunner.RuntimeOut = queueLog
	queueRunner.Log = queueLog
	queueRunner.OnStatus = func(status tui.Status) {
		hub.SetStatus(tui.StreamQueue, status)
		if status == tui.StatusError {
			hub.SetStatus(tui.StreamBuild, tui.StatusError)
		}
	}
	wg.Go(func() {
		if err := queueRunner.Run(ctx, queueRebuild); err != nil {
			fmt.Fprintf(queueLog, "[shadowfax] queue runner: %v\n", err)
			<-ctx.Done()
		}
	})

	// Handle templ changes
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case change := <-templChange:
				switch change {
				case watcher.TemplChangeNeedsBrowserReload:
					if clearLogs != nil {
						clearLogs()
					}
					if trk.HasErrorAt(state.IndexTempl) {
						fmt.Fprintln(buildLog, "[shadowfax] Templ has errors, skipping browser reload")
						continue
					}
					if useTailwindCLI {
						fmt.Fprintln(buildLog, "[shadowfax] Template changed, triggering CSS rebuild")
						templPendingCSSReload.Store(true)
						if err := touchFile("./css/base.css"); err != nil {
							fmt.Fprintf(buildLog, "[shadowfax] Warning: could not touch CSS file: %v\n", err)
							templPendingCSSReload.Store(false)
							// Fall back to broadcasting directly
							broadcaster.Broadcast()
						}
					} else {
						fmt.Fprintln(buildLog, "[shadowfax] Template changed, reloading browser")
						broadcaster.Broadcast()
					}
				case watcher.TemplChangeNeedsRestart:
					if trk.HasErrorAt(state.IndexTempl) {
						fmt.Fprintln(buildLog, "[shadowfax] Templ has errors, skipping rebuild")
						continue
					}
					fmt.Fprintln(buildLog, "[shadowfax] Template Go code changed, rebuilding")
					if useTailwindCLI {
						rebuildInProgress.Store(true)
						if err := touchFile("./css/base.css"); err != nil && verbose {
							fmt.Fprintf(buildLog, "[shadowfax] Warning: could not touch CSS file: %v\n", err)
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

	fmt.Fprintf(buildLog, "\n  Proxy server: http://localhost:%s\n", proxyPort)
	fmt.Fprintf(buildLog, "  App server:   http://localhost:%s (internal)\n", appPort)
	fmt.Fprintf(buildLog, "  TEMPL_DEV_MODE: enabled (fast template reloads)\n")
	if useInertia {
		fmt.Fprintf(buildLog, "  Inertia frontend: %s run dev (Vite dev server)\n", jsRuntime)
		fmt.Fprintf(buildLog, "  Inertia SSR:      Vite /__inertia_ssr\n")
	}
	fmt.Fprintln(buildLog)

	go func() {
		select {
		case sig := <-sigChan:
			fmt.Fprintf(buildLog, "\nReceived signal: %v\n", sig)
			cancel()
		case err := <-errChan:
			if err != nil {
				fmt.Fprintf(buildLog, "Error: %v\n", err)
				fmt.Fprintf(buildLog, "Shutting down all processes...\n")
				cancel()
			}
		}
	}()

	if useTUI {
		header := tui.Header{
			Version:  Version,
			ProxyURL: fmt.Sprintf("http://localhost:%s", proxyPort),
		}
		if err := tui.Run(ctx, hub, header); err != nil {
			fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		}
		cancel()
	}

	wg.Wait()
	close(errChan)

	hasErrors := false
	for err := range errChan {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			hasErrors = true
		}
	}

	if useTUI {
		hub.Dump(os.Stdout)
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
	fmt.Fprintf(os.Stderr, "\nCleaning up processes...\n")

	processMutex.Lock()
	compactRunningProcessesLocked()
	processes := make([]*exec.Cmd, len(runningProcesses))
	copy(processes, runningProcesses)
	runningProcesses = nil
	processMutex.Unlock()

	// Ask tracked child processes to stop first.
	for _, cmd := range processes {
		signalTrackedProcess(cmd, syscall.SIGTERM)
	}

	// Fallback to force kill only tracked child processes that are still alive.
	time.Sleep(500 * time.Millisecond)
	for _, cmd := range processes {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if processAlive(cmd.Process) {
			signalTrackedProcess(cmd, syscall.SIGKILL)
		}
	}

	// Clean up templ temp files.
	wd, err := os.Getwd()
	if err == nil {
		os.RemoveAll(wd + "/tmp/templ")
	}

	fmt.Fprintf(os.Stderr, "Cleanup complete.\n")
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

func signalTrackedProcess(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid && cmd.Process.Pid > 0 {
		err := syscall.Kill(-cmd.Process.Pid, sig)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return
		}
	}
	_ = cmd.Process.Signal(sig)
}

func runProxyServer(
	ctx context.Context,
	proxyPort, appPort string,
	broadcaster *reload.Broadcaster,
	isRebuilding func() bool,
) error {
	targetURL := fmt.Sprintf("http://localhost:%s", appPort)

	proxyServer, err := proxy.NewServer(targetURL, reload.WebSocketPath, isRebuilding)
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

// shouldBroadcastCSSRebuild decides whether a Tailwind CLI rebuild should
// full-reload the tab. Swap the pending-Templ flag first so a blocked or
// Inertia JS-driven rebuild cannot leak into a later event.
func shouldBroadcastCSSRebuild(useInertia bool, templTriggered *atomic.Bool, reloadBlocked bool) bool {
	triggered := templTriggered.Swap(false)
	if reloadBlocked {
		return false
	}
	return !useInertia || triggered
}

func handleCSSRebuild(
	broadcaster *reload.Broadcaster,
	useInertia bool,
	templPending *atomic.Bool,
	reloadBlocked bool,
	verbose bool,
	log io.Writer,
) {
	if log == nil {
		log = os.Stdout
	}
	if shouldBroadcastCSSRebuild(useInertia, templPending, reloadBlocked) {
		fmt.Fprintln(log, "[shadowfax] CSS rebuilt, broadcasting reload")
		broadcaster.Broadcast()
		return
	}
	if !verbose {
		return
	}
	if reloadBlocked {
		fmt.Fprintln(log, "[shadowfax] CSS rebuilt (server restart in progress, skipping broadcast)")
		return
	}
	fmt.Fprintln(log, "[shadowfax] CSS rebuilt (Inertia, skipping broadcast)")
}

func runJsDev(ctx context.Context, runtime string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, runtime, "run", "dev")
	cmd.Dir = wd

	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s run dev: %w", runtime, err)
	}

	addProcess(cmd)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		if ctx.Err() != nil {
			return nil
		}
		fmt.Fprintf(out, "[shadowfax] %s run dev exited: %v\n", runtime, err)
		<-ctx.Done()
		return nil
	}
}
