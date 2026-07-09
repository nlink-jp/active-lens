//go:build darwin

package platform

import (
	"os"
	"strings"
	"testing"
)

func TestRenderDaemonConfig(t *testing.T) {
	got, err := renderDaemonConfig("/usr/local/bin/active-lens")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, must := range []string{
		"com.nlink-jp.active-lens",
		"<string>daemon</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"/usr/local/bin/active-lens",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("plist missing %q\n%s", must, got)
		}
	}
	// A resident daemon must NOT be a periodic StartInterval job.
	if strings.Contains(got, "StartInterval") {
		t.Error("resident daemon plist must not contain StartInterval")
	}
}

func TestResolveDaemonLogPath(t *testing.T) {
	if got := resolveDaemonLogPath("/data", nil); got != "/data/daemon.log" {
		t.Errorf("with data dir = %q, want /data/daemon.log", got)
	}
	got := resolveDaemonLogPath("", os.ErrNotExist)
	if !strings.HasSuffix(got, "active-lens-daemon.log") {
		t.Errorf("fallback = %q, want suffix active-lens-daemon.log", got)
	}
}
