// Package lolmobil holds the files embedded in the companion executable:
// the mobile web interface and the Pengu Loader plugin.
package lolmobil

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var webFiles embed.FS

//go:embed pengu-plugin/LolMobil-Remote
var pluginFiles embed.FS

// Web returns the mobile web interface (index.html at its root).
func Web() fs.FS {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	return sub
}

// Plugin returns the LolMobil-Remote plugin folder content.
func Plugin() fs.FS {
	sub, err := fs.Sub(pluginFiles, "pengu-plugin/LolMobil-Remote")
	if err != nil {
		panic(err)
	}
	return sub
}
