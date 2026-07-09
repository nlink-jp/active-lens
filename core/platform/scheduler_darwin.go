//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const daemonLabel = "com.nlink-jp.active-lens"

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", daemonLabel+".plist"), nil
}

func daemonLogPath() string {
	d, err := dataDir()
	return resolveDaemonLogPath(d, err)
}

// resolveDaemonLogPath prefers the per-user data dir; the fallback uses the
// per-user $TMPDIR (via os.TempDir) rather than the world-writable /tmp. Pure
// for testability.
func resolveDaemonLogPath(dataDir string, dataDirErr error) string {
	if dataDirErr == nil && dataDir != "" {
		return filepath.Join(dataDir, "daemon.log")
	}
	return filepath.Join(os.TempDir(), "active-lens-daemon.log")
}

// renderDaemonConfig builds a resident LaunchAgent: RunAtLoad starts it at login,
// KeepAlive restarts it if it exits. Unlike a StartInterval job it is a single
// long-lived process, which suits the sub-minute sampling cadence (no per-tick
// process/cgo startup cost). The sampling interval is read from config.toml by
// the daemon itself, so it is not encoded here.
func renderDaemonConfig(binPath string) (string, error) {
	log := daemonLogPath()
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, daemonLabel, binPath, log, log), nil
}

func installDaemon(binPath string) (DaemonInfo, error) {
	p, err := plistPath()
	if err != nil {
		return DaemonInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return DaemonInfo{}, err
	}
	content, err := renderDaemonConfig(binPath)
	if err != nil {
		return DaemonInfo{}, err
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return DaemonInfo{}, err
	}
	// Reload: unload any previous version (ignore error), then load.
	_ = exec.Command("launchctl", "unload", p).Run()
	if out, err := exec.Command("launchctl", "load", p).CombinedOutput(); err != nil {
		return DaemonInfo{Kind: "launchd", Label: daemonLabel, ConfigPath: p}, fmt.Errorf("launchctl load: %v: %s", err, out)
	}
	return DaemonInfo{Kind: "launchd", Label: daemonLabel, ConfigPath: p, Loaded: true}, nil
}

func uninstallDaemon() (DaemonInfo, error) {
	p, err := plistPath()
	if err != nil {
		return DaemonInfo{}, err
	}
	_ = exec.Command("launchctl", "unload", p).Run()
	info := DaemonInfo{Kind: "launchd", Label: daemonLabel, ConfigPath: p, Loaded: false}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return info, err
	}
	return info, nil
}

func daemonStatus() (DaemonInfo, error) {
	p, err := plistPath()
	if err != nil {
		return DaemonInfo{}, err
	}
	info := DaemonInfo{Kind: "launchd", Label: daemonLabel, ConfigPath: p}
	if _, err := os.Stat(p); err == nil {
		info.Loaded = exec.Command("launchctl", "list", daemonLabel).Run() == nil
	}
	return info, nil
}
