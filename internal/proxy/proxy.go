package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/andybalholm/brotli"
)

const localAssetsPrefix = "/__shadowfax/assets/"

// Server is a reverse proxy that injects the hot reload script into HTML responses.
type Server struct {
	target      *url.URL
	proxy       *httputil.ReverseProxy
	wsPath      string
	projectRoot string
}

func NewServer(targetURL string, wsPath string) (*Server, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}

	ps := &Server{
		target: target,
		proxy:  proxy,
		wsPath: wsPath,
	}

	if wd, err := os.Getwd(); err == nil {
		ps.projectRoot = wd
	}

	proxy.ModifyResponse = ps.modifyResponse

	return ps, nil
}

func (ps *Server) modifyResponse(resp *http.Response) error {
	if isBodylessResponse(resp) {
		return nil
	}

	contentType := resp.Header.Get("Content-Type")
	if !IsHTMLResponse(contentType) {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	encoding := resp.Header.Get("Content-Encoding")
	var decompressed []byte

	switch encoding {
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return err
		}
		decompressed, err = io.ReadAll(gr)
		gr.Close()
		if err != nil {
			return err
		}
	case "br":
		br := brotli.NewReader(bytes.NewReader(body))
		decompressed, err = io.ReadAll(br)
		if err != nil {
			return err
		}
	default:
		decompressed = body
	}

	modified := RewriteStylesheetHrefs(decompressed)
	modified = InjectScript(modified)

	var finalBody []byte
	switch encoding {
	case "gzip":
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		gw.Write(modified)
		gw.Close()
		finalBody = buf.Bytes()
	case "br":
		var buf bytes.Buffer
		bw := brotli.NewWriter(&buf)
		bw.Write(modified)
		bw.Close()
		finalBody = buf.Bytes()
	default:
		finalBody = modified
	}

	resp.Body = io.NopCloser(bytes.NewReader(finalBody))
	resp.ContentLength = int64(len(finalBody))
	resp.Header.Set("Content-Length", strconv.Itoa(len(finalBody)))

	return nil
}

func isBodylessResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	if resp.Request != nil && resp.Request.Method == http.MethodHead {
		return true
	}
	return resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified
}

func (ps *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ps.serveLocalAsset(w, r) {
		return
	}
	ps.proxy.ServeHTTP(w, r)
}

func (ps *Server) Handler(wsHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a WebSocket request to our endpoint
		if r.URL.Path == ps.wsPath && isWebSocketRequest(r) {
			wsHandler.ServeHTTP(w, r)
			return
		}
		if ps.serveLocalAsset(w, r) {
			return
		}
		ps.proxy.ServeHTTP(w, r)
	})
}

func (ps *Server) serveLocalAsset(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, localAssetsPrefix) || ps.projectRoot == "" {
		return false
	}

	assetRelativePath := strings.TrimPrefix(r.URL.Path, localAssetsPrefix)
	localPath, ok := ps.resolveLocalAssetPath(assetRelativePath)
	if !ok {
		return false
	}

	content, err := os.ReadFile(localPath)
	if err != nil {
		return false
	}

	contentType := mime.TypeByExtension(filepath.Ext(localPath))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(content)
	}
	return true
}

func (ps *Server) resolveLocalAssetPath(assetRelativePath string) (string, bool) {
	assetsRoot := filepath.Join(ps.projectRoot, "assets")
	candidates := []string{assetRelativePath}

	parts := strings.Split(strings.Trim(filepath.ToSlash(assetRelativePath), "/"), "/")
	if len(parts) >= 3 {
		for i := 1; i < len(parts)-1; i++ {
			if isCacheBusterSegment(parts[i]) {
				trimmed := append([]string{}, parts[:i]...)
				trimmed = append(trimmed, parts[i+1:]...)
				candidates = append(candidates, strings.Join(trimmed, "/"))
			}
		}
	}

	for _, candidate := range candidates {
		localPath := filepath.Clean(filepath.Join(assetsRoot, filepath.FromSlash(candidate)))
		rel, err := filepath.Rel(assetsRoot, localPath)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(localPath)
		if err == nil && !info.IsDir() {
			return localPath, true
		}
	}

	return "", false
}

func isCacheBusterSegment(part string) bool {
	if part == "" {
		return false
	}
	allDigits := true
	for _, r := range part {
		if !unicode.IsDigit(r) {
			allDigits = false
			break
		}
	}
	if allDigits && len(part) >= 6 {
		return true
	}
	return false
}

func isWebSocketRequest(r *http.Request) bool {
	connection := strings.ToLower(r.Header.Get("Connection"))
	upgrade := strings.ToLower(r.Header.Get("Upgrade"))
	return strings.Contains(connection, "upgrade") && upgrade == "websocket"
}
