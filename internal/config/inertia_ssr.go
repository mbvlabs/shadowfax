package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	defaultSSRURL    = "http://127.0.0.1:13714"
	defaultSSRBundle = "assets/dist/ssr/ssr.js"
	defaultSSRPort   = "13714"
)

// SSRSettings describes Inertia SSR configuration for local development.
type SSRSettings struct {
	Mode    string
	URL     string
	Bundle  string
	Runtime string
	Host    string
	Port    string
}

// ReadSSRSettings loads SSR settings from the environment and scaffold lock.
func ReadSSRSettings(scaffold *ScaffoldConfig) (SSRSettings, error) {
	settings := SSRSettings{
		Mode:    strings.ToLower(strings.TrimSpace(os.Getenv("INERTIA_SSR_MODE"))),
		URL:     strings.TrimSpace(os.Getenv("INERTIA_SSR_URL")),
		Bundle:  strings.TrimSpace(os.Getenv("INERTIA_SSR_BUNDLE")),
		Runtime: strings.TrimSpace(os.Getenv("INERTIA_SSR_RUNTIME")),
	}

	if settings.Mode == "" && strings.EqualFold(strings.TrimSpace(os.Getenv("INERTIA_SSR_ENABLED")), "true") {
		settings.Mode = "managed"
	}
	if settings.URL == "" {
		settings.URL = defaultSSRURL
	}
	if settings.Bundle == "" {
		settings.Bundle = defaultSSRBundle
	}
	if settings.Runtime == "" {
		if scaffold != nil {
			settings.Runtime = scaffold.SSRRuntime()
		} else {
			settings.Runtime = "node"
		}
	}

	host, port, err := parseSSRURL(settings.URL)
	if err != nil {
		return SSRSettings{}, err
	}
	settings.Host = host
	settings.Port = port

	return settings, nil
}

// ShouldShadowfaxStart reports whether shadowfax should own the SSR Node process.
// Managed mode is owned by the Go application; disabled mode has no SSR process.
func (settings SSRSettings) ShouldShadowfaxStart() bool {
	return settings.Mode == "external"
}

func parseSSRURL(rawURL string) (host, port string, err error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", fmt.Errorf("invalid INERTIA_SSR_URL %q", rawURL)
	}

	host = parsed.Hostname()
	port = parsed.Port()
	if port == "" {
		port = defaultSSRPort
	}
	if host == "" {
		return "", "", fmt.Errorf("invalid INERTIA_SSR_URL %q: missing host", rawURL)
	}

	return host, port, nil
}
