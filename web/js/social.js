// Lobby members (promote, kick, invite rights) and inviting friends.

import { html, useEffect, useMemo, useState } from "../vendor/preact-htm.js";
import { act, get, lcu, observe, useLcu } from "./api.js";
import { positionLabel, profileIcon, searchKey } from "./data.js";

const FRIENDS = "/lol-chat/v1/friends";
const SUGGESTED = "/lol-suggested-players/v1/suggested-players";
observe([FRIENDS]);

const STATUS = {
  outOfGame: "Disponible",
  hosting_NORMAL_GAME: "Dans un salon",
  inQueue: "En file",
  championSelect: "Sélection",
  inGame: "En partie",
  spectating: "Spectateur",
};

function friendStatus(f) {
  const lol = f.lol || {};
  if (f.availability === "offline") return "Hors ligne";
  if (f.availability === "mobile") return "Sur mobile";
  if (f.availability === "away") return "Absent";
  if (lol.gameStatus && STATUS[lol.gameStatus]) return STATUS[lol.gameStatus];
  if (f.availability === "dnd") return "Occupé";
  return "En ligne";
}

const friendName = (f) => f.gameName || f.name || f.summonerName || "Ami";

function memberPositions(m) {
  if (!m.firstPositionPreference || m.firstPositionPreference === "UNSELECTED") return "";
  const second =
    m.secondPositionPreference && m.secondPositionPreference !== "UNSELECTED"
      ? ` / ${positionLabel(m.secondPositionPreference)}`
      : "";
  return positionLabel(m.firstPositionPreference) + second;
}

export function LobbyMember({ member, local, showPositions }) {
  const [open, setOpen] = useState(false);
  const isMe = member.summonerId === local.summonerId;
  const canManage = local.isLeader && !isMe;
  const path = `/lol-lobby/v2/lobby/members/${member.summonerId}`;
  const run = (suffix, text) => {
    setOpen(false);
    act(lcu("POST", `${path}/${suffix}`), text);
  };
  return html`<div class="member-wrap">
    <div class="member">
      <img class="avatar" src=${profileIcon(member.summonerIconId)} alt="" />
      <div class="grow">
        <b>${member.gameName || member.summonerName || "Joueur"}</b>
        ${member.isLeader && html` <span class="tag">Chef</span>`}
        ${!member.isLeader && member.allowedInviteOthers && html` <span class="tag">Invite</span>`}
        ${showPositions && html`<div class="small muted">${memberPositions(member)}</div>`}
      </div>
      ${canManage &&
      html`<button class="icon-btn small" aria-label="Actions" onClick=${() => setOpen(!open)}>⋯</button>`}
    </div>
    ${open &&
    html`<div class="member-actions">
      <button class="btn small secondary" onClick=${() => run("promote", "Nouveau chef du salon")}>Nommer chef</button>
      <button
        class="btn small secondary"
        onClick=${() =>
          run(member.allowedInviteOthers ? "revoke-invite" : "grant-invite", "Droits d'invitation modifiés")}
      >
        ${member.allowedInviteOthers ? "Retirer l'invitation" : "Autoriser à inviter"}
      </button>
      <button class="btn small danger" onClick=${() => run("kick", "Joueur exclu")}>Exclure</button>
    </div>`}
  </div>`;
}

export function PendingInvites({ lobby, friends }) {
  const pending = (lobby.invitations || []).filter((i) => i.state === "Pending" || i.state === "Requested");
  if (!pending.length) return null;
  const byId = new Map((friends || []).map((f) => [f.summonerId, f]));
  return html`<div class="pending-invites small muted">
    ${"Invitations envoyées : "}${pending
      .map((i) => {
        const f = byId.get(i.toSummonerId);
        return i.toSummonerName || (f && friendName(f)) || "un joueur";
      })
      .join(", ")}
  </div>`;
}

async function findByRiotId(riotId) {
  const [gameName, tagLine] = riotId.split("#").map((s) => (s || "").trim());
  if (!gameName) throw new Error("Entre un Riot ID, par exemple Pseudo#EUW");
  try {
    return await get(`/lol-summoner/v1/summoners?name=${encodeURIComponent(riotId.trim())}`);
  } catch (e) {
    if (!tagLine) throw new Error("Ajoute le tag : Pseudo#EUW");
  }
  const alias = await get(
    `/lol-summoner/v1/alias/lookup?gameName=${encodeURIComponent(gameName)}&tagLine=${encodeURIComponent(tagLine)}`
  );
  if (!alias || !alias.puuid) throw new Error("Joueur introuvable");
  return get(`/lol-summoner/v2/summoners/puuid/${alias.puuid}`);
}

