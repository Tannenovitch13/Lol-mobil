import { html, render, useEffect, useState } from "../vendor/preact-htm.js";
import {
  act,
  connect,
  lcu,
  observe,
  pair,
  saveSettings,
  setToken,
  store,
  initConnection,
  isRemoteMode,
  toast,
  useLcu,
  useStore,
} from "./api.js";
import { clearDataCache, profileIcon } from "./data.js";
import { forgetRemoteSecret } from "./remote.js";
import { Home } from "./home.js";
import { ReadyCheck } from "./readycheck.js";
import { ChampSelect } from "./champselect.js";

// LCU resources every screen relies on.
observe([
  "/lol-gameflow/v1/gameflow-phase",
  "/lol-gameflow/v1/session",
  "/lol-summoner/v1/current-summoner",
  "/lol-lobby/v2/lobby",
  "/lol-lobby/v2/received-invitations",
  "/lol-matchmaking/v1/search",
  "/lol-matchmaking/v1/ready-check",
  "/lol-champ-select/v1/session",
]);

function Toasts() {
  const toasts = useStore((s) => s.toasts);
  return html`<div class="toasts">
    ${toasts.map((t) => html`<div key=${t.id} class="toast ${t.kind}">${t.text}</div>`)}
  </div>`;
}

function StatusDot({ ok, label, title }) {
  return html`<span class="dot ${ok ? "ok" : "off"}" title=${title}>${label}</span>`;
}

function Header({ onSettings }) {
  const status = useStore((s) => s.status);
  const connection = useStore((s) => s.connection);
  const me = useLcu("/lol-summoner/v1/current-summoner");
  const name = me ? me.gameName || me.displayName || "" : "";
  return html`<header class="top">
    <div class="me">
      ${me && html`<img class="avatar" src=${profileIcon(me.profileIconId)} alt="" />`}
      <div>
        <div class="me-name">${name || "LolMobil"}</div>
        <div class="dots">
          <${StatusDot} ok=${connection === "open"} label="PC" title="Compagnon PC" />
          <${StatusDot} ok=${status.lcu} label="LoL" title="Client League of Legends" />
          <${StatusDot} ok=${status.rose} label="Rose" title="Rose (changeur de skins)" />
          <${StatusDot} ok=${status.plugin} label="Plugin" title="Plugin LolMobil-Remote dans le client" />
        </div>
      </div>
    </div>
    <button class="icon-btn" onClick=${onSettings} aria-label="Réglages">⚙</button>
  </header>`;
}

function Settings({ onClose }) {
  const settings = useStore((s) => s.settings);
  const status = useStore((s) => s.status);
  const pcName = useStore((s) => s.pcName);
  const version = useStore((s) => s.version);
  const remoteMode = useStore((s) => s.remote);
  const android = window.LolMobilAndroid;
  return html`<div class="sheet-backdrop" onClick=${onClose}>
    <div class="sheet" onClick=${(e) => e.stopPropagation()}>
      <h2>Réglages</h2>
      <label class="switch-row">
        <span>
          <b>Accepter les parties automatiquement</b>
          <small>Le PC accepte chaque partie trouvée, même téléphone fermé.</small>
        </span>
        <input
          type="checkbox"
          checked=${settings.autoAccept}
          onChange=${(e) => act(saveSettings({ autoAccept: e.target.checked }))}
        />
      </label>
      <div class="info-block">
        <div>PC : <b>${pcName || "?"}</b> · compagnon ${version}</div>
        <div>Connexion : ${remoteMode ? "à distance (relais, chiffrée)" : "Wi-Fi local"}</div>
        ${!remoteMode &&
        html`<div>Accès à distance : ${status.relay ? "prêt ✓" : "non configuré"}</div>`}
        <div>Rose : ${status.rose ? "connecté ✓" : "non détecté"}</div>
        <div>
          Plugin LolMobil-Remote :
          ${status.plugin ? `actif ✓ (v${status.pluginVersion || "?"})` : "inactif"}
        </div>
        ${!status.plugin &&
        html`<p class="muted">
          Sans le plugin, seuls les skins possédés peuvent être choisis. Le compagnon l'installe
          automatiquement dans Rose ; redémarre ensuite le client LoL.
        </p>`}
      </div>
      ${android && html`<${AndroidSettings} android=${android} />`}
      ${android &&
      html`<button class="btn secondary wide" onClick=${() => android.disconnect && android.disconnect()}>
        Changer de PC
      </button>`}
      ${!android &&
      html`<button
        class="btn secondary wide"
        onClick=${() => {
          setToken("");
          forgetRemoteSecret();
          location.reload();
        }}
      >
        Oublier ce PC
      </button>`}
      <button class="btn wide" onClick=${onClose}>Fermer</button>
    </div>
  </div>`;
}

function AndroidSettings({ android }) {
  const [notif, setNotif] = useState(() => {
    try {
      return android.notificationsEnabled();
    } catch (e) {
      return true;
    }
  });
  return html`<div class="android-settings">
    <label class="switch-row">
      <span>
        <b>Notifications</b>
        <small>Partie trouvée (avec Accepter/Refuser) et « à toi de jouer », même écran éteint.</small>
      </span>
      <input
        type="checkbox"
        checked=${notif}
        onChange=${(e) => {
          setNotif(e.target.checked);
          android.setNotifications(e.target.checked);
        }}
      />
    </label>
    ${notif &&
    html`<button class="btn secondary wide" onClick=${() => android.requestBatteryExemption()}>
      Autoriser LolMobil en arrière-plan (batterie)
    </button>`}
  </div>`;
}

