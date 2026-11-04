// "Builds" tab: popular builds from OP.GG or Blitz, applied in one tap
// (rune page, item set for the in-game shop, summoner spells).

import { html, useEffect, useState } from "../vendor/preact-htm.js";
import { asset, call, saveSettings, toast, useLcu, useStore } from "./api.js";
import { loadItems, loadPerks, loadSpells, loadStyles, positionLabel, useData } from "./data.js";
import { PageSummary } from "./runes.js";

const SOURCES = [
  ["opgg", "OP.GG"],
  ["blitz", "Blitz"],
];
const ROLES = ["top", "jungle", "middle", "bottom", "utility"];
const PARTS_KEY = "lolmobil.buildParts";

function modeForQueue(gameMode) {
  switch (String(gameMode || "").toUpperCase()) {
    case "ARAM":
    case "KIWI":
      return "aram";
    case "URF":
    case "ARURF":
      return "urf";
    case "CHERRY":
      return "arena";
  }
  return "ranked";
}

const pct = (v) => (v ? `${(v * 100).toFixed(1).replace(".", ",")} %` : "—");
const games = (n) => (n ? `${n.toLocaleString("fr-FR")} parties` : "");

function loadParts() {
  try {
    return { runes: true, items: true, spells: true, ...JSON.parse(localStorage.getItem(PARTS_KEY) || "{}") };
  } catch (e) {
    return { runes: true, items: true, spells: true };
  }
}

function ItemIcons({ ids, items, size = 30 }) {
  if (!ids || !ids.length) return null;
  return html`<span class="item-row">
    ${ids.map((id, i) => {
      const item = items && items.get(id);
      return html`<img
        key=${i}
        class="item-icon"
        width=${size}
        height=${size}
        src=${item ? asset(item.iconPath) : ""}
        title=${item ? item.name : id}
        alt=${item ? item.name : id}
      />`;
    })}
  </span>`;
}

function SpellIcons({ ids, spells }) {
  if (!ids || !ids.length || !spells) return null;
  const byId = new Map(spells.map((s) => [s.id, s]));
  return html`<span class="item-row">
    ${ids.map((id) => {
      const s = byId.get(id);
      return html`<img key=${id} class="item-icon" width="30" height="30" src=${s ? asset(s.iconPath) : ""} title=${s ? s.name : id} />`;
    })}
  </span>`;
}

// Some sources give only perk ids: find the trees (and the client order),
// as the companion does when applying.
function completePage(runes, styles) {
  const page = {
    primaryStyleId: runes.primaryStyleId,
    subStyleId: runes.subStyleId,
    selectedPerkIds: runes.selectedPerkIds || [],
  };
  if ((page.primaryStyleId && page.subStyleId) || !styles) return page;
  const styleOf = new Map();
  const keystones = new Set();
  const shards = new Set();
  for (const style of styles) {
    (style.slots || []).forEach((slot, i) => {
      for (const p of slot.perks) {
        if (slot.type === "kStatMod") shards.add(p);
        else {
          styleOf.set(p, style.id);
          if (i === 0) keystones.add(p);
        }
      }
    });
  }
  const ids = page.selectedPerkIds;
  const key = ids.find((p) => keystones.has(p));
  if (!key) return page;
  const primary = styleOf.get(key);
  const sub = ids.find((p) => !shards.has(p) && styleOf.get(p) !== primary);
  return {
    primaryStyleId: primary,
    subStyleId: sub ? styleOf.get(sub) : 0,
    selectedPerkIds: [
      key,
      ...ids.filter((p) => p !== key && styleOf.get(p) === primary),
      ...ids.filter((p) => !shards.has(p) && styleOf.get(p) !== primary),
      ...ids.filter((p) => shards.has(p)),
    ],
  };
}

function BuildCard({ build, index, items, spells, perks, styles, onApply, busy }) {
  const page = completePage(build.runes, styles);
  return html`<div class="build-card">
    <div class="row space">
      <b>${build.label}</b>
      <span class="small muted">
        ${build.runeStats.winRate ? `${pct(build.runeStats.winRate)} victoires` : ""}
        ${build.runeStats.games ? ` · ${games(build.runeStats.games)}` : ""}
      </span>
    </div>
    ${build.runes.selectedPerkIds && build.runes.selectedPerkIds.length > 0 &&
    html`<${PageSummary} page=${page} perks=${perks} styles=${styles} />`}
    <div class="build-line">
      <span class="small muted">Sorts</span>
      <${SpellIcons} ids=${build.spells} spells=${spells} />
      ${build.skillOrder && build.skillOrder.length > 0 &&
      html`<span class="skill-order">${build.skillOrder.join(" > ")}</span>`}
    </div>
    ${build.starter && build.starter.length > 0 &&
    html`<div class="build-line"><span class="small muted">Départ</span><${ItemIcons} ids=${build.starter} items=${items} /></div>`}
    <div class="build-line">
      <span class="small muted">Principaux</span>
      <${ItemIcons} ids=${build.core} items=${items} />
      ${build.boots && build.boots.length > 0 && html`<span class="muted">+</span><${ItemIcons} ids=${build.boots} items=${items} />`}
    </div>
    ${build.situational && build.situational.length > 0 &&
    html`<div class="build-line"><span class="small muted">Ensuite</span><${ItemIcons} ids=${build.situational} items=${items} size=${24} /></div>`}
    <button class="btn wide" disabled=${busy} onClick=${() => onApply(index)}>
      ${busy ? "Application…" : "Appliquer ce build"}
    </button>
  </div>`;
}

