import { html, useEffect, useMemo, useState } from "../vendor/preact-htm.js";
import { act, asset, get, lcu, observe, refresh, useLcu } from "./api.js";
import { loadChampions, loadPerks, loadStyles, positionLabel, useData } from "./data.js";

const PAGES = "/lol-perks/v1/pages";
const CURRENT = "/lol-perks/v1/currentpage";
observe([PAGES, CURRENT]);

function Icon({ perks, styles, id, kind, size = 32 }) {
  let path = null;
  let name = "";
  if (kind === "style") {
    const s = styles && styles.find((x) => x.id === id);
    path = s && s.iconPath;
    name = s ? s.name : "";
  } else {
    const p = perks && perks.get(id);
    path = p && p.iconPath;
    name = p ? p.name : "";
  }
  if (!path) return html`<span class="rune-icon empty" style=${`width:${size}px;height:${size}px`}></span>`;
  return html`<img class="rune-icon" src=${asset(path)} title=${name} alt=${name} width=${size} height=${size} />`;
}

export function PageSummary({ page, perks, styles }) {
  if (!page) return null;
  const ids = page.selectedPerkIds || [];
  return html`<div class="page-summary">
    <${Icon} styles=${styles} id=${page.primaryStyleId} kind="style" size=${24} />
    <${Icon} perks=${perks} id=${ids[0]} size=${44} />
    ${ids.slice(1, 4).map((id) => html`<${Icon} key=${id} perks=${perks} id=${id} size=${28} />`)}
    <span class="sep"></span>
    <${Icon} styles=${styles} id=${page.subStyleId} kind="style" size=${20} />
    ${ids.slice(4, 6).map((id) => html`<${Icon} key=${id} perks=${perks} id=${id} size=${24} />`)}
    <span class="sep"></span>
    ${ids.slice(6, 9).map((id, i) => html`<${Icon} key=${i} perks=${perks} id=${id} size=${18} />`)}
  </div>`;
}

const slotsOf = (style, type) => (style ? style.slots.filter((s) => s.type === type) : []);
const regularSlots = (style) =>
  style ? style.slots.filter((s) => s.type !== "kKeyStone" && s.type !== "kStatMod") : [];

