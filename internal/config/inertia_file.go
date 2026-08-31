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

func readEnvFileDefaults(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	values := make(map[string]string)
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		values[key] = strings.Trim(value, `"`)
	}
	return values
}

func applySSRSetting(current, envKey, fileDefault string, example map[string]string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value
	}
	if example != nil {
		if value := strings.TrimSpace(example[envKey]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fileDefault)
}