export function BuildsTab({ myCell }) {
  const settings = useStore((s) => s.settings);
  const flow = useLcu("/lol-gameflow/v1/session");
  const items = useData(loadItems);
  const spells = useData(loadSpells);
  const perks = useData(loadPerks);
  const styles = useData(loadStyles);
  const [source, setSource] = useState(settings.buildSource || "opgg");
  const [position, setPosition] = useState((myCell.assignedPosition || "").toLowerCase());
  const [result, setResult] = useState(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(-1);
  const [parts, setParts] = useState(loadParts);

  const championId = myCell.championId > 0 ? myCell.championId : myCell.championPickIntent;
  const queue = flow && flow.gameData && flow.gameData.queue;
  const mode = modeForQueue(queue && queue.gameMode);

  useEffect(() => {
    if (!(championId > 0)) return undefined;
    let alive = true;
    setLoading(true);
    setError("");
    call({ type: "builds", source, championId, position: mode === "ranked" ? position : "", mode }, 35000)
      .then((res) => {
        if (!alive) return;
        if (res.error || res.status >= 300) {
          setError(res.error || `Erreur ${res.status}`);
          setResult(null);
        } else {
          setResult(res.body);
          if (!position && res.body.position) setPosition(res.body.position);
        }
      })
      .catch((e) => alive && setError(e.message))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [championId, source, position, mode]);

  const setPart = (key, value) => {
    const next = { ...parts, [key]: value };
    setParts(next);
    try {
      localStorage.setItem(PARTS_KEY, JSON.stringify(next));
    } catch (e) {
      /* ignore */
    }
  };

  const apply = async (index) => {
    setBusy(index);
    try {
      const res = await call(
        {
          type: "apply-build",
          source,
          championId,
          position: mode === "ranked" ? position || (result && result.position) || "" : "",
          mode,
          buildIndex: index,
          ...parts,
        },
        45000
      );
      const out = res.body || {};
      if (out.done && out.done.length) {
        toast(`Appliqué : ${out.done.join(", ")}`, "ok", 3000);
      }
      if (res.error) toast(res.error, "error", 6000);
    } catch (e) {
      toast(e.message, "error", 5000);
    }
    setBusy(-1);
  };

  if (!(championId > 0)) return html`<p class="muted">Choisis d'abord un champion.</p>`;
  if (mode === "arena") return html`<p class="muted">Les builds de l'Arène ne sont pas encore pris en charge.</p>`;

  return html`<div class="builds">
    <div class="chips">
      ${SOURCES.map(
        ([key, label]) => html`<button key=${key} class="chip ${source === key ? "active" : ""}" onClick=${() => setSource(key)}>
          ${label}
        </button>`
      )}
    </div>
    ${mode === "ranked" &&
    html`<div class="chips">
      ${ROLES.map(
        (r) => html`<button key=${r} class="chip ${position === r ? "active" : ""}" onClick=${() => setPosition(r)}>
          ${positionLabel(r)}
        </button>`
      )}
    </div>`}
    ${result &&
    html`<p class="small muted">
      ${result.sourceName} · patch ${result.patch || "?"}${result.stats && result.stats.winRate
        ? ` · ${pct(result.stats.winRate)} de victoires`
        : ""}${result.stats && result.stats.games ? ` · ${games(result.stats.games)}` : ""}
    </p>`}
    <div class="apply-parts">
      <label><input type="checkbox" checked=${parts.runes} onChange=${(e) => setPart("runes", e.target.checked)} /> Runes</label>
      <label><input type="checkbox" checked=${parts.items} onChange=${(e) => setPart("items", e.target.checked)} /> Set d'objets</label>
      <label><input type="checkbox" checked=${parts.spells} onChange=${(e) => setPart("spells", e.target.checked)} /> Sorts</label>
    </div>
    ${loading && html`<div class="spinner"></div>`}
    ${error && !loading && html`<p class="error">${error}</p>`}
    ${!loading &&
    result &&
    result.builds.map(
      (b, i) => html`<${BuildCard}
        key=${i}
        build=${b}
        index=${i}
        items=${items}
        spells=${spells}
        perks=${perks}
        styles=${styles}
        busy=${busy === i}
        onApply=${apply}
      />`
    )}
    <label class="switch-row auto-build">
      <span>
        <b>Appliquer automatiquement</b>
        <small>Dès que ton champion est verrouillé, le build le plus joué (${SOURCES.find((s) => s[0] === source)[1]}) est appliqué.</small>
      </span>
      <input
        type="checkbox"
        checked=${settings.autoBuild && settings.buildSource === source}
        onChange=${(e) => saveSettings({ autoBuild: e.target.checked, buildSource: source })}
      />
    </label>
  </div>`;
}
