/**
 * @name LolMobil-Remote
 * @author LolMobil
 * @description Lets the LolMobil phone app pick skins in champion select (works with Rose)
 * @link https://github.com/tannenovitch13/lol-mobil
 *
 * Rose learns the skin to inject from the skin name shown in the champion
 * select carousel (ROSE-SkinMonitor reads it). So when the phone picks a
 * skin, this plugin moves the client's own carousel to it, exactly like a
 * click: Rose then detects it, and the client selects it if it is owned.
 *
 * The plugin connects to the LolMobil Companion running on this PC
 * (ws://127.0.0.1:<port>/plugin). It never talks to the network.
 */
(() => {
  "use strict";

  const VERSION = "1.0.0";
  const LOG = "[LolMobil-Remote]";
  // Replaced by the companion's port when it installs the plugin.
  const PORT_SETTING = "__LOLMOBIL_PORT__";
  const PORT = /^\d+$/.test(PORT_SETTING) ? Number(PORT_SETTING) : 8787;
  const URL = `ws://127.0.0.1:${PORT}/plugin`;

  // Champion select layouts the plugin knows how to drive.
  const LAYOUTS = [
    {
      name: "classic",
      items: ".skin-selection-carousel .skin-selection-item",
      title: [".skin-name-text"],
      isSelected: (item) =>
        item.classList.contains("skin-selection-item-selected") || carouselOffset(item) === 2,
      clickTarget: (item) => item.querySelector(".skin-selection-thumbnail") || item,
    },
    {
      name: "rift-classic",
      items: ".skins-pane .skins-pane__skin-card",
      title: [".skins-pane__skin-title"],
      isSelected: (item) => item.classList.contains("skins-pane__skin-card--center-tile"),
      clickTarget: (item) => item,
    },
  ];

  const log = (...args) => console.log(LOG, ...args);
  const warn = (...args) => console.warn(LOG, ...args);
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  function norm(value) {
    return String(value || "")
      .normalize("NFKC")
      .toLowerCase()
      .replace(/\s+/g, " ")
      .trim();
  }

  function carouselOffset(item) {
    for (const cls of item.classList) {
      const match = /^skin-carousel-offset-(-?\d+)$/.exec(cls);
      if (match) return Number(match[1]);
    }
    return null;
  }

  function visible(el) {
    return el.offsetParent !== null || getComputedStyle(el).position === "fixed";
  }

  function activeLayout() {
    for (const layout of LAYOUTS) {
      if (document.querySelector(layout.items)) return layout;
    }
    return null;
  }

  function displayedName(layout) {
    const selectors = layout ? layout.title : [".skin-name-text", ".skins-pane__skin-title", ".skin-name"];
    for (const selector of selectors) {
      let fallback = null;
      for (const node of document.querySelectorAll(selector)) {
        const text = (node.textContent || "").trim();
        if (!text) continue;
        if (visible(node)) return text;
        fallback = fallback || text;
      }
      if (fallback) return fallback;
    }
    return null;
  }

  function tileOf(item) {
    const candidates = [item, ...item.querySelectorAll("*")];
    for (const el of candidates) {
      if (el.tagName === "IMG" && el.getAttribute("src")) return el.getAttribute("src");
      const bg = el.style && el.style.backgroundImage;
      const match = bg && /url\(["']?([^"')]+)["']?\)/.exec(bg);
      if (match) return match[1];
    }
    return null;
  }

  function samePath(a, b) {
    if (!a || !b) return false;
    const clean = (p) => {
      try {
        p = decodeURIComponent(p);
      } catch (e) {
        /* keep as is */
      }
      return p.replace(/^[a-z]+:\/\/[^/]+/i, "").toLowerCase();
    };
    const x = clean(a);
    const y = clean(b);
    return x === y || x.endsWith(y) || y.endsWith(x);
  }

  function findTarget(layout, cmd) {
    const items = [...document.querySelectorAll(layout.items)];
    if (cmd.tilePath) {
      const byTile = items.find((item) => samePath(tileOf(item), cmd.tilePath));
      if (byTile) return { items, target: byTile };
    }
    // The carousel lists skins in the order of
    // /lol-champ-select/v1/skin-carousel-skins.
    if (Number.isInteger(cmd.index) && cmd.index >= 0 && cmd.index < items.length) {
      return { items, target: items[cmd.index] };
    }
    return { items, target: null };
  }

  // Done when the client shows the skin's name (what Rose reads), or when
  // the requested tile is the centred one.
  function isDone(layout, cmd, target) {
    const shown = displayedName(layout);
    if (cmd.name && shown && norm(shown) === norm(cmd.name)) return true;
    return Boolean(target && layout.isSelected(target));
  }

  function click(el, full) {
    const opts = { bubbles: true, cancelable: true, composed: true, view: window, button: 0 };
    if (full) {
      el.dispatchEvent(new PointerEvent("pointerdown", opts));
      el.dispatchEvent(new MouseEvent("mousedown", opts));
      el.dispatchEvent(new PointerEvent("pointerup", opts));
      el.dispatchEvent(new MouseEvent("mouseup", opts));
    }
    el.dispatchEvent(new MouseEvent("click", opts));
  }

  async function waitFor(check, timeoutMs) {
    const end = Date.now() + timeoutMs;
    while (Date.now() < end) {
      if (check()) return true;
      await sleep(50);
    }
    return check();
  }

  async function selectSkin(cmd) {
    const layout = activeLayout();
    if (!layout) {
      return { ok: false, error: "Carrousel de skins introuvable (verrouille ton champion d'abord)" };
    }
    let { target } = findTarget(layout, cmd);
    if (!target) {
      return { ok: false, error: "Skin introuvable dans le carrousel" };
    }
    if (isDone(layout, cmd, target)) {
      return { ok: true, shown: displayedName(layout), method: "already" };
    }

    // 1. Click the skin directly (the client centres any clicked skin).
    for (const full of [false, true]) {
      click(layout.clickTarget(target), full);
      if (await waitFor(() => isDone(layout, cmd, target), 700)) {
        return { ok: true, shown: displayedName(layout), method: "click" };
      }
      ({ target } = findTarget(layout, cmd));
      if (!target) break;
    }

    // 2. Walk the carousel one neighbour at a time towards the skin.
    for (let step = 0; step < 80; step++) {
      const found = findTarget(layout, cmd);
      target = found.target;
      if (!target) break;
      if (isDone(layout, cmd, target)) {
        return { ok: true, shown: displayedName(layout), method: "walk" };
      }
      const items = found.items;
      const selectedIndex = items.findIndex((item) => layout.isSelected(item));
      const targetIndex = items.indexOf(target);
      if (selectedIndex < 0 || targetIndex < 0 || selectedIndex === targetIndex) break;
      const neighbour = items[selectedIndex + (targetIndex > selectedIndex ? 1 : -1)];
      const before = displayedName(layout);
      click(layout.clickTarget(neighbour), step % 2 === 1);
      await waitFor(() => displayedName(layout) !== before, 600);
    }

    const shown = displayedName(layout);
    ({ target } = findTarget(layout, cmd));
    if (isDone(layout, cmd, target)) {
      return { ok: true, shown, method: "walk" };
    }
    return { ok: false, shown, error: "Le carrousel n'a pas pu être déplacé jusqu'à ce skin" };
  }

  function state() {
    const layout = activeLayout();
    const items = layout ? [...document.querySelectorAll(layout.items)] : [];
    return {
      ok: true,
      layout: layout ? layout.name : null,
      shown: displayedName(layout),
      count: items.length,
      selectedIndex: layout ? items.findIndex((item) => layout.isSelected(item)) : -1,
      rose: Boolean(window.__roseBridge),
    };
  }

  // ------------------------------------------------------------ connection

  let socket = null;
  let retryMs = 1000;
  let lastReported = null;

  function send(message) {
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(message));
    }
  }

  async function handle(message) {
    let result;
    try {
      if (message.type === "select-skin") {
        result = await selectSkin(message);
      } else if (message.type === "get-state") {
        result = state();
      } else {
        return;
      }
    } catch (error) {
      warn("command failed", error);
      result = { ok: false, error: String((error && error.message) || error) };
    }
    send({ type: "result", id: message.id, ...result });
  }

  function connect() {
    try {
      socket = new WebSocket(URL);
    } catch (error) {
      scheduleReconnect();
      return;
    }
    socket.addEventListener("open", () => {
      retryMs = 1000;
      lastReported = null;
      log(`connected to the companion on port ${PORT}`);
      send({ type: "hello", version: VERSION });
    });
    socket.addEventListener("message", (event) => {
      let message;
      try {
        message = JSON.parse(event.data);
      } catch (error) {
        return;
      }
      if (message && message.type) handle(message);
    });
    socket.addEventListener("close", () => {
      socket = null;
      scheduleReconnect();
    });
    socket.addEventListener("error", () => {
      /* close follows */
    });
  }

  function scheduleReconnect() {
    setTimeout(connect, retryMs);
    retryMs = Math.min(retryMs * 2, 15000);
  }

  // Tell the phone which skin the client shows (cheap: only while a carousel
  // exists, i.e. during champion select).
  setInterval(() => {
    if (!socket || socket.readyState !== WebSocket.OPEN) return;
    const layout = activeLayout();
    const shown = layout ? displayedName(layout) : null;
    if (shown !== lastReported) {
      lastReported = shown;
      send({ type: "carousel", shown });
    }
  }, 500);

  window.__lolMobilRemote = { version: VERSION, selectSkin, state };
  log(`v${VERSION} loaded`);
  connect();
})();
