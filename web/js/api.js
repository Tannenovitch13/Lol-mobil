// Connection to the LolMobil Companion and a tiny global store.
//
// The companion relays the League client API (LCU): the page observes LCU
// URIs (current value + live events) and sends requests through one
// websocket.

import { useEffect, useRef, useState } from "../vendor/preact-htm.js";
import { cdragonAsset, getRemoteSecret, isRelayPage, open, remoteKeys, seal, takeRemoteSecretFromUrl } from "./remote.js";

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
export function takeTokenFromUrl() {
  const match = /(?:^|[#&])token=([0-9a-fA-F]{16,})/.exec(location.hash);
  if (match) {
    setToken(match[1]);
    history.replaceState(null, "", location.pathname + location.search);
  }
}

export async function pair(pin) {
  const res = await fetch("/api/pair", {
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

// Wi-Fi (page served by the companion) or remote (page served by a relay,
// with the secret of the remote access link).
let mode = null;
let remote = null; // { room, key }

export async function initConnection() {
  takeTokenFromUrl();
  const gotLink = takeRemoteSecretFromUrl();
  const secret = getRemoteSecret();
  if ((gotLink || secret) && (await isRelayPage())) {
    if (!window.crypto || !crypto.subtle) {
      setStore({ connection: "unpaired" });
      toast("Ce navigateur ne permet pas le chiffrement : utilise une page en https", "error", 8000);
      return;
    }
    try {
      remote = await remoteKeys(secret);
      mode = "remote";
      setStore({ remote: true });
    } catch (e) {
      toast(e.message, "error", 8000);
      setStore({ connection: "unpaired" });
      return;
    }
  } else if (await isRelayPage()) {
    mode = "remote";
    setStore({ remote: true, connection: "unpaired" });
    return;
  } else {
    mode = "lan";
  }
  connect();
}

export const isRemoteMode = () => mode === "remote";

export function connect() {
  started = true;
  if (mode === "remote") {
    connectRemote();
    return;
  }
  const token = getToken();
  if (!token) {
    setStore({ connection: "unpaired" });
    return;
  }
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(token)}`);
  socket = ws;
  setStore({ connection: store.connection === "open" ? "connecting" : store.connection });
  let opened = false;

  ws.onopen = () => {
    opened = true;
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
    if (socket !== ws) return;
    socket = null;
    for (const [, p] of pending) p.reject(new Error("Connexion perdue"));
    pending.clear();
    if (!opened && (await tokenRejected())) {
      setToken("");
      setStore({ connection: "unpaired" });
      return;
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

function connectRemote() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/client/${remote.room}`);
  socket = ws;
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
      if (frame.t === "host") {
        if (frame.online) {
          setStore({ connection: "open" });
          if (observed.size) send({ type: "observe", paths: [...observed] });
        } else {
          setStore({ connection: "pc-offline", status: { ...store.status, lcu: false } });
        }
      }
      return;
    }
    recvQueue = recvQueue
      .then(async () => handleMessage(JSON.parse(await open(remote.key, data))))
      .catch(() => {
        /* not for us / corrupted */
      });
  };
  ws.onclose = () => {
    if (socket !== ws) return;
    socket = null;
    clearInterval(pingTimer);
    for (const [, p] of pending) p.reject(new Error("Connexion perdue"));
    pending.clear();
    setStore({ connection: "closed", status: { ...store.status, lcu: false } });
    setTimeout(connect, retryMs);
    retryMs = Math.min(retryMs * 1.7, 10000);
  };
}

async function tokenRejected() {
  try {
    const res = await fetch(`/api/check?token=${encodeURIComponent(getToken())}`);
    return res.status === 401;
  } catch (e) {
    return false;
  }
}

function send(msg) {
  const ws = socket;
  if (!ws || ws.readyState !== WebSocket.OPEN) return false;
  if (mode === "remote") {
    if (store.connection !== "open") return false;
    const text = JSON.stringify(msg);
    // Keep the order of messages while encrypting.
    sendQueue = sendQueue
      .then(async () => {
        const wire = await seal(remote.key, text);
        if (ws.readyState === WebSocket.OPEN) ws.send(wire);
      })
      .catch(() => {});
    return true;
  }
  ws.send(JSON.stringify(msg));
  return true;
}

function handleMessage(msg) {
  switch (msg.type) {
    case "hello": {
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
  if (path.startsWith("/lol-game-data/assets/")) return mode === "remote" ? cdragonAsset(path) : "/lcu" + path;
  return path;
}
