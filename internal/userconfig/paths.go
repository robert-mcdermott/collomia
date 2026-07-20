// Package userconfig defines the single per-user root for Collomia files.
package userconfig

import (
	"os"
	"path/filepath"
)

const (
	DirectoryName = ".collomia"
	ConfigName    = "config.json"
)

// Dir returns the per-user Collomia root. os.UserHomeDir maps to the user's
// home directory on Unix and USERPROFILE on Windows.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DirectoryName), nil
}

func ConfigPath() (string, error) {
	return Path(ConfigName)
}

// Path returns a path below the per-user Collomia directory.
func Path(elements ...string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	parts := append([]string{dir}, elements...)
	return filepath.Join(parts...), nil
}
