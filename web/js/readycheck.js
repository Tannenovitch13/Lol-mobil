import { html, useEffect, useState } from "../vendor/preact-htm.js";
import { act, lcu, useLcu, useStore } from "./api.js";

const READY = "/lol-matchmaking/v1/ready-check";
const TOTAL = 12; // seconds the client gives to accept

export function ReadyCheck() {
  const rc = useLcu(READY);
  const receivedAt = useStore((s) => s.lcuReceivedAt[READY]);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    if (navigator.vibrate) navigator.vibrate([300, 150, 300, 150, 300]);
    const t = setInterval(() => setNow(Date.now()), 200);
    return () => clearInterval(t);
  }, []);

  const response = rc ? rc.playerResponse : "None";
  const elapsed = rc ? (rc.timer || 0) + (now - (receivedAt || now)) / 1000 : 0;
  const left = Math.max(0, TOTAL - elapsed);

  return html`<main class="ready-check">
    <div class="ready-ring" style=${`--p:${left / TOTAL}`}>
      <span>${Math.ceil(left)}</span>
    </div>
    <h1>Partie trouvée !</h1>
    ${response === "Accepted" && html`<p class="ok-text">Acceptée ✓ — en attente des autres joueurs…</p>`}
    ${response === "Declined" && html`<p class="error">Refusée</p>`}
    ${response === "None" &&
    html`<div class="ready-buttons">
      <button class="btn huge accept" onClick=${() => act(lcu("POST", `${READY}/accept`))}>
        Accepter
      </button>
      <button class="btn secondary decline" onClick=${() => act(lcu("POST", `${READY}/decline`))}>
        Refuser
      </button>
    </div>`}
  </main>`;
}
