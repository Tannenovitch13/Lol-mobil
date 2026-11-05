import { html, useEffect, useMemo, useState } from "../vendor/preact-htm.js";
import { act, get, lcu, refresh, store, toast, useLcu, useStore } from "./api.js";
import { loadQueues, positionLabel, profileIcon, useData } from "./data.js";
import { FRIENDS, InviteSheet, LobbyMember, PendingInvites } from "./social.js";

const LOBBY = "/lol-lobby/v2/lobby";
const SEARCH = "/lol-matchmaking/v1/search";
const PHASE = "/lol-gameflow/v1/gameflow-phase";

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

// Custom games have their own queues on current clients (16.x): the
// practice tool is queue 3140. Without it the client answers "Invalid
// session" or creates a lobby that never starts.
const PRACTICE_TOOL_QUEUE = 3140;

// The request the client itself sends (rcp-fe-lol-parties 16.22,
// _generatePracticeToolPayload).
async function practiceToolLobby(lobbyName) {
  let region = "";
  try {
    const custom = await get("/lol-game-queues/v1/custom");
    region = (custom && custom.gameServerRegions && custom.gameServerRegions[0]) || "";
  } catch (e) {
    // the default region then
  }
  return {
    queueId: PRACTICE_TOOL_QUEUE,
    customGameLobby: {
      configuration: {
        gameMode: "PRACTICETOOL",
        gameMutator: "",
        gameServerRegion: region,
        mapId: 11,
        mutators: { id: 1 },
        spectatorDelayEnabled: false,
        spectatorPolicy: "AllAllowed",
        teamSize: 5,
        hidePublicly: true,
      },
      lobbyName,
      hidePublicly: true,
      lobbyPassword: null,
    },
  };
}

async function createPracticeTool() {
  const me = store.lcu["/lol-summoner/v1/current-summoner"];
  const name = (me && (me.gameName || me.displayName)) || "LolMobil";
  try {
    return await lcu("POST", LOBBY, await practiceToolLobby(`Partie de ${name}`));
  } catch (e) {
    // Lobby names must be unique on the server.
    if (!/name/i.test(e.message || "")) throw e;
    const suffix = Math.floor(1000 + Math.random() * 9000);
    return await lcu("POST", LOBBY, await practiceToolLobby(`Partie de ${name} ${suffix}`));
  }
}

const CUSTOM_MODES = { PRACTICETOOL: "Outil d'entraînement", CLASSIC: "Partie personnalisée", ARAM: "ARAM personnalisée" };

// Why the client refused to start a custom game (failedPlayers[].reason).
const START_ERRORS = {
  RANKED_NUM_CHAMPS: "pas assez de champions possédés",
  MINOR_RESTRICTED: "restriction de temps de jeu",
  PARENTAL_CONTROL_RESTRICTION: "contrôle parental",
  TIME_PLAYED_RESTRICTION: "restriction de temps de jeu",
  NOT_ENOUGH_PLAYERS_FOR_MAP: "pas assez de joueurs pour cette carte",
  GAME_MODE_NOT_SUPPORTED: "mode de jeu non pris en charge",
};

function startError(res) {
  const reasons = [...new Set((res.failedPlayers || []).map((p) => p.reason).filter(Boolean))];
  const text = reasons.map((r) => START_ERRORS[r] || r).join(", ");
  return `Le client n'a pas lancé la partie${text ? ` : ${text}` : ""}`;
}

// Events can be missed (phone asleep, network): read the phase again for a
// while after an action that should change it.
function watchPhase(seconds = 25) {
  let n = 0;
  const timer = setInterval(() => {
    n++;
    refresh(PHASE);
    refresh(SEARCH);
    const phase = store.lcu[PHASE];
    if (n >= seconds || (phase && phase !== "Lobby" && phase !== "Matchmaking")) clearInterval(timer);
  }, 1000);
}

