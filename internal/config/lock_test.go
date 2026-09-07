package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShouldUseTailwindUsesProjectFiles(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	if got, err := ShouldUseTailwind(); err != nil || got {
		t.Fatalf("ShouldUseTailwind() = %v, %v; want false, nil", got, err)
	}

	cssPath := filepath.Join(tmp, "css", "base.css")
	if err := os.MkdirAll(filepath.Dir(cssPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cssPath, []byte("@import \"tailwindcss\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := ShouldUseTailwind(); err != nil || !got {
		t.Fatalf("ShouldUseTailwind() = %v, %v; want true, nil", got, err)
	}
}

func TestShouldUseTailwindIgnoresAndurelLock(t *testing.T) {
	tmp := t.TempDir()
	chdir(t, tmp)

	if err := os.WriteFile(filepath.Join(tmp, "andurel.lock"), []byte(`{
  "scaffoldConfig": {
    "inertia": "react",
    "javascriptRuntime": "bun"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := ShouldUseTailwind(); err != nil || got {
		t.Fatalf("ShouldUseTailwind() = %v, %v; want false, nil", got, err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
}