function RuneEditor({ page, perks, styles, onCancel, onSave }) {
  const firstStyle = styles.find((s) => s.id === page?.primaryStyleId) || styles[0];
  const [primary, setPrimary] = useState(firstStyle.id);
  const [sub, setSub] = useState(page?.subStyleId || firstStyle.defaultSubStyle);
  const [sel, setSel] = useState(() => {
    const ids = (page && page.selectedPerkIds) || [];
    return Array.from({ length: 9 }, (_, i) => ids[i] || null);
  });
  const [subOrder, setSubOrder] = useState([4, 5]);
  const [name, setName] = useState(page?.name || "LolMobil");

  const primaryStyle = styles.find((s) => s.id === primary);
  const subStyle = styles.find((s) => s.id === sub);
  const subChoices = styles.filter(
    (s) => s.id !== primary && (!primaryStyle.allowedSubStyles || primaryStyle.allowedSubStyles.includes(s.id))
  );

  const changePrimary = (id) => {
    if (id === primary) return;
    const style = styles.find((s) => s.id === id);
    setPrimary(id);
    const next = [...sel];
    next[0] = next[1] = next[2] = next[3] = null;
    if (sub === id || (style.allowedSubStyles && !style.allowedSubStyles.includes(sub))) {
      setSub(style.defaultSubStyle || (style.allowedSubStyles || [])[0]);
      next[4] = next[5] = null;
    }
    const shardSlots = slotsOf(style, "kStatMod");
    for (let i = 0; i < 3; i++) {
      if (next[6 + i] && shardSlots[i] && !shardSlots[i].perks.includes(next[6 + i])) next[6 + i] = null;
    }
    setSel(next);
  };

  const changeSub = (id) => {
    if (id === sub) return;
    setSub(id);
    const next = [...sel];
    next[4] = next[5] = null;
    setSel(next);
  };

  const pickPrimary = (index, perkId) => {
    const next = [...sel];
    next[index] = perkId;
    setSel(next);
  };

  const rowOf = (perkId) => regularSlots(subStyle).findIndex((slot) => slot.perks.includes(perkId));

  const pickSub = (perkId) => {
    const next = [...sel];
    if (next[4] === perkId || next[5] === perkId) return;
    const row = rowOf(perkId);
    // Same row already used: replace that one. Else replace the oldest.
    let index = [4, 5].find((i) => next[i] && rowOf(next[i]) === row);
    if (index === undefined) index = [4, 5].find((i) => !next[i]);
    if (index === undefined) index = subOrder[0];
    next[index] = perkId;
    setSel(next);
    setSubOrder([index === 4 ? 5 : 4, index]);
  };

  const complete = sel.every(Boolean);

  return html`<div class="rune-editor">
    <input class="search" value=${name} maxlength="40" onInput=${(e) => setName(e.target.value)} />
    <div class="small muted">Arbre principal</div>
    <div class="style-row">
      ${styles.map(
        (s) => html`<button key=${s.id} class="style-btn ${s.id === primary ? "active" : ""}" onClick=${() => changePrimary(s.id)}>
          <${Icon} styles=${styles} id=${s.id} kind="style" size=${30} />
        </button>`
      )}
    </div>
    ${slotsOf(primaryStyle, "kKeyStone").map(
      (slot) => html`<div class="perk-row keystones">
        ${slot.perks.map(
          (id) => html`<button key=${id} class="perk ${sel[0] === id ? "active" : ""}" onClick=${() => pickPrimary(0, id)}>
            <${Icon} perks=${perks} id=${id} size=${46} />
          </button>`
        )}
      </div>`
    )}
    ${regularSlots(primaryStyle).map(
      (slot, row) => html`<div class="perk-row" key=${row}>
        ${slot.perks.map(
          (id) => html`<button key=${id} class="perk ${sel[1 + row] === id ? "active" : ""}" onClick=${() => pickPrimary(1 + row, id)}>
            <${Icon} perks=${perks} id=${id} size=${36} />
          </button>`
        )}
      </div>`
    )}
    <div class="small muted">Arbre secondaire</div>
    <div class="style-row">
      ${subChoices.map(
        (s) => html`<button key=${s.id} class="style-btn ${s.id === sub ? "active" : ""}" onClick=${() => changeSub(s.id)}>
          <${Icon} styles=${styles} id=${s.id} kind="style" size=${26} />
        </button>`
      )}
    </div>
    ${regularSlots(subStyle).map(
      (slot, row) => html`<div class="perk-row" key=${"s" + row}>
        ${slot.perks.map(
          (id) => html`<button key=${id} class="perk ${sel[4] === id || sel[5] === id ? "active" : ""}" onClick=${() => pickSub(id)}>
            <${Icon} perks=${perks} id=${id} size=${32} />
          </button>`
        )}
      </div>`
    )}
    <div class="small muted">Fragments</div>
    ${slotsOf(primaryStyle, "kStatMod").map(
      (slot, row) => html`<div class="perk-row shards" key=${"f" + row}>
        ${slot.perks.map(
          (id) => html`<button key=${id} class="perk ${sel[6 + row] === id ? "active" : ""}" onClick=${() => pickPrimary(6 + row, id)}>
            <${Icon} perks=${perks} id=${id} size=${26} />
          </button>`
        )}
      </div>`
    )}
    <div class="row gap">
      <button class="btn secondary grow" onClick=${onCancel}>Annuler</button>
      <button
        class="btn grow"
        disabled=${!complete}
        onClick=${() => onSave({ name: name || "LolMobil", primaryStyleId: primary, subStyleId: sub, selectedPerkIds: sel })}
      >
        Enregistrer
      </button>
    </div>
  </div>`;
}

// Writes a page: into the current page when it is editable, else into a new
// page, else over the first editable page.
async function savePage(draft, current, pages) {
  const body = { ...draft, current: true };
  if (current && current.isEditable) {
    await lcu("PUT", `${PAGES}/${current.id}`, { ...current, ...body, id: current.id });
  } else {
    try {
      await lcu("POST", PAGES, body);
    } catch (e) {
      const editable = (pages || []).find((p) => p.isEditable && p.isDeletable);
      if (!editable) throw e;
      await lcu("PUT", `${PAGES}/${editable.id}`, { ...editable, ...body, id: editable.id });
      await lcu("PUT", CURRENT, editable.id);
    }
  }
  refresh(PAGES);
  refresh(CURRENT);
  return true;
}

