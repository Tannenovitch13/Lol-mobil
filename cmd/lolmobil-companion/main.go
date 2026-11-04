// LolMobil Companion runs on the gaming PC. It connects to the League
// client (and to Rose when it runs) and lets the LolMobil phone app drive
// the lobby, queue, ready check and champion select over the Wi-Fi.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"

	lolmobil "github.com/tannenovitch13/lol-mobil"
	"github.com/tannenovitch13/lol-mobil/internal/config"
	"github.com/tannenovitch13/lol-mobil/internal/lcu"
	"github.com/tannenovitch13/lol-mobil/internal/pengu"
	"github.com/tannenovitch13/lol-mobil/internal/rose"
	"github.com/tannenovitch13/lol-mobil/internal/server"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nErreur : %v\n", err)
		pauseOnWindows()
		os.Exit(1)
	}
}

func run() error {
	var (
		port          = flag.Int("port", 0, "port of the phone server (default 8787, saved in the config)")
		lcuURL        = flag.String("lcu-url", "", "force the LCU base URL (development, e.g. a mock client)")
		lcuPassword   = flag.String("lcu-password", "", "password for -lcu-url")
		leaguePath    = flag.String("league-path", "", "League of Legends install folder (saved in the config)")
		pluginsDir    = flag.String("pengu-plugins", "", "Pengu Loader plugins folder to install LolMobil-Remote into (saved)")
		noPlugin      = flag.Bool("no-plugin-install", false, "do not install the LolMobil-Remote plugin")
		noBrowser     = flag.Bool("no-browser", false, "do not open the pairing page in the browser")
		openPair      = flag.Bool("pair", false, "open the pairing page in the browser")
		resetPairing  = flag.Bool("reset-pairing", false, "generate a new PIN and token (paired phones must pair again)")
		showVersion   = flag.Bool("version", false, "print the version and exit")
		roseDisabled  = flag.Bool("no-rose", false, "do not connect to Rose")
		discoveryPort = flag.Int("discovery-port", 0, "UDP port for automatic discovery (default: port+1)")
		installOnly   = flag.Bool("install-plugin-only", false, "install the plugin into -pengu-plugins with -port, then exit (used for the administrator prompt)")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	if *installOnly {
		return installPluginOnly(*pluginsDir, *port)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	firstRun := false
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg.Path()), ".paired-once")); errors.Is(err, os.ErrNotExist) {
		firstRun = true
	}
	if err := cfg.Update(func(c *config.Config) {
		if *port != 0 {
			c.Port = *port
		}
		if *leaguePath != "" {
			c.LeaguePath = *leaguePath
		}
		if *pluginsDir != "" {
			c.PenguPluginsDir = *pluginsDir
		}
		if *resetPairing {
			c.Token = config.NewToken()
			c.PIN = config.NewPIN()
		}
	}); err != nil {
		return err
	}

	setupLogging(filepath.Dir(cfg.Path()))
	log.Printf("LolMobil Companion %s (%s/%s)", version, runtime.GOOS, runtime.GOARCH)
	log.Printf("Config: %s", cfg.Path())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	name, _ := os.Hostname()
	if name == "" {
		name = "PC"
	}

	hub := &pengu.Hub{}
	bridge := &rose.Bridge{}
	connector := &lcu.Connector{LeaguePath: cfg.LeaguePath}
	if *lcuURL != "" {
		connector.Override = &lcu.Credentials{BaseURL: *lcuURL, Password: *lcuPassword, Source: *lcuURL}
	}
	srv := &server.Server{
		Cfg:     cfg,
		LCU:     connector,
		Rose:    bridge,
		Plugin:  hub,
		Web:     lolmobil.Web(),
		Version: version,
		Name:    name,
	}
	connector.OnEvent = srv.OnLCUEvent
	connector.OnStatus = srv.OnLCUStatus
	bridge.OnMessage = srv.OnRoseMessage
	bridge.OnStatus = func(bool, int) { srv.BroadcastStatus() }
	hub.OnStatus = func(bool, string) { srv.BroadcastStatus() }
	hub.OnMessage = srv.OnPluginMessage

	if !*noPlugin {
		installPlugin(cfg)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("impossible d'écouter sur le port %d (le compagnon est peut-être déjà lancé) : %w", cfg.Port, err)
	}
	httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[HTTP] %v", err)
			stop()
		}
	}()

	udpPort := *discoveryPort
	if udpPort == 0 {
		udpPort = cfg.Port + 1
	}
	go srv.RunDiscovery(ctx, udpPort)
	go connector.Run(ctx)
	if !*roseDisabled {
		go bridge.Run(ctx)
	}

	printBanner(srv, cfg)

	pairURL := fmt.Sprintf("http://127.0.0.1:%d/pair", cfg.Port)
	if *openPair || (firstRun && !*noBrowser) {
		openBrowser(pairURL)
		_ = os.WriteFile(filepath.Join(filepath.Dir(cfg.Path()), ".paired-once"), []byte("1"), 0o600)
	}

	<-ctx.Done()
	log.Printf("Arrêt...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	return nil
}

