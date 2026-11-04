// Static game data read from the client (champions, spells, runes, queues),
// cached for the session.

import { asset, get, useStore } from "./api.js";
import { useEffect, useState } from "../vendor/preact-htm.js";

const cache = new Map();

function load(key, fn) {
  if (!cache.has(key)) {
    const promise = fn().catch((e) => {
      cache.delete(key);
      throw e;
    });
    cache.set(key, promise);
  }
  return cache.get(key);
}

export function clearDataCache() {
  cache.clear();
}

export const loadChampions = () =>
  load("champions", async () => {
    const list = await get("/lol-game-data/assets/v1/champion-summary.json");
    const byId = new Map();
    for (const c of list) if (c.id > 0) byId.set(c.id, c);
    return byId;
  });

export const loadSpells = () =>
  load("spells", async () => {
    const list = await get("/lol-game-data/assets/v1/summoner-spells.json");
    return list.filter((s) => s.id > 0);
  });

export const loadPerks = () =>
  load("perks", async () => {
    const list = await get("/lol-perks/v1/perks");
    return new Map(list.map((p) => [p.id, p]));
  });

export const loadStyles = () =>
  load("styles", async () => {
    const list = await get("/lol-perks/v1/styles");
    return list;
  });

export const loadQueues = () =>
  load("queues", async () => {
    const list = await get("/lol-game-queues/v1/queues");
    return list;
  });

// React-ish hook: value of a loader once ready (undefined before).
export function useData(loader) {
  const lcuOk = useStore((s) => s.status.lcu);
  const [value, setValue] = useState(undefined);
  useEffect(() => {
    if (!lcuOk) return undefined;
    let alive = true;
    loader()
      .then((v) => alive && setValue(v))
      .catch(() => alive && setValue(null));
    return () => {
      alive = false;
    };
  }, [lcuOk]);
  return value;
}

export const POSITIONS = {
  TOP: "Haut",
  JUNGLE: "Jungle",
  MIDDLE: "Milieu",
  BOTTOM: "Bas",
  UTILITY: "Support",
  FILL: "Rempli",
  UNSELECTED: "—",
};

export function positionLabel(pos) {
  if (!pos) return "";
  return POSITIONS[String(pos).toUpperCase()] || pos;
}

export const championIcon = (id) =>
  id > 0 ? asset(`/lol-game-data/assets/v1/champion-icons/${id}.png`) : "";

export const profileIcon = (id) =>
  id >= 0 && id !== undefined && id !== null ? asset(`/lol-game-data/assets/v1/profile-icons/${id}.jpg`) : "";

// Accent/case-insensitive search key.
export function searchKey(text) {
  return String(text || "")
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[^a-z0-9]/gi, "")
    .toLowerCase();
}
