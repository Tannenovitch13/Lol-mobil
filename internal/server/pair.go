package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sort"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// LANAddresses returns the IPv4 addresses of this PC on local networks,
// the most likely home network first.
func LANAddresses() []string {
	type scored struct {
		ip    string
		score int
	}
	var found []scored
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		virtual := false
		for _, v := range []string{"vethernet", "virtualbox", "vmware", "wsl", "hyper-v", "docker", "vbox", "loopback", "tailscale", "zerotier", "veth", "br-"} {
			if strings.Contains(name, v) {
				virtual = true
			}
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			score := 0
			switch {
			case ip[0] == 192 && ip[1] == 168:
				score = 30
			case ip[0] == 10:
				score = 20
			default:
				score = 10
			}
			if virtual {
				score -= 25
			}
			found = append(found, scored{ip.String(), score})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.ip)
	}
	return out
}

// PairURL is the address encoded in the QR code: opening it on the phone
// (camera app or the LolMobil app) connects without typing the PIN.
func (s *Server) PairURL(ip string) string {
	return fmt.Sprintf("http://%s:%d/#token=%s", ip, s.Cfg.Port, s.Cfg.Token)
}

func (s *Server) bestIP() string {
	if ips := LANAddresses(); len(ips) > 0 {
		return ips[0]
	}
	return "127.0.0.1"
}

var pairPage = template.Must(template.New("pair").Parse(`<!doctype html>
<html lang="fr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>LolMobil – Appairage</title>
<style>
body{font-family:system-ui,sans-serif;background:#010a13;color:#f0e6d2;display:flex;justify-content:center;padding:32px;margin:0}
main{max-width:560px;text-align:center}
h1{color:#c8aa6e;font-weight:600}
.qr{background:#fff;padding:12px;border-radius:8px;display:inline-block}
.pin{font-size:44px;letter-spacing:10px;color:#c8aa6e;font-weight:700;margin:8px 0}
code{background:#1e2328;padding:2px 6px;border-radius:4px}
li{margin:6px 0;text-align:left}
.muted{color:#a09b8c;font-size:14px}
</style></head><body><main>
<h1>LolMobil</h1>
<p>Scanne ce QR code avec l'application LolMobil (ou l'appareil photo du téléphone) :</p>
<div class="qr"><img src="data:image/png;base64,{{.QR}}" width="280" height="280" alt="QR code"></div>
<p>ou tape ce code dans l'application :</p>
<div class="pin">{{.PIN}}</div>
<p class="muted">Adresse(s) de ce PC :</p>
<ul>{{range .URLs}}<li><code>{{.}}</code></li>{{end}}</ul>
<p class="muted">Le téléphone doit être sur le même Wi-Fi que ce PC. Si rien ne se connecte, autorise « LolMobil Companion » dans le pare-feu Windows (réseaux privés).</p>
</main></body></html>`))

// servePairPage shows the QR code and PIN, only on this PC.
func (s *Server) servePairPage(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "Cette page n'est visible que sur le PC", http.StatusForbidden)
		return
	}
	png, err := qrcode.Encode(s.PairURL(s.bestIP()), qrcode.Medium, 512)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var urls []string
	for _, ip := range LANAddresses() {
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip, s.Cfg.Port))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var buf bytes.Buffer
	_ = pairPage.Execute(&buf, map[string]any{
		"QR":   base64.StdEncoding.EncodeToString(png),
		"PIN":  s.Cfg.PIN,
		"URLs": urls,
	})
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) servePairQR(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	png, err := qrcode.Encode(s.PairURL(s.bestIP()), qrcode.Medium, 512)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}