func installPlugin(cfg *config.Config) {
	dirs := pengu.FindPluginDirs(cfg.PenguPluginsDir)
	if len(dirs) == 0 {
		log.Printf("[Plugin] Rose / Pengu Loader introuvable : plugin LolMobil-Remote non installé. " +
			"Les skins possédés restent sélectionnables. Avec Rose, relance avec -pengu-plugins \"<dossier plugins de Pengu>\".")
		return
	}
	for _, dir := range dirs {
		changed, err := pengu.Install(lolmobil.Plugin(), dir, cfg.Port)
		switch {
		case errors.Is(err, pengu.ErrNeedsAdmin):
			log.Printf("[Plugin] %s est protégé (Program Files) : Windows va demander l'autorisation d'installer le plugin LolMobil-Remote.", dir)
			if err := runElevated([]string{"-install-plugin-only", "-pengu-plugins", dir, "-port", strconv.Itoa(cfg.Port)}); err != nil {
				log.Printf("[Plugin] Installation refusée (%v). Lance le compagnon en administrateur, ou copie le dossier LolMobil-Remote dans %s.", err, dir)
			}
		case err != nil:
			log.Printf("[Plugin] %v", err)
		case changed:
			log.Printf("[Plugin] Plugin LolMobil-Remote installé dans %s (redémarre le client LoL, ou Rose, pour le charger)", dir)
		default:
			log.Printf("[Plugin] Plugin LolMobil-Remote à jour dans %s", dir)
		}
	}
}

// installPluginOnly runs in the elevated copy started by runElevated.
func installPluginOnly(dir string, port int) error {
	if dir == "" {
		return errors.New("-install-plugin-only needs -pengu-plugins")
	}
	if port == 0 {
		port = config.DefaultPort
	}
	changed, err := pengu.Install(lolmobil.Plugin(), dir, port)
	if err != nil {
		return err
	}
	if changed {
		fmt.Printf("Plugin LolMobil-Remote installé dans %s.\nRedémarre le client LoL pour le charger.\n", dir)
	} else {
		fmt.Printf("Plugin LolMobil-Remote déjà à jour dans %s.\n", dir)
	}
	time.Sleep(4 * time.Second)
	return nil
}

func printBanner(srv *server.Server, cfg *config.Config) {
	ips := server.LANAddresses()
	fmt.Println()
	fmt.Println("==============================================================")
	fmt.Println("  LolMobil Companion est prêt")
	fmt.Println("==============================================================")
	if len(ips) == 0 {
		fmt.Println("  Aucune adresse réseau local trouvée : le PC est-il connecté au Wi-Fi/Ethernet ?")
	}
	for _, ip := range ips {
		fmt.Printf("  Adresse : http://%s:%d\n", ip, cfg.Port)
	}
	fmt.Printf("  Code d'appairage : %s\n", cfg.PIN)
	fmt.Printf("  Page QR code (sur ce PC) : http://127.0.0.1:%d/pair\n", cfg.Port)
	fmt.Println()
	if len(ips) > 0 {
		fmt.Println("  Scanne ce QR code avec l'application LolMobil :")
		qrterminal.GenerateWithConfig(srv.PairURL(ips[0]), qrterminal.Config{
			Level:          qrterminal.L,
			Writer:         os.Stdout,
			HalfBlocks:     true,
			BlackChar:      qrterminal.BLACK_BLACK,
			WhiteBlackChar: qrterminal.WHITE_BLACK,
			WhiteChar:      qrterminal.WHITE_WHITE,
			BlackWhiteChar: qrterminal.BLACK_WHITE,
			QuietZone:      2,
		})
	}
	fmt.Println("  Laisse cette fenêtre ouverte pendant que tu joues. Ctrl+C pour quitter.")
	fmt.Println("==============================================================")
	fmt.Println()
}

func setupLogging(dir string) {
	path := filepath.Join(dir, "companion.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stdout, f))
	log.SetFlags(log.Ltime)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// pauseOnWindows keeps the console open after an error when the program was
// started by double-click.
func pauseOnWindows() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Fprintln(os.Stderr, "Appuie sur Entrée pour fermer.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
