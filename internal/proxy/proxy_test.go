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
	if err := os.WriteFile(cssPath, []byte("h1 { margin: 0; }"), 0o644); err != nil {
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
