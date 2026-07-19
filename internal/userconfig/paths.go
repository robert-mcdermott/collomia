// Package userconfig defines the locations of user-editable Collomia files.
package userconfig

import (
	"os"
	"path/filepath"
)

const (
	DirectoryName = ".collomia"
	ConfigName    = "config.json"
)

// Dir returns the user-editable Collomia directory. os.UserHomeDir maps to
// the user's home directory on Unix and USERPROFILE on Windows.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DirectoryName), nil
}

func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigName), nil
}

// LegacyDir returns the configuration directory used before user-editable
// files moved to ~/.collomia. It remains readable during migration.
func LegacyDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "collomia"), nil
}

func LegacyConfigPath() (string, error) {
	dir, err := LegacyDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigName), nil
}

// SearchDirs returns the preferred directory followed by the former one,
// without duplicates. It is used for a non-breaking transition of global
// instructions and skills.
func SearchDirs() []string {
	var dirs []string
	if dir, err := Dir(); err == nil {
		dirs = append(dirs, dir)
	}
	if legacy, err := LegacyDir(); err == nil {
		for _, dir := range dirs {
			if filepath.Clean(dir) == filepath.Clean(legacy) {
				return dirs
			}
		}
		dirs = append(dirs, legacy)
	}
	return dirs
}
