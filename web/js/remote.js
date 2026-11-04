// Remote access through the LolMobil relay (see internal/relay/crypto.go).
// The secret comes from the pairing link fragment (#k=...), stays on the
// phone and encrypts everything end to end (AES-256-GCM).

const SECRET_KEY = "lolmobil.remote";
const enc = new TextEncoder();
const dec = new TextDecoder();

export function takeRemoteSecretFromUrl() {
  const match = /(?:^|[#&])k=([A-Za-z0-9_-]{43})(?:&|$)/.exec(location.hash);
  if (!match) return false;
  try {
    localStorage.setItem(SECRET_KEY, match[1]);
  } catch (e) {
    /* private mode: kept for this visit only */
    sessionSecret = match[1];
  }
  history.replaceState(null, "", location.pathname + location.search);
  return true;
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
  try {
    localStorage.removeItem(SECRET_KEY);
  } catch (e) {
    /* ignore */
  }
}

// True when this page is served by a LolMobil relay (not by the companion).
export async function isRelayPage() {
  try {
    const res = await fetch("/relay-info", { cache: "no-store" });
    if (!res.ok) return false;
    const info = await res.json();
    return info && info.app === "lolmobil-relay";
  } catch (e) {
    return false;
  }
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

export async function seal(key, text) {
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const cipher = new Uint8Array(await crypto.subtle.encrypt({ name: "AES-GCM", iv: nonce }, key, enc.encode(text)));
  const out = new Uint8Array(12 + cipher.length);
  out.set(nonce, 0);
  out.set(cipher, 12);
  return toBase64(out);
}

export async function open(key, wire) {
  const data = fromBase64(wire);
  const plain = await crypto.subtle.decrypt({ name: "AES-GCM", iv: data.subarray(0, 12) }, key, data.subarray(12));
  return dec.decode(plain);
}

// Game images from Community Dragon, which mirrors the client's
// /lol-game-data/assets tree (lower case), so remote phones do not pull
// every icon through the relay.
const CDRAGON = "https://raw.communitydragon.org/latest/plugins/rcp-be-lol-game-data/global/default/";
export function cdragonAsset(path) {
  return CDRAGON + path.slice("/lol-game-data/assets/".length).toLowerCase();
}
