// Connection to the LolMobil Companion and a tiny global store.
//
// The companion relays the League client API (LCU): the page observes LCU
// URIs (current value + live events) and sends requests through one
// websocket.

import { useEffect, useRef, useState } from "../vendor/preact-htm.js";
import {
  cdragonAsset,
  getBrokers,
  getRemoteSecret,
  open,
  pageHost,
  remoteKeys,
  seal,
  takeRemoteSecretFromUrl,
} from "./remote.js";
import { DEFAULT_BROKERS, MqttClient } from "./mqtt.js";

const TOKEN_KEY = "lolmobil.token";

// ------------------------------------------------------------------ store

const listeners = new Set();
export const store = {
  connection: "connecting", // connecting | open | closed | unpaired | pc-offline
  remote: false, // connected through the relay (remote access)
  status: { lcu: false, rose: false, plugin: false, pluginVersion: "" },
  settings: { autoAccept: false },
  pcName: "",
  version: "",
  lcu: {}, // uri -> data (null when absent)
  lcuReceivedAt: {}, // uri -> Date.now() of the last update
  rose: {}, // Rose message type -> last message
  carousel: null, // skin name shown in the client's carousel (plugin)
  toasts: [],
};

let notifyQueued = false;
function notify() {
  if (notifyQueued) return;
  notifyQueued = true;
  queueMicrotask(() => {
    notifyQueued = false;
    for (const fn of listeners) fn();
  });
}

export function setStore(patch) {
  Object.assign(store, patch);
  notify();
}

// Re-render the calling component whenever the selected value changes.
export function useStore(select) {
  const [, force] = useState(0);
  const selectRef = useRef(select);
  selectRef.current = select;
  const value = select(store);
  // The value this render used: a change made before the effect subscribes
  // must still trigger a render.
  const renderedRef = useRef(value);
  renderedRef.current = value;
  useEffect(() => {
    const fn = () => {
      const next = selectRef.current(store);
      if (next !== renderedRef.current) {
        renderedRef.current = next;
        force((n) => n + 1);
      }
    };
    listeners.add(fn);
    fn();
    return () => listeners.delete(fn);
  }, []);
  return value;
}

export const useLcu = (uri) => useStore((s) => s.lcu[uri]);

let toastId = 0;
export function toast(text, kind = "info", ms = kind === "ok" ? 2200 : 3500) {
  const id = ++toastId;
  setStore({ toasts: [...store.toasts, { id, text, kind }] });
  setTimeout(() => setStore({ toasts: store.toasts.filter((t) => t.id !== id) }), ms);
}

// ------------------------------------------------------------------ token

export function getToken() {
  try {
    return localStorage.getItem(TOKEN_KEY) || "";
  } catch (e) {
    return "";
  }
}

export function setToken(token) {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token);
    else localStorage.removeItem(TOKEN_KEY);
  } catch (e) {
    /* private mode */
  }
  const android = window.LolMobilAndroid;
  if (android && token && android.onPaired) {
    try {
      android.onPaired(token);
    } catch (e) {
      /* ignore */
    }
  }
}

// A token in the URL fragment (QR code, Android app) wins.
// Address of the companion when this page does not come from it (the
// interface bundled in the Android app passes #lan=http://<pc>:<port>), so
// a new app version updates the interface without updating the PC.
let lanBase = "";
const LAN_KEY = "lolmobil.lanBase";

const apiURL = (path) => lanBase + path;

