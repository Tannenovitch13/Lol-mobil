//go:build !windows

package lcu

import (
	"os/exec"
	"strings"
)

func platformDefaultDirs() []string {
	return []string{
		"/Applications/League of Legends.app/Contents/LoL",
	}
}

// scanProcess looks for LeagueClientUx in the process list (macOS, or Wine
// on Linux).
func scanProcess() (Credentials, string, bool) {
	out, err := exec.Command("ps", "-A", "-o", "args").Output()
	if err != nil {
		return Credentials{}, "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "LeagueClientUx") {
			continue
		}
		if creds, install, ok := ParseCommandLine(line); ok {
			return creds, install, true
		}
	}
	return Credentials{}, "", false
}
