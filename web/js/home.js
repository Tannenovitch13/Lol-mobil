import { html, useEffect, useMemo, useState } from "../vendor/preact-htm.js";
import { act, lcu, refresh, useLcu, useStore } from "./api.js";
import { loadQueues, positionLabel, profileIcon, useData } from "./data.js";

const LOBBY = "/lol-lobby/v2/lobby";
const SEARCH = "/lol-matchmaking/v1/search";

// Usual queues first, the rest after.
const QUEUE_ORDER = [420, 440, 400, 490, 480, 450, 1700, 1710, 900, 1900, 870, 880, 890];
const MAP_NAMES = {
  11: "Faille de l'invocateur",
  12: "Abîme hurlant",
  30: "Arène",
  21: "Nexus Blitz",
};

function queueLabel(q) {
  if (!q) return "";
  return q.description || q.detailedDescription || q.shortName || q.name || `File ${q.id}`;
}

function Invitations() {
  const invites = useLcu("/lol-lobby/v2/received-invitations") || [];
  const pendingInvites = invites.filter((i) => i.state === "Pending");
  if (!pendingInvites.length) return null;
  return html`<section class="card">
    <h3>Invitations</h3>
    ${pendingInvites.map(
      (inv) => html`<div class="row invite" key=${inv.invitationId}>
        <div class="grow">
          <b>${inv.fromSummonerName || inv.gameName || "Un ami"}</b>
          <div class="muted small">${inv.gameConfig && queueLabelFromId(inv.gameConfig.queueId)}</div>
        </div>
        <button
          class="btn small"
          onClick=${() =>
            act(lcu("POST", `/lol-lobby/v2/received-invitations/${inv.invitationId}/accept`))}
        >
          Rejoindre
        </button>
        <button
          class="btn small secondary"
          onClick=${() =>
            act(lcu("POST", `/lol-lobby/v2/received-invitations/${inv.invitationId}/decline`))}
        >
          ✕
        </button>
      </div>`
    )}
  </section>`;
}

let queueNames = new Map();
function queueLabelFromId(id) {
  return queueNames.get(id) || "";
}

export function ModePicker({ onPicked, currentQueueId }) {
  const queues = useData(loadQueues);
  const groups = useMemo(() => {
    if (!queues) return [];
    queueNames = new Map(queues.map((q) => [q.id, queueLabel(q)]));
    const usable = queues.filter(
      (q) =>
        q.queueAvailability === "Available" &&
        !q.isCustom &&
        (q.category === "PvP" || q.category === "VersusAi")
    );
    usable.sort((a, b) => {
      const ia = QUEUE_ORDER.indexOf(a.id);
      const ib = QUEUE_ORDER.indexOf(b.id);
      return (ia < 0 ? 999 : ia) - (ib < 0 ? 999 : ib) || a.id - b.id;
    });
    const byGroup = new Map();
    for (const q of usable) {
      const key = q.category === "VersusAi" ? "Contre l'IA" : MAP_NAMES[q.mapId] || "Autres modes";
      if (!byGroup.has(key)) byGroup.set(key, []);
      byGroup.get(key).push(q);
    }
    return [...byGroup.entries()];
  }, [queues]);

  if (queues === undefined) return html`<div class="spinner"></div>`;
  if (!queues) return html`<p class="error">Impossible de lire la liste des modes de jeu.</p>`;

  const create = async (q) => {
    const ok = await act(lcu("POST", LOBBY, { queueId: q.id }));
    if (ok !== undefined) {
      refresh(LOBBY);
      onPicked && onPicked();
    }
  };

  return html`<div class="modes">
    ${groups.map(
      ([name, list]) => html`<section class="card" key=${name}>
        <h3>${name}</h3>
        <div class="mode-list">
          ${list.map(
            (q) => html`<button
              key=${q.id}
              class="mode ${q.id === currentQueueId ? "current" : ""}"
              onClick=${() => create(q)}
            >
              <span>${queueLabel(q)}</span>
              ${q.isRanked && html`<span class="tag">Classé</span>`}
            </button>`
          )}
        </div>
      </section>`
    )}
  </div>`;
}

function useElapsed(baseSeconds, receivedAt) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return Math.max(0, Math.floor((baseSeconds || 0) + (now - (receivedAt || now)) / 1000));
}

const fmt = (s) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;

function SearchBanner() {
  const search = useLcu(SEARCH);
  const receivedAt = useStoreReceivedAt(SEARCH);
  const elapsed = useElapsed(search && search.timeInQueue, receivedAt);
  if (!search || search.searchState !== "Searching") {
    if (search && search.errors && search.errors.length) {
      return html`<div class="banner error">
        ${search.errors.map((e) => html`<div>${e.message || e.errorType}</div>`)}
      </div>`;
    }
    return null;
  }
  const penalty = search.lowPriorityData && search.lowPriorityData.penaltyTime;
  return html`<div class="banner searching">
    <div class="pulse"></div>
    <div class="grow">
      <b>Recherche en cours… ${fmt(elapsed)}</b>
      <div class="small muted">
        Temps estimé ${fmt(search.estimatedQueueTime || 0)}
        ${penalty ? ` · pénalité ${fmt(penalty)}` : ""}
      </div>
    </div>
    <button
      class="btn small secondary"
      onClick=${() => act(lcu("DELETE", "/lol-lobby/v2/lobby/matchmaking/search"))}
    >
      Annuler
    </button>
  </div>`;
}