export function InviteSheet({ lobby, onClose }) {
  const friends = useLcu(FRIENDS);
  const [suggested, setSuggested] = useState([]);
  const [query, setQuery] = useState("");
  const [riotId, setRiotId] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    get(SUGGESTED)
      .then((list) => setSuggested(Array.isArray(list) ? list : []))
      .catch(() => setSuggested([]));
  }, []);

  const taken = useMemo(() => {
    const ids = new Set((lobby.members || []).map((m) => m.summonerId));
    for (const i of lobby.invitations || []) {
      if (i.state === "Pending" || i.state === "Accepted") ids.add(i.toSummonerId);
    }
    return ids;
  }, [lobby]);

  const invite = (summonerId, name) =>
    act(lcu("POST", "/lol-lobby/v2/lobby/invitations", [{ toSummonerId: summonerId }]), `${name} invité`);

  const online = useMemo(() => {
    const key = searchKey(query);
    const order = { chat: 0, away: 1, dnd: 2, mobile: 3, offline: 4 };
    return (friends || [])
      .filter((f) => f.summonerId && f.availability !== "offline")
      .filter((f) => !key || searchKey(friendName(f)).includes(key))
      .sort((a, b) => (order[a.availability] ?? 5) - (order[b.availability] ?? 5) || friendName(a).localeCompare(friendName(b)));
  }, [friends, query]);

  const byRiotId = async (e) => {
    e.preventDefault();
    setBusy(true);
    try {
      const summoner = await findByRiotId(riotId);
      await lcu("POST", "/lol-lobby/v2/lobby/invitations", [{ toSummonerId: summoner.summonerId }]);
      setRiotId("");
      act(Promise.resolve(), `${summoner.gameName || riotId} invité`);
    } catch (err) {
      act(Promise.reject(err));
    }
    setBusy(false);
  };

  const Row = ({ id, name, detail, icon }) => html`<div class="friend">
    <img class="avatar small" src=${profileIcon(icon)} alt="" />
    <div class="grow">
      <b>${name}</b>
      ${detail && html`<div class="small muted">${detail}</div>`}
    </div>
    <button class="btn small" disabled=${taken.has(id)} onClick=${() => invite(id, name)}>
      ${taken.has(id) ? "Invité" : "Inviter"}
    </button>
  </div>`;

  return html`<div class="sheet-backdrop" onClick=${onClose}>
    <div class="sheet" onClick=${(e) => e.stopPropagation()}>
      <div class="row space">
        <h2 class="no-margin">Inviter</h2>
        <button class="btn small secondary" onClick=${onClose}>Fermer</button>
      </div>
      <form class="row" onSubmit=${byRiotId}>
        <input class="search no-margin grow" placeholder="Riot ID (Pseudo#EUW)" value=${riotId} onInput=${(e) => setRiotId(e.target.value)} />
        <button class="btn small" disabled=${busy || !riotId.trim()}>Inviter</button>
      </form>
      <h4>Amis en ligne</h4>
      ${(friends || []).length > 8 &&
      html`<input class="search" type="search" placeholder="Filtrer…" value=${query} onInput=${(e) => setQuery(e.target.value)} />`}
      <div class="friend-list">
        ${online.map(
          (f) => html`<${Row} key=${f.summonerId} id=${f.summonerId} name=${friendName(f)} detail=${friendStatus(f)} icon=${f.icon} />`
        )}
        ${friends !== undefined && !online.length && html`<p class="muted small">Aucun ami en ligne.</p>`}
      </div>
      ${suggested.length > 0 &&
      html`<h4>Suggestions</h4>
        <div class="friend-list">
          ${suggested
            .filter((s) => s.summonerId)
            .slice(0, 10)
            .map(
              (s) => html`<${Row}
                key=${"s" + s.summonerId}
                id=${s.summonerId}
                name=${s.gameName || s.summonerName || "Joueur"}
                detail=${s.reason === "RECENT_GAMES" ? "Parties récentes" : ""}
                icon=${s.summonerIconId}
              />`
            )}
        </div>`}
    </div>
  </div>`;
}

export { FRIENDS };
