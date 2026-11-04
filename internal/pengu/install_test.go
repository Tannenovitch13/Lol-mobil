package pengu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestInstallReplacesPort(t *testing.T) {
	dir := t.TempDir()
	files := fstest.MapFS{"index.js": {Data: []byte(`const PORT_SETTING = "__LOLMOBIL_PORT__";`)}}

	changed, err := Install(files, dir, 9123)
	if err != nil || !changed {
		t.Fatalf("first install: changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, PluginName, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"9123"`) {
		t.Fatalf("port not substituted: %s", data)
	}

	changed, err = Install(files, dir, 9123)
	if err != nil || changed {
		t.Fatalf("second install should be a no-op: changed=%v err=%v", changed, err)
	}
}
