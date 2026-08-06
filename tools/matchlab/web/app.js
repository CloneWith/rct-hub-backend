const commandDefinitions = {
  START_MATCH: [],
  BAN_POOL_SLOT: ["poolSlotId"],
  PLACE_PIECE: ["poolSlotId", "pieceId", "cell"],
  PLACE_SHIRO: ["pieceId", "cell"],
  ROB_PIECE: ["targetPieceId", "sacrificeSets"],
  CONFIRM_BEATMAP_RESULT: ["pieceId", "winningTeam"],
  REQUEST_TB: ["requestId", "basis"],
  RESPOND_TB_REQUEST: ["requestId", "accept"],
  START_TB: ["reason"],
  CONFIRM_TB_RESULT: ["winningTeam"],
  RECORD_SURRENDER: ["surrenderingTeam", "confirmingPlayerIds", "reason"],
  GRANT_ADDITIONAL_TIME: ["reason"],
  CALIBRATE_TIMER: ["remainingSeconds", "reason"],
  PAUSE_TIMER: ["reason"],
  RESUME_TIMER: ["reason"],
  SUSPEND_MATCH: ["reason"],
  RESUME_MATCH: ["reason"],
  SKIP_CURRENT_ACTION: ["reason"],
  ABORT_MATCH: ["reason"],
  REFEREE_BAN_POOL_SLOT: ["actingTeam", "poolSlotId", "reason"],
  REFEREE_PLACE_PIECE: ["actingTeam", "poolSlotId", "pieceId", "cell", "reason"],
  REFEREE_PLACE_SHIRO: ["actingTeam", "pieceId", "cell", "reason"],
  REFEREE_ROB_PIECE: ["actingTeam", "targetPieceId", "sacrificeSets", "reason"],
  REFEREE_REQUEST_TB: ["actingTeam", "requestId", "basis", "reason"],
  REFEREE_RESPOND_TB_REQUEST: ["actingTeam", "requestId", "accept", "reason"]
};

const labels = {
  poolSlotId: "PoolSlot ID", pieceId: "BoardPiece ID", cell: "棋盘格",
  winningTeam: "获胜方", targetPieceId: "目标 BoardPiece ID",
  sacrificeSets: "牺牲集合 JSON", reason: "审计原因", requestId: "TB Request ID",
  basis: "TB 条件", accept: "接受请求", remainingSeconds: "校准后剩余秒数",
  surrenderingTeam: "认输方", confirmingPlayerIds: "确认选手 ID",
  actingTeam: "代理队伍"
};

let snapshot = null;
let actor = "REFEREE";
let selectedCell = "";
let selectedPool = "";
let commandPending = false;

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: {"Content-Type": "application/json", ...(options.headers || {})}
  });
  const body = await response.json();
  if (!response.ok) throw new Error(`${body.code || response.status}: ${body.message || "请求失败"}`);
  return body;
}

function zoneFor(cell) {
  const column = cell.charCodeAt(0) - 65;
  const row = Number(cell[1]) - 1;
  if (row < 2 && column < 2) return "DT";
  if (row < 2) return "HD";
  if (column < 2) return "HR";
  return "DT";
}

function render(next) {
	const analysis = normalizeAnalysis(next.analysis);
	snapshot = {...next, analysis};
	const state = next.state;
  $("#statusline").textContent = `scenario ${next.scenario} · version ${state.version} · ${state.lifecycle}`;
  $("#clock").textContent = new Date(next.now).toISOString().slice(11, 19);
  const remaining = Math.max(0, Math.ceil(next.remainingMs / 1000));
  $("#timer").textContent = state.timer?.paused ? `PAUSED · ${remaining}s` : `TIMER · ${remaining}s`;
  $("#turn-summary").textContent = `${state.phase} · Turn ${state.turn} · Active ${state.activeTeam || "-"}`;
  $("#counts").innerHTML = `
		<div class="count red">RED<strong>${analysis.wonCounts.RED || 0}</strong></div>
		<div class="count blue">BLUE<strong>${analysis.wonCounts.BLUE || 0}</strong></div>`;

	renderBoard(state.board?.pieces || {});
	renderPool(state.poolSlots || {}, analysis);
	renderAnalysis(analysis, state);
  renderEvents(next.recentEvents || []);
  $("#raw-state").textContent = JSON.stringify(state, null, 2);
  $$("[data-scenario]").forEach(button => button.classList.toggle("active", button.dataset.scenario === next.scenario));
  updateFieldDefaults();
}

function normalizeAnalysis(analysis = {}) {
	return {
		...analysis,
		selectablePoolSlotIds: Array.isArray(analysis.selectablePoolSlotIds) ? analysis.selectablePoolSlotIds : [],
		emptyCells: Array.isArray(analysis.emptyCells) ? analysis.emptyCells : [],
		legalCellsByPoolSlot: analysis.legalCellsByPoolSlot || {},
		legalPlacements: Array.isArray(analysis.legalPlacements) ? analysis.legalPlacements : [],
		wonCounts: analysis.wonCounts || {}
	};
}

