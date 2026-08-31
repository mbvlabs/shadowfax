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

// ReadSSRSettings resolves SSR settings using the same precedence as generated
// config/inertia.go: process environment (including .env), .env.example, then
// DefaultInertiaSSR* constants from config/inertia.go.
func ReadSSRSettings(scaffold *ScaffoldConfig) (SSRSettings, error) {
	fileDefaults := readInertiaFileDefaults("config/inertia.go")
	exampleEnv := readEnvFileDefaults(".env.example")

	settings := SSRSettings{
		Mode: applySSRSetting("", "INERTIA_SSR_MODE", fileDefaults.Mode, exampleEnv),
		URL:  applySSRSetting("", "INERTIA_SSR_URL", fileDefaults.URL, exampleEnv),
		Bundle: applySSRSetting(
			"",
			"INERTIA_SSR_BUNDLE",
			fileDefaults.Bundle,
			exampleEnv,
		),
		Runtime: applySSRSetting(
			"",
			"INERTIA_SSR_RUNTIME",
			fileDefaults.Runtime,
			exampleEnv,
		),
	}
	settings.Mode = strings.ToLower(strings.TrimSpace(settings.Mode))

	enabled := strings.TrimSpace(os.Getenv("INERTIA_SSR_ENABLED"))
	if exampleEnv != nil && enabled == "" {
		enabled = strings.TrimSpace(exampleEnv["INERTIA_SSR_ENABLED"])
	}
	if settings.Mode == "" && strings.EqualFold(enabled, "true") {
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