function useStoreReceivedAt(uri) {
  return useStore((s) => s.lcuReceivedAt[uri]);
}

const POSITION_KEYS = ["TOP", "JUNGLE", "MIDDLE", "BOTTOM", "UTILITY", "FILL"];

function PositionPicker({ member }) {
  const first = member.firstPositionPreference || "UNSELECTED";
  const second = member.secondPositionPreference || "UNSELECTED";
  const setPositions = (firstPreference, secondPreference) =>
    act(
      lcu("PUT", "/lol-lobby/v2/lobby/members/localMember/position-preferences", {
        firstPreference,
        secondPreference,
      })
    );
  const pick = (slot, pos) => {
    if (slot === 1) {
      const newSecond = pos === "FILL" ? "UNSELECTED" : second === pos ? first : second;
      setPositions(pos, newSecond);
    } else {
      const newFirst = first === pos ? second : first;
      setPositions(newFirst, pos);
    }
  };
  return html`<div class="positions">
    ${[1, 2].map((slot) => {
      if (slot === 2 && first === "FILL") return null;
      const current = slot === 1 ? first : second;
      return html`<div class="position-row" key=${slot}>
        <span class="small muted">${slot === 1 ? "Rôle principal" : "Rôle secondaire"}</span>
        <div class="chips">
          ${POSITION_KEYS.filter((p) => !(slot === 2 && p === "FILL")).map(
            (p) => html`<button
              key=${p}
              class="chip ${current === p ? "active" : ""}"
              onClick=${() => pick(slot, p)}
            >
              ${positionLabel(p)}
            </button>`
          )}
        </div>
      </div>`;
    })}
  </div>`;
}

function Lobby({ lobby, phase }) {
  const [changing, setChanging] = useState(false);
  const local = lobby.localMember || {};
  const config = lobby.gameConfig || {};
  const searching = phase === "Matchmaking";
  const queues = useData(loadQueues);
  const queue = queues && queues.find((q) => q.id === config.queueId);

  if (changing) {
    return html`<main class="screen">
      <div class="row space">
        <h2>Changer de mode</h2>
        <button class="btn small secondary" onClick=${() => setChanging(false)}>Retour</button>
      </div>
      <${ModePicker} currentQueueId=${config.queueId} onPicked=${() => setChanging(false)} />
    </main>`;
  }

  return html`<main class="screen">
    <${Invitations} />
    <${SearchBanner} />
    <section class="card">
      <div class="row space">
        <div>
          <div class="small muted">Salon</div>
          <h2 class="no-margin">${queue ? queueLabel(queue) : config.gameMode || "Partie"}</h2>
        </div>
        ${local.isLeader &&
        !searching &&
        html`<button class="btn small secondary" onClick=${() => setChanging(true)}>Changer</button>`}
      </div>
      <div class="members">
        ${(lobby.members || []).map(
          (m) => html`<div class="member" key=${m.puuid || m.summonerId}>
            <img class="avatar" src=${profileIcon(m.summonerIconId)} alt="" />
            <div class="grow">
              <b>${m.gameName || m.summonerName || "Joueur"}</b>
              ${m.isLeader && html` <span class="tag">Chef</span>`}
              ${config.showPositionSelector &&
              html`<div class="small muted">
                ${positionLabel(m.firstPositionPreference)}
                ${m.secondPositionPreference && m.secondPositionPreference !== "UNSELECTED"
                  ? ` / ${positionLabel(m.secondPositionPreference)}`
                  : ""}
              </div>`}
            </div>
          </div>`
        )}
      </div>
      ${config.showPositionSelector && !searching && html`<${PositionPicker} member=${local} />`}
    </section>
    <div class="actions">
      ${!searching &&
      html`<button
        class="btn big wide"
        disabled=${!local.isLeader || lobby.canStartActivity === false}
        onClick=${() => act(lcu("POST", "/lol-lobby/v2/lobby/matchmaking/search"))}
      >
        ${local.isLeader ? "Trouver une partie" : "En attente du chef…"}
      </button>`}
      ${!searching &&
      html`<button class="btn secondary wide" onClick=${() => act(lcu("DELETE", LOBBY))}>
        Quitter le salon
      </button>`}
    </div>
  </main>`;
}

export function Home({ phase }) {
  const lobby = useLcu(LOBBY);
  if (lobby && (phase === "Lobby" || phase === "Matchmaking")) {
    return html`<${Lobby} lobby=${lobby} phase=${phase} />`;
  }
  return html`<main class="screen">
    <${Invitations} />
    <h2>Choisis un mode de jeu</h2>
    <${ModePicker} />
  </main>`;
}