function renderBoard(pieces) {
  $$(".cell").forEach(cellButton => {
    const cell = cellButton.dataset.cell;
    const piece = pieces[cell];
    cellButton.dataset.zone = zoneFor(cell);
    cellButton.classList.toggle("selected", selectedCell === cell);
    if (!piece) {
      cellButton.innerHTML = `<span class="zone">${zoneFor(cell)}</span>`;
      cellButton.title = `${cell} · ${zoneFor(cell)} · empty`;
      return;
    }
    const ownerClass = piece.owner ? piece.owner.toLowerCase() : piece.outcome === "WHITE" ? "white" : "waiting";
    const outcomeClass = piece.outcome === "DEAD" ? "dead" : piece.outcome === "WAITING_RESULT" ? "waiting" : "";
    cellButton.innerHTML = `<span class="zone">${zoneFor(cell)}</span><div class="piece ${ownerClass} ${outcomeClass}"><strong>${piece.id}</strong><span>${piece.mod} · ${piece.outcome}</span></div>`;
    cellButton.title = `${cell} · ${piece.id} · ${piece.outcome} · owner ${piece.owner || "none"}`;
  });
}

function renderPool(slots, analysis) {
  const legal = new Set((analysis.legalPlacements || []).map(option => option.poolSlotId));
  const ids = Object.keys(slots).sort((a, b) => a.localeCompare(b, undefined, {numeric: true}));
  $("#pool").innerHTML = ids.map(id => {
    const slot = slots[id];
    const classes = [slot.state !== "AVAILABLE" ? "unavailable" : "", legal.has(id) ? "legal" : "", selectedPool === id ? "selected" : ""].join(" ");
    return `<button class="${classes}" data-pool="${id}" title="${slot.state}">${id}<small>${slot.mod} · ${slot.state}</small></button>`;
  }).join("");
  $("#pool-summary").textContent = `${analysis.selectablePoolSlotIds.length} selectable · ${analysis.legalPlacements.length} legal pairings`;
  $$("[data-pool]").forEach(button => button.addEventListener("click", () => selectPool(button.dataset.pool)));
}

function renderAnalysis(analysis, state) {
  const pending = state.pendingTbRequest ? `${state.pendingTbRequest.requestedBy} / ${state.pendingTbRequest.basis}` : "none";
  $("#analysis").innerHTML = `
    <div class="metric">Stalemate<strong>${analysis.stalemate ? "YES" : "NO"}</strong></div>
    <div class="metric">Captain TB window<strong>${state.turn >= 11 && state.turn <= 14 ? "OPEN" : "CLOSED"}</strong></div>
    <div class="metric">TB entry<strong class="code-value">${state.tbEntry ? `${state.tbEntry.basis}` : "none"}</strong></div>
    <div class="metric">Empty cells<strong>${analysis.emptyCells.length}</strong></div>
    <div class="metric">Pending TB<strong>${pending}</strong></div>
    <div class="metric">Red has robbed<strong>${state.robberyUsed?.RED ? "YES" : "NO"}</strong></div>
    <div class="metric">Blue has robbed<strong>${state.robberyUsed?.BLUE ? "YES" : "NO"}</strong></div>`;
}

function renderEvents(events) {
  $("#event-count").textContent = String(events.length);
  $("#events").innerHTML = [...events].reverse().map(event => {
    const detail = Object.entries(event).filter(([key, value]) => key !== "type" && value !== "" && value != null).map(([key, value]) => `${key}: ${Array.isArray(value) ? value.join(", ") : value}`).join(" · ");
    return `<li class="${event.type.includes("FINISHED") ? "finish" : ""}"><strong>${event.type}</strong>${detail || "state transition accepted"}</li>`;
  }).join("");
}

function fieldHTML(name) {
  const value = defaultValue(name);
  if (["winningTeam", "surrenderingTeam", "actingTeam"].includes(name)) {
    return `<label>${labels[name]}<select name="${name}"><option value="RED" ${value === "RED" ? "selected" : ""}>RED</option><option value="BLUE" ${value === "BLUE" ? "selected" : ""}>BLUE</option></select></label>`;
  }
  if (name === "basis") {
    return `<label>${labels[name]}<select name="basis"><option value="CAPTAIN_AGREEMENT">CAPTAIN_AGREEMENT</option></select></label>`;
  }
  if (name === "accept") {
    return `<label class="checkbox"><input type="checkbox" name="accept">${labels[name]}</label>`;
  }
  if (name === "sacrificeSets") {
    return `<label>${labels[name]}<textarea name="${name}" spellcheck="false">${value}</textarea></label>`;
  }
  const type = name === "remainingSeconds" ? "number" : "text";
  return `<label>${labels[name]}<input type="${type}" name="${name}" value="${value}" ${type === "number" ? "min=0" : ""}></label>`;
}

