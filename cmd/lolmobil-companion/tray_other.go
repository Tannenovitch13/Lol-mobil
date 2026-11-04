//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tannenovitch13/lol-mobil/internal/config"
	"github.com/tannenovitch13/lol-mobil/internal/server"
)

type uiOptions struct {
	srv     *server.Server
	cfg     *config.Config
	pairURL string
	logPath string
}

// runUI waits for Ctrl+C (the notification area icon is Windows-only).
func runUI(ctx context.Context, _ context.CancelFunc, _ uiOptions) {
	<-ctx.Done()
}

func showError(err error) {
	fmt.Fprintf(os.Stderr, "\nErreur : %v\n", err)
}

func notifyUser(_, text string) {
	fmt.Println(text)
}
