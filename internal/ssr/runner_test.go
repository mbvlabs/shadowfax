package ssr

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mbvlabs/shadowfax/internal/config"
)

const (
	fakeSSREnv  = "SHADOWFAX_FAKE_SSR"
	fakeSSRPort = "SHADOWFAX_FAKE_SSR_PORT"
)

func TestMain(m *testing.M) {
	switch os.Getenv(fakeSSREnv) {
	case "parent":
		runFakeSSRParent()
		os.Exit(0)
	case "deaf-parent":
		runFakeDeafSSRParent()
		os.Exit(0)
	case "child":
		runFakeSSRChild()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRunnerRestartReleasesChildPort(t *testing.T) {
	port := freePort(t)
	runner := newFakeRunner(t, port, "parent")

	ctx := t.Context()
	if err := runner.start(ctx); err != nil {
		t.Fatalf("first start: %v", err)
	}
	t.Cleanup(runner.stop)

	if err := checkHealth(ctx, runner.Settings.URL); err != nil {
		t.Fatalf("health after first start: %v", err)
	}

	runner.stop()

	if err := runner.start(ctx); err != nil {
		t.Fatalf("restart after stop: %v", err)
	}

	if err := checkHealth(ctx, runner.Settings.URL); err != nil {
		t.Fatalf("health after restart: %v", err)
	}
}

func TestRunnerStopFreesChildPort(t *testing.T) {
	port := freePort(t)
	runner := newFakeRunner(t, port, "parent")

	if err := runner.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(runner.stop)

	runner.stop()

	addr := net.JoinHostPort("127.0.0.1", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s should be free after stop: %v", addr, err)
	}
	_ = ln.Close()
}

func TestRunnerReadyCallbackTracksHealth(t *testing.T) {
	port := freePort(t)
	var mu sync.Mutex
	var states []bool
	runner := newFakeRunner(t, port, "parent")
	runner.OnReadyStateChanged = func(ready bool) {
		mu.Lock()
		states = append(states, ready)
		mu.Unlock()
	}

	if err := runner.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(runner.stop)

	mu.Lock()
	afterStart := append([]bool(nil), states...)
	mu.Unlock()
	if len(afterStart) != 1 || !afterStart[0] {
		t.Fatalf("after start, ready states = %v, want [true]", afterStart)
	}

	if err := checkHealth(t.Context(), runner.Settings.URL); err != nil {
		t.Fatalf("health should succeed before ready callback: %v", err)
	}

	runner.stop()

	mu.Lock()
	afterStop := append([]bool(nil), states...)
	mu.Unlock()
	if len(afterStop) != 2 || afterStop[0] != true || afterStop[1] != false {
		t.Fatalf("after stop, ready states = %v, want [true false]", afterStop)
	}
}

func TestRunnerRestartNotifiesUnreadyThenReady(t *testing.T) {
	port := freePort(t)
	var mu sync.Mutex
	var states []bool
	runner := newFakeRunner(t, port, "parent")
	runner.OnReadyStateChanged = func(ready bool) {
		mu.Lock()
		states = append(states, ready)
		mu.Unlock()
	}

	ctx := t.Context()
	if err := runner.start(ctx); err != nil {
		t.Fatalf("first start: %v", err)
	}
	t.Cleanup(runner.stop)

	runner.stop()
	if err := runner.start(ctx); err != nil {
		t.Fatalf("restart: %v", err)
	}

	mu.Lock()
	got := append([]bool(nil), states...)
	mu.Unlock()
	want := []bool{true, false, true}
	if len(got) != len(want) {
		t.Fatalf("ready states = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ready states = %v, want %v", got, want)
		}
	}
}

func TestRunnerKillsChildWhenParentIgnoresSIGTERM(t *testing.T) {
	port := freePort(t)
	runner := newFakeRunner(t, port, "deaf-parent")
	runner.stopTimeout = 200 * time.Millisecond

	if err := runner.start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(runner.stop)

	runner.stop()

	addr := net.JoinHostPort("127.0.0.1", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s should be free after force-killing the process group: %v", addr, err)
	}
	_ = ln.Close()
}

func newFakeRunner(t *testing.T, port, role string) *Runner {
	t.Helper()
	t.Setenv(fakeSSREnv, role)
	t.Setenv(fakeSSRPort, port)

	return &Runner{
		Settings: config.SSRSettings{
			URL:  "http://127.0.0.1:" + port,
			Host: "127.0.0.1",
			Port: port,
		},
		binPath:     os.Args[0],
		stopTimeout: time.Second,
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func runFakeSSRParent() {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), fakeSSREnv+"=child")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("Inertia SSR listening on http://127.0.0.1:%s\n", os.Getenv(fakeSSRPort))

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, os.Interrupt)

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case <-sigs:
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-waitErr:
		case <-time.After(time.Second):
			_ = cmd.Process.Kill()
			<-waitErr
		}
	case err := <-waitErr:
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func runFakeDeafSSRParent() {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), fakeSSREnv+"=child")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("Inertia SSR listening on http://127.0.0.1:%s\n", os.Getenv(fakeSSRPort))
	signal.Ignore(syscall.SIGTERM)
	_ = cmd.Wait()
}

func runFakeSSRChild() {
	port := os.Getenv(fakeSSRPort)
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Starting SSR server on port %s...\n", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	server := &http.Server{Handler: mux}

	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM, os.Interrupt)
		<-sigs
		_ = server.Close()
	}()

	_ = server.Serve(ln)
}
