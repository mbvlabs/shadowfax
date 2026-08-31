package config

import (
	"encoding/json"
	"errors"
	"os"
)

type AndurelLock struct {
	ScaffoldConfig *ScaffoldConfig `json:"scaffoldConfig,omitempty"`
}

type ScaffoldConfig struct {
	Inertia                  string `json:"inertia,omitempty"`
	JavaScriptPackageManager string `json:"javascriptPackageManager,omitempty"`
	InertiaSSRRuntime        string `json:"inertiaSSRRuntime,omitempty"`
	JavascriptRuntime        string `json:"javascriptRuntime,omitempty"` // Deprecated: use JavaScriptPackageManager.
}

// PackageManager returns the configured JavaScript package manager.
func (config *ScaffoldConfig) PackageManager() string {
	if config == nil {
		return "npm"
	}
	if config.JavaScriptPackageManager != "" {
		return config.JavaScriptPackageManager
	}
	if config.JavascriptRuntime != "" {
		return config.JavascriptRuntime
	}
	return "npm"
}

// SSRRuntime returns the JavaScript executable used for Inertia SSR.
func (config *ScaffoldConfig) SSRRuntime() string {
	if config == nil || config.InertiaSSRRuntime == "" {
		return "node"
	}
	return config.InertiaSSRRuntime
}

func ReadAndurelLock(path string) (*AndurelLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var lock AndurelLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}

	return &lock, nil
}

func ShouldUseTailwind() (bool, error) {
	info, err := os.Stat("css/base.css")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return !info.IsDir(), nil
}

func ShouldUseInertia() (bool, error) {
	lock, err := ReadAndurelLock("andurel.lock")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	if lock.ScaffoldConfig == nil {
		return false, nil
	}

	return lock.ScaffoldConfig.Inertia != "", nil
}

func GetJavascriptRuntime() (string, error) {
	lock, err := ReadAndurelLock("andurel.lock")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "npm", nil
		}
		return "", err
	}

	if lock.ScaffoldConfig == nil {
		return "npm", nil
	}

	return lock.ScaffoldConfig.PackageManager(), nil
}
