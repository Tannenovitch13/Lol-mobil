package main

import "net/http"

// fakeClientPage imitates the champion select skin carousel of the League
// client (same class names) plus a miniature ROSE-SkinMonitor, so the
// LolMobil-Remote plugin can be tested in a normal browser. Open
// /mock/client (add ?adjacent to only accept clicks on the neighbours of the
// centred skin) and inject pengu-plugin/LolMobil-Remote/index.js.
const fakeClientPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>Fake League client</title>
<style>
body{background:#010a13;color:#f0e6d2;font-family:sans-serif}
.skin-selection-carousel{display:flex;gap:8px;overflow:hidden;width:560px;margin:40px auto}
.skin-selection-item{flex:none;width:100px;height:100px;border:2px solid #333;cursor:pointer}
.skin-selection-item.skin-selection-item-selected{border-color:#c8aa6e}
.skin-thumbnail-img{width:100%;height:100%;background:#234}
.skin-name-text{text-align:center;font-size:20px}
#rose{text-align:center;color:#ff8fa0}
</style></head><body>
<div class="champion-select">
  <div class="skin-selection-carousel-container"><div class="skin-selection-carousel"></div></div>
  <div class="skin-name-text"></div>
  <div id="rose">Rose: -</div>
</div>
<script>
let skins = [], selected = 0;
const adjacentOnly = new URLSearchParams(location.search).has("adjacent");
function render() {
  const car = document.querySelector(".skin-selection-carousel");
  car.innerHTML = "";
  skins.forEach((s, i) => {
    const off = i - selected + 2;
    const item = document.createElement("div");
    item.className = "skin-selection-item skin-carousel-offset-" + off + (i === selected ? " skin-selection-item-selected" : "") + (s.unlocked ? "" : " disabled");
    const thumb = document.createElement("div");
    thumb.className = "skin-selection-thumbnail";
    const img = document.createElement("div");
    img.className = "skin-thumbnail-img";
    img.style.backgroundImage = 'url("' + s.tilePath + '")';
    thumb.appendChild(img);
    item.appendChild(thumb);
    item.addEventListener("click", () => {
      if (adjacentOnly && off !== 1 && off !== 3) return;
      selected = i;
      setTimeout(render, 80); // the real client re-renders asynchronously
    });
    car.appendChild(item);
  });
  document.querySelector(".skin-name-text").textContent = skins[selected] ? skins[selected].name : "";
}
async function load() {
  try {
    const list = await (await fetch("/mock/skins")).json();
    if (JSON.stringify(list.map(s => s.id)) !== JSON.stringify(skins.map(s => s.id))) {
      skins = list; selected = 0; render();
    }
  } catch (e) {}
}
setInterval(load, 1000); load();

// Miniature ROSE-SkinMonitor: reports the displayed name to Rose when it
// changes, and adopts the name Rose echoes back (like the real plugin).
let last = null, ws = null;
function connectRose() {
  ws = new WebSocket("ws://127.0.0.1:50000/");
  ws.onmessage = (e) => {
    const d = JSON.parse(e.data);
    if (d.type === "skin-state" && d.skinName) { last = d.skinName; document.getElementById("rose").textContent = "Rose: " + d.skinName; }
  };
  ws.onclose = () => setTimeout(connectRose, 1000);
}
connectRose();
setInterval(() => {
  const n = document.querySelector(".skin-name-text").textContent.trim();
  if (n && n !== last && ws && ws.readyState === 1) { last = n; ws.send(JSON.stringify({ skin: n })); }
}, 250);
</script></body></html>`

func (m *mock) serveFakeClient(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/mock/client":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fakeClientPage))
		return true
	case "/mock/skins":
		m.mu.Lock()
		skins := m.skins()
		if m.phase != "ChampSelect" {
			skins = []any{}
		}
		m.mu.Unlock()
		writeJSON(w, 200, skins)
		return true
	}
	return false
}