function defaultValue(name) {
  if (!snapshot) return "";
  const state = snapshot.state;
  const defaults = {
    poolSlotId: selectedPool || snapshot.analysis.selectablePoolSlotIds[0] || "",
    pieceId: state.pendingPieceId || `lab-piece-${state.version + 1}`,
    cell: selectedCell || snapshot.analysis.emptyCells[0] || "",
    winningTeam: state.activeTeam || "RED",
    targetPieceId: "",
    sacrificeSets: '[["piece-1","piece-3","piece-5"]]',
    reason: "manual engine verification",
    requestId: state.pendingTbRequest?.id || `tb-${state.version + 1}`,
    remainingSeconds: "30",
    surrenderingTeam: "RED",
    confirmingPlayerIds: "1001,1002,1003,1004",
    actingTeam: state.activeTeam || "RED"
  };
  return defaults[name] ?? "";
}

function renderFields() {
  const type = $("#command-type").value;
  $("#command-fields").innerHTML = commandDefinitions[type].map(fieldHTML).join("");
}

function updateFieldDefaults() {
  const poolInput = $('[name="poolSlotId"]');
  const cellInput = $('[name="cell"]');
  if (poolInput && !poolInput.value) poolInput.value = defaultValue("poolSlotId");
  if (cellInput && !cellInput.value) cellInput.value = defaultValue("cell");
}

function selectCell(cell) {
  selectedCell = cell;
  const input = $('[name="cell"]');
  if (input) input.value = cell;
  renderBoard(snapshot.state.board?.pieces || {});
}

function selectPool(pool) {
  selectedPool = pool;
  const input = $('[name="poolSlotId"]');
  if (input) input.value = pool;
  renderPool(snapshot.state.poolSlots, snapshot.analysis);
}

async function submitCommand(event) {
  event.preventDefault();
  if (commandPending) return;
  const form = new FormData(event.currentTarget);
  const payload = {actor, type: $("#command-type").value};
  for (const name of commandDefinitions[payload.type]) {
    if (name === "accept") payload[name] = form.get(name) === "on";
    else if (name === "remainingSeconds") payload[name] = Number(form.get(name));
    else if (name === "confirmingPlayerIds") payload[name] = String(form.get(name)).split(",").map(Number).filter(Number.isFinite);
    else if (name === "sacrificeSets") {
      try { payload[name] = JSON.parse(form.get(name)); }
      catch { showError("INVALID_JSON: 牺牲集合必须是二维 JSON 字符串数组"); return; }
    } else payload[name] = form.get(name);
  }
  setCommandPending(true);
  try {
    showError("");
    render(await api("/api/command", {method: "POST", body: JSON.stringify(payload)}));
    renderFields();
  } catch (error) {
    showError(error.message);
  } finally {
    setCommandPending(false);
  }
}

function setCommandPending(pending) {
  commandPending = pending;
  $$('button, input, select, textarea').forEach(control => { control.disabled = pending; });
  const submit = $('#command-form button[type="submit"]');
  if (submit) {
    submit.textContent = pending ? "执行中..." : "执行命令";
    submit.setAttribute("aria-busy", String(pending));
  }
}

function showError(message) {
  const banner = $("#error");
  banner.hidden = !message;
  banner.textContent = message;
}

function init() {
  $("#command-type").innerHTML = Object.keys(commandDefinitions).map(type => `<option value="${type}">${type}</option>`).join("");
  $("#command-type").addEventListener("change", renderFields);
  $("#command-form").addEventListener("submit", submitCommand);
  $$("[data-actor]").forEach(button => button.addEventListener("click", () => {
    actor = button.dataset.actor;
    $$("[data-actor]").forEach(candidate => candidate.classList.toggle("active", candidate === button));
  }));
  $$(".cell").forEach(button => button.addEventListener("click", () => selectCell(button.dataset.cell)));
  $$("[data-scenario]").forEach(button => button.addEventListener("click", async () => {
    try {
      selectedCell = selectedPool = "";
      render(await api("/api/reset", {method: "POST", body: JSON.stringify({scenario: button.dataset.scenario})}));
      renderFields(); showError("");
    } catch (error) { showError(error.message); }
  }));
  $$("[data-time]").forEach(button => button.addEventListener("click", async () => {
    try { render(await api("/api/time", {method: "POST", body: JSON.stringify({seconds: Number(button.dataset.time)})})); showError(""); }
    catch (error) { showError(error.message); }
  }));
  api("/api/state").then(next => { render(next); renderFields(); }).catch(error => showError(error.message));
}

init();
