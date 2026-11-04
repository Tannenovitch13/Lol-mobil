import { html, useEffect, useMemo, useState } from "../vendor/preact-htm.js";
import { act, lcu, observe, refresh, useLcu, useStore } from "./api.js";
import { championIcon, loadChampions, positionLabel, searchKey, useData } from "./data.js";
import { SkinsTab } from "./skins.js";
import { RunesTab } from "./runes.js";
import { SpellsTab } from "./spells.js";

const SESSION = "/lol-champ-select/v1/session";
const PICKABLE = "/lol-champ-select/v1/pickable-champion-ids";
const BANNABLE = "/lol-champ-select/v1/bannable-champion-ids";

observe([PICKABLE, BANNABLE]);

function useNow(ms = 250) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

function Timer({ timer }) {
  const receivedAt = useStore((s) => s.lcuReceivedAt[SESSION]);
  const now = useNow();
  if (!timer) return null;
  if (timer.isInfinite) return html`<span class="timer">∞</span>`;
  const left = Math.max(0, (timer.adjustedTimeLeftInPhase || 0) - (now - (receivedAt || now)));
  const s = Math.ceil(left / 1000);
  return html`<span class="timer ${s <= 10 ? "urgent" : ""}">${s}</span>`;
}

export function ChampionGrid({ ids, selectedId, onPick, emptyText }) {
  const champions = useData(loadChampions);
  const [query, setQuery] = useState("");
  const list = useMemo(() => {
    if (!champions) return [];
    const key = searchKey(query);
    return (ids || [])
      .map((id) => champions.get(id))
      .filter((c) => c && (!key || searchKey(c.name).includes(key) || searchKey(c.alias).includes(key)))
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [champions, ids, query]);

  if (!champions) return html`<div class="spinner"></div>`;
  return html`<div class="champion-grid-wrap">
    <input
      class="search"
      type="search"
      placeholder="Rechercher un champion…"
      value=${query}
      onInput=${(e) => setQuery(e.target.value)}
    />
    <div class="champion-grid">
      ${list.map(
        (c) => html`<button
          key=${c.id}
          class="champ ${c.id === selectedId ? "selected" : ""}"
          onClick=${() => onPick(c.id)}
        >
          <img loading="lazy" src=${championIcon(c.id)} alt="" />
          <span>${c.name}</span>
        </button>`
      )}
    </div>
    ${!list.length && html`<p class="muted center">${emptyText || "Aucun champion"}</p>`}
  </div>`;
}

function ChampIcon({ id, faded, small }) {
  if (!id || id <= 0) return html`<div class="champ-icon empty ${small ? "small" : ""}"></div>`;
  return html`<img
    class="champ-icon ${faded ? "faded" : ""} ${small ? "small" : ""}"
    src=${championIcon(id)}
    alt=""
  />`;
}

function cellName(cell, isMe) {
  if (isMe) return "Toi";
  if (cell.gameName) return cell.gameName;
  if (cell.assignedPosition) return positionLabel(cell.assignedPosition);
  return "";
}

function cellDetail(cell, isMe, champ, enemy) {
  const parts = [champ ? champ.name : ""];
  // The position is already the name when the player has no visible name.
  if (!enemy && cell.assignedPosition && (isMe || cell.gameName)) parts.push(positionLabel(cell.assignedPosition));
  return parts.filter(Boolean).join(" · ");
}

function Team({ cells, localCellId, activeCells, trades, champions, enemy }) {
  return html`<div class="team ${enemy ? "enemy" : ""}">
    ${cells.map((cell) => {
      const isMe = cell.cellId === localCellId;
      const id = cell.championId || cell.championPickIntent;
      const trade = trades && trades.find((t) => t.cellId === cell.cellId && t.state === "AVAILABLE");
      const champ = champions && champions.get(id);
      return html`<div
        key=${cell.cellId}
        class="cell ${isMe ? "me" : ""} ${activeCells.has(cell.cellId) ? "active" : ""}"
      >
        <${ChampIcon} id=${id} faded=${!cell.championId} />
        <div class="cell-text">
          <div class="cell-name">${cellName(cell, isMe)}</div>
          <div class="small muted">${cellDetail(cell, isMe, champ, enemy)}</div>
        </div>
        ${trade &&
        html`<button
          class="btn tiny secondary"
          title="Proposer un échange"
          onClick=${() =>
            act(lcu("POST", `/lol-champ-select/v1/session/trades/${trade.id}/request`), "Échange proposé")}
        >
          ⇄
        </button>`}
      </div>`;
    })}
  </div>`;
}

function Bans({ session }) {
  const bans = session.bans || {};
  const mine = bans.myTeamBans || [];
  const theirs = bans.theirTeamBans || [];
  // Recent clients list bans only as completed ban actions.
  const fromActions = (session.actions || [])
    .flat()
    .filter((a) => a.type === "ban" && a.completed && a.championId > 0);
  const ally = mine.length ? mine : fromActions.filter((a) => a.isAllyAction).map((a) => a.championId);
  const enemy = theirs.length ? theirs : fromActions.filter((a) => !a.isAllyAction).map((a) => a.championId);
  if (!ally.length && !enemy.length) return null;
  return html`<div class="bans">
    <div class="ban-row">${ally.map((id, i) => html`<${ChampIcon} key=${i} id=${id} small />`)}</div>
    <span class="small muted">Bannis</span>
    <div class="ban-row">${enemy.map((id, i) => html`<${ChampIcon} key=${i} id=${id} small />`)}</div>
  </div>`;
}

function Requests({ session, champions }) {
  const list = [];
  const add = (items, kind, label) => {
    for (const r of items || []) {
      if (r.state === "RECEIVED") list.push({ ...r, kind, label });
    }
  };
  add(session.trades, "trades", "propose d'échanger vos champions");
  add(session.pickOrderSwaps, "swaps", "propose d'échanger l'ordre de sélection");
  add(session.positionSwaps, "position-swaps", "propose d'échanger vos rôles");
  if (!list.length) return null;
  const cellOf = (cellId) => (session.myTeam || []).find((c) => c.cellId === cellId) || {};
  return html`${list.map((r) => {
    const cell = cellOf(r.cellId);
    const champ = champions && champions.get(cell.championId);
    return html`<div class="banner request" key=${r.kind + r.id}>
      <div class="grow">
        <b>${cell.gameName || (champ && champ.name) || "Un allié"}</b> ${r.label}
      </div>
      <button
        class="btn small"
        onClick=${() => act(lcu("POST", `/lol-champ-select/v1/session/${r.kind}/${r.id}/accept`))}
      >
        Accepter
      </button>
      <button
        class="btn small secondary"
        onClick=${() => act(lcu("POST", `/lol-champ-select/v1/session/${r.kind}/${r.id}/decline`))}
      >
        ✕
      </button>
    </div>`;
  })}`;
}

function Bench({ session }) {
  const bench = session.benchChampions
    ? session.benchChampions.map((b) => b.championId)
    : session.benchChampionIds || [];
  const canReroll = session.allowRerolling && session.rerollsRemaining > 0;
  if (!session.benchEnabled && !canReroll) return null;
  return html`<section class="card">
    <div class="row space">
      <h3 class="no-margin">Banc</h3>
      ${session.allowRerolling &&
      html`<button
        class="btn small secondary"
        disabled=${!canReroll}
        onClick=${() => act(lcu("POST", "/lol-champ-select/v1/session/my-selection/reroll"))}
      >
        🎲 Relancer (${session.rerollsRemaining || 0})
      </button>`}
    </div>
    <div class="bench">
      ${bench.map(
        (id) => html`<button
          key=${id}
          class="bench-champ"
          onClick=${() => act(lcu("POST", `/lol-champ-select/v1/session/bench/swap/${id}`))}
        >
          <${ChampIcon} id=${id} />
        </button>`
      )}
      ${!bench.length && html`<span class="muted small">Vide</span>`}
    </div>
  </section>`;
}

async function lockAction(action) {
  try {
    await lcu("POST", `/lol-champ-select/v1/session/actions/${action.id}/complete`);
  } catch (e) {
    // Older clients complete the action with a PATCH.
    await lcu("PATCH", `/lol-champ-select/v1/session/actions/${action.id}`, {
      championId: action.championId,
      completed: true,
    });
  }
}

function ActionPanel({ session, active, futurePick, planning }) {
  const pickable = useLcu(PICKABLE);
  const bannable = useLcu(BANNABLE);
  const [showFuture, setShowFuture] = useState(false);

  useEffect(() => {
    refresh(PICKABLE);
    refresh(BANNABLE);
  }, [active && active.id, active && active.type]);

  const hover = (action, championId) =>
    act(lcu("PATCH", `/lol-champ-select/v1/session/actions/${action.id}`, { championId }));

  if (active) {
    const isBan = active.type === "ban";
    return html`<section class="card action ${isBan ? "ban" : "pick"}">
      <h3>${isBan ? "À toi de bannir !" : "À toi de choisir !"}</h3>
      <${ChampionGrid}
        ids=${isBan ? bannable : pickable}
        selectedId=${active.championId}
        onPick=${(id) => hover(active, id)}
      />
      <div class="sticky-actions">
        <button
          class="btn big wide ${isBan ? "danger" : ""}"
          disabled=${!(active.championId > 0)}
          onClick=${() => act(lockAction(active))}
        >
          ${isBan ? "Bannir" : "Verrouiller"}
        </button>
      </div>
    </section>`;
  }

  if (futurePick) {
    if (planning || showFuture) {
      return html`<section class="card action">
        <div class="row space">
          <h3 class="no-margin">${planning ? "Déclare ton champion" : "Prépare ton choix"}</h3>
          ${!planning &&
          html`<button class="btn small secondary" onClick=${() => setShowFuture(false)}>Masquer</button>`}
        </div>
        <${ChampionGrid}
          ids=${pickable}
          selectedId=${futurePick.championId}
          onPick=${(id) => hover(futurePick, id)}
        />
      </section>`;
    }
    return html`<section class="card">
      <p class="muted">En attente de ton tour…</p>
      <button class="btn secondary wide" onClick=${() => setShowFuture(true)}>Préparer mon choix</button>
    </section>`;
  }
  return null;
}

function Tabs({ locked, myCell, session }) {
  const tabs = [
    ...(locked ? [["skins", "Skins"]] : []),
    ["runes", "Runes"],
    ["spells", "Sorts"],
  ];
  const [tab, setTab] = useState(locked ? "skins" : "runes");
  useEffect(() => {
    if (locked) setTab("skins");
  }, [locked]);
  const current = tabs.find((t) => t[0] === tab) ? tab : tabs[0][0];
  return html`<section class="tabs-card">
    <div class="tabs">
      ${tabs.map(
        ([key, label]) => html`<button
          key=${key}
          class="tab ${current === key ? "active" : ""}"
          onClick=${() => setTab(key)}
        >
          ${label}
        </button>`
      )}
    </div>
    <div class="tab-body">
      ${current === "skins" && html`<${SkinsTab} myCell=${myCell} session=${session} />`}
      ${current === "runes" && html`<${RunesTab} myCell=${myCell} />`}
      ${current === "spells" && html`<${SpellsTab} myCell=${myCell} />`}
    </div>
  </section>`;
}

const PHASES = {
  PLANNING: "Déclaration des intentions",
  BAN_PICK: "Sélection",
  FINALIZATION: "Finalisation",
  GAME_STARTING: "Lancement de la partie",
};

export function ChampSelect() {
  const session = useLcu(SESSION);
  const champions = useData(loadChampions);
  if (!session) return html`<main class="center-screen"><div class="spinner"></div></main>`;

  const localCellId = session.localPlayerCellId;
  const myTeam = session.myTeam || [];
  const theirTeam = session.theirTeam || [];
  const myCell = myTeam.find((c) => c.cellId === localCellId) || {};
  const actions = (session.actions || []).flat();
  const mine = actions.filter((a) => a.actorCellId === localCellId);
  const active = mine.find((a) => a.isInProgress && !a.completed && (a.type === "pick" || a.type === "ban"));
  const myPicks = mine.filter((a) => a.type === "pick");
  const futurePick = myPicks.find((a) => !a.completed);
  const locked = myCell.championId > 0 && (myPicks.length === 0 || myPicks.every((a) => a.completed));
  const activeCells = new Set(actions.filter((a) => a.isInProgress).map((a) => a.actorCellId));
  const phase = session.timer && session.timer.phase;
  const anyBan = actions.some((a) => a.isInProgress && a.type === "ban");
  const phaseLabel =
    phase === "BAN_PICK" ? (anyBan ? "Bannissements" : "Sélection") : PHASES[phase] || "Sélection";

  return html`<main class="screen champ-select">
    <div class="cs-header">
      <div>
        <div class="small muted">Sélection des champions</div>
        <h2 class="no-margin">${phaseLabel}</h2>
      </div>
      <${Timer} timer=${session.timer} />
    </div>
    <${Requests} session=${session} champions=${champions} />
    <div class="teams">
      <${Team}
        cells=${myTeam}
        localCellId=${localCellId}
        activeCells=${activeCells}
        trades=${locked ? session.trades : null}
        champions=${champions}
      />
      ${theirTeam.length > 0 &&
      html`<${Team}
        cells=${theirTeam}
        localCellId=${localCellId}
        activeCells=${activeCells}
        champions=${champions}
        enemy
      />`}
    </div>
    <${Bans} session=${session} />
    <${ActionPanel}
      session=${session}
      active=${active}
      futurePick=${!active && !locked ? futurePick : null}
      planning=${phase === "PLANNING"}
    />
    <${Bench} session=${session} />
    <${Tabs} locked=${locked} myCell=${myCell} session=${session} />
  </main>`;
}
