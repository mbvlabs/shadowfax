package config

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	defaultSSRURL         = "http://127.0.0.1:13714"
	defaultSSRBundle      = "assets/dist/ssr/ssr.js"
	defaultSSRPort        = "13714"
	defaultPackageManager = "npm"
)

// RunOptions is the explicit contract passed by `andurel run`.
type RunOptions struct {
	Inertia        bool
	Inline         bool
	PackageManager string
	SSRURL         string
	SSRBundle      string
	SSRHost        string
	SSRPort        string
}

// SSRSettings describes health-check and JS-bundle paths for the SSR runner.
type SSRSettings struct {
	URL    string
	Bundle string
	Host   string
	Port   string
}

// ParseRunOptions parses Shadowfax CLI flags. `--version` / `-v` are handled
// before this is called.
func ParseRunOptions(args []string) (RunOptions, error) {
	fs := flag.NewFlagSet("shadowfax", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	inertia := fs.Bool("inertia", false, "enable Inertia Vite dev server")
	inline := fs.Bool("inline", false, "print interleaved logs instead of the interactive TUI")
	packageManager := fs.String(
		"js-package-manager",
		defaultPackageManager,
		"JavaScript package manager for Vite",
	)
	ssrURL := fs.String("ssr-url", defaultSSRURL, "SSR renderer base URL (must match app config)")
	ssrBundle := fs.String(
		"ssr-bundle",
		defaultSSRBundle,
		"path to the built SSR JS bundle (for missing-bundle detection)",
	)

	if err := fs.Parse(args); err != nil {
		return RunOptions{}, err
	}

	opts := RunOptions{
		Inertia:        *inertia,
		Inline:         *inline,
		PackageManager: strings.TrimSpace(*packageManager),
		SSRURL:         strings.TrimSpace(*ssrURL),
		SSRBundle:      strings.TrimSpace(*ssrBundle),
	}
	if opts.PackageManager == "" {
		opts.PackageManager = defaultPackageManager
	}
	if opts.SSRURL == "" {
		opts.SSRURL = defaultSSRURL
	}
	if opts.SSRBundle == "" {
		opts.SSRBundle = defaultSSRBundle
	}

	host, port, err := parseSSRURL(opts.SSRURL)
	if err != nil {
		return RunOptions{}, err
	}
	opts.SSRHost = host
	opts.SSRPort = port
	return opts, nil
}

// SSRSettings converts run options into SSR runner settings.
func (opts RunOptions) SSRSettings() SSRSettings {
	return SSRSettings{
		URL:    opts.SSRURL,
		Bundle: opts.SSRBundle,
		Host:   opts.SSRHost,
		Port:   opts.SSRPort,
	}
}

func parseSSRURL(rawURL string) (host, port string, err error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", fmt.Errorf("invalid --ssr-url %q", rawURL)
	}

	host = parsed.Hostname()
	port = parsed.Port()
	if port == "" {
		port = defaultSSRPort
	}
	if host == "" {
		return "", "", fmt.Errorf("invalid --ssr-url %q: missing host", rawURL)
	}

	return host, port, nil
}
