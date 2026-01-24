package config

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

type AndurelLock struct {
	ScaffoldConfig *ScaffoldConfig `json:"scaffoldConfig,omitempty"`
}

type ScaffoldConfig struct {
	CSSFramework string `json:"cssFramework"`
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

	return strings.EqualFold(lock.ScaffoldConfig.CSSFramework, "tailwind"), nil
}