function RemotePairScreen() {
  return html`<main class="center-screen">
    <h1 class="logo">LolMobil</h1>
    <p>Accès à distance</p>
    <p class="muted">
      Sur le PC, ouvre la page du compagnon (http://127.0.0.1:8787/pair) et scanne le QR code « Accès à
      distance » avec ce téléphone, ou ouvre le lien affiché dans la fenêtre du compagnon.
    </p>
  </main>`;
}

function PairScreen() {
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await pair(pin.trim());
      connect();
    } catch (err) {
      setError(err.message);
    }
    setBusy(false);
  };
  return html`<main class="center-screen">
    <h1 class="logo">LolMobil</h1>
    <p>Entre le code d'appairage affiché par le compagnon sur ton PC.</p>
    <form onSubmit=${submit} class="pair-form">
      <input
        class="pin-input"
        inputmode="numeric"
        autocomplete="one-time-code"
        maxlength="6"
        placeholder="000000"
        value=${pin}
        onInput=${(e) => setPin(e.target.value.replace(/\D/g, ""))}
      />
      <button class="btn wide" disabled=${busy || pin.length !== 6}>Connecter</button>
      ${error && html`<p class="error">${error}</p>`}
    </form>
  </main>`;
}

function Waiting({ title, text }) {
  return html`<main class="center-screen">
    <div class="spinner"></div>
    <h2>${title}</h2>
    ${text && html`<p class="muted">${text}</p>`}
  </main>`;
}

function InGame({ phase }) {
  const session = useLcu("/lol-gameflow/v1/session");
  const roseSkin = useStore((s) => s.rose["skin-state"]);
  const queue = session && session.gameData && session.gameData.queue;
  return html`<main class="center-screen">
    <h2>${phase === "Reconnect" ? "Partie en cours (déconnecté)" : "Partie en cours"}</h2>
    ${queue && html`<p class="muted">${queue.description || queue.shortName || ""}</p>`}
    ${roseSkin && roseSkin.skinName && html`<p>Skin (Rose) : <b>${roseSkin.skinName}</b></p>`}
    ${phase === "Reconnect" &&
    html`<button class="btn wide" onClick=${() => act(lcu("POST", "/lol-gameflow/v1/reconnect"))}>
      Se reconnecter
    </button>`}
    <p class="muted">Bonne partie !</p>
  </main>`;
}

function EndOfGame({ phase }) {
  return html`<main class="center-screen">
    <h2>${phase === "WaitingForStats" ? "En attente des statistiques…" : "Partie terminée"}</h2>
    <button class="btn wide" onClick=${() => act(lcu("POST", "/lol-lobby/v2/play-again"))}>
      Rejouer
    </button>
    <button
      class="btn secondary wide"
      onClick=${() => act(lcu("DELETE", "/lol-lobby/v2/lobby").catch(() => {}))}
    >
      Retour à l'accueil
    </button>
  </main>`;
}

function Router() {
  const connection = useStore((s) => s.connection);
  const lcuOk = useStore((s) => s.status.lcu);
  const phase = useLcu("/lol-gameflow/v1/gameflow-phase");

  useEffect(() => {
    if (!lcuOk) clearDataCache();
  }, [lcuOk]);

  if (connection === "unpaired") return isRemoteMode() ? html`<${RemotePairScreen} />` : html`<${PairScreen} />`;
  if (connection === "pc-offline")
    return html`<${Waiting}
      title="Ton PC est hors ligne"
      text="Le relais est joignable, mais LolMobil Companion n'y est pas connecté. Lance-le sur le PC (avec l'option -relay)."
    />`;
  if (connection !== "open")
    return html`<${Waiting}
      title="Connexion au PC…"
      text="Vérifie que LolMobil Companion est lancé sur le PC et que le téléphone est sur le même Wi-Fi."
    />`;
  if (!lcuOk)
    return html`<${Waiting} title="En attente du client League of Legends…" text="Lance le client LoL sur le PC." />`;

  switch (phase) {
    case "ReadyCheck":
      return html`<${ReadyCheck} />`;
    case "ChampSelect":
      return html`<${ChampSelect} />`;
    case "GameStart":
    case "InProgress":
    case "Reconnect":
      return html`<${InGame} phase=${phase} />`;
    case "WaitingForStats":
    case "PreEndOfGame":
    case "EndOfGame":
      return html`<${EndOfGame} phase=${phase} />`;
    case undefined:
      return html`<${Waiting} title="Chargement…" />`;
    default:
      return html`<${Home} phase=${phase} />`;
  }
}

function App() {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const connection = useStore((s) => s.connection);
  const phase = useLcu("/lol-gameflow/v1/gameflow-phase");
  useEffect(() => {
    document.body.dataset.phase = phase || "";
    // Keep the phone awake while a decision is needed.
    const android = window.LolMobilAndroid;
    if (android && android.keepScreenOn) {
      android.keepScreenOn(phase === "ReadyCheck" || phase === "ChampSelect");
    }
  }, [phase]);
  return html`
    ${connection !== "unpaired" && html`<${Header} onSettings=${() => setSettingsOpen(true)} />`}
    <${Router} />
    ${settingsOpen && html`<${Settings} onClose=${() => setSettingsOpen(false)} />`}
    <${Toasts} />
  `;
}

window.onAndroidNotice = (msg) => toast(msg && msg.text ? msg.text : String(msg));

render(html`<${App} />`, document.getElementById("app"));
initConnection();

// Exposed for debugging from the console.
window.lolmobil = { store };
