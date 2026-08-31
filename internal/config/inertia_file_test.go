package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadInertiaFileDefaults(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config", "inertia.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package config

const (
	DefaultInertiaSSRMode   = "external"
	DefaultInertiaSSRURL    = "http://127.0.0.1:13715"
	DefaultInertiaSSRBundle = "custom/ssr.js"
	DefaultInertiaSSRRuntime = "node"
)
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	defaults := readInertiaFileDefaults(path)
	if defaults.Mode != "external" {
		t.Fatalf("Mode = %q, want external", defaults.Mode)
	}
	if defaults.URL != "http://127.0.0.1:13715" {
		t.Fatalf("URL = %q", defaults.URL)
	}
	if defaults.Bundle != "custom/ssr.js" {
		t.Fatalf("Bundle = %q", defaults.Bundle)
	}
}

func TestReadSSRSettingsUsesConfigInertiaDefaults(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "inertia.go"), []byte(`package config

const (
	DefaultInertiaSSRMode = "external"
	DefaultInertiaSSRURL = "http://127.0.0.1:13715"
	DefaultInertiaSSRBundle = "assets/dist/ssr/ssr.js"
	DefaultInertiaSSRRuntime = "node"
)
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("INERTIA_SSR_MODE", "")
	t.Setenv("INERTIA_SSR_URL", "")
	t.Setenv("INERTIA_SSR_BUNDLE", "")
	t.Setenv("INERTIA_SSR_RUNTIME", "")

	settings, err := ReadSSRSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.ShouldShadowfaxStart() {
		t.Fatal("expected external mode from config/inertia.go defaults")
	}
	if settings.Port != "13715" {
		t.Fatalf("Port = %q, want 13715", settings.Port)
	}
}

func TestReadSSRSettingsPrefersProcessEnvironment(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "inertia.go"), []byte(`package config

const DefaultInertiaSSRMode = "external"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("INERTIA_SSR_MODE", "managed")

	settings, err := ReadSSRSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "managed" {
		t.Fatalf("Mode = %q, want managed", settings.Mode)
	}
	if settings.ShouldShadowfaxStart() {
		t.Fatal("managed mode should not be started by shadowfax")
	}
}
