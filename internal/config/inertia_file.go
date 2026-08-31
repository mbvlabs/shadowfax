package config

import (
	"os"
	"regexp"
	"strings"
)

type inertiaFileDefaults struct {
	Mode    string
	URL     string
	Bundle  string
	Runtime string
}

var inertiaQuotedConstPattern = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)\s*=\s*"([^"]*)"`)

// readInertiaFileDefaults loads DefaultInertiaSSR* constants from config/inertia.go.
// Generated Andurel apps apply these when the matching environment variable is unset.
func readInertiaFileDefaults(path string) inertiaFileDefaults {
	data, err := os.ReadFile(path)
	if err != nil {
		return inertiaFileDefaults{}
	}

	values := parseQuotedConsts(data)
	return inertiaFileDefaults{
		Mode:    values["DefaultInertiaSSRMode"],
		URL:     values["DefaultInertiaSSRURL"],
		Bundle:  values["DefaultInertiaSSRBundle"],
		Runtime: values["DefaultInertiaSSRRuntime"],
	}
}

func parseQuotedConsts(source []byte) map[string]string {
	values := make(map[string]string)
	for _, match := range inertiaQuotedConstPattern.FindAllStringSubmatch(string(source), -1) {
		if len(match) != 3 {
			continue
		}
		values[match[1]] = match[2]
	}
	return values
}

func resolveSSRSetting(envKey, fileDefault string) string {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value
	}
	return strings.TrimSpace(fileDefault)
}
