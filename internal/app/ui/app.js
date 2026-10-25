"use strict";
(function () {
  // ------------------------------------------------------------------ api --
  const call = (name, ...args) => {
    const f = window[name];
    if (typeof f !== "function") return Promise.reject("PlaySwap isn't ready yet.");
    return f(...args);
  };
  const api = {
    ready: () => call("ps_ready"),
    state: () => call("ps_state"),
    savePlayers: (p) => call("ps_savePlayers", p),
    saveGame: (g) => call("ps_saveGame", g),
    deleteGame: (id) => call("ps_deleteGame", id),
    saveSettings: (s) => call("ps_saveSettings", s),
    steamGames: () => call("ps_steamGames"),
    inspectSteam: (id) => call("ps_inspectSteam", id),
    inspectExe: (p) => call("ps_inspectExe", p),
    checkPath: (p) => call("ps_checkPath", p),
    browse: (id, kind, start) => call("ps_browse", id, kind, start || ""),
    play: (r) => call("ps_play", r),
    load: (r) => call("ps_load", r),
    shortcut: (id) => call("ps_shortcut", id),
    open: (kind, arg) => call("ps_open", kind, arg || ""),
    log: () => call("ps_log"),
    quit: () => call("ps_quit"),
    steamSetup: () => call("ps_steamSetup"),
    copy: (t) => call("ps_copy", t),
    steamPick: (r) => call("ps_steamPick", r),
    cancelRequest: (id) => call("ps_cancelRequest", id),
    found: () => call("ps_found"),
    scan: () => call("ps_scan"),
    ignoreFound: (k) => call("ps_ignoreFound", k),
    addFound: (keys) => call("ps_addFound", keys),
    foundGame: (k) => call("ps_foundGame", k),
    applyHooks: (restart) => call("ps_applyHooks", restart),
  };

  // ---------------------------------------------------------------- state --
  const S = {
    d: null,          // AppState from Go
    view: "games",
    menuFor: null,
    picker: null,     // {gameId, mode, ask, playerId, owner, newMode, working}
    dialog: null,     // {kind, ...}
    edit: null,       // editor state
    players: null,    // players being edited
    logText: "",
    hooks: null,        // Steam launch option status per game
    hideHookBanner: false,
    found: { scanning: true, games: [] },
    adding: false,
    hooksBusy: false,
  };
  const browseWaiters = {};
  let seq = 0;

  const $ = (sel, root = document) => root.querySelector(sel);
  const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const errText = (e) => (typeof e === "string" ? e : (e && e.message) || "Something went wrong.");
  const cfg = () => S.d.config;
  const player = (id) => cfg().players.find((p) => p.id === id);
  const game = (id) => cfg().games.find((g) => g.id === id);
  const color = (id) => (player(id) || {}).color || "var(--dim)";
  const pname = (id) => (player(id) || {}).name || "Someone";
  const session = (gid) => (S.d.sessions || []).find((s) => s.gameId === gid);
  const hasProfile = (gid, pid) => ((S.d.profiles || {})[gid] || []).includes(pid);
  const possessive = (n) => (/s$/i.test(n) ? n + "’" : n + "’s");
  const fmtSize = (b) => (b < 1024 ? b + " B" : b < 1048576 ? (b / 1024).toFixed(0) + " KB" : b < 1073741824 ? (b / 1048576).toFixed(1) + " MB" : (b / 1073741824).toFixed(2) + " GB");
  const short = (name) => (name.length > 22 ? name.slice(0, 20).trim() + "…" : name);

  const icon = {
    dots: '<svg viewBox="0 0 16 16"><circle cx="3" cy="8" r="1.4" fill="currentColor"/><circle cx="8" cy="8" r="1.4" fill="currentColor"/><circle cx="13" cy="8" r="1.4" fill="currentColor"/></svg>',
    folder: '<svg viewBox="0 0 16 16"><path d="M1.75 3.5h4l1.5 1.5h7v7.25a.75.75 0 0 1-.75.75H2.5a.75.75 0 0 1-.75-.75z" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/></svg>',
    x: '<svg viewBox="0 0 16 16"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>',
    back: '<svg viewBox="0 0 16 16"><path d="M10 3 5 8l5 5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    plus: '<svg viewBox="0 0 16 16"><path d="M8 3v10M3 8h10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
  };

  // --------------------------------------------------------------- toasts --
  function toast(text, kind) {
    const el = document.createElement("div");
    el.className = "toast" + (kind === "error" ? " error" : "");
    el.textContent = text;
    $("#toasts").appendChild(el);
    setTimeout(() => el.remove(), kind === "error" ? 7000 : 3800);
  }

  // ---------------------------------------------------------------- events --
  window.__ps = function (ev) {
    switch (ev.type) {
      case "sessions":
        if (!S.d) return;
        S.d.sessions = ev.sessions || [];
        S.d.active = ev.active || {};
        onSessions();
        refresh();
        break;
      case "launch":
        openPicker(ev.gameId, ev.reqId ? "steam" : "play", ev.reqId);
        break;
      case "found":
        loadFound();
        break;
      case "hooks":
        loadHooks().then(() => { render(); if (S.dialog && S.dialog.kind === "steam") renderOverlay(); });
        break;
      case "pickerClosed":
        if (S.picker && S.picker.reqId) { S.picker = null; render(); }
        break;
      case "toast":
        toast(ev.text, ev.kind);
        break;
      case "browse": {
        const w = browseWaiters[ev.reqId];
        if (w) { delete browseWaiters[ev.reqId]; w(ev); }
        break;
      }
    }
  };

  function browse(kind, start) {
    const id = "b" + ++seq;
    return new Promise((resolve) => {
      browseWaiters[id] = resolve;
      api.browse(id, kind, start).catch((e) => { delete browseWaiters[id]; resolve({ error: errText(e) }); });
    });
  }

  async function refresh() {
    try { S.d = await api.state(); } catch (e) { toast(errText(e), "error"); return; }
    render();
  }

  // ---------------------------------------------------------------- render --
  function render() {
    if (!S.d) return;
    document.documentElement.style.setProperty("--p1", (cfg().players[0] || {}).color || "#4F8CFF");
    document.documentElement.style.setProperty("--p2", (cfg().players[1] || {}).color || "#FF5D8F");
    const top = S.view === "edit" ? "games" : S.view;
    document.querySelectorAll(".tab").forEach((t) => t.setAttribute("aria-selected", String(t.dataset.view === top)));
    renderNow();
    const v = $("#view");
    const keepScroll = v.scrollTop;
    if (S.view === "games") v.innerHTML = viewGames();
    else if (S.view === "edit") v.innerHTML = viewEdit();
    else if (S.view === "players") v.innerHTML = viewPlayers();
    else if (S.view === "settings") v.innerHTML = viewSettings();
    v.scrollTop = keepScroll;
    renderOverlay();
  }

  function renderNow() {
    const playing = (S.d.sessions || []).filter((s) => s.phase === "playing");
    $("#now").innerHTML = playing
      .map((s) => `<span class="now-pill"><span class="dot" style="background:${esc(color(s.playerId))}"></span><span><b>${esc(pname(s.playerId))}</b> is playing ${esc(short((game(s.gameId) || {}).name || ""))}</span></span>`)
      .join("");
  }

  // ----------------------------------------------------------------- games --
  function launchLabel(g) {
    if (g.launch.type === "steam") return "Steam";
    const p = g.launch.exe || "";
    return p.split(/[\\/]/).pop();
  }

  function sessionText(s) {
    const n = pname(s.playerId);
    return { loading: `Loading ${possessive(n)} files`, launching: "Starting", playing: `${n} is playing`, saving: `Saving ${possessive(n)} progress` }[s.phase] || "";
  }

  function viewGames() {
    const games = cfg().games;
    const head = `<div class="page-head"><div><h1>Games</h1><p class="sub">Choose who's playing before a game starts. Everyone keeps their own settings and saves.</p></div>
      ${games.length ? `<button class="btn" data-action="add-game">${icon.plus}Add game</button>` : ""}</div>`;
    if (!games.length) {
      const cs2 = S.d.steam.found;
      if ((S.found.games || []).length || S.found.scanning) return `<div class="page">${head}${foundSection(true)}
        <div class="row" style="margin-top:18px"><button class="btn" data-action="add-game">${icon.plus}Add a game by hand</button></div></div>`;
      return `<div class="page">${head}<div class="empty">
        <div><h2>Add the first game you share</h2><p class="sub">PlaySwap needs to know where the game keeps its settings and saves. For Steam games it can find most of that for you.</p></div>
        <div class="quick">
          <button class="btn primary" data-action="add-game">${icon.plus}Add a game</button>
          ${cs2 ? `<button class="btn" data-action="add-steam" data-app="730">Add Counter-Strike 2</button>` : ""}
        </div></div></div>`;
    }
    return `<div class="page">${head}${hookBanner()}<div class="games">${games.map(gameRow).join("")}</div>${foundSection(false)}</div>`;
  }

  function hookBanner() {
    if (!S.hooks || S.hideHookBanner) return "";
    const missing = S.hooks.games.filter((h) => !h.detected && game(h.gameId));
    if (!missing.length) return "";
    const what = missing.length === 1 ? esc(missing[0].name) : missing.length + " games";
    const text = S.hooks.steamRunning
      ? `So that Steam's Play button asks who's playing ${what}, Steam has to restart once (about 10 seconds). PlaySwap does it for you.`
      : `PlaySwap can set Steam up so its Play button asks who's playing ${what}.`;
    return `<div class="banner"><div><b>Make Steam ask who's playing</b><p>${text}</p></div>
      <div class="row"><button class="btn" data-action="hooks-apply" ${S.hooksBusy ? "disabled" : ""}>${S.hooksBusy ? "Setting up" : S.hooks.steamRunning ? "Restart Steam and set up" : "Set up"}</button>
      <button class="btn quiet" data-action="hide-banner">Later</button></div></div>`;
  }

  // ----------------------------------------------------------- found list --
  async function loadFound() {
    try { S.found = await api.found(); } catch (e) { /* not ready */ }
    if (S.view === "games" && !S.picker) render();
  }

  const sourceLabel = (f) => ({ steam: "Steam", epic: "Epic Games", gog: "GOG", shortcut: "Shortcut", folder: "Folder" }[f.source] || f.source);

  function foundSection(empty) {
    const F = S.found;
    const list = F.games || [];
    const ready = list.filter((f) => f.ready);
    if (!list.length && !F.scanning && !empty) return "";
    const head = `<div class="found-head"><div><h2>${empty ? "Games found on this PC" : "Found on this PC"}</h2>
        <p class="sub">${F.scanning ? "Scanning Steam, Epic, GOG, game folders and shortcuts" : list.length ? "Not in PlaySwap yet. Ready ones have their settings and saves located already." : "Nothing new found. Add a game by hand if it's missing."}</p></div>
        <div class="row">${ready.length > 1 ? `<button class="btn primary" data-action="add-all" ${S.adding ? "disabled" : ""}>Add all ${ready.length} ready</button>` : ""}
        <button class="btn quiet" data-action="rescan" ${F.scanning ? "disabled" : ""}>${F.scanning ? "Scanning" : "Scan again"}</button></div></div>`;
    const rows = list.map((f) => `<div class="found">
        <div class="grow"><b>${esc(f.name)}</b>
          <div class="game-meta"><span>${esc(sourceLabel(f))}</span>
          ${f.ready ? `<span class="ok-text">${f.paths.length === 1 ? "Save location found" : f.paths.length + " save locations found"}</span>` : `<span>Needs its save location</span>`}</div></div>
        <button class="btn small" data-action="found-add" data-key="${esc(f.key)}" ${S.adding ? "disabled" : ""}>${f.ready ? "Add" : "Choose"}</button>
        <button class="btn small quiet" data-action="found-hide" data-key="${esc(f.key)}" aria-label="Hide ${esc(f.name)}">Hide</button>
      </div>`).join("");
    return `<section class="found-section">${head}${list.length ? `<div class="found-list">${rows}</div>` : ""}</section>`;
  }

  function gameRow(g) {
    const owner = S.d.active[g.id];
    const s = session(g.id);
    const profiles = (S.d.profiles[g.id] || []).length;
    const who = owner && player(owner)
      ? `<span class="who"><span class="dot" style="background:${esc(color(owner))}"></span>${esc(possessive(pname(owner)))} files loaded</span>`
      : `<span>Not set up yet</span>`;
    const actions = s
      ? `<span class="status"><span class="dot" style="background:${esc(color(s.playerId))}"></span>${esc(sessionText(s))}</span>`
      : `<button class="btn primary" data-action="play" data-game="${esc(g.id)}">Play</button>
         <div class="menu-wrap">
           <button class="btn icon quiet" data-action="menu" data-game="${esc(g.id)}" aria-label="More for ${esc(g.name)}" aria-expanded="${S.menuFor === g.id}">${icon.dots}</button>
           ${S.menuFor === g.id ? gameMenu(g) : ""}
         </div>`;
    return `<article class="game${s ? " busy" : ""}">
      <div class="game-owner" style="${owner && player(owner) ? `background:${esc(color(owner))}` : ""}"></div>
      <div class="game-body">
        <div class="game-name">${esc(g.name)}</div>
        <div class="game-meta">${who}<span>${esc(launchLabel(g))}</span><span>${profiles === 1 ? "1 profile" : profiles + " profiles"}</span></div>
      </div>
      <div class="game-actions">${actions}</div>
    </article>`;
  }

  function gameMenu(g) {
    return `<div class="menu" role="menu">
      <button role="menuitem" data-action="edit-game" data-game="${esc(g.id)}">Edit game</button>
      <button role="menuitem" data-action="load-only" data-game="${esc(g.id)}">Load someone's files without playing</button>
      ${g.launch.type === "steam" ? `<button role="menuitem" data-action="steam-setup" data-game="${esc(g.id)}">Ask who's playing when started from Steam</button>` : ""}
      <button role="menuitem" data-action="shortcut" data-game="${esc(g.id)}">Create desktop shortcut</button>
      <hr>
      <button role="menuitem" data-action="open-path" data-path="${esc((g.paths[0] || {}).path || "")}">Open the game's save folder</button>
      <button role="menuitem" data-action="open-backups" data-game="${esc(g.id)}">Open backups</button>
      <hr>
      <button role="menuitem" class="danger" data-action="remove-game" data-game="${esc(g.id)}">Remove game</button>
    </div>`;
  }

  // ---------------------------------------------------------------- picker --
  function openPicker(gameId, mode, reqId) {
    const g = game(gameId);
    if (!g) { toast("That game isn't in PlaySwap anymore.", "error"); if (reqId) api.cancelRequest(reqId); return; }
    if (S.picker && S.picker.reqId && S.picker.reqId !== reqId) api.cancelRequest(S.picker.reqId);
    S.menuFor = null;
    if (S.dialog && S.dialog.kind !== "welcome") S.dialog = null;
    S.picker = { gameId, mode: mode || "play", reqId: reqId || "" };
    render();
    setTimeout(() => { const pk = $(".picker"); if (pk) pk.focus(); }, 30); // keyboard 1-9 works without favouring anyone
  }

  function closePicker() {
    if (S.picker && S.picker.reqId && !S.picker.working) api.cancelRequest(S.picker.reqId);
    S.picker = null;
    render();
  }

  function slotState(gid, pid) {
    if (S.d.active[gid] === pid) return "Files loaded now";
    if (hasProfile(gid, pid)) return "Has a profile";
    return "First time";
  }

  function viewPicker() {
    const P = S.picker, g = game(P.gameId);
    if (!g) return "";
    if (P.working) {
      const c = color(P.playerId);
      return `<div class="picker" style="--c:${esc(c)}"><div class="picker-head"><div></div></div>
        <div class="working"><div class="big" style="color:${esc(c)}">${esc(pname(P.playerId))}</div>
        <div class="bar" style="--c:${esc(c)}"></div><p class="hint">${P.mode !== "load" ? `Loading ${esc(possessive(pname(P.playerId)))} files and starting ${esc(g.name)}` : `Loading ${esc(possessive(pname(P.playerId)))} files`}</p></div><div></div></div>`;
    }
    if (P.ask === "owner") {
      return `<div class="picker"><div class="picker-head"><div></div><button class="btn quiet" data-action="picker-back">${icon.back}Back</button></div>
        <div class="question"><div><h2>Whose files are in ${esc(g.name)} right now?</h2>
        <p>This is the first time PlaySwap handles this game. The settings and saves on this PC will become that person's profile.</p></div>
        <div class="choices">
          ${cfg().players.map((p) => `<button class="choice" data-action="answer-owner" data-player="${esc(p.id)}"><span class="dot" style="background:${esc(p.color)}"></span><span><b>${esc(possessive(p.name))}</b></span></button>`).join("")}
          <button class="choice" data-action="answer-owner" data-player="none"><span class="dot"></span><span><b>Nobody's, or not sure</b><span>They're kept as a backup in case you need them.</span></span></button>
        </div></div><div></div></div>`;
    }
    if (P.ask === "newMode") {
      const owner = P.owner && P.owner !== "none" ? P.owner : S.d.active[g.id];
      const ownerName = owner && player(owner) ? possessive(pname(owner)) + " files" : "the current files";
      const n = pname(P.playerId);
      return `<div class="picker"><div class="picker-head"><div></div><button class="btn quiet" data-action="picker-back">${icon.back}Back</button></div>
        <div class="question"><div><h2>${esc(n)} doesn't have a profile for ${esc(g.name)} yet</h2>
        <p>Choose what ${esc(n)} starts with. Whatever happens, the files on the PC now are kept.</p></div>
        <div class="choices">
          <button class="choice" data-action="answer-new" data-mode="fresh"><span class="dot" style="background:${esc(color(P.playerId))}"></span><span><b>Start fresh</b><span>Game defaults and no saves, as if ${esc(n)} just installed it.</span></span></button>
          <button class="choice" data-action="answer-new" data-mode="copy"><span class="dot"></span><span><b>Start from a copy of ${esc(ownerName)}</b><span>Same settings and saves to begin with; changes stay separate from then on.</span></span></button>
        </div></div><div></div></div>`;
    }
    const ps = cfg().players;
    return `<div class="picker" role="dialog" tabindex="-1" aria-label="Who's playing ${esc(g.name)}?">
      <div class="picker-head"><div><h1>Who's playing ${esc(g.name)}?</h1>
        <p class="hint">${P.mode === "steam" ? "Steam is starting the game. Their settings and saves load first." : P.mode === "play" ? "Their settings and saves load before the game starts." : "Their settings and saves are loaded; the game isn't started."}</p></div>
        <button class="btn quiet" data-action="picker-close">${P.mode === "steam" ? "Don't start" : "Cancel"}</button></div>
      <div class="slots">
        ${ps.map((p, i) => `<button class="slot" style="--c:${esc(p.color)}" data-action="pick" data-player="${esc(p.id)}">
          <span class="initial" aria-hidden="true">${esc(p.name.trim().charAt(0).toUpperCase())}</span>
          ${i < 9 ? `<span class="key" aria-hidden="true">${i + 1}</span>` : ""}
          <span class="name">${esc(p.name)}</span><span class="state">${esc(slotState(g.id, p.id))}</span></button>`).join("")}
      </div>
      <div class="picker-foot"><span>Press ${ps.length > 1 ? "1–" + Math.min(ps.length, 9) : "1"} to choose, Esc ${P.mode === "steam" ? "to not start the game" : "to cancel"}</span><span>${esc(g.name)}</span></div>
    </div>`;
  }

  async function runPick() {
    const P = S.picker;
    const req = { gameId: P.gameId, playerId: P.playerId, firstOwner: P.owner || "", newProfile: P.newMode || "", reqId: P.reqId || "" };
    try {
      const res = await (P.mode === "steam" ? api.steamPick(req) : P.mode === "play" ? api.play(req) : api.load(req));
      if (!S.picker) return;
      if (res && res.need) { S.picker.ask = res.need; render(); return; }
      S.picker.working = true;
      render();
    } catch (e) {
      toast(errText(e), "error");
      if (S.picker) { S.picker.ask = null; S.picker.working = false; render(); }
    }
  }

  function onSessions() {
    const P = S.picker;
    if (!P || !P.working) return;
    const s = session(P.gameId);
    if (!s || s.phase === "launching" || s.phase === "playing") S.picker = null;
  }

  // ---------------------------------------------------------------- dialogs --
  let confirmResolve = null;
  function confirmDialog(o) {
    return new Promise((resolve) => { confirmResolve = resolve; S.dialog = Object.assign({ kind: "confirm" }, o); renderOverlay(); setTimeout(() => { const b = $(".dialog .btn.primary, .dialog .btn.danger-primary"); if (b) b.focus(); }, 20); });
  }
  function closeDialog(v) { const wasSteam = S.dialog && S.dialog.kind === "steam"; S.dialog = null; renderOverlay(); if (wasSteam) render(); if (confirmResolve) { const r = confirmResolve; confirmResolve = null; r(v); } }

  function renderOverlay() {
    const o = $("#overlay");
    if (S.picker) { o.innerHTML = viewPicker(); return; }
    if (!S.dialog) { o.innerHTML = ""; return; }
    const D = S.dialog;
    if (D.kind === "confirm") {
      o.innerHTML = `<div class="scrim" data-action="scrim"><div class="dialog narrow" role="alertdialog" aria-modal="true">
        <div><h2>${esc(D.title)}</h2><p class="sub">${esc(D.body)}</p></div>
        <div class="dialog-foot"><button class="btn quiet" data-action="dialog-cancel">${esc(D.cancel || "Cancel")}</button>
        <button class="btn primary" data-action="dialog-ok">${esc(D.ok || "OK")}</button></div></div></div>`;
    } else if (D.kind === "add") {
      o.innerHTML = viewAddDialog();
    } else if (D.kind === "welcome") {
      o.innerHTML = viewWelcome();
    } else if (D.kind === "steam") {
      o.innerHTML = viewSteamSetup();
    }
  }

  // ------------------------------------------------------- steam set-up --
  async function loadHooks() {
    try { S.hooks = await api.steamSetup(); } catch (e) { S.hooks = null; }
  }

  async function openSteamSetup(gameId) {
    S.menuFor = null;
    S.dialog = { kind: "steam", focus: gameId || "", copied: "" };
    renderOverlay();
    await loadHooks();
    if (S.dialog && S.dialog.kind === "steam") renderOverlay();
  }

  function viewSteamSetup() {
    const D = S.dialog;
    const H = S.hooks;
    let body;
    if (!H) body = `<p class="hint">Reading Steam's settings</p>`;
    else if (!H.games.length) body = `<div class="note">No Steam games in PlaySwap yet. Add one first.</div>`;
    else {
      const list = D.focus ? H.games.filter((h) => h.gameId === D.focus) : H.games;
      body = list.map((h) => `<div class="hook${h.detected ? " done" : ""}">
          <div class="row" style="justify-content:space-between"><b>${esc(h.name)}</b>
            <span class="${h.detected ? "ok-text" : "hint"}">${h.detected ? "Set up: Steam asks who's playing" : "Not set up yet"}</span></div>
          <div class="hook-line"><code class="selectable">${esc(h.option)}</code>
            <button class="btn small" data-action="copy-hook" data-game="${esc(h.gameId)}">${D.copied === h.gameId ? "Copied" : "Copy"}</button></div>
          ${h.current && !h.detected ? `<span class="hint">Includes the launch options this game already had.</span>` : ""}
        </div>`).join("");
    }
    return `<div class="scrim" data-action="scrim"><div class="dialog" role="dialog" aria-modal="true" aria-label="Ask who's playing when started from Steam">
      <div class="page-head" style="margin:0"><div><h2>Ask who's playing when started from Steam</h2>
        <p class="sub">Do this once per game. Afterwards, pressing Play in Steam opens the picker, loads that person's files, then starts the game.</p></div>
        <button class="btn quiet icon" data-action="dialog-cancel" aria-label="Close">${icon.x}</button></div>
      ${H && H.games.some((h) => !h.detected) ? `<div class="note"><b>Easiest:</b> let PlaySwap do it. ${H.steamRunning ? "Steam closes and reopens by itself (about 10 seconds)." : "Steam is closed, so this takes a second."}
        <div class="row" style="margin-top:10px"><button class="btn primary" data-action="hooks-apply" ${S.hooksBusy ? "disabled" : ""}>${S.hooksBusy ? "Setting up" : "Set up automatically"}</button></div></div>
      <p class="hint">Or by hand:</p>` : ""}
      <ol class="steps">
        <li>Copy the line for the game.</li>
        <li>In Steam, right-click the game in your library and choose <b>Properties</b>.</li>
        <li>On the <b>General</b> page, paste it into <b>Launch options</b>, replacing what's there.</li>
      </ol>
      ${H && H.error ? `<div class="note">${esc(H.error)}</div>` : ""}
      <div class="hooks">${body}</div>
      <div class="dialog-foot" style="justify-content:space-between"><span class="hint">Steam can take a minute to save the change. Games outside Steam: use the desktop shortcut from the game's menu.</span>
        <button class="btn" data-action="steam-recheck">Check again</button></div>
    </div></div>`;
  }

  // -------------------------------------------------------------- add game --
  function viewAddDialog() {
    const D = S.dialog;
    const q = (D.q || "").toLowerCase();
    const added = new Set(cfg().games.filter((g) => g.launch.type === "steam").map((g) => g.launch.appId));
    let list;
    if (!S.d.steam.found) list = `<div class="note">Steam wasn't found on this PC. You can still add any game by choosing its .exe.</div>`;
    else if (!D.steam) list = `<p class="hint">Looking for installed Steam games</p>`;
    else {
      const items = D.steam.filter((g) => !q || g.name.toLowerCase().includes(q));
      list = items.length
        ? `<div class="steam-list">${items.map((g) => `<button class="steam-item${added.has(g.appId) ? " added" : ""}" data-action="add-steam" data-app="${g.appId}" ${added.has(g.appId) ? "disabled" : ""}><b>${esc(g.name)}</b><span>${added.has(g.appId) ? "Already added" : "Steam"}</span></button>`).join("")}</div>`
        : `<p class="hint">No installed Steam game matches “${esc(D.q)}”.</p>`;
    }
    return `<div class="scrim" data-action="scrim"><div class="dialog" role="dialog" aria-modal="true" aria-label="Add a game">
      <div class="page-head" style="margin:0"><div><h2>Add a game</h2><p class="sub">Pick an installed Steam game, or any other game by its .exe.</p></div>
      <button class="btn quiet icon" data-action="dialog-cancel" aria-label="Close">${icon.x}</button></div>
      ${S.d.steam.found ? `<input type="text" id="steam-q" placeholder="Search your Steam games" value="${esc(D.q || "")}" autocomplete="off">` : ""}
      ${list}
      <div class="dialog-foot" style="justify-content:space-between"><span class="hint">Epic, GOG, Battle.net, Xbox and standalone games: choose the .exe.</span>
      <button class="btn" data-action="add-exe">Choose an .exe</button></div>
    </div></div>`;
  }

  async function startAddDialog() {
    S.menuFor = null;
    S.dialog = { kind: "add", q: "" };
    renderOverlay();
    setTimeout(() => { const i = $("#steam-q"); if (i) i.focus(); }, 30);
    if (S.d.steam.found) {
      try { S.dialog.steam = await api.steamGames(); } catch (e) { S.dialog.steam = []; toast(errText(e), "error"); }
      if (S.dialog && S.dialog.kind === "add") { const q = $("#steam-q"); const pos = q ? q.selectionStart : 0; renderOverlay(); const q2 = $("#steam-q"); if (q2) { q2.focus(); q2.setSelectionRange(pos, pos); } }
    }
  }

  async function addSteam(appId) {
    appId = Number(appId);
    S.dialog = null; renderOverlay();
    try {
      const ins = await api.inspectSteam(appId);
      const preset = ins.preset;
      openEditor({
        isNew: true,
        game: { id: "", name: ins.name, launch: { type: "steam", appId, args: "" }, processName: ins.processName || "", playerArgs: {}, paths: preset ? preset.paths.map((p) => ({ id: "", path: p })) : [] },
        inspect: ins,
      });
    } catch (e) { toast(errText(e), "error"); }
  }

  async function addExe() {
    const r = await browse("exe");
    if (!r.path) { if (r.error) toast(r.error, "error"); return; }
    S.dialog = null; renderOverlay();
    try {
      const ins = await api.inspectExe(r.path);
      openEditor({
        isNew: true,
        game: { id: "", name: ins.name || "", launch: { type: "exe", exe: r.tokenized || r.path, args: "" }, processName: ins.processName, playerArgs: {}, paths: [] },
        inspect: ins,
      });
    } catch (e) { toast(errText(e), "error"); }
  }

  // ---------------------------------------------------------------- editor --
  function openEditor(e) {
    e.game = JSON.parse(JSON.stringify(e.game));
    e.game.paths.forEach((p) => { if (!p._k) p._k = "k" + ++seq; });
    e.checks = {};
    e.error = "";
    S.edit = e;
    S.view = "edit";
    render();
    $("#view").scrollTop = 0;
    e.game.paths.forEach(checkPath);
  }

  function editGame(id) {
    const g = game(id);
    if (!g) return;
    S.menuFor = null;
    const e = { isNew: false, game: g, inspect: null };
    openEditor(e);
    if (g.launch.type === "steam") api.inspectSteam(g.launch.appId).then((ins) => { if (S.edit === e) { e.inspect = ins; render(); } }).catch(() => {});
  }

  async function checkPath(p) {
    const e = S.edit;
    if (!e) return;
    const raw = p.path;
    if (!raw.trim()) { e.checks[p._k] = null; updatePathInfo(p); return; }
    try {
      const c = await api.checkPath(raw);
      if (S.edit !== e || p.path !== raw) return;
      e.checks[p._k] = c;
    } catch (err) { e.checks[p._k] = { error: errText(err) }; }
    updatePathInfo(p);
  }

  function pathInfo(p) {
    const c = S.edit.checks[p._k];
    if (!c) return `<div class="path-info">Checking</div>`;
    if (c.error) return `<div class="path-info bad">${esc(c.error)}</div>`;
    if (!c.exists) return `<div class="path-info">${esc(c.resolved)}. Doesn't exist yet; it's created the first time the game saves.</div>`;
    return `<div class="path-info">${esc(c.resolved)} (${c.isDir ? "folder" : "file"}, ${fmtSize(c.size)})</div>`;
  }

  function updatePathInfo(p) {
    const row = document.querySelector(`.path[data-k="${p._k}"] .path-info`);
    if (row) row.outerHTML = pathInfo(p);
  }

  function viewEdit() {
    const E = S.edit, g = E.game, ins = E.inspect || {};
    const taken = new Set(g.paths.map((p) => p.path.toLowerCase()));
    const sugg = (ins.suggestions || []).filter((s) => !taken.has(s.path.toLowerCase()));
    const cands = (ins.candidates || []).filter((c) => c.name.toLowerCase() !== (g.processName || "").toLowerCase()).slice(0, 6);
    const launch = g.launch.type === "steam"
      ? `<div class="note">Launched through Steam (app ${esc(g.launch.appId)}).</div>`
      : `<div class="row"><input type="text" class="grow" data-field="exe" value="${esc(g.launch.exe)}" spellcheck="false"><button class="btn" data-action="browse-exe">Browse</button></div>`;
    return `<div class="page">
      <div class="page-head"><div><button class="btn quiet small" data-action="edit-cancel" style="margin:0 0 10px -12px">${icon.back}Games</button>
      <h1>${E.isNew ? "Add " + esc(g.name || "a game") : "Edit " + esc(g.name)}</h1></div></div>
      <div class="form">
        <section class="section">
          <div class="field"><label for="f-name">Name</label><input id="f-name" type="text" data-field="name" value="${esc(g.name)}" maxlength="60"></div>
          <div class="field"><span class="label">How it starts</span>${launch}</div>
        </section>

        <section class="section">
          <div class="section-head"><h2>Game process</h2><p>The program that runs while someone plays. PlaySwap watches it to know when to save that person's progress.</p></div>
          <div class="row"><input type="text" class="grow" data-field="processName" value="${esc(g.processName)}" placeholder="for example cs2" spellcheck="false" style="max-width:320px"><span class="hint">.exe</span></div>
          ${cands.length ? `<div class="row"><span class="hint">Other programs in the game folder:</span>${cands.map((c) => `<button class="btn small" data-action="set-proc" data-name="${esc(c.name)}">${esc(c.name)}</button>`).join("")}</div>` : ""}
        </section>

        <section class="section">
          <div class="section-head"><h2>What gets swapped</h2><p>Every folder or file where the game keeps settings or saves. Each person gets their own copy of everything listed here.</p></div>
          ${ins.preset && ins.preset.note && g.paths.length ? `<div class="note">${esc(ins.preset.note)}</div>` : ""}
          <div class="paths">
            ${g.paths.length ? g.paths.map((p) => `<div class="path" data-k="${p._k}">
                <input type="text" data-field="path" data-k="${p._k}" value="${esc(p.path)}" spellcheck="false" aria-label="Save location">
                <div class="path-tools"><button class="btn icon quiet" data-action="open-path" data-path="${esc(p.path)}" aria-label="Open in Explorer">${icon.folder}</button>
                <button class="btn icon quiet" data-action="remove-path" data-k="${p._k}" aria-label="Remove">${icon.x}</button></div>
                ${pathInfo(p)}</div>`).join("") : `<p class="hint">Nothing yet. Add the game's settings folder and its saves folder.</p>`}
          </div>
          <div class="row"><button class="btn" data-action="add-path" data-kind="folder">${icon.plus}Add folder</button><button class="btn" data-action="add-path" data-kind="file">${icon.plus}Add file</button>
            <button class="btn quiet" data-action="pcgw">Look up where ${esc(g.name || "this game")} saves</button></div>
          ${sugg.length ? `<div class="suggest"><span class="label">Found on this PC</span>${sugg.map((s) => `<div class="suggest-item"><div class="grow"><code title="${esc(s.resolved)}">${esc(s.path)}</code><span class="hint">${esc(s.reason)}, ${fmtSize(s.size)}</span></div><button class="btn small" data-action="add-sugg" data-path="${esc(s.path)}">Add</button></div>`).join("")}</div>` : ""}
          ${g.launch.type === "steam" ? `<div class="note"><b>Turn off Steam Cloud for this game.</b> In Steam, open the game's Properties and switch off Steam Cloud under General. Otherwise Steam can sync one person's files over another's.</div>` : ""}
        </section>

        <section class="section">
          <div class="section-head"><h2>Launch options</h2><p>Optional. Added when PlaySwap starts the game, for example <span class="selectable">-novid</span> or <span class="selectable">-w 1920 -h 1080</span>.</p></div>
          <div class="args">
            <span class="label">Everyone</span><input type="text" data-field="args" value="${esc(g.launch.args || "")}" spellcheck="false">
            ${cfg().players.map((p) => `<span class="who"><span class="dot" style="background:${esc(p.color)}"></span>${esc(p.name)}</span><input type="text" data-field="parg" data-player="${esc(p.id)}" value="${esc((g.playerArgs || {})[p.id] || "")}" spellcheck="false">`).join("")}
          </div>
        </section>
      </div>
      <div class="form-foot"><button class="btn primary" data-action="save-game" ${E.saving ? "disabled" : ""}>${E.isNew ? "Add game" : "Save changes"}</button>
        <button class="btn quiet" data-action="edit-cancel">Cancel</button><span class="error-text" id="edit-error">${esc(E.error)}</span></div>
    </div>`;
  }

  async function saveGame() {
    const E = S.edit;
    if (!E) return;
    E.saving = true; E.error = "";
    const g = JSON.parse(JSON.stringify(E.game));
    g.paths = g.paths.filter((p) => p.path.trim()).map((p) => ({ id: p.id || "", path: p.path.trim() }));
    try {
      const saved = await api.saveGame(g);
      toast(E.isNew ? `Added ${saved.name}` : `Saved ${saved.name}`);
      S.edit = null; S.view = "games";
      await refresh();
      loadFound();
      loadHooks().then(render);
    } catch (e) {
      E.saving = false; E.error = errText(e);
      const b = $('[data-action="save-game"]'); if (b) b.disabled = false;
      const t = $("#edit-error"); if (t) t.textContent = E.error;
    }
  }

  // --------------------------------------------------------------- players --
  function viewWelcome() {
    const ps = S.players || (S.players = JSON.parse(JSON.stringify(cfg().players)));
    return `<div class="scrim"><div class="dialog narrow" role="dialog" aria-modal="true" aria-label="Welcome">
      <div><h2 style="font-size:22px">Who shares this PC?</h2><p class="sub">Each person gets a colour and their own settings and saves in every game you add. You can add more people later.</p></div>
      <div class="players">${ps.map((p, i) => playerRow(p, i, ps.length, true)).join("")}</div>
      <div class="dialog-foot"><span class="error-text grow" id="players-error"></span><button class="btn primary" data-action="welcome-done">Continue</button></div>
    </div></div>`;
  }

  function playerRow(p, i, total, welcome) {
    return `<div class="player">
      <div class="swatches" role="radiogroup" aria-label="Colour for ${esc(p.name || "player " + (i + 1))}">${S.d.colors.map((c) => `<button class="swatch" style="background:${c}" data-action="swatch" data-i="${i}" data-color="${c}" aria-pressed="${c.toLowerCase() === (p.color || "").toLowerCase()}" aria-label="${c}"></button>`).join("")}</div>
      <input type="text" data-field="pname" data-i="${i}" value="${welcome && /^Player \d$/.test(p.name) ? "" : esc(p.name)}" placeholder="${welcome ? (i === 0 ? "Your name" : "Their name") : "Name"}" maxlength="24" aria-label="Name">
      ${welcome ? "" : `<button class="btn icon quiet" data-action="remove-player" data-i="${i}" aria-label="Remove ${esc(p.name)}" ${total <= 1 ? "disabled" : ""}>${icon.x}</button>`}
    </div>`;
  }

  function viewPlayers() {
    const ps = S.players || (S.players = JSON.parse(JSON.stringify(cfg().players)));
    const dirty = JSON.stringify(ps) !== JSON.stringify(cfg().players);
    return `<div class="page"><div class="page-head"><div><h1>Players</h1><p class="sub">Everyone who plays on this PC. Their colour shows whose files are loaded in each game.</p></div></div>
      <div class="players">${ps.map((p, i) => playerRow(p, i, ps.length, false)).join("")}</div>
      <div class="row" style="margin-top:14px">${ps.length < 8 ? `<button class="btn" data-action="add-player">${icon.plus}Add player</button>` : ""}</div>
      <div class="row" style="margin-top:28px"><button class="btn primary" data-action="save-players" ${dirty ? "" : "disabled"}>Save players</button>
      ${dirty ? `<button class="btn quiet" data-action="reset-players">Discard changes</button>` : ""}<span class="error-text" id="players-error"></span></div></div>`;
  }

  async function savePlayers(welcome) {
    const ps = S.players.map((p, i) => ({ id: p.id, name: (p.name || "").trim() || (welcome ? "Player " + (i + 1) : ""), color: p.color }));
    const removed = cfg().players.filter((o) => !ps.some((p) => p.id === o.id));
    if (removed.length && !welcome) {
      const ok = await confirmDialog({ title: `Remove ${removed.map((p) => p.name).join(" and ")}?`, body: "Their profiles are moved to the “removed” folder inside PlaySwap's data folder, so nothing is deleted for good.", ok: "Remove" });
      if (!ok) return;
    }
    try {
      await api.savePlayers(ps);
      S.players = null;
      if (welcome) S.dialog = null;
      await refresh();
      if (!welcome) toast("Players saved");
    } catch (e) { const t = $("#players-error"); if (t) t.textContent = errText(e); }
  }

  // -------------------------------------------------------------- settings --
  function sw(field, title, desc, checked) {
    return `<label class="toggle"><span><b style="font-weight:500">${title}</b><p>${desc}</p></span>
      <span class="switch"><input type="checkbox" data-setting="${field}" ${checked ? "checked" : ""}><span></span></span></label>`;
  }

  function viewSettings() {
    const s = cfg().settings, st = S.d.steam;
    return `<div class="page"><div class="page-head"><div><h1>Settings</h1></div></div>
      <div class="form">
        <section class="section"><div class="toggles">
          ${sw("autostart", "Start with Windows", "PlaySwap opens quietly in the tray when you sign in, so it's always watching your games.", S.d.autostart)}
          ${sw("closeToTray", "Keep running when the window is closed", "Closing the window leaves PlaySwap in the tray. Quit from the tray icon.", s.closeToTray)}
          ${sw("hideWhilePlaying", "Hide while a game runs", "The window gets out of the way once the game starts.", s.hideWhilePlaying)}
          ${sw("watchOutsideLaunches", "Follow games started from Steam or elsewhere", "If a game starts without PlaySwap, its progress is saved to whoever's files are loaded.", s.watchOutsideLaunches)}
          ${sw("autoSteamHooks", "Set up Steam automatically", "Steam games you add get a launch option so Steam's Play button asks who's playing. Removed games get their own options back.", s.autoSteamHooks)}
          ${sw("notifications", "Notifications", "A short Windows notification when progress is saved.", s.notifications)}
        </div></section>
        <section class="section"><div class="numbers">
          <div class="field"><label for="s-backups">Backups kept per game</label><input id="s-backups" type="number" min="1" max="100" data-setting="backupsToKeep" value="${s.backupsToKeep}"></div>
          <div class="field"><label for="s-timeout">Seconds to wait for a game to start</label><input id="s-timeout" type="number" min="30" max="1800" data-setting="startTimeoutSeconds" value="${s.startTimeoutSeconds}"></div>
          ${st.found ? `<div class="field"><label for="s-steam">Steam account</label><select id="s-steam" data-setting="steamAccountId">
            <option value="">Automatic${st.accountInUse && !s.steamAccountId ? " (" + esc(st.accountInUse) + ")" : ""}</option>
            ${(st.accounts || []).map((a) => `<option value="${esc(a)}" ${a === s.steamAccountId ? "selected" : ""}>${esc(a)}</option>`).join("")}</select>
            ${st.accountError ? `<span class="error-text">${esc(st.accountError)}</span>` : ""}</div>` : ""}
        </div></section>
        <section class="section">
          <div class="section-head"><h2>Data</h2><p class="selectable">Profiles and backups are stored in ${esc(S.d.dataDir)}</p></div>
          <div class="row"><button class="btn" data-action="open-data">Open data folder</button><button class="btn" data-action="show-log">Show recent activity</button></div>
          ${S.logText ? `<div class="log">${esc(S.logText)}</div>` : ""}
        </section>
        <section class="section">
          <div class="section-head"><h2>About</h2><p>PlaySwap ${esc(S.d.version)}. Uses the Microsoft Edge WebView2 runtime. To uninstall, use Settings, Apps in Windows.</p></div>
          <div class="row"><button class="btn quiet danger" data-action="quit">Quit PlaySwap</button></div>
        </section>
      </div></div>`;
  }

  async function saveSetting(el) {
    const s = JSON.parse(JSON.stringify(cfg().settings));
    let autostart = S.d.autostart;
    const f = el.dataset.setting;
    if (f === "autostart") autostart = el.checked;
    else if (el.type === "checkbox") s[f] = el.checked;
    else if (el.type === "number") s[f] = parseInt(el.value, 10) || 0;
    else s[f] = el.value;
    try {
      await api.saveSettings({ settings: s, autostart });
      await refresh();
    } catch (e) { toast(errText(e), "error"); await refresh(); }
  }

  // --------------------------------------------------------------- actions --
  const actions = {
    nav(el) {
      if (S.view === "edit" && !confirmLeaveEdit()) return;
      S.view = el.dataset.view; S.menuFor = null; S.edit = null;
      if (S.view !== "players") S.players = null;
      render(); $("#view").scrollTop = 0;
    },
    "add-game": startAddDialog,
    "add-steam": (el) => addSteam(el.dataset.app),
    "add-exe": addExe,
    play: (el) => openPicker(el.dataset.game, "play"),
    "load-only": (el) => openPicker(el.dataset.game, "load"),
    menu(el) { S.menuFor = S.menuFor === el.dataset.game ? null : el.dataset.game; render(); const m = $(".menu button"); if (m) m.focus(); },
    "edit-game": (el) => editGame(el.dataset.game),
    async shortcut(el) {
      S.menuFor = null; render();
      try { await api.shortcut(el.dataset.game); toast("Shortcut added to your desktop"); } catch (e) { toast(errText(e), "error"); }
    },
    "open-path": (el) => { S.menuFor = null; render(); api.open("path", el.dataset.path).catch((e) => toast(errText(e), "error")); },
    "open-backups": (el) => { S.menuFor = null; render(); api.open("backups", el.dataset.game).catch((e) => toast(errText(e), "error")); },
    async "remove-game"(el) {
      S.menuFor = null; render();
      const g = game(el.dataset.game);
      const ok = await confirmDialog({ title: `Remove ${g.name}?`, body: "PlaySwap stops managing it. Everyone's profiles are moved to the “removed” folder in PlaySwap's data, and the game keeps the files of whoever played last.", ok: "Remove game" });
      if (!ok) return;
      try { await api.deleteGame(g.id); toast(`Removed ${g.name}`); await refresh(); } catch (e) { toast(errText(e), "error"); }
    },
    pick(el) { S.picker.playerId = el.dataset.player; S.picker.owner = ""; S.picker.newMode = ""; runPick(); },
    "answer-owner"(el) { S.picker.owner = el.dataset.player; S.picker.ask = null; runPick(); },
    "answer-new"(el) { S.picker.newMode = el.dataset.mode; S.picker.ask = null; runPick(); },
    "picker-back"() { S.picker.ask = null; S.picker.owner = ""; S.picker.newMode = ""; render(); },
    "picker-close": closePicker,
    "dialog-ok": () => closeDialog(true),
    "dialog-cancel": () => closeDialog(false),
    scrim(el, ev) { if (ev.target === el && S.dialog && S.dialog.kind !== "welcome") closeDialog(false); },
    "edit-cancel"() { if (!confirmLeaveEdit()) return; S.edit = null; S.view = "games"; render(); },
    async "browse-exe"() {
      const r = await browse("exe", S.edit.game.launch.exe);
      if (r.path && S.edit) { S.edit.game.launch.exe = r.tokenized || r.path; render(); }
    },
    "set-proc"(el) { S.edit.game.processName = el.dataset.name; render(); },
    async "add-path"(el) {
      const start = (S.edit.game.paths[0] || {}).path || "";
      const r = await browse(el.dataset.kind, start);
      if (r.error) toast(r.error, "error");
      if (!r.path || !S.edit) return;
      addPath(r.tokenized || r.path);
    },
    "add-sugg"(el) { addPath(el.dataset.path); },
    "remove-path"(el) { S.edit.game.paths = S.edit.game.paths.filter((p) => p._k !== el.dataset.k); render(); },
    pcgw() { api.open("pcgw", S.edit.game.name).catch((e) => toast(errText(e), "error")); },
    "save-game": saveGame,
    swatch(el) { S.players[+el.dataset.i].color = el.dataset.color; S.dialog && S.dialog.kind === "welcome" ? renderOverlay() : render(); },
    "add-player"() { const used = new Set(S.players.map((p) => p.color)); S.players.push({ id: "", name: "", color: S.d.colors.find((c) => !used.has(c)) || S.d.colors[0] }); render(); const ins = document.querySelectorAll('[data-field="pname"]'); ins[ins.length - 1].focus(); },
    "remove-player"(el) { S.players.splice(+el.dataset.i, 1); render(); },
    "reset-players"() { S.players = null; render(); },
    "save-players": () => savePlayers(false),
    "welcome-done": () => savePlayers(true),
    "open-data": () => api.open("data").catch((e) => toast(errText(e), "error")),
    "steam-setup": (el) => openSteamSetup(el.dataset.game),
    async "steam-recheck"() { S.hooks = null; renderOverlay(); await loadHooks(); if (S.dialog && S.dialog.kind === "steam") renderOverlay(); render(); },
    async "copy-hook"(el) {
      const h = S.hooks && S.hooks.games.find((x) => x.gameId === el.dataset.game);
      if (!h) return;
      try { await api.copy(h.option); S.dialog.copied = h.gameId; renderOverlay(); } catch (e) { toast(errText(e), "error"); }
    },
    "hide-banner"() { S.hideHookBanner = true; render(); toast("PlaySwap will finish this the next time Steam is closed."); },
    async "hooks-apply"() {
      S.hooksBusy = true; render(); if (S.dialog) renderOverlay();
      try {
        const r = await api.applyHooks(true);
        await loadHooks();
        if (r && r.changed) toast(r.restarted ? "Steam restarted. Its Play button now asks who's playing." : "Done. Steam's Play button now asks who's playing.");
      } catch (e) { toast(errText(e), "error"); }
      S.hooksBusy = false; render(); if (S.dialog) renderOverlay();
    },
    rescan() { S.found = { scanning: true, games: S.found.games || [] }; render(); api.scan(); },
    async "add-all"() {
      const keys = (S.found.games || []).filter((f) => f.ready).map((f) => f.key);
      await addFound(keys);
    },
    async "found-add"(el) {
      const f = (S.found.games || []).find((x) => x.key === el.dataset.key);
      if (!f) return;
      if (f.ready) { await addFound([f.key]); return; }
      openEditor({
        isNew: true,
        game: { id: "", name: f.name, launch: f.source === "steam" ? { type: "steam", appId: f.appId, args: "" } : { type: "exe", exe: f.exe, args: "" },
          processName: f.processName || "", playerArgs: {}, paths: (f.paths || []).map((p) => ({ id: "", path: p })) },
        inspect: { suggestions: f.suggestions || [], candidates: [] },
      });
    },
    async "found-hide"(el) {
      try { await api.ignoreFound(el.dataset.key); S.found.games = S.found.games.filter((f) => f.key !== el.dataset.key); render(); } catch (e) { toast(errText(e), "error"); }
    },
    async "show-log"() { try { S.logText = (await api.log()) || "Nothing yet."; } catch (e) { S.logText = errText(e); } render(); },
    quit: () => api.quit(),
  };

  async function addFound(keys) {
    S.adding = true; render();
    try {
      const r = await api.addFound(keys);
      if (r.added.length) toast(r.added.length === 1 ? `Added ${r.added[0]}` : `Added ${r.added.length} games`);
      if (r.failed.length) toast(r.failed.join("\n"), "error");
      await refresh();
      await loadFound();
      await loadHooks();
    } catch (e) { toast(errText(e), "error"); }
    S.adding = false; render();
  }

  function addPath(path) {
    const E = S.edit;
    if (E.game.paths.some((p) => p.path.toLowerCase() === path.toLowerCase())) return;
    const p = { id: "", path, _k: "k" + ++seq };
    E.game.paths.push(p);
    render();
    checkPath(p);
  }

  function confirmLeaveEdit() {
    return true; // edits are explicit; leaving discards them
  }

  document.addEventListener("click", (ev) => {
    const el = ev.target.closest("[data-action]");
    if (!el) {
      if (S.menuFor && !ev.target.closest(".menu")) { S.menuFor = null; render(); }
      return;
    }
    if (el.dataset.action !== "menu" && S.menuFor && !el.closest(".menu")) S.menuFor = null;
    const f = actions[el.dataset.action];
    if (f) f(el, ev);
  });

  document.addEventListener("input", (ev) => {
    const el = ev.target;
    if (el.id === "steam-q" && S.dialog) {
      S.dialog.q = el.value;
      const pos = el.selectionStart;
      renderOverlay();
      const q = $("#steam-q"); q.focus(); q.setSelectionRange(pos, pos);
      return;
    }
    const f = el.dataset.field;
    if (!f) return;
    if (f === "pname") { S.players[+el.dataset.i].name = el.value; if (S.view === "players" && !S.dialog) { const b = $('[data-action="save-players"]'); if (b) b.disabled = false; } return; }
    const E = S.edit;
    if (!E) return;
    const g = E.game;
    if (f === "name") g.name = el.value;
    else if (f === "exe") g.launch.exe = el.value;
    else if (f === "processName") g.processName = el.value;
    else if (f === "args") g.launch.args = el.value;
    else if (f === "parg") { g.playerArgs = g.playerArgs || {}; g.playerArgs[el.dataset.player] = el.value; }
    else if (f === "path") { const p = g.paths.find((x) => x._k === el.dataset.k); if (p) p.path = el.value; }
  });

  document.addEventListener("change", (ev) => {
    const el = ev.target;
    if (el.dataset.setting) { saveSetting(el); return; }
    if (el.dataset.field === "path" && S.edit) { const p = S.edit.game.paths.find((x) => x._k === el.dataset.k); if (p) checkPath(p); }
  });

  document.addEventListener("keydown", (ev) => {
    if (S.picker && !S.picker.working) {
      if (ev.key === "Escape") { ev.preventDefault(); if (S.picker.ask) actions["picker-back"](); else closePicker(); return; }
      if (!S.picker.ask && /^[1-9]$/.test(ev.key)) {
        const p = cfg().players[+ev.key - 1];
        if (p) { ev.preventDefault(); S.picker.playerId = p.id; runPick(); }
      }
      return;
    }
    if (ev.key === "Escape") {
      if (S.dialog && S.dialog.kind !== "welcome") { closeDialog(false); return; }
      if (S.menuFor) { S.menuFor = null; render(); }
    }
    if (ev.key === "Enter" && S.dialog && S.dialog.kind === "welcome" && ev.target.dataset.field === "pname") savePlayers(true);
  });

  // block browser shortcuts that make no sense in an app window
  document.addEventListener("keydown", (ev) => {
    if ((ev.ctrlKey && ["p", "f", "g", "r", "u", "s"].includes(ev.key.toLowerCase())) || ev.key === "F5" || ev.key === "F3") ev.preventDefault();
  }, true);
  document.addEventListener("dragover", (ev) => ev.preventDefault());
  document.addEventListener("drop", (ev) => ev.preventDefault());

  // ------------------------------------------------------------------ boot --
  async function boot() {
    try {
      S.d = await api.state();
    } catch (e) {
      setTimeout(boot, 150); // bindings not injected yet
      return;
    }
    if (S.d.loadError) toast(S.d.loadError, "error");
    if (S.d.firstRun) { S.players = JSON.parse(JSON.stringify(cfg().players)); S.dialog = { kind: "welcome" }; }
    render();
    loadHooks().then(() => { if (!S.picker) render(); });
    loadFound();
    if (S.d.firstRun) setTimeout(() => { const i = $('[data-field="pname"]'); if (i) i.focus(); }, 50);
    try {
      const r = await api.ready();
      if (r && r.launch) openPicker(r.launch, r.reqId ? "steam" : "play", r.reqId);
    } catch (e) { /* ignore */ }
  }
  boot();
})();
