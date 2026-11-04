import { html, useEffect, useState } from "../vendor/preact-htm.js";
import { lcu, refresh, store, toast, useLcu, useStore } from "./api.js";

const READY = "/lol-matchmaking/v1/ready-check";
const TOTAL = 12; // seconds the client gives to accept

export function ReadyCheck() {
  const rc = useLcu(READY);
  const receivedAt = useStore((s) => s.lcuReceivedAt[READY]);
  const [now, setNow] = useState(Date.now());
  const [sending, setSending] = useState(null); // "accept" | "decline"

  useEffect(() => {
    if (navigator.vibrate) navigator.vibrate([300, 150, 300, 150, 300]);
    const t = setInterval(() => setNow(Date.now()), 200);
    return () => clearInterval(t);
  }, []);

  const response = rc ? rc.playerResponse : "None";
  const elapsed = rc ? (rc.timer || 0) + (now - (receivedAt || now)) / 1000 : 0;
  const left = Math.max(0, TOTAL - elapsed);

  // One tap = one answer. The client sometimes refuses an answer it already
  // has (double tap, auto-accept, button pressed on the PC) or fails once:
  // check the state and try again before reporting an error.
  const answer = async (action) => {
    if (sending) return;
    setSending(action);
    const wanted = action === "accept" ? "Accepted" : "Declined";
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        await lcu("POST", `${READY}/${action}`);
        break;
      } catch (e) {
        await new Promise((r) => setTimeout(r, 400));
        await refresh(READY);
        const now = store.lcu[READY];
        if (!now || now.state !== "InProgress" || now.playerResponse === wanted) break;
        if (attempt === 2) toast(e.message || String(e), "error", 5000);
      }
    }
    setSending(null);
  };

  return html`<main class="ready-check">
    <div class="ready-ring" style=${`--p:${left / TOTAL}`}>
      <span>${Math.ceil(left)}</span>
    </div>
    <h1>Partie trouvée !</h1>
    ${response === "Accepted" && html`<p class="ok-text">Acceptée ✓ — en attente des autres joueurs…</p>`}
    ${response === "Declined" && html`<p class="error">Refusée</p>`}
    ${response === "None" &&
    html`<div class="ready-buttons">
      <button class="btn huge accept" disabled=${!!sending} onClick=${() => answer("accept")}>
        ${sending === "accept" ? "Acceptation…" : "Accepter"}
      </button>
      <button class="btn secondary decline" disabled=${!!sending} onClick=${() => answer("decline")}>
        Refuser
      </button>
    </div>`}
  </main>`;
}
