// Remote access through the LolMobil relay (see internal/relay/crypto.go).
// The secret comes from the pairing link fragment (#k=...), stays on the
// phone and encrypts everything end to end (AES-256-GCM).

const SECRET_KEY = "lolmobil.remote";
const enc = new TextEncoder();
const dec = new TextDecoder();

const BROKERS_KEY = "lolmobil.brokers";

export function takeRemoteSecretFromUrl() {
  const match = /(?:^|[#&])k=([A-Za-z0-9_-]{43})(?:&|$)/.exec(location.hash);
  if (!match) return false;
  const brokers = /(?:^|[#&])m=([^&]+)/.exec(location.hash);
  sessionSecret = match[1];
  try {
    localStorage.setItem(SECRET_KEY, match[1]);
    if (brokers) localStorage.setItem(BROKERS_KEY, decodeURIComponent(brokers[1]));
    else localStorage.removeItem(BROKERS_KEY);
  } catch (e) {
    /* private mode: kept for this visit only */
  }
  sessionBrokers = brokers ? decodeURIComponent(brokers[1]) : "";
  history.replaceState(null, "", location.pathname + location.search);
  return true;
}

let sessionBrokers = "";

// Brokers chosen in the link (m=...), else the public defaults.
export function getBrokers(defaults) {
  let list = sessionBrokers;
  try {
    list = localStorage.getItem(BROKERS_KEY) || list;
  } catch (e) {
    /* ignore */
  }
  const custom = (list || "").split(",").map((s) => s.trim()).filter(Boolean);
  return custom.length ? custom : defaults;
}

let sessionSecret = "";

export function getRemoteSecret() {
  try {
    return localStorage.getItem(SECRET_KEY) || sessionSecret;
  } catch (e) {
    return sessionSecret;
  }
}

export function forgetRemoteSecret() {
  sessionSecret = "";
  sessionBrokers = "";
  try {
    localStorage.removeItem(SECRET_KEY);
    localStorage.removeItem(BROKERS_KEY);
  } catch (e) {
    /* ignore */
  }
}

// Who serves this page: "companion" (Wi-Fi), "relay" (your Cloudflare
// relay) or "static" (the Android app, GitHub...: remote access goes
// through the public MQTT brokers).
export async function pageHost() {
  try {
    const res = await fetch("/relay-info", { cache: "no-store" });
    if (res.ok) {
      const info = await res.json();
      if (info && info.app === "lolmobil-relay") return "relay";
      if (info && info.app === "lolmobil-companion") return "companion";
    }
  } catch (e) {
    /* not ours */
  }
  return "static";
}

function fromBase64Url(text) {
  const b64 = text.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (text.length % 4)) % 4);
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
}

function toBase64(bytes) {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return btoa(s);
}

function fromBase64(text) {
  return Uint8Array.from(atob(text), (c) => c.charCodeAt(0));
}

const hex = (bytes) => [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");

async function derive(label, secret) {
  const data = new Uint8Array(label.length + secret.length);
  data.set(enc.encode(label), 0);
  data.set(secret, label.length);
  return new Uint8Array(await crypto.subtle.digest("SHA-256", data));
}

// { room, hostAuth, key } from the base64url secret.
export async function remoteKeys(secretText) {
  const secret = fromBase64Url(secretText);
  if (secret.length !== 32) throw new Error("Lien d'accès à distance invalide");
  const room = hex(await derive("lolmobil-room", secret)).slice(0, 32);
  const hostAuth = hex(await derive("lolmobil-host", secret));
  const key = await crypto.subtle.importKey("raw", await derive("lolmobil-key", secret), "AES-GCM", false, [
    "encrypt",
    "decrypt",
  ]);
  return { room, hostAuth, key };
}

async function transform(data, stream) {
  return new Uint8Array(await new Response(new Blob([data]).stream().pipeThrough(stream)).arrayBuffer());
}

// Plaintext = flag byte (0 raw, 1 gzip) + JSON, like internal/relay/crypto.go.
export async function seal(key, text) {
  const json = enc.encode(text);
  let payload;
  if (json.length > 1024 && typeof CompressionStream !== "undefined") {
    const gz = await transform(json, new CompressionStream("gzip"));
    payload = new Uint8Array(gz.length + 1);
    payload[0] = 1;
    payload.set(gz, 1);
  } else {
    payload = new Uint8Array(json.length + 1);
    payload.set(json, 1);
  }
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const cipher = new Uint8Array(await crypto.subtle.encrypt({ name: "AES-GCM", iv: nonce }, key, payload));
  const out = new Uint8Array(12 + cipher.length);
  out.set(nonce, 0);
  out.set(cipher, 12);
  return toBase64(out);
}

export async function open(key, wire) {
  const data = fromBase64(wire);
  const plain = new Uint8Array(
    await crypto.subtle.decrypt({ name: "AES-GCM", iv: data.subarray(0, 12) }, key, data.subarray(12))
  );
  if (plain[0] === 1) return dec.decode(await transform(plain.subarray(1), new DecompressionStream("gzip")));
  return dec.decode(plain.subarray(1));
}

// Game images from Community Dragon, which mirrors the client's
// /lol-game-data/assets tree (lower case), so remote phones do not pull
// every icon through the relay.
const CDRAGON = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/default/";
export function cdragonAsset(path) {
  return CDRAGON + path.slice("/lol-game-data/assets/".length).toLowerCase();
}
