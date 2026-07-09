package releaseconfig_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseTargetMatrix(t *testing.T) {
	root := repositoryRoot(t)
	targets := readFile(t, filepath.Join(root, ".github", "release-targets.txt"))

	want := strings.Join([]string{
		"linux amd64 shadowfax-linux-amd64",
		"linux arm64 shadowfax-linux-arm64",
		"darwin amd64 shadowfax-darwin-amd64",
		"darwin arm64 shadowfax-darwin-arm64",
		"",
	}, "\n")

	if targets != want {
		t.Fatalf("unexpected release target matrix\nwant:\n%s\ngot:\n%s", want, targets)
	}
}

func TestReleasePublicationIsGatedByVerification(t *testing.T) {
	root := repositoryRoot(t)
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "release.yml"))

	required := []string{
		"workflow_dispatch:",
		"runs-on: ubuntu-24.04-arm",
		"go-version: '1.26.5'",
		"done < .github/release-targets.txt",
		"CGO_ENABLED=0 GOOS=\"$goos\" GOARCH=\"$goarch\"",
		"sha256sum --check --strict checksums.txt",
		"gh release create \"$TAG\"",
		"--draft",
		"gh release download \"$TAG\" --dir uploaded",
		"gh release edit \"$TAG\" --draft=false",
	}
	for _, value := range required {
		if !strings.Contains(workflow, value) {
			t.Errorf("release workflow is missing %q", value)
		}
	}

	localValidation := strings.Index(workflow, "- name: Validate local release assets")
	draftCreation := strings.Index(workflow, "- name: Create draft release")
	remoteValidation := strings.Index(workflow, "- name: Verify uploaded release assets")
	publication := strings.Index(workflow, "- name: Publish release")
	if localValidation < 0 || draftCreation < 0 || remoteValidation < 0 || publication < 0 {
		t.Fatal("release workflow is missing a required publication stage")
	}
	if !(localValidation < draftCreation && draftCreation < remoteValidation && remoteValidation < publication) {
		t.Fatal("release publication must occur after local and uploaded asset verification")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate release configuration test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
