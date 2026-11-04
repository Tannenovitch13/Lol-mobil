// LolMobil Companion runs on the gaming PC. It connects to the League
// client (and to Rose when it runs) and lets the LolMobil phone app drive
// the lobby, queue, ready check and champion select over the Wi-Fi.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"

	lolmobil "github.com/tannenovitch13/lol-mobil"
	"github.com/tannenovitch13/lol-mobil/internal/builds"
	"github.com/tannenovitch13/lol-mobil/internal/config"
	"github.com/tannenovitch13/lol-mobil/internal/lcu"
	"github.com/tannenovitch13/lol-mobil/internal/pengu"
	"github.com/tannenovitch13/lol-mobil/internal/relay"
	"github.com/tannenovitch13/lol-mobil/internal/rose"
	"github.com/tannenovitch13/lol-mobil/internal/server"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	if err := run(); err != nil {
		showError(err)
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
		relayURL      = flag.String("relay", "", "relay URL for remote access, e.g. https://lolmobil-relay.<you>.workers.dev (saved; \"off\" disables)")
		remoteFlag    = flag.String("remote", "", "remote access through public MQTT brokers: on (default) or off (saved)")
		brokersFlag   = flag.String("brokers", "", "comma-separated wss:// MQTT brokers for remote access (saved; \"default\" resets)")
		webURLFlag    = flag.String("web-url", "", "static copy of the web interface for phones without the app (saved)")
		opggURL       = flag.String("opgg-url", "", "OP.GG champion API base URL (tests)")
		blitzURL      = flag.String("blitz-url", "", "Blitz GraphQL URL (tests)")
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
			c.RemoteSecret = config.NewRemoteSecret()
		}
		switch *remoteFlag {
		case "off":
			c.RemoteDisabled = true
		case "on":
			c.RemoteDisabled = false
		}
		switch *brokersFlag {
		case "":
		case "default":
			c.Brokers = nil
		default:
			c.Brokers = nil
			for _, b := range strings.Split(*brokersFlag, ",") {
				if b = strings.TrimSpace(b); b != "" {
					c.Brokers = append(c.Brokers, b)
				}
			}
		}
		if *webURLFlag != "" {
			c.WebURL = *webURLFlag
		}
		switch *relayURL {
		case "":
		case "off":
			c.RelayURL = ""
		default:
			c.RelayURL = strings.TrimRight(*relayURL, "/")
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
		Builds:  builds.NewService(*opggURL, *blitzURL),
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

	pairURL := fmt.Sprintf("http://127.0.0.1:%d/pair", cfg.Port)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		if alreadyRunning(cfg.Port) {
			// Started twice (e.g. from the Start menu): show the pairing page.
			log.Printf("LolMobil Companion is already running")
			openBrowser(pairURL)
			return nil
		}
		return fmt.Errorf("impossible d'écouter sur le port %d : %w", cfg.Port, err)
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
	remoteDone := make(chan struct{})
	keys, err := relay.DeriveKeys(cfg.RemoteSecret)
	if err != nil {
		return err
	}
	newSession := func(id string, send func([]byte) error) relay.Session {
		return srv.NewRelaySession(id, send)
	}
	switch {
	case cfg.RelayURL != "":
		// Your own relay (Cloudflare Worker), see relay/README.md.
		host := &relay.Host{URL: cfg.RelayURL, Keys: keys, NewSession: newSession,
			OnStatus: func(bool) { srv.BroadcastStatus() }}
		srv.RelayConnected = host.Connected
		srv.RemoteLink = cfg.RelayURL + "/#k=" + cfg.RemoteSecret
		srv.RemoteVia = "ton relais " + cfg.RelayURL
		go func() { host.Run(ctx); close(remoteDone) }()
	case !cfg.RemoteDisabled:
		// No setup: public MQTT brokers, end-to-end encrypted.
		brokers := cfg.Brokers
		if len(brokers) == 0 {
			brokers = relay.DefaultBrokers
		}
		host := &relay.MQTTHost{Brokers: brokers, Keys: keys, NewSession: newSession,
			OnStatus: func(bool) { srv.BroadcastStatus() }}
		srv.RelayConnected = host.Connected
		srv.RemoteLink = remoteLink(cfg)
		srv.RemoteVia = "des serveurs MQTT publics, chiffré de bout en bout"
		go func() { host.Run(ctx); close(remoteDone) }()
	default:
		close(remoteDone)
	}

	printBanner(srv, cfg)

	if *openPair || (firstRun && !*noBrowser) {
		openBrowser(pairURL)
		_ = os.WriteFile(filepath.Join(filepath.Dir(cfg.Path()), ".paired-once"), []byte("1"), 0o600)
	}

	// Windows: icon in the notification area until "Quitter". Elsewhere:
	// until Ctrl+C.
	runUI(ctx, stop, uiOptions{
		srv:     srv,
		cfg:     cfg,
		pairURL: pairURL,
		logPath: filepath.Join(filepath.Dir(cfg.Path()), "companion.log"),
	})
	log.Printf("Arrêt...")
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	// Let remote access say the PC is offline.
	select {
	case <-remoteDone:
	case <-shutdownCtx.Done():
	}
	return nil
}

// defaultWebURL is the interface on GitHub, for phones without the app.
const defaultWebURL = "https://raw.githack.com/Tannenovitch13/Lol-mobil/HEAD/web/index.html"

// remoteLink is the remote access link of the MQTT mode. The Android app
// only uses its fragment (secret and brokers); a browser loads the page.
func remoteLink(cfg *config.Config) string {
	base := cfg.WebURL
	if base == "" {
		base = defaultWebURL
	}
	link := base + "#k=" + cfg.RemoteSecret
	if len(cfg.Brokers) > 0 {
		link += "&m=" + url.QueryEscape(strings.Join(cfg.Brokers, ","))
	}
	return link
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
		notifyUser("LolMobil", fmt.Sprintf("Plugin LolMobil-Remote installé dans %s.\nRedémarre le client LoL pour le charger.", dir))
	} else {
		notifyUser("LolMobil", fmt.Sprintf("Plugin LolMobil-Remote déjà à jour dans %s.", dir))
	}
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
	if link := srv.RemoteURL(); link != "" {
		fmt.Println("  Accès à distance (4G, autre Wi-Fi) : scanne le 2e QR code de la")
		fmt.Println("  page ci-dessus avec l'appli LolMobil, ou ouvre ce lien :")
		fmt.Println("  " + link)
		fmt.Println("  Garde ce lien secret : il donne le contrôle de ton client LoL.")
		fmt.Println()
	} else {
		fmt.Println("  Accès à distance : désactivé (-remote on pour le réactiver).")
		fmt.Println()
	}
	fmt.Println("  Laisse le compagnon ouvert pendant que tu joues. Ctrl+C pour quitter.")
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
	log.SetOutput(teeWriter{f, os.Stdout})
	log.SetFlags(log.Ltime)
}

// teeWriter writes to the log file and, when there is one, the console
// (the Windows build has no console: errors there are ignored).
type teeWriter struct{ file, console io.Writer }

func (t teeWriter) Write(p []byte) (int, error) {
	n, err := t.file.Write(p)
	_, _ = t.console.Write(p)
	return n, err
}

// alreadyRunning reports whether a companion answers on this port.
func alreadyRunning(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/info", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.Contains(string(body), `"lolmobil"`)
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
