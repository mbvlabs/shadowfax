package config

import (
	"strings"
	"testing"
)

func TestParseRunOptionsDefaults(t *testing.T) {
	opts, err := ParseRunOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Inertia {
		t.Fatal("expected inertia false by default")
	}
	if opts.PackageManager != defaultPackageManager {
		t.Fatalf("PackageManager = %q", opts.PackageManager)
	}
	if opts.SSRURL != defaultSSRURL || opts.SSRHost != "127.0.0.1" || opts.SSRPort != "13714" {
		t.Fatalf("SSR URL/host/port = %q %q %q", opts.SSRURL, opts.SSRHost, opts.SSRPort)
	}
}

func TestParseRunOptionsInertiaFlags(t *testing.T) {
	opts, err := ParseRunOptions([]string{
		"--inertia",
		"--js-package-manager", "pnpm",
		"--ssr-url", "http://127.0.0.1:13715",
		"--ssr-bundle", "custom/ssr.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Inertia {
		t.Fatal("expected inertia")
	}
	if opts.PackageManager != "pnpm" || opts.SSRPort != "13715" || opts.SSRBundle != "custom/ssr.js" {
		t.Fatalf("%#v", opts)
	}
}

func TestParseRunOptionsInvalidURL(t *testing.T) {
	_, err := ParseRunOptions([]string{"--ssr-url", "not-a-url"})
	if err == nil || !strings.Contains(err.Error(), "--ssr-url") {
		t.Fatalf("expected --ssr-url error, got %v", err)
	}
}
