//go:build windows

package lcu

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

func platformDefaultDirs() []string {
	return []string{
		`C:\Riot Games\League of Legends`,
		`D:\Riot Games\League of Legends`,
		`C:\Program Files\Riot Games\League of Legends`,
		`C:\Program Files (x86)\Riot Games\League of Legends`,
	}
}

// scanProcess reads the LeagueClientUx.exe command line through CIM
// (wmic is gone from recent Windows builds).
func scanProcess() (Credentials, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`Get-CimInstance Win32_Process -Filter "Name='LeagueClientUx.exe'" | Select-Object -ExpandProperty CommandLine`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		return Credentials{}, "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if creds, install, ok := ParseCommandLine(line); ok {
			return creds, install, true
		}
	}
	return Credentials{}, "", false
}