export function takeTokenFromUrl() {
  const match = /(?:^|[#&])token=([0-9a-fA-F]{16,})/.exec(location.hash);
  const lan = /(?:^|[#&])lan=([^&]+)/.exec(location.hash);
  if (!lan) {
    // A reload of the bundled page keeps talking to the same PC. (Pages
    // served by the companion have their own storage: nothing there.)
    try {
      lanBase = localStorage.getItem(LAN_KEY) || "";
    } catch (e) {
      /* ignore */
    }
  }
  if (lan) {
    lanBase = decodeURIComponent(lan[1]).replace(/\/+$/, "");
    try {
      localStorage.setItem(LAN_KEY, lanBase);
    } catch (e) {
      /* ignore */
    }
  }
  if (match) setToken(match[1]);
  if (match || lan) history.replaceState(null, "", location.pathname + location.search);
}

export async function pair(pin) {
  const res = await fetch(apiURL("/api/pair"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ pin, device: navigator.userAgent.slice(0, 80) }),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `Erreur ${res.status}`);
  setToken(body.token);
  return body;
}

// ------------------------------------------------------------- websocket

let socket = null;
let nextId = 1;
const pending = new Map();
const observed = new Set();
let retryMs = 800;
let started = false;

// "lan": page served by the companion on the Wi-Fi.
// "relay": page served by your Cloudflare relay (remote access).
// "mqtt": page from the Android app or a static copy; remote access
//         through public MQTT brokers. Both remote modes are end-to-end
//         encrypted with the secret of the remote access link.
let mode = null;
let remote = null; // { room, key }

export async function initConnection() {
  takeTokenFromUrl();
  takeRemoteSecretFromUrl();
  if (lanBase) {
    // Bundled interface on the Wi-Fi: talk to the companion directly.
    mode = "lan";
    connect();
    return;
  }
  const host = await pageHost();
  if (host === "companion") {
    mode = "lan";
    connect();
    return;
  }
  mode = host === "relay" ? "relay" : "mqtt";
  setStore({ remote: true });
  const secret = getRemoteSecret();
  if (!secret) {
    setStore({ connection: "unpaired" });
    return;
  }
  if (!window.crypto || !crypto.subtle) {
    setStore({ connection: "unpaired" });
    toast("Ce navigateur ne permet pas le chiffrement : utilise une page en https", "error", 8000);
    return;
  }
  try {
    remote = await remoteKeys(secret);
  } catch (e) {
    toast(e.message, "error", 8000);
    setStore({ connection: "unpaired" });
    return;
  }
  connect();
}

export const isRemoteMode = () => mode === "relay" || mode === "mqtt";

export function connect() {
  started = true;
  if (mode === "relay") {
    connectRelay();
    return;
  }
  if (mode === "mqtt") {
    connectMqtt();
    return;
  }
  const token = getToken();
  if (!token) {
    setStore({ connection: "unpaired" });
    return;
  }
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const wsBase = lanBase ? lanBase.replace(/^http/, "ws") : `${proto}://${location.host}`;
  const ws = new WebSocket(`${wsBase}/ws?token=${encodeURIComponent(token)}`);
  socket = ws;
  setStore({ connection: store.connection === "open" ? "connecting" : store.connection });
  let opened = false;
  // Away from the PC's Wi-Fi a connection can hang for a minute: give up
  // sooner, so the app can switch to remote access.
  const openTimer = setTimeout(() => {
    if (!opened) ws.close();
  }, 5000);

  ws.onopen = () => {
    clearTimeout(openTimer);
    opened = true;
    lanFailures = 0;
    retryMs = 800;
    setStore({ connection: "open" });
    if (observed.size) send({ type: "observe", paths: [...observed] });
  };
  ws.onmessage = (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch (e) {
      return;
    }
    handleMessage(msg);
  };
  ws.onclose = async () => {
    clearTimeout(openTimer);
    if (socket !== ws) return;
    socket = null;
    for (const [, p] of pending) p.reject(new Error("Connexion perdue"));
    pending.clear();
    if (!opened && (await tokenRejected())) {
      setToken("");
      setStore({ connection: "unpaired" });
      return;
    }
    if (lanBase && !opened) {
      lanFailures++;
      const android = window.LolMobilAndroid;
      if (lanFailures >= 2 && android) {
        const reachable = await companionReachable();
        // Not on the PC's Wi-Fi (any more): the app switches to remote
        // access when it has the link.
        if (!reachable && hasRemoteAccess() && android.retry) {
          android.retry();
          return;
        }
        // The PC answers but this page cannot reach it: let the app load
        // the companion's own page instead.
        if (reachable && lanFailures >= 3 && android.useCompanionPage) {
          android.useCompanionPage();
          return;
        }
      }
    }
    setStore({ connection: "closed", status: { ...store.status, lcu: false } });
    setTimeout(connect, retryMs);
    retryMs = Math.min(retryMs * 1.7, 8000);
  };
}

// ------------------------------------------------------------ remote mode

let sendQueue = Promise.resolve();
let recvQueue = Promise.resolve();
let pingTimer = null;
// Writes one encrypted message to the PC (set by the remote transport).
let sendWire = null;

function receiveWire(wire) {
  recvQueue = recvQueue
    .then(async () => handleMessage(JSON.parse(await open(remote.key, wire))))
    .catch(() => {
      /* not for us / corrupted */
    });
}

function hostPresence(online) {
  if (online) {
    setStore({ connection: "open" });
    if (observed.size) send({ type: "observe", paths: [...observed] });
  } else {
    setStore({ connection: "pc-offline", status: { ...store.status, lcu: false } });
  }
}

function lostConnection() {
  clearInterval(pingTimer);
  if (mode === "mqtt") closeLinks();
  sendWire = null;
  for (const [, p] of pending) p.reject(new Error("Connexion perdue"));
  pending.clear();
  setStore({ connection: "closed", status: { ...store.status, lcu: false } });
  setTimeout(connect, retryMs);
  retryMs = Math.min(retryMs * 1.7, 10000);
}

// ---- through public MQTT brokers
//
// Every broker is dialed at once. The first one through which the PC
// answers a ping carries the session: the fastest path right now (a broker
// across the ocean adds a lot to every tap). The others stay connected,
// listening to the PC's presence only, and take over at once if it drops.

let mqttGen = 0;
let mqttId = "";
let links = []; // { url, client, online, known, probing, pingId, buffer }
let activeLink = null;
const redialMs = new Map(); // broker url -> next delay
const randomId = () => [...crypto.getRandomValues(new Uint8Array(6))].map((b) => b.toString(16).padStart(2, "0")).join("");
const mqttPrefix = () => `lolmobil/v1/${remote.room}`;
const frame = (t, d) => JSON.stringify(d === undefined ? { t, c: mqttId } : { t, c: mqttId, d });

// Broker carrying the session (for tests and debugging).
export const activeBroker = () => (activeLink ? activeLink.url : "");

function closeLinks() {
  const hostTopic = `${mqttPrefix()}/host`;
  for (const link of links) {
    if (link === activeLink || link.probing) link.client.publish(hostTopic, frame("close"));
    link.client.onclose = null;
    link.client.close();
  }
  links = [];
  activeLink = null;
  sendWire = null;
}

function connectMqtt() {
  const gen = ++mqttGen;
  closeLinks();
  mqttId = randomId();
  const brokers = getBrokers(DEFAULT_BROKERS);
  let failed = 0;
  for (const url of brokers) {
    dialLink(url, gen).catch(() => {
      failed++;
      if (gen === mqttGen && failed === brokers.length && !links.length) {
        // No broker reachable: try again later.
        setStore({ connection: "closed" });
        setTimeout(() => gen === mqttGen && connect(), retryMs);
        retryMs = Math.min(retryMs * 1.7, 15000);
      }
    });
  }
}

async function dialLink(url, gen) {
  const prefix = mqttPrefix();
  const client = new MqttClient(url, {
    clientId: "lmc-" + mqttId,
    keepAlive: 30,
    // Tells the PC this session is over if the phone vanishes.
    will: { topic: `${prefix}/host`, payload: frame("close"), retain: false },
  });
  await client.connect();
  if (gen !== mqttGen) {
    client.close();
    return;
  }
  redialMs.delete(url);
  const link = { url, client, online: false, known: false, probing: false, pingId: 0, buffer: [] };
  links.push(link);
  client.onmessage = (topic, text) => onLinkMessage(link, topic, text);
  client.onclose = () => onLinkClosed(link, gen);
  client.subscribe(`${prefix}/c/${mqttId}`);
  // Retained: tells right away whether the PC is online on this broker.
  client.subscribe(`${prefix}/presence`);
  if (store.connection === "closed") setStore({ connection: "connecting" });
}

function onLinkMessage(link, topic, text) {
  if (topic === `${mqttPrefix()}/presence`) {
    link.online = text === "1";
    link.known = true;
    if (link === activeLink) {
      if (link.online) {
        // The PC came back on this broker: new session.
        link.client.publish(`${mqttPrefix()}/host`, frame("open"));
        hostPresence(true);
      } else {
        switchLink();
      }
      return;
    }
    if (!activeLink) {
      if (link.online) probeLink(link);
      else if (links.every((l) => l.known && !l.online)) hostPresence(false);
    }
    return;
  }
  if (link === activeLink) {
    receiveWire(text);
    return;
  }
  if (!link.probing) return;
  link.buffer.push(text);
  open(remote.key, text)
    .then((plain) => {
      const msg = JSON.parse(plain);
      if (msg.type === "pong" && msg.id === link.pingId && link.probing && !activeLink) activate(link);
    })
    .catch(() => {});
}

// Opens a session through this broker and times a ping through the PC.
async function probeLink(link) {
  if (link.probing) return;
  link.probing = true;
  link.buffer = [];
  link.pingId = nextId++;
  const hostTopic = `${mqttPrefix()}/host`;
  link.client.publish(hostTopic, frame("open"));
  const wire = await seal(remote.key, JSON.stringify({ type: "ping", id: link.pingId }));
  link.client.publish(hostTopic, frame("msg", wire));
}

function activate(link) {
  const hostTopic = `${mqttPrefix()}/host`;
  activeLink = link;
  socket = link.client;
  retryMs = 800;
  sendWire = (wire) => link.client.publish(hostTopic, frame("msg", wire));
  // The slower brokers: free their session on the PC, keep them as standbys.
  for (const other of links) {
    if (other !== link && other.probing) {
      other.client.publish(hostTopic, frame("close"));
      other.probing = false;
      other.buffer = [];
    }
  }
  const buffered = link.buffer;
  link.probing = false;
  link.buffer = [];
  for (const text of buffered) receiveWire(text); // hello, pong
  hostPresence(true);
}

// The active broker lost the PC or the connection: another one at once.
function switchLink() {
  const old = activeLink;
  activeLink = null;
  sendWire = null;
  for (const [, p] of pending) p.reject(new Error("Connexion perdue"));
  pending.clear();
  const next = links.find((l) => l !== old && l.online);
  if (next) {
    next.client.publish(`${mqttPrefix()}/host`, frame("open"));
    next.probing = true;
    activate(next);
    return;
  }
  if (links.some((l) => l.online === false && l.known) && !links.some((l) => !l.known)) {
    hostPresence(false);
  } else {
    setStore({ connection: "connecting", status: { ...store.status, lcu: false } });
  }
}

function onLinkClosed(link, gen) {
  links = links.filter((l) => l !== link);
  if (gen !== mqttGen) return;
  if (link === activeLink) switchLink();
  if (!links.length) {
    // Every broker dropped (the phone's network, most likely): start over.
    lostConnection();
    return;
  }
  // Dial this broker again in the background.
  const delay = redialMs.get(link.url) || 3000;
  redialMs.set(link.url, Math.min(delay * 2, 60000));
  setTimeout(() => {
    if (gen === mqttGen) dialLink(link.url, gen).catch(() => {});
  }, delay);
}

// ---- through your Cloudflare relay

function connectRelay() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/client/${remote.room}`);
  socket = ws;
  sendWire = (wire) => ws.readyState === WebSocket.OPEN && ws.send(wire);
  ws.onopen = () => {
    retryMs = 800;
    clearInterval(pingTimer);
    pingTimer = setInterval(() => ws.readyState === WebSocket.OPEN && ws.send("ping"), 25000);
  };
  ws.onmessage = (event) => {
    const data = event.data;
    if (data === "pong") return;
    if (data.charAt(0) === "{") {
      // Relay control frame.
      let frame;
      try {
        frame = JSON.parse(data);
      } catch (e) {
        return;
      }
      if (frame.t === "host") hostPresence(frame.online);
      return;
    }
    receiveWire(data);
  };
  ws.onclose = () => {
    if (socket !== ws) return;
    socket = null;
    lostConnection();
  };
}

let lanFailures = 0;

function hasRemoteAccess() {
  const android = window.LolMobilAndroid;
  try {
    return Boolean(android && android.savedPc && JSON.parse(android.savedPc()).remote);
  } catch (e) {
    return false;
  }
}

// The PC's Wi-Fi details, sent through remote access: the app connects
// directly next time the phone is on the same network (much faster).
function rememberLan(lan) {
  const android = window.LolMobilAndroid;
  if (!android || !android.rememberLan || !lan) return;
  try {
    android.rememberLan(JSON.stringify(lan));
  } catch (e) {
    /* older app */
  }
}

// fetch that gives up after a few seconds (unreachable address).
function quickFetch(url, ms = 2500) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), ms);
  return fetch(url, { cache: "no-store", signal: ctrl.signal }).finally(() => clearTimeout(timer));
}

async function companionReachable() {
  try {
    const res = await quickFetch(apiURL("/api/info"));
    return res.ok;
  } catch (e) {
    return false;
  }
}

async function tokenRejected() {
  try {
    const res = await quickFetch(apiURL(`/api/check?token=${encodeURIComponent(getToken())}`));
    return res.status === 401;
  } catch (e) {
    return false;
  }
}

function send(msg) {
  if (isRemoteMode()) {
    const write = sendWire;
    if (!write || store.connection !== "open") return false;
    const text = JSON.stringify(msg);
    // Keep the order of messages while encrypting.
    sendQueue = sendQueue
      .then(async () => write(await seal(remote.key, text)))
      .catch(() => {});
    return true;
  }
  const ws = socket;
  if (!ws || ws.readyState !== WebSocket.OPEN) return false;
  ws.send(JSON.stringify(msg));
  return true;
}

function handleMessage(msg) {
  switch (msg.type) {
    case "hello": {
      if (isRemoteMode() && msg.lan) rememberLan(msg.lan);
      const rose = {};
      for (const [k, v] of Object.entries(msg.rose || {})) rose[k] = v;
      setStore({
        status: msg.status,
        settings: msg.settings,
        pcName: msg.name,
        version: msg.version,
        rose,
      });
      break;
    }
    case "status": {
      const wasConnected = store.status.lcu;
      const patch = { status: msg };
      if (wasConnected && !msg.lcu) {
        patch.lcu = {};
      }
      if (!msg.rose) patch.rose = {};
      if (!msg.plugin) patch.carousel = null;
      setStore(patch);
      break;
    }
    case "settings":
      setStore({ settings: msg.settings });
      break;
    case "event":
      store.lcu = { ...store.lcu, [msg.uri]: msg.eventType === "Delete" ? null : msg.data };
      store.lcuReceivedAt = { ...store.lcuReceivedAt, [msg.uri]: Date.now() };
      notify();
      break;
    case "rose":
      if (msg.msg && msg.msg.type) {
        setStore({ rose: { ...store.rose, [msg.msg.type]: msg.msg } });
      }
      break;
    case "plugin":
      if (msg.msg && msg.msg.type === "carousel") setStore({ carousel: msg.msg.shown || null });
      break;
    case "notice":
      toast(msg.text, "info");
      break;
    case "response": {
      const p = pending.get(msg.id);
      if (p) {
        pending.delete(msg.id);
        p.resolve(msg);
      }
      break;
    }
  }
}

// Send a message expecting a {type:"response"} answer.
export function call(message, timeoutMs = 20000) {
  return new Promise((resolve, reject) => {
    const id = nextId++;
    if (!send({ ...message, id })) {
      reject(new Error("Pas connecté au PC"));
      return;
    }
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error("Pas de réponse du PC"));
    }, timeoutMs);
    pending.set(id, {
      resolve: (v) => {
        clearTimeout(timer);
        resolve(v);
      },
      reject: (e) => {
        clearTimeout(timer);
        reject(e);
      },
    });
  });
}

// LCU request. Resolves with the body of a 2xx answer, rejects otherwise.
export async function lcu(method, path, body) {
  const res = await call({ type: "request", method, path, body });
  if (res.error || res.status < 200 || res.status > 299) {
    const detail =
      res.error || (res.body && (res.body.message || res.body.errorCode)) || `HTTP ${res.status}`;
    const err = new Error(detail);
    err.status = res.status;
    err.body = res.body;
    throw err;
  }
  return res.body;
}

export const get = (path) => lcu("GET", path);

// Run an action and show its error as a toast.
export async function act(promiseOrFn, okText) {
  try {
    const result = await (typeof promiseOrFn === "function" ? promiseOrFn() : promiseOrFn);
    if (okText) toast(okText, "ok");
    return result;
  } catch (e) {
    toast(e.message || String(e), "error", 5000);
    return undefined;
  }
}

export function observe(paths) {
  const fresh = paths.filter((p) => !observed.has(p));
  fresh.forEach((p) => observed.add(p));
  if (fresh.length && started) send({ type: "observe", paths: fresh });
}

// Re-read a URI now (e.g. after a request whose change has no event).
export async function refresh(path) {
  try {
    const data = await get(path);
    store.lcu = { ...store.lcu, [path]: data };
    store.lcuReceivedAt = { ...store.lcuReceivedAt, [path]: Date.now() };
  } catch (e) {
    if (e.status === 404) store.lcu = { ...store.lcu, [path]: null };
  }
  notify();
}

export async function saveSettings(patch) {
  const res = await call({ type: "settings", ...patch });
  if (res.body) setStore({ settings: res.body });
}

export function selectSkin(skin) {
  return call({ type: "select-skin", ...skin }, 20000);
}

export function selectChroma(chroma) {
  return call({ type: "select-chroma", ...chroma });
}

// Asset URL served by the companion (images from the client).
export function asset(path) {
  if (!path) return "";
  if (path.startsWith("/lol-game-data/assets/")) return isRemoteMode() ? cdragonAsset(path) : lanBase + "/lcu" + path;
  return path;
}