export function ModePicker({ onPicked, currentQueueId }) {
  const queues = useData(loadQueues);
  const [creating, setCreating] = useState(false);
  const groups = useMemo(() => {
    if (!queues) return [];
    queueNames = new Map(queues.map((q) => [q.id, queueLabel(q)]));
    const usable = queues.filter(
      (q) =>
        q.queueAvailability === "Available" &&
        !q.isCustom &&
        q.category !== "Custom" &&
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
  if (!queues) return html`<p class="error">Impossible de lire la liste des modes de jeu, nouvel essai…</p>`;

  const create = async (q) => {
    const ok = await act(lcu("POST", LOBBY, { queueId: q.id }));
    if (ok !== undefined) {
      refresh(LOBBY);
      onPicked && onPicked();
    }
  };

  const practice = async () => {
    if (creating) return;
    setCreating(true);
    const ok = await act(createPracticeTool().then(() => true));
    setCreating(false);
    if (ok) {
      refresh(LOBBY);
      onPicked && onPicked();
    }
  };

  return html`<div class="modes">
    <section class="card">
      <h3>Entraînement</h3>
      <div class="mode-list">
        <button class="mode ${currentQueueId === PRACTICE_TOOL_QUEUE ? "current" : ""}" disabled=${creating} onClick=${practice}>
          <span>Outil d'entraînement</span>
          ${creating && html`<span class="small muted">Création…</span>`}
        </button>
      </div>
    </section>
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

// Matchmaking state. A custom game (practice tool...) also goes through a
// short search for a game server on current clients: it is shown as the
// launch of the game, not as a queue.
function SearchBanner({ custom, starting }) {
  const search = useLcu(SEARCH);
  const receivedAt = useStoreReceivedAt(SEARCH);
  const elapsed = useElapsed(search && search.timeInQueue, receivedAt);
  const cancel = html`<button
    class="btn small secondary"
    onClick=${() => act(lcu("DELETE", "/lol-lobby/v2/lobby/matchmaking/search"))}
  >
    Annuler
  </button>`;
  if (!search || search.searchState !== "Searching") {
    if (search && search.errors && search.errors.length) {
      return html`<div class="banner error">
        ${search.errors.map((e) => html`<div>${e.message || e.errorType}</div>`)}
      </div>`;
    }
    if (custom && starting) {
      return html`<div class="banner searching">
        <div class="pulse"></div>
        <div class="grow"><b>Lancement de la partie…</b></div>
      </div>`;
    }
    return null;
  }
  if (custom) {
    return html`<div class="banner searching">
      <div class="pulse"></div>
      <div class="grow">
        <b>Lancement de la partie… ${fmt(elapsed)}</b>
        <div class="small muted">La sélection des champions va s'ouvrir.</div>
      </div>
      ${cancel}
    </div>`;
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
    ${cancel}
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
  const custom = Boolean(config.isCustom);
  const friends = useLcu(FRIENDS);
  const [inviting, setInviting] = useState(false);
  const canInvite = local.isLeader || local.allowedInviteOthers;
  const [starting, setStarting] = useState(false);
  // Only our own start or the client's phase block the button: a search
  // state left over from an earlier game must not.
  const launching = custom && (starting || searching);

  useEffect(() => {
    if (!starting) return undefined;
    // Still in the lobby long after: let the leader try again.
    const t = setTimeout(() => setStarting(false), 30000);
    return () => clearTimeout(t);
  }, [starting]);

  const startCustom = async () => {
    setStarting(true);
    try {
      const res = await lcu("POST", "/lol-lobby/v1/lobby/custom/start-champ-select");
      if (res && res.success === false) throw new Error(startError(res));
      watchPhase();
    } catch (e) {
      setStarting(false);
      toast(e.message || String(e), "error", 6000);
    }
  };

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
    <${SearchBanner} custom=${custom} starting=${starting} />
    <section class="card">
      <div class="row space">
        <div>
          <div class="small muted">Salon</div>
          <h2 class="no-margin">
            ${custom
              ? CUSTOM_MODES[config.gameMode] || config.customLobbyName || "Partie personnalisée"
              : queue
              ? queueLabel(queue)
              : config.gameMode || "Partie"}
          </h2>
        </div>
        ${local.isLeader &&
        !searching &&
        !launching &&
        html`<button class="btn small secondary" onClick=${() => setChanging(true)}>Changer</button>`}
      </div>
      <div class="members">
        ${(lobby.members || []).map(
          (m) => html`<${LobbyMember}
            key=${m.puuid || m.summonerId}
            member=${m}
            local=${local}
            showPositions=${config.showPositionSelector}
          />`
        )}
      </div>
      <${PendingInvites} lobby=${lobby} friends=${friends} />
      ${canInvite &&
      !searching &&
      html`<button class="btn secondary wide" onClick=${() => setInviting(true)}>+ Inviter des amis</button>`}
      ${config.showPositionSelector && !searching && html`<${PositionPicker} member=${local} />`}
    </section>
    <div class="actions">
      ${!searching &&
      !custom &&
      html`<button
        class="btn big wide"
        disabled=${!local.isLeader || lobby.canStartActivity === false}
        onClick=${() => act(lcu("POST", "/lol-lobby/v2/lobby/matchmaking/search"))}
      >
        ${local.isLeader ? "Trouver une partie" : "En attente du chef…"}
      </button>`}
      ${custom &&
      html`<button class="btn big wide" disabled=${!local.isLeader || launching} onClick=${startCustom}>
        ${launching ? "Lancement…" : local.isLeader ? "Lancer la partie" : "En attente du chef…"}
      </button>`}
      ${!searching &&
      html`<button class="btn secondary wide" onClick=${() => act(lcu("DELETE", LOBBY))}>
        Quitter le salon
      </button>`}
    </div>
    ${inviting && html`<${InviteSheet} lobby=${lobby} onClose=${() => setInviting(false)} />`}
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
