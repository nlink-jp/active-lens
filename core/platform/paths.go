// Package platform holds the OS-specific bits: where config and data live, and
// how the sampling daemon is registered with the system scheduler. active-lens
// targets darwin/arm64; non-darwin builds compile but the scheduler is a stub.
package platform

import "path/filepath"

// AppName is the on-disk directory / service base name.
const AppName = "active-lens"

// ConfigDir returns the per-user config directory.
func ConfigDir() (string, error) { return configDir() }

// DataDir returns the per-user durable data directory (holds the DB).
func DataDir() (string, error) { return dataDir() }

// ConfigFilePath returns the path to config.toml.
func ConfigFilePath() (string, error) {
	d, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}
