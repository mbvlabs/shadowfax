package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestServeLocalAssetGET(t *testing.T) {
	dir := t.TempDir()
	cssPath := filepath.Join(dir, "assets", "css", "style.css")
	if err := os.MkdirAll(filepath.Dir(cssPath), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "body { margin-top: 1rem; }"
	if err := os.WriteFile(cssPath, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	ps := &Server{projectRoot: dir}
	req := httptest.NewRequest(http.MethodGet, "/__shadowfax/assets/css/style.css", nil)
	rec := httptest.NewRecorder()

	if ok := ps.serveLocalAsset(rec, req); !ok {
		t.Fatal("expected local asset to be served")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != want {
		t.Fatalf("unexpected body: got %q want %q", rec.Body.String(), want)
	}
	if got := rec.Header().Get("Content-Type"); got == "" {
		t.Fatal("expected content type header to be set")
	}
	if got := rec.Header().Get("Cache-Control"); got == "" {
		t.Fatal("expected Cache-Control header to be set")
	}
}

func TestServeLocalAssetHEAD(t *testing.T) {
	dir := t.TempDir()
	cssPath := filepath.Join(dir, "assets", "css", "style.css")
	if err := os.MkdirAll(filepath.Dir(cssPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "h1 { margin: 0; }"
	if err := os.WriteFile(cssPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ps := &Server{projectRoot: dir}
	req := httptest.NewRequest(http.MethodHead, "/__shadowfax/assets/css/style.css", nil)
	rec := httptest.NewRecorder()

	if ok := ps.serveLocalAsset(rec, req); !ok {
		t.Fatal("expected local asset to be served")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty body for HEAD request, got %d bytes", rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(content)) {
		t.Fatalf("unexpected content-length: got %q want %q", got, strconv.Itoa(len(content)))
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Fatalf("unexpected content-type: got %q", got)
	}
}

func TestServeLocalAssetRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	ps := &Server{projectRoot: dir}
	req := httptest.NewRequest(http.MethodGet, "/__shadowfax/assets/../../etc/passwd", nil)
	rec := httptest.NewRecorder()

	if ok := ps.serveLocalAsset(rec, req); ok {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestServeLocalAssetTimestampedPathFallback(t *testing.T) {
	dir := t.TempDir()
	cssPath := filepath.Join(dir, "assets", "css", "style.css")
	if err := os.MkdirAll(filepath.Dir(cssPath), 0o755); err != nil {
		t.Fatal(err)
	}
	want := ".x { margin-top: 1rem; }"
	if err := os.WriteFile(cssPath, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	ps := &Server{projectRoot: dir}
	req := httptest.NewRequest(http.MethodGet, "/__shadowfax/assets/css/1770715671/style.css", nil)
	rec := httptest.NewRecorder()

	if ok := ps.serveLocalAsset(rec, req); !ok {
		t.Fatal("expected timestamped path to fall back to local asset")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != want {
		t.Fatalf("unexpected body: got %q want %q", rec.Body.String(), want)
	}
}

func TestModifyResponseSkipsInjectionForHEAD(t *testing.T) {
	ps := &Server{}
	original := "<html><head></head><body>ok</body></html>"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":   []string{"text/html; charset=utf-8"},
			"Content-Length": []string{strconv.Itoa(len(original))},
		},
		Body:    io.NopCloser(strings.NewReader(original)),
		Request: httptest.NewRequest(http.MethodHead, "http://example.com", nil),
	}

	if err := ps.modifyResponse(resp); err != nil {
		t.Fatalf("modifyResponse returned error: %v", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body failed: %v", err)
	}
	if string(body) != original {
		t.Fatalf("expected HEAD response to remain unchanged, got %q", string(body))
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(original)) {
		t.Fatalf("expected content-length unchanged, got %q", got)
	}
}

func TestModifyResponseSkipsInjectionForNoContent(t *testing.T) {
	ps := &Server{}
	original := "<html><head></head><body>ignored</body></html>"
	resp := &http.Response{
		StatusCode: http.StatusNoContent,
		Header: http.Header{
			"Content-Type":   []string{"text/html; charset=utf-8"},
			"Content-Length": []string{strconv.Itoa(len(original))},
		},
		Body: io.NopCloser(strings.NewReader(original)),
	}

	if err := ps.modifyResponse(resp); err != nil {
		t.Fatalf("modifyResponse returned error: %v", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body failed: %v", err)
	}
	if string(body) != original {
		t.Fatalf("expected 204 response to remain unchanged, got %q", string(body))
	}
}

func TestModifyResponseSkipsInjectionForNotModified(t *testing.T) {
	ps := &Server{}
	original := "<html><head></head><body>cached</body></html>"
	resp := &http.Response{
		StatusCode: http.StatusNotModified,
		Header: http.Header{
			"Content-Type":   []string{"text/html; charset=utf-8"},
			"Content-Length": []string{strconv.Itoa(len(original))},
		},
		Body: io.NopCloser(strings.NewReader(original)),
	}

	if err := ps.modifyResponse(resp); err != nil {
		t.Fatalf("modifyResponse returned error: %v", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body failed: %v", err)
	}
	if string(body) != original {
		t.Fatalf("expected 304 response to remain unchanged, got %q", string(body))
	}
}

func TestProxyUnavailableReturnsAutoRetryPage(t *testing.T) {
	ps, err := NewServer("http://127.0.0.1:65535", "/__shadowfax/events")
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	handler := ps.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "http://localhost:3000/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when upstream is unavailable, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected HTML content type, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "App restarting...") {
		t.Fatalf("expected recovery page body, got %q", body)
	}
	if !strings.Contains(body, "window.location.reload()") {
		t.Fatalf("expected auto-retry script in response body")
	}
}
