package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestHubRoutesWritersToNamedStreams(t *testing.T) {
	h := NewHub()
	if _, err := h.Writer(StreamApp).Write([]byte("hello app\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PrefixWriter(StreamApp, "[ssr] ").Write([]byte("ssr line\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Writer(StreamBuild).Write([]byte("go build\npartial")); err != nil {
		t.Fatal(err)
	}

	app := h.Lines(StreamApp)
	if len(app) != 2 || app[0] != "hello app" || app[1] != "[ssr] ssr line" {
		t.Fatalf("app lines = %#v", app)
	}
	if got := h.Lines(StreamBuild); len(got) != 1 || got[0] != "go build" {
		t.Fatalf("complete line should flush, got %#v", got)
	}
	if _, err := h.Writer(StreamBuild).Write([]byte(" rest\n")); err != nil {
		t.Fatal(err)
	}
	if got := h.Lines(StreamBuild); len(got) != 2 || got[1] != "partial rest" {
		t.Fatalf("build lines = %#v", got)
	}
}

func TestHubFallbackAndDump(t *testing.T) {
	h := NewHub()
	var fallback bytes.Buffer
	h.SetFallback(&fallback)
	_, _ = h.Writer(StreamQueue).Write([]byte("job ran\n"))

	if !strings.Contains(fallback.String(), "[queue] job ran\n") {
		t.Fatalf("fallback = %q", fallback.String())
	}

	var dump bytes.Buffer
	h.Dump(&dump)
	got := dump.String()
	if !strings.Contains(got, "--- queue ---") || !strings.Contains(got, "[queue] job ran") {
		t.Fatalf("dump = %q", got)
	}
}

func TestHubSubscribeAndRingCap(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe()
	defer unsub()

	_, _ = h.Writer(StreamApp).Write([]byte("one\n"))
	select {
	case line := <-ch:
		if line.Stream != StreamApp || line.Text != "one" {
			t.Fatalf("got %#v", line)
		}
	default:
		t.Fatal("expected subscribed line")
	}

	small := newRing(2)
	small.add("a")
	small.add("b")
	small.add("c")
	got := small.snapshot()
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("ring snapshot = %#v", got)
	}
}

func TestHubStatus(t *testing.T) {
	h := NewHub()
	if h.Status(StreamApp) != StatusIdle {
		t.Fatal("default idle")
	}
	h.SetStatus(StreamApp, StatusReady)
	if h.Status(StreamApp) != StatusReady {
		t.Fatal("expected ready")
	}
}
