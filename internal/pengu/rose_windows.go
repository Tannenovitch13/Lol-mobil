//go:build windows

package pengu

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// roseUninstallKey is the Inno Setup key of Rose's installer (its AppId).
const roseUninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}_is1`

func roseInstallDirs() []string {
	var dirs []string
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			k, err := registry.OpenKey(root, roseUninstallKey, registry.QUERY_VALUE|view)
			if err != nil {
				continue
			}
			if loc, _, err := k.GetStringValue("InstallLocation"); err == nil && loc != "" {
				dirs = append(dirs, loc)
			}
			k.Close()
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if base := os.Getenv(env); base != "" {
			dirs = append(dirs, filepath.Join(base, "Rose"))
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, "Programs", "Rose"))
	}
	return dirs
}
