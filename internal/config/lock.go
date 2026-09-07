package config

import (
	"errors"
	"os"
)

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
