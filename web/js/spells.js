import { html, useState } from "../vendor/preact-htm.js";
import { act, asset, lcu, useLcu } from "./api.js";
import { loadSpells, useData } from "./data.js";

export function SpellsTab({ myCell }) {
  const spells = useData(loadSpells);
  const session = useLcu("/lol-gameflow/v1/session");
  const [slot, setSlot] = useState(1);
  if (!spells) return html`<div class="spinner"></div>`;

  const queue = session && session.gameData && session.gameData.queue;
  const gameMode = (queue && queue.gameMode) || (session && session.map && session.map.gameMode) || "CLASSIC";
  const available = spells.filter((s) => !s.gameModes || !s.gameModes.length || s.gameModes.includes(gameMode));
  const byId = new Map(spells.map((s) => [s.id, s]));
  const s1 = myCell.spell1Id;
  const s2 = myCell.spell2Id;

  const choose = (id) => {
    let spell1Id = s1;
    let spell2Id = s2;
    if (slot === 1) {
      if (id === s2) spell2Id = s1;
      spell1Id = id;
    } else {
      if (id === s1) spell1Id = s2;
      spell2Id = id;
    }
    act(lcu("PATCH", "/lol-champ-select/v1/session/my-selection", { spell1Id, spell2Id }));
    setSlot(slot === 1 ? 2 : 1);
  };

  const SlotButton = ({ n, id }) => {
    const spell = byId.get(id);
    return html`<button class="spell-slot ${slot === n ? "active" : ""}" onClick=${() => setSlot(n)}>
      ${spell ? html`<img src=${asset(spell.iconPath)} alt="" />` : html`<span class="rune-icon empty"></span>`}
      <span>${n === 1 ? "D" : "F"} · ${spell ? spell.name : "—"}</span>
    </button>`;
  };

  return html`<div class="spells">
    <div class="spell-slots">
      <${SlotButton} n=${1} id=${s1} />
      <${SlotButton} n=${2} id=${s2} />
    </div>
    <p class="small muted">Touche un emplacement puis un sort.</p>
    <div class="spell-grid">
      ${available.map(
        (s) => html`<button
          key=${s.id}
          class="spell ${s.id === s1 || s.id === s2 ? "active" : ""}"
          onClick=${() => choose(s.id)}
        >
          <img loading="lazy" src=${asset(s.iconPath)} alt="" />
          <span>${s.name}</span>
        </button>`
      )}
    </div>
  </div>`;
}
