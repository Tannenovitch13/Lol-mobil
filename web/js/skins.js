import { html, useEffect, useState } from "../vendor/preact-htm.js";
import { asset, observe, refresh, selectChroma, selectSkin, toast, useLcu, useStore } from "./api.js";

const SKINS = "/lol-champ-select/v1/skin-carousel-skins";
observe([SKINS]);

const norm = (s) => String(s || "").normalize("NFKC").toLowerCase().replace(/\s+/g, " ").trim();

const isOwned = (skin) => Boolean(skin.unlocked || (skin.ownership && skin.ownership.owned));

function chromaStyle(chroma) {
  const colors = (chroma.colors || []).filter(Boolean);
  if (colors.length >= 2) return `background: linear-gradient(135deg, ${colors[0]} 50%, ${colors[1]} 50%)`;
  if (colors.length === 1) return `background: ${colors[0]}`;
  if (chroma.chromaPreviewPath) return `background-image: url(${asset(chroma.chromaPreviewPath)}); background-size: cover`;
  return "background: #555";
}

export function SkinsTab({ myCell }) {
  const all = useLcu(SKINS);
  const status = useStore((s) => s.status);
  const roseSkin = useStore((s) => s.rose["skin-state"]);
  const roseChroma = useStore((s) => s.rose["chroma-state"]);
  const carousel = useStore((s) => s.carousel);
  const [busy, setBusy] = useState(null);
  const [focusId, setFocusId] = useState(null);

  useEffect(() => {
    refresh(SKINS);
  }, [myCell.championId]);

  const skins = (all || []).filter((s) => !s.championId || s.championId === myCell.championId);
  if (all === undefined) return html`<div class="spinner"></div>`;
  if (!skins.length) return html`<p class="muted">Aucun skin disponible pour ce champion.</p>`;

  const roseForChamp =
    roseSkin && (!roseSkin.championId || roseSkin.championId === myCell.championId) ? roseSkin : null;
  // What the client's carousel shows is what Rose will inject.
  const shownSkin =
    (carousel && skins.find((s) => norm(s.name) === norm(carousel))) ||
    (roseForChamp && skins.find((s) => s.id === roseForChamp.skinId)) ||
    skins.find((s) => s.id === myCell.selectedSkinId || (s.childSkins || []).some((c) => c.id === myCell.selectedSkinId));
  const focused = skins.find((s) => s.id === focusId) || shownSkin;
  const canUnowned = status.plugin;

  const pick = async (skin, index) => {
    const owned = isOwned(skin);
    setFocusId(skin.id);
    if (!owned && !canUnowned) {
      toast(
        status.rose
          ? "Plugin LolMobil-Remote inactif : redémarre le client LoL après l'installation."
          : "Skin non possédé : il faut Rose et le plugin LolMobil-Remote.",
        "error",
        5000
      );
      return;
    }
    setBusy(skin.id);
    try {
      const res = await selectSkin({
        skinId: skin.id,
        championId: myCell.championId,
        name: skin.name,
        tilePath: skin.tilePath,
        index,
        owned,
      });
      if (res.error || res.status >= 300) throw new Error(res.error || `Erreur ${res.status}`);
    } catch (e) {
      toast(e.message, "error", 5000);
    }
    setBusy(null);
  };

  const pickChroma = async (skin, chroma) => {
    const owned = isOwned(chroma);
    if (!owned && !status.rose) {
      toast("Chroma non possédé : Rose doit être lancé.", "error");
      return;
    }
    setBusy(chroma.id);
    try {
      const res = await selectChroma({
        chromaId: chroma.id,
        name: chroma.name,
        championId: myCell.championId,
        baseSkinId: skin.id,
        owned,
      });
      if (res.error || res.status >= 300) throw new Error(res.error || `Erreur ${res.status}`);
    } catch (e) {
      toast(e.message, "error", 5000);
    }
    setBusy(null);
  };

  const selectedChroma =
    (roseChroma && roseChroma.selectedChromaId) || (focused && myCell.selectedSkinId !== focused.id ? myCell.selectedSkinId : null);

  return html`<div class="skins">
    <div class="skin-status">
      ${shownSkin
        ? html`<span>Dans le client : <b>${shownSkin.name}</b></span>`
        : html`<span class="muted">Choisis un skin</span>`}
      ${status.rose
        ? roseForChamp && roseForChamp.skinName
          ? html`<span class="rose-line">🌹 Rose : <b>${roseForChamp.skinName}</b></span>`
          : html`<span class="rose-line muted">🌹 Rose connecté</span>`
        : html`<span class="muted small">Rose non détecté : seuls tes skins sont utilisables.</span>`}
    </div>
    ${focused &&
    focused.childSkins &&
    focused.childSkins.length > 0 &&
    html`<div class="chromas">
      <span class="small muted">Chromas de ${focused.name}</span>
      <div class="chroma-list">
        ${focused.childSkins.map(
          (c) => html`<button
            key=${c.id}
            class="chroma ${selectedChroma === c.id ? "active" : ""} ${isOwned(c) ? "" : "unowned"}"
            style=${chromaStyle(c)}
            title=${c.name}
            disabled=${busy === c.id}
            onClick=${() => pickChroma(focused, c)}
          ></button>`
        )}
      </div>
    </div>`}
    <div class="skin-grid">
      ${skins.map((skin, index) => {
        const owned = isOwned(skin);
        const shown = shownSkin && shownSkin.id === skin.id;
        const usable = owned || canUnowned;
        return html`<button
          key=${skin.id}
          class="skin ${shown ? "shown" : ""} ${usable ? "" : "locked"} ${busy === skin.id ? "busy" : ""}"
          onClick=${() => pick(skin, index)}
        >
          <img loading="lazy" src=${asset(skin.tilePath)} alt="" />
          <div class="skin-name">${skin.name}</div>
          <div class="skin-badges">
            ${owned
              ? html`<span class="tag owned">Possédé</span>`
              : html`<span class="tag ${canUnowned ? "rose" : ""}">${canUnowned ? "🌹 Rose" : "🔒"}</span>`}
            ${skin.childSkins && skin.childSkins.length > 0 && html`<span class="tag">${skin.childSkins.length} chromas</span>`}
          </div>
        </button>`;
      })}
    </div>
  </div>`;
}
