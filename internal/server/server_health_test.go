package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsHealthyReturnsTrueForOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	app := &AppServer{
		healthURL: srv.URL,
		heartbeat: heartbeatConfig{Timeout: 200 * time.Millisecond},
	}

	if !app.isHealthy(context.Background()) {
		t.Fatal("expected healthy for 200 response")
	}
}

func TestIsHealthyReturnsFalseForServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	app := &AppServer{
		healthURL: srv.URL,
		heartbeat: heartbeatConfig{Timeout: 200 * time.Millisecond},
	}

	if app.isHealthy(context.Background()) {
		t.Fatal("expected unhealthy for 500 response")
	}
}

func TestIsHealthyReturnsFalseOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	app := &AppServer{
		healthURL: srv.URL,
		heartbeat: heartbeatConfig{Timeout: 100 * time.Millisecond},
	}

	if app.isHealthy(context.Background()) {
		t.Fatal("expected unhealthy when probe times out")
	}
}