function Recommended({ myCell, perks, styles, onApply }) {
  const session = useLcu("/lol-gameflow/v1/session");
  const champions = useData(loadChampions);
  const [list, setList] = useState(null);
  const mapId = session && session.map ? session.map.id : 11;
  const position = (myCell.assignedPosition || "none").toLowerCase() || "none";

  useEffect(() => {
    if (!(myCell.championId > 0)) {
      setList(null);
      return;
    }
    let alive = true;
    get(`/lol-perks/v1/recommended-pages/champion/${myCell.championId}/position/${position}/map/${mapId}`)
      .then((r) => alive && setList(Array.isArray(r) ? r : []))
      .catch(() => alive && setList([]));
    return () => {
      alive = false;
    };
  }, [myCell.championId, position, mapId]);

  if (!list || !list.length) return null;
  const champ = champions && champions.get(myCell.championId);
  return html`<div class="recommended">
    <h4>Recommandées pour ${champ ? champ.name : "ce champion"}</h4>
    ${list.slice(0, 4).map((rec, i) => {
      const ids = (rec.perks || []).map((p) => (typeof p === "number" ? p : p.id));
      const page = { primaryStyleId: rec.primaryPerkStyleId, subStyleId: rec.secondaryPerkStyleId, selectedPerkIds: ids };
      return html`<div class="rec" key=${i}>
        <${PageSummary} page=${page} perks=${perks} styles=${styles} />
        <button
          class="btn small"
          onClick=${() =>
            onApply({
              name: `${champ ? champ.name : "Reco"} ${rec.position ? positionLabel(rec.position) : ""}`.trim(),
              ...page,
            })}
        >
          Appliquer
        </button>
      </div>`;
    })}
  </div>`;
}

export function RunesTab({ myCell }) {
  const pages = useLcu(PAGES);
  const current = useLcu(CURRENT);
  const perks = useData(loadPerks);
  const styles = useData(loadStyles);
  const [editing, setEditing] = useState(false);

  const usable = useMemo(() => (pages || []).filter((p) => p.isValid !== false && !p.isTemporary), [pages]);

  if (pages === undefined || !perks || !styles) return html`<div class="spinner"></div>`;

  const apply = async (draft, okText) => {
    const ok = await act(savePage(draft, current, pages), okText);
    return ok !== undefined;
  };

  if (editing) {
    return html`<${RuneEditor}
      page=${current}
      perks=${perks}
      styles=${styles.filter((s) => s.slots && s.slots.length)}
      onCancel=${() => setEditing(false)}
      onSave=${async (draft) => {
        if (await apply(draft, "Page de runes enregistrée")) setEditing(false);
      }}
    />`;
  }

  return html`<div class="runes">
    <div class="current-page">
      <div class="row space">
        <div>
          <div class="small muted">Page active</div>
          <b>${current ? current.name : "—"}</b>
        </div>
        <button class="btn small" onClick=${() => setEditing(true)}>
          ${current && current.isEditable ? "Modifier" : "Créer"}
        </button>
      </div>
      <${PageSummary} page=${current} perks=${perks} styles=${styles} />
    </div>
    <${Recommended}
      myCell=${myCell}
      perks=${perks}
      styles=${styles}
      onApply=${(draft) => apply(draft, "Runes recommandées appliquées")}
    />
    <h4>Mes pages</h4>
    <div class="page-list">
      ${usable.map(
        (p) => html`<button
          key=${p.id}
          class="page-item ${current && current.id === p.id ? "active" : ""}"
          onClick=${async () => {
            const ok = await act(lcu("PUT", CURRENT, p.id));
            if (ok !== undefined) refresh(CURRENT);
          }}
        >
          <${Icon} perks=${perks} id=${(p.selectedPerkIds || [])[0]} size=${30} />
          <span class="grow">${p.name}</span>
          ${current && current.id === p.id && html`<span class="tag owned">Active</span>`}
        </button>`
      )}
    </div>
  </div>`;
}

