package config

import "testing"

func TestReadSSRSettingsDefaults(t *testing.T) {
	t.Setenv("INERTIA_SSR_MODE", "")
	t.Setenv("INERTIA_SSR_ENABLED", "")
	t.Setenv("INERTIA_SSR_URL", "")
	t.Setenv("INERTIA_SSR_BUNDLE", "")
	t.Setenv("INERTIA_SSR_RUNTIME", "")

	settings, err := ReadSSRSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "" {
		t.Fatalf("Mode = %q, want empty", settings.Mode)
	}
	if settings.URL != defaultSSRURL {
		t.Fatalf("URL = %q, want %q", settings.URL, defaultSSRURL)
	}
	if settings.Bundle != defaultSSRBundle {
		t.Fatalf("Bundle = %q, want %q", settings.Bundle, defaultSSRBundle)
	}
	if settings.Runtime != "node" {
		t.Fatalf("Runtime = %q, want node", settings.Runtime)
	}
	if settings.Host != "127.0.0.1" || settings.Port != "13714" {
		t.Fatalf("Host/Port = %s:%s, want 127.0.0.1:13714", settings.Host, settings.Port)
	}
	if settings.ShouldShadowfaxStart() {
		t.Fatal("expected shadowfax not to start SSR when mode is empty")
	}
}

func TestReadSSRSettingsExternalMode(t *testing.T) {
	t.Setenv("INERTIA_SSR_MODE", "external")
	t.Setenv("INERTIA_SSR_URL", "http://127.0.0.1:13715")
	t.Setenv("INERTIA_SSR_BUNDLE", "assets/dist/ssr/ssr.js")
	t.Setenv("INERTIA_SSR_RUNTIME", "node")

	settings, err := ReadSSRSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.ShouldShadowfaxStart() {
		t.Fatal("expected shadowfax to start SSR in external mode")
	}
	if settings.Port != "13715" {
		t.Fatalf("Port = %q, want 13715", settings.Port)
	}
}

func TestReadSSRSettingsManagedMode(t *testing.T) {
	t.Setenv("INERTIA_SSR_MODE", "managed")

	settings, err := ReadSSRSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ShouldShadowfaxStart() {
		t.Fatal("expected shadowfax not to start SSR in managed mode")
	}
}

func TestScaffoldConfigPackageManagerAndSSRRuntime(t *testing.T) {
	config := &ScaffoldConfig{
		JavaScriptPackageManager: "pnpm",
		InertiaSSRRuntime:        "node",
	}
	if got := config.PackageManager(); got != "pnpm" {
		t.Fatalf("PackageManager() = %q, want pnpm", got)
	}
	if got := config.SSRRuntime(); got != "node" {
		t.Fatalf("SSRRuntime() = %q, want node", got)
	}

	legacy := &ScaffoldConfig{JavascriptRuntime: "bun"}
	if got := legacy.PackageManager(); got != "bun" {
		t.Fatalf("legacy PackageManager() = %q, want bun", got)
	}
}
