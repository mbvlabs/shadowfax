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
	if opts.Inline {
		t.Fatal("expected inline false by default")
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

func TestParseRunOptionsInline(t *testing.T) {
	opts, err := ParseRunOptions([]string{"--inline"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Inline {
		t.Fatal("expected inline")
	}
}

func TestParseRunOptionsInvalidURL(t *testing.T) {
	_, err := ParseRunOptions([]string{"--ssr-url", "not-a-url"})
	if err == nil || !strings.Contains(err.Error(), "--ssr-url") {
		t.Fatalf("expected --ssr-url error, got %v", err)
	}
}

func TestParseRunOptionsTools(t *testing.T) {
	opts, err := ParseRunOptions([]string{"--tools", "mailpit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Tools) != 1 || opts.Tools[0] != "mailpit" {
		t.Fatalf("Tools = %#v", opts.Tools)
	}

	opts, err = ParseRunOptions([]string{"--tools", " mailpit "})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Tools) != 1 || opts.Tools[0] != "mailpit" {
		t.Fatalf("trimmed Tools = %#v", opts.Tools)
	}
}

func TestParseToolsRejects(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"dblab", "unknown tool"},
		{"mailpit,mailpit", "duplicate"},
		{"mailpit,", "empty name"},
		{",mailpit", "empty name"},
		{"  ", "empty list"},
	}
	for _, tc := range cases {
		_, err := ParseTools(tc.raw)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("ParseTools(%q) = %v, want substring %q", tc.raw, err, tc.want)
		}
	}
}

func TestParseToolsOmitted(t *testing.T) {
	got, err := ParseTools("")
	if err != nil || got != nil {
		t.Fatalf("got %#v err %v", got, err)
	}
}
