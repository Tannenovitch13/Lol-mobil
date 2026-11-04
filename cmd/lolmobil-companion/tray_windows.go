//go:build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/tannenovitch13/lol-mobil/internal/config"
	"github.com/tannenovitch13/lol-mobil/internal/server"
)

//go:embed icon.ico
var trayIcon []byte

type uiOptions struct {
	srv     *server.Server
	cfg     *config.Config
	pairURL string
	logPath string
}

// runUI shows the companion in the notification area (like Mimic's
// Conduit) and returns when "Quitter" is chosen or ctx ends.
func runUI(ctx context.Context, stop context.CancelFunc, o uiOptions) {
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(func() { trayReady(ctx, stop, o) }, stop)
}

func trayReady(ctx context.Context, stop context.CancelFunc, o uiOptions) {
	systray.SetIcon(trayIcon)
	systray.SetTitle("LolMobil")
	systray.SetTooltip("LolMobil Companion")

	status := systray.AddMenuItem("LolMobil Companion "+version, "")
	status.Disable()
	details := systray.AddMenuItem("", "")
	details.Disable()
	systray.AddSeparator()
	pair := systray.AddMenuItem("Connecter un téléphone (QR code)…", "Affiche le QR code et le code d'appairage")
	auto := systray.AddMenuItemCheckbox("Accepter les parties automatiquement", "", o.cfg.GetAutoAccept())
	start := systray.AddMenuItemCheckbox("Démarrer avec Windows", "", autostartEnabled())
	logs := systray.AddMenuItem("Ouvrir le journal", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quitter LolMobil", "")

	refresh := func() {
		sum := o.srv.Summary()
		yes := func(ok bool, on, off string) string {
			if ok {
				return on
			}
			return off
		}
		parts := []string{
			yes(sum.LCU, "LoL ✓", "LoL fermé"),
			yes(sum.Rose, "Rose ✓", "sans Rose"),
		}
		if sum.Phones > 0 {
			parts = append(parts, fmt.Sprintf("%d téléphone(s)", sum.Phones))
		}
		if sum.Relay {
			parts = append(parts, "à distance ✓")
		}
		text := strings.Join(parts, " · ")
		details.SetTitle(text)
		systray.SetTooltip("LolMobil – " + text)
		if auto.Checked() != o.cfg.GetAutoAccept() {
			if o.cfg.GetAutoAccept() {
				auto.Check()
			} else {
				auto.Uncheck()
			}
		}
	}
	refresh()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refresh()
			case <-pair.ClickedCh:
				openBrowser(o.pairURL)
			case <-auto.ClickedCh:
				o.srv.SetAutoAccept(!auto.Checked())
				refresh()
			case <-start.ClickedCh:
				on := !start.Checked()
				if err := setAutostart(on); err != nil {
					notifyUser("LolMobil", "Impossible de modifier le démarrage automatique : "+err.Error())
				} else if on {
					start.Check()
				} else {
					start.Uncheck()
				}
			case <-logs.ClickedCh:
				_ = exec.Command("notepad.exe", o.logPath).Start()
			case <-quit.ClickedCh:
				stop()
				systray.Quit()
				return
			}
		}
	}()
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("LolMobil")
	return err == nil && v != ""
}

func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("LolMobil"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("LolMobil", fmt.Sprintf(`"%s" -no-browser`, exe))
}

func messageBox(title, text string, flags uint32) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	_, _ = windows.MessageBox(0, m, t, flags|windows.MB_OK|windows.MB_SETFOREGROUND)
}

// showError reports a fatal error (the Windows build has no console).
func showError(err error) {
	fmt.Fprintf(os.Stderr, "Erreur : %v\n", err)
	messageBox("LolMobil Companion", "Erreur : "+err.Error(), windows.MB_ICONERROR)
}

func notifyUser(title, text string) {
	fmt.Println(text)
	messageBox(title, text, windows.MB_ICONINFORMATION)
}
