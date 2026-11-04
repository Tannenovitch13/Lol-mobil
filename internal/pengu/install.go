package pengu

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PluginName is the folder name of the plugin in Pengu's plugins folder.
const PluginName = "LolMobil-Remote"

// portPlaceholder is replaced by the companion port when installing.
const portPlaceholder = "__LOLMOBIL_PORT__"

// candidatePluginDirs lists Pengu Loader plugins folders, Rose's first.
func candidatePluginDirs(configured string) []string {
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		d = filepath.Clean(d)
		for _, e := range dirs {
			if strings.EqualFold(e, d) {
				return
			}
		}
		dirs = append(dirs, d)
	}
	add(configured)
	for _, root := range roseInstallDirs() {
		add(filepath.Join(root, "_internal", "Pengu Loader", "plugins"))
		add(filepath.Join(root, "Pengu Loader", "plugins"))
	}
	return dirs
}

// FindPluginDirs returns the existing Pengu plugins folders.
func FindPluginDirs(configured string) []string {
	var found []string
	for _, d := range candidatePluginDirs(configured) {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			found = append(found, d)
		}
	}
	return found
}

// Install writes the plugin files into pluginsDir/LolMobil-Remote. files is
// the embedded plugin folder (holding index.js). It returns whether
// anything changed.
func Install(files fs.FS, pluginsDir string, port int) (bool, error) {
	target := filepath.Join(pluginsDir, PluginName)
	changed := false
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(target, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		data = bytes.ReplaceAll(data, []byte(portPlaceholder), []byte(strconv.Itoa(port)))
		if existing, err := os.ReadFile(dest); err == nil && bytes.Equal(existing, data) {
			return nil
		}
		changed = true
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return false, fmt.Errorf("no permission to write in %s: start the companion once as administrator, or copy the %s folder there yourself", pluginsDir, PluginName)
		}
		return false, err
	}
	return changed, nil
}
