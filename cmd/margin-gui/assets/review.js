import { Events, Clipboard } from "/wails/runtime.js";
import { api, $, $$, state, hooks, esc, prose, oneLine, el, toast, errText, tail, plural, shortList, base, openModal, closeModal } from "./core.js";
import { openAnswers } from "./answers.js";

export { closeModal };

export const verdicts = [
  { id: "good", key: "1", label: "Looks good", cls: "v-good" },
  { id: "changes", key: "2", label: "Needs changes", cls: "v-changes" },
  { id: "unsure", key: "3", label: "Not sure yet", cls: "v-unsure" },
];

const reader = () =>
  state.view?.reader ?? { verdicts: {}, comments: [], questions: [], reviewed: [], flagged: [], dismissed: [], revealed: [], visited: [], labels: [], seen: {} };
const cur = () => state.view?.stations[state.station];
const guided = () => !reader().full;
const walking = () => guided() && cur()?.kind === "code";
const partKey = (s, pi) => `${s.id}|${pi}`;
const partData = (s, pi) => state.parts[partKey(s, pi)] ?? s.parts[pi];

// Loading

export async function openReviewFile(path) {
  return openReview("", "", false, path);
}

export async function openReview(dir, arg, fresh = false, file = "") {
  hooks.showReviewer();
  $("#station").innerHTML = `<div class="spinner">Preparing the review…</div>`;
  let opened;
  try {
    opened = file ? await api("Reviews.OpenFile", file) : await api("Reviews.Open", dir, arg, fresh);
  } catch (e) {
    $("#station").innerHTML = `<div class="spinner error">${esc(errText(e))}</div>`;
    return;
  }
  const same = state.opened?.path === opened.path;
  state.opened = { ...opened, arg, file };
  if (!same) {
    state.view = null;
    Object.assign(state, { station: 0, note: -1, step: 0, sel: null, compose: null, parts: {}, expand: {}, whole: {}, around: {}, known: null,
      query: "", spot: -1, spotLines: null, filter: 0 });
    state.fresh = new Set();
  }
  hooks.reviewOpened?.();
  await reload(!same);
  if (!same && state.view) {
    // Like the terminal viewer: the first code stop, unless a guided walk starts fresh at the overview.
    const fresh = guided() && !reader().visited.length;
    if (!fresh && state.view.stations.length > 1) goStation(1);
  }
  if (opened.shared) toast("This review is also open in a terminal viewer: the agent's margin goto and where drive that one, not this window.", true);
  $("#workspace").classList.add("focus");
  hooks.fitTerminal();
  await hooks.attachAgent?.();
}

async function reload(top = false) {
  try {
    applyView(await api("Reviews.Current"), top);
  } catch (e) {
    if (!state.view) $("#station").innerHTML = `<div class="spinner error">${esc(errText(e))}</div>`;
    else toast(errText(e), true);
  }
}

function applyView(view, top = false) {
  const prevId = cur()?.id;
  trackFresh(view);
  if (state.view && state.view !== view && !top) {
    state.around = {};
    if (state.spot >= 0) {
      if (!view.coverage || state.spot >= view.coverage.tests.length) {
        state.spot = -1;
        state.spotLines = null;
      } else {
        api("Reviews.RunsLines", state.spot).then((l) => {
          state.spotLines = l;
          renderKeepScroll();
        });
      }
    }
  }
  state.view = view;
  const i = view.stations.findIndex((s) => s.id === prevId);
  state.station = i >= 0 ? i : Math.min(state.station, view.stations.length - 1);
  const scroll = $("#station").scrollTop;
  renderReview();
  if (!top) $("#station").scrollTop = scroll;
  refreshParts();
}

// trackFresh marks notes that appeared since the last load, so notes the agent adds elsewhere are not missed.
function trackFresh(view) {
  const keys = new Set();
  const where = new Set();
  let added = 0;
  for (const s of view.stations) {
    for (const n of s.notes) {
      keys.add(n.key);
      if (state.known && !state.known.has(n.key)) {
        state.fresh.add(n.key);
        where.add(s.id);
        added++;
      }
    }
  }
  for (const k of state.fresh) if (!keys.has(k)) state.fresh.delete(k);
  if (added) toast(`${plural(added, "new note", "new notes")} in ${[...where].join(", ")} · N jumps there`);
  state.known = keys;
}

async function refreshParts() {
  const s = cur();
  if (!s) return;
  let changed = false;
  for (const k of Object.keys(state.parts)) {
    const [sid, pi] = k.split("|");
    if (sid !== s.id) continue;
    try {
      state.parts[k] = await api("Reviews.Lines", sid, +pi, state.expand[k] ?? 0, !!state.whole[k]);
      changed = true;
    } catch {
      delete state.parts[k];
    }
  }
  if (changed) renderKeepScroll();
}

export async function act(name, ...args) {
  try {
    const v = await api("Reviews." + name, ...args);
    if (v) applyView(v);
    return true;
  } catch (e) {
    toast(errText(e), true);
    return false;
  }
}

let reloadTimer;
Events.On("review:changed", () => {
  clearTimeout(reloadTimer);
  reloadTimer = setTimeout(() => state.opened && reload(), 150);
});

// The agent drives the window through the control socket: margin goto, where and reload.

Events.On("ctl:request", async (ev) => {
  const { id, cmd, arg } = ev.data ?? {};
  let err = "";
  for (let i = 0; i < 100 && !state.view; i++) await new Promise((r) => setTimeout(r, 100));
  try {
    if (cmd === "reload") await reload();
    if (!state.view) throw new Error("review not loaded");
    if (cmd === "goto") await gotoTarget(arg);
  } catch (e) {
    err = errText(e);
  }
  api("Reviews.CtlReply", id, position(err));
});

function position(err = "") {
  const s = cur();
  const sel = s && state.sel && state.sel.station === s.id ? state.sel : null;
  const visible = [];
  const box = $("#station").getBoundingClientRect();
  for (const r of $$("#station .row[data-line]")) {
    const b = r.getBoundingClientRect();
    if (b.bottom < box.top || b.top > box.bottom) continue;
    const part = +r.dataset.part, line = +r.dataset.line;
    const last = visible[visible.length - 1];
    if (last && last.part === part && last.to + 1 >= line) last.to = Math.max(last.to, line);
    else visible.push({ part, from: line, to: line });
  }
  return { station: state.station, note: state.note, part: sel?.part ?? -1, line: sel?.line ?? -1, visible, spot: state.spot, err };
}

function findStation(head) {
  const st = state.view.stations;
  if (/^-?\d+$/.test(head)) {
    const n = +head;
    if (n < 0 || n >= st.length) throw new Error(`station ${n} out of range 0-${st.length - 1}`);
    return n;
  }
  const i = st.findIndex((s) => s.id === head);
  if (i < 0) throw new Error(`no station "${head}"`);
  return i;
}

// gotoTarget goes to a stop, a note of a stop (stop:N) or a line of a file (file:line), like the
// terminal viewer's goto.
async function gotoTarget(arg) {
  arg = (arg ?? "").trim();
  let head = arg, tail = "";
  const i = arg.lastIndexOf(":");
  if (i >= 0) [head, tail] = [arg.slice(0, i), arg.slice(i + 1)];
  if (tail && /[./]/.test(head) && /^\d+$/.test(tail)) return gotoFileLine(head, +tail);
  const si = findStation(head);
  goStation(si);
  if (!tail) return;
  const n = Number(tail);
  const s = state.view.stations[si];
  if (!Number.isInteger(n) || n < 1 || n > s.notes.length) throw new Error(`station ${s.id} has ${s.notes.length} notes`);
  const note = s.notes[n - 1];
  focusNote(n - 1);
  if (note.part >= 0) selectRow(s.id, note.part, note.line, false);
}

async function gotoFileLine(file, line) {
  const st = state.view.stations;
  const order = [state.station, ...st.map((_, i) => i).filter((i) => i !== state.station)];
  const match = (p) => !p.err && !p.base && (p.file === file || p.file.endsWith("/" + file));
  for (let pass = 0; pass < 2; pass++) {
    for (const si of order) {
      for (let pi = 0; pi < st[si].parts.length; pi++) {
        const p = partData(st[si], pi);
        if (!match(p) || line < 1 || line > p.len || (pass === 0 && (line - 1 < p.start || line - 1 > p.end))) continue;
        if (pass === 1) {
          if (si !== state.station) goStation(si);
          state.whole[partKey(st[si], pi)] = true;
          await loadPart(st[si], pi);
        }
        jump({ si, pi, line: line - 1 });
        return;
      }
    }
  }
  throw new Error(`${file}:${line} is not in any station`);
}

// Notes: blind mode and filters

function blind(s) {
  const r = reader();
  return s?.kind === "code" && s.risk === "high" && r.blind && !r.revealed.includes(s.id);
}

function filters() {
  const out = ["all notes", "what asks you", "problems only"];
  const areas = new Set();
  for (const s of state.view?.stations ?? []) for (const n of s.notes) if (n.focus) areas.add("focus: " + n.focus);
  return out.concat([...areas].sort());
}

const asks = (n) => ["issue", "question", "decide"].includes(n.kind);

function noteVisible(s, n) {
  if (blind(s)) return false;
  switch (state.filter) {
    case 0:
      return true;
    case 1:
      return asks(n);
    case 2:
      return n.kind === "issue" && !n.unbacked;
  }
  return "focus: " + n.focus === filters()[state.filter];
}

// Header, phases and the stop list

export function renderReview() {
  const v = state.view;
  $("#r-kicker").textContent = v.kicker;
  $("#r-title").textContent = v.title;
  $("#r-stats").textContent = v.stats;
  const drafts = reader().drafts ?? 0;
  $("#send-btn").classList.toggle("hidden", !drafts);
  $("#send-btn").textContent = `Send ${drafts} comment${drafts > 1 ? "s" : ""}`;
  renderToolbar();
  renderStatus();
  const nav = $("#stations");
  nav.innerHTML = "";
  let n = 0;
  v.stations.forEach((s, i) => {
    const num = s.kind === "code" ? String(++n) : s.kind === "overview" ? "◆" : s.kind === "recap" ? "✓" : "⚗";
    const meta = [];
    if (s.added) meta.push(`<span class="m-add">+${s.added}</span>`);
    if (s.changed) meta.push(`<span class="m-chg">~${s.changed}</span>`);
    if (s.removed) meta.push(`<span class="m-del">−${s.removed}</span>`);
    if (s.issues) meta.push(`<span class="m-issue">${s.issues} issue${s.issues > 1 ? "s" : ""}</span>`);
    if (s.questions) meta.push(`<span class="m-q">${s.questions}?</span>`);
    if (s.decisions) meta.push(`<span class="m-decide">${s.decisions} call${s.decisions > 1 ? "s" : ""}</span>`);
    if (s.kind === "code" && s.notes.length) meta.push(`<span class="muted">${s.notes.length} notes</span>`);
    if (s.notes.some((x) => state.fresh.has(x.key))) meta.push(`<span class="m-fresh">new</span>`);
    if (s.risk) meta.push(`<span class="risk-${esc(s.risk)}">${esc(s.risk)} risk</span>`);
    if (s.cov?.stale) meta.push(`<span class="m-chg">coverage stale</span>`);
    else if (s.cov?.measured && s.cov.run + s.cov.missed > 0) meta.push(`<span class="${s.cov.missed ? (s.cov.run ? "m-chg" : "m-del") : "m-add"}">${s.cov.run}/${s.cov.run + s.cov.missed} tested</span>`);
    if (s.needsTests) meta.push(`<span class="m-del">no tests named</span>`);
    if (s.auto) meta.push(`<span class="m-del">not in the tour</span>`);
    const mine = reader().comments.filter((c) => c.station === s.id && !c.resolved).length;
    if (mine) meta.push(`<span class="m-mine">${mine} comment${mine > 1 ? "s" : ""}</span>`);
    const vd = verdicts.find((x) => x.id === reader().verdicts?.[s.id]);
    const visited = !vd && reader().visited.includes(s.id) && s.kind === "code";
    const item = el(`<div class="st${i === state.station ? " on" : ""}"><span class="num">${vd ? `<span class="vdot ${vd.cls}" title="${vd.label}"></span>` : visited ? `<span class="vdot half" title="visited"></span>` : num}</span>
      <span class="t">${esc(s.title || s.id)}</span><div class="meta">${meta.join("")}</div></div>`);
    item.onclick = () => goStation(i);
    nav.append(item);
  });
  renderStation();
}

function renderToolbar() {
  const s = cur();
  const tb = $("#toolbar");
  tb.innerHTML = "";
  const btn = (label, title, on, fn, cls = "") => {
    const b = el(`<button class="tb${on ? " on" : ""} ${cls}" title="${esc(title)}">${label}</button>`);
    b.onclick = fn;
    tb.append(b);
    return b;
  };
  btn(guided() ? "Guided walk" : "Whole stop", "Switch between the guided walk and whole stops (w)", false, toggleGuide);
  btn(filters()[state.filter] ?? "all notes", "Which notes to show (F)", state.filter > 0, cycleFilter);
  btn("Removed", "Show removed lines (d)", state.showGhosts, () => toggle("showGhosts"));
  btn("Split", "Old and new side by side (s)", state.split, () => toggle("split"));
  btn("Notes", "Show the agent's notes (n)", state.showNotes, () => toggle("showNotes"));
  btn("Fold", "Fold unchanged stretches (z)", !state.unfold, () => toggle("unfold"));
  btn("Blind", "Hide the agent's notes on high-risk stops until you revealed them", reader().blind, () => act("SetBlind", !reader().blind));
  if (blind(s)) btn("Reveal notes", "Show the agent's notes on this stop (v)", false, reveal, "warn");
  const search = el(`<input id="search" class="search" placeholder="Search /" value="${esc(state.query)}">`);
  search.onkeydown = (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      state.query = search.value;
      searchNext(e.shiftKey ? -1 : 1, true);
      search.blur();
    }
    if (e.key === "Escape") {
      e.preventDefault();
      endSearch();
      search.blur();
    }
  };
  tb.append(search);
  if (state.spot >= 0) btn("Spotlight: " + esc(state.view.coverage?.tests[state.spot]?.label ?? "") + " ✕", "End the spotlight (esc)", true, endSpot, "warn");
  if (reader().comments.some((c) => !c.draft)) btn("Answers", "The agent's answers to your comments, to read, edit or reply to (A)", false, () => openAnswers());
  btn("Export", "Your comments, flagged problems and calls, to post yourself", false, openExport);
  btn("?", "All keys (H)", false, openHelp);
}

function toggle(key) {
  state[key] = !state[key];
  renderKeepScroll();
  renderToolbar();
}

function phaseRow(phase, detail) {
  const names = ["Orient", "Walk", "Decide"];
  const row = el(`<div class="phase">${names.map((nm, i) => `<span class="${i + 1 === phase ? "on" : ""}">${["①", "②", "③"][i]} ${nm}</span>`).join('<span class="sep">─</span>')}
    ${detail ? `<span class="detail">${esc(detail)}</span>` : ""}</div>`);
  return row;
}

function codeStops() {
  let at = 0, of = 0;
  state.view.stations.forEach((s, i) => {
    if (s.kind !== "code") return;
    of++;
    if (i <= state.station) at = of;
  });
  return [at, of];
}

export function goStation(i, noteIdx = -1, step = 0) {
  if (!state.view) return;
  state.station = Math.max(0, Math.min(i, state.view.stations.length - 1));
  state.note = noteIdx;
  state.step = step;
  state.nudged = false;
  state.compose = null;
  const s = cur();
  countRead(s);
  if (s.kind === "code" && !reader().visited.includes(s.id)) {
    reader().visited.push(s.id);
    api("Reviews.Visit", s.id).catch(() => {});
  }
  renderReview();
  $("#station").scrollTop = 0;
  document.querySelector("#stations .st.on")?.scrollIntoView({ block: "nearest" });
  if (noteIdx >= 0) focusNote(noteIdx);
  else if (walking()) enterStep();
  refreshParts();
}

function renderStation() {
  const s = cur();
  const box = $("#station");
  box.innerHTML = "";
  if (s.kind === "code") lookAround(s);
  if (s.kind === "overview") return renderOverview(box);
  if (s.kind === "recap") return renderRecap(box);
  if (s.kind === "tests") return renderTests(box);
  if (guided()) return renderWalk(box, s);
  renderWhole(box, s);
}

export function renderKeepScroll() {
  const sc = $("#station").scrollTop;
  renderStation();
  $("#station").scrollTop = sc;
}

// Code

// renderText draws text, plain or as highlighted spans, with layers of ranges wrapped in tags on top:
// search matches and the changed part of a changed line.
function renderText(spans, layers) {
  if (typeof spans === "string") spans = [{ t: spans }];
  let pos = 0;
  let out = "";
  const open = layers.map(() => false);
  for (const sp of spans ?? []) {
    const st = [sp.c ? `color:${sp.c}` : "", sp.b ? "font-weight:600" : "", sp.i ? "font-style:italic" : ""].filter(Boolean).join(";");
    let piece = layers.map((l, li) => (open[li] ? l.open : "")).join("");
    const chars = [...sp.t];
    for (let i = 0; i < chars.length; i++) {
      const p = pos + i;
      layers.forEach((l, li) => {
        if (open[li] && l.ranges.some(([, b]) => b === p)) {
          piece += l.close;
          open[li] = false;
        }
      });
      layers.forEach((l, li) => {
        if (!open[li] && l.ranges.some(([a, b]) => a === p && b > a)) {
          piece += l.open;
          open[li] = true;
        }
      });
      piece += esc(chars[i]);
    }
    pos += chars.length;
    layers.forEach((l, li) => {
      if (open[li]) {
        piece += l.close;
        if (l.ranges.some(([, b]) => b === pos)) open[li] = false;
      }
    });
    out += st ? `<span style="${st}">${piece}</span>` : piece;
  }
  return out;
}

function matchRanges(text, query) {
  const ranges = [];
  if (!query) return ranges;
  const fold = query === query.toLowerCase();
  const hay = [...(fold ? text.toLowerCase() : text)].join("");
  const chars = [...hay];
  const q = [...query];
  for (let i = 0; i + q.length <= chars.length; i++) {
    if (q.every((c, j) => chars[i + j] === c)) {
      ranges.push([i, i + q.length]);
      i += q.length - 1;
    }
  }
  return ranges;
}

function renderSpans(spans, query, hl) {
  const text = typeof spans === "string" ? spans : (spans ?? []).map((sp) => sp.t).join("");
  const layers = [{ ranges: matchRanges(text, query), open: "<mark>", close: "</mark>" }];
  if (hl) layers.push(hl);
  return renderText(spans, layers);
}

const covMarks = { run: ["┃", "cov-run"], missed: ["✗", "cov-missed"], idle: ["·", "cov-idle"] };

const kindStyles = {
  issue: ["!", "problem"],
  question: ["?", "question"],
  decide: ["◆", "your call"],
  ok: ["✓", "looks right"],
  info: ["i", "context"],
  nit: ["·", "nit"],
};

function noteStyle(n) {
  if (n.unbacked) return ["!?", "problem without evidence", "question"];
  const [icon, label] = kindStyles[n.kind] ?? kindStyles.info;
  return [icon, label, kindStyles[n.kind] ? n.kind : "info"];
}

const untab = (t) => String(t ?? "").replaceAll("\t", "    ");

function ghostRow(text, moved, hl, right) {
  text = untab(text);
  const cls = moved ? "ghost moved" : "ghost";
  const body = moved ? "← " + esc(text) : renderSpans(text, state.query, hl ? { ranges: [hl], open: '<span class="del-hl">', close: "</span>" } : null);
  return el(`<div class="row ${cls}"${moved ? ` title="moved to ${esc(moved)}"` : ""}><span class="mark"></span><span class="cov"></span><span class="ln"></span>
    <span class="src${right !== undefined ? " left" : ""}">${body}</span>${right !== undefined ? `<span class="src right">${right}</span>` : ""}</div>`);
}

// codeRows draws lines from..to of a part, with the notes, comments and questions on each line below it.
function codeRows(s, pi, from, to, opts = {}) {
  const p = partData(s, pi);
  const split = state.split && p.diffed;
  const scroll = el(`<div class="code-scroll"><div class="code code-inner${split ? " split" : ""}"></div></div>`);
  const code = scroll.firstElementChild;
  const shown = opts.shown ?? new Set();
  const spotSet = state.spotLines ? new Set(state.spotLines[partKey(s, pi)] ?? []) : null;
  const folds = state.unfold || opts.step ? [] : (p.folds ?? []);
  const rows = p.rows.filter((r) => (r.n ? r.n - 1 >= from && r.n - 1 <= to : to >= p.len - 1));
  const hlOld = (r) => (r.hl ? [r.hl[0], r.hl[1]] : null);
  // In side by side, removed lines pair with the changed lines that replaced them.
  const pairedLeft = new Map();
  for (let k = 0; k < rows.length; k++) {
    const r = rows[k];
    for (let j = 0; j < (r.g ?? []).length; j++) {
      const c = rows[k + j];
      if (!split || !c || c.k !== "chg" || (j > 0 && (c.g ?? []).length) || r.gm?.[j]) break;
      pairedLeft.set(c.n, { text: r.g[j], hl: hlOld(c), from: k });
    }
  }
  for (let k = 0; k < rows.length; k++) {
    const r = rows[k];
    const line = r.n - 1;
    const fold = r.n && folds.find((f) => f.start === line);
    if (fold && fold.end <= to) {
      const n = fold.end - fold.start + 1;
      const f = el(`<div class="row fold navable"><span class="mark"></span><span class="cov"></span><span class="ln">⋯</span>
        <span class="src">${n} unchanged lines${fold.label ? " · " + esc(fold.label) : ""} · click to show</span></div>`);
      f.onclick = () => {
        p.folds = p.folds.filter((x) => x !== fold);
        renderKeepScroll();
      };
      code.append(f);
      while (k + 1 < rows.length && rows[k + 1].n - 1 <= fold.end) k++;
      continue;
    }
    const ghosts = r.g ?? [];
    if (split) {
      const paired = [...pairedLeft.values()].filter((v) => v.from === k).length;
      ghosts.slice(paired).forEach((g, j) => code.append(ghostRow(g, r.gm?.[paired + j], null, "")));
    } else if (state.showGhosts) {
      ghosts.forEach((g, j) => {
        const next = rows.slice(k).find((x) => x.k === "chg" && x.was === g);
        code.append(ghostRow(g, r.gm?.[j], next ? hlOld(next) : null));
      });
    }
    if (!r.n) continue;
    const notes = state.showNotes ? (r.notes ?? []).filter((i) => !shown.has(i) && noteVisible(s, s.notes[i])) : [];
    const allNotes = state.showNotes ? (r.notes ?? []).filter((i) => noteVisible(s, s.notes[i])) : [];
    const sel = state.sel && state.sel.station === s.id && state.sel.part === pi && state.sel.line === line;
    const [cm, ccls] = covMarks[r.cov] ?? ["", ""];
    const dim = spotSet && !spotSet.has(line);
    const newHl = r.hl ? { ranges: [[r.hl[2], r.hl[3]]], open: '<span class="add-hl">', close: "</span>" } : null;
    const src = renderSpans(r.s, state.query, newHl) || " ";
    let left = "";
    if (split) {
      const pl = pairedLeft.get(r.n);
      left = `<span class="src left">${pl ? renderSpans(untab(pl.text), state.query, pl.hl ? { ranges: [pl.hl], open: '<span class="del-hl">', close: "</span>" } : null) : r.k === "add" || r.k === "chg" ? "" : src}</span>`;
    }
    const badge = allNotes.length ? `<span class="nb k-${esc(noteStyle(s.notes[allNotes[0]])[2])}${allNotes.includes(state.note) ? " on" : ""}">${allNotes[0] + 1}${allNotes.length > 1 ? "+" : ""}</span>` : "";
    const moved = r.mv ? " moved" : "";
    const row = el(`<div class="row ${r.k ?? ""}${moved}${notes.length ? " has-note" : ""}${sel ? " sel" : ""}${dim ? " dim" : ""}" data-part="${pi}" data-line="${line}"${r.mv ? ` title="moved here unchanged from ${esc(r.mv)}"` : ""}>
      <span class="mark">${r.mv ? "→" : ""}</span><span class="cov ${ccls}" title="${r.cov === "run" ? `${r.tn} tests run this line · click lists them (t)` : r.cov === "missed" ? "changed, and no test runs it" : ""}">${cm}</span>
      <span class="ln" title="Comment on this line (c)">${badge}${r.n}</span>${left}<span class="src${split ? " right" : ""}">${src}</span></div>`);
    row.onclick = (e) => {
      if (window.getSelection()?.toString()) return;
      if (e.target.classList.contains("cov") && r.cov === "run") {
        state.sel = { station: s.id, part: pi, line };
        return openTests();
      }
      if (e.target.classList.contains("nb")) return focusNote(allNotes[0]);
      selectRow(s.id, pi, line, e.target.classList.contains("ln"));
    };
    code.append(row);
    for (const i of notes) {
      shown.add(i);
      code.append(noteCard(s, s.notes[i], i));
    }
    for (const c of reader().comments) {
      if (c.station === s.id && c.file === p.file && c.line === r.n && (c.side === "base") === !!p.base && !c.resolved) code.append(commentCard(c));
    }
    for (const q of reader().questions) {
      if (q.station === s.id && q.file === p.file && q.line === r.n && (q.side === "base") === !!p.base && !q.answered) code.append(questionCard(q));
    }
    const cp = state.compose;
    if (cp && cp.station === s.id && cp.part === pi && cp.line === line) code.append(composer(s, p));
  }
  return scroll;
}

// partTitle says what a part shows, the way the terminal viewer's part header does.
function partTitle(s, pi, p) {
  const k = partKey(s, pi);
  const bits = [p.hunkCount > 1 ? `change ${p.hunk}/${p.hunkCount}` : p.label];
  if (p.baseFile) bits.push("renamed from " + p.baseFile);
  if (!p.err) bits.push(`${p.start + 1}–${p.end + 1}`);
  if (p.base) bits.push("base side, removed");
  else if (p.newFile) bits.push("new file");
  if (!p.err && p.diffed && (p.plus + p.minus > 0 || !p.background)) bits.push(`<span class="m-add">+${p.plus}</span> <span class="m-del">-${p.minus}</span>`);
  if (p.movedN) bits.push(`<span class="m-moved">${p.movedN} moved unchanged</span>`);
  if (state.whole[k]) bits.push("whole file");
  else if (state.expand[k]) bits.push(`${state.expand[k]} more lines around changes`);
  if (p.background) bits.push("background, read first");
  else if (!p.err && !blind(s) && !s.auto && !s.notes.some((n) => n.part === pi)) bits.push(`<span class="m-chg">no agent notes, read it yourself</span>`);
  return bits.map((b) => (b.startsWith("<") ? b : esc(b))).join(" · ");
}

function partBox(s, pi, from, to, title, opts = {}) {
  const p = partData(s, pi);
  const k = partKey(s, pi);
  const part = el(`<section class="part${p.background ? " bgpart" : ""}">
    <div class="part-head"><span class="file">${esc(title ?? p.file)}</span><span class="label">${title ? (p.baseFile ? "renamed from " + esc(p.baseFile) : "") : partTitle(s, pi, p)}</span>
    <span class="spacer"></span>
    ${p.err ? "" : `<button class="na" data-w="-1" title="Less context (-)">−</button><button class="na" data-w="1" title="More context (+)">+</button>
    <button class="na${state.whole[k] ? " on" : ""}" data-f="1" title="Whole file (f)">whole file</button>`}</div></section>`);
  part.querySelectorAll("[data-w]").forEach((b) => (b.onclick = () => widen(s, pi, +b.dataset.w)));
  part.querySelector("[data-f]")?.addEventListener("click", () => wholeFile(s, pi));
  if (p.about && opts.about !== false && (p.hunk ?? 0) <= 1) part.append(el(`<div class="part-about">${prose(p.about)}</div>`));
  if (p.err) {
    part.append(el(`<div class="part-err">${esc(p.err)}</div>`));
    return part;
  }
  const extra = state.expand[k] || state.whole[k];
  part.append(codeRows(s, pi, extra && opts.step ? Math.min(from, p.start) : from, extra && opts.step ? Math.max(to, p.end) : to, opts));
  return part;
}

async function widen(s, pi, dir) {
  const k = partKey(s, pi);
  const step = await stepSize();
  state.expand[k] = Math.max(0, (state.expand[k] ?? 0) + dir * step);
  await loadPart(s, pi);
}

async function wholeFile(s, pi) {
  const k = partKey(s, pi);
  state.whole[k] = !state.whole[k];
  await loadPart(s, pi);
}

let expandStep = 0;
async function stepSize() {
  if (!expandStep) expandStep = await api("Reviews.ExpandStep");
  return expandStep;
}

async function loadPart(s, pi) {
  const k = partKey(s, pi);
  if (!state.expand[k] && !state.whole[k]) {
    delete state.parts[k];
  } else {
    try {
      state.parts[k] = await api("Reviews.Lines", s.id, pi, state.expand[k] ?? 0, !!state.whole[k]);
    } catch (e) {
      toast(errText(e), true);
    }
  }
  renderKeepScroll();
}

// Notes, comments and questions

function noteLabel(n) {
  if (n.qid?.startsWith("c")) return "agent's answer";
  const [icon, label] = noteStyle(n);
  return `${icon} ${label}`;
}

function noteCard(s, n, i, opts = {}) {
  const tags = [n.focus, n.confidence ? n.confidence + " confidence" : ""].filter(Boolean).join(" · ");
  const kind = n.unbacked ? "question" : n.kind;
  const rd = reader();
  const marks = { reviewed: rd.reviewed.includes(n.key), flagged: rd.flagged.includes(n.key), dismissed: rd.dismissed.includes(n.key) };
  const answered = rd.comments.some((c) => c.answers === n.key);
  const changed = n.changed && rd.seen?.[n.key] !== n.changed;
  const where = n.part >= 0 ? `${base(s.parts[n.part].file)}:${n.line + 1}` : "";
  const card = el(`<div class="note ${esc(kind)}${i === state.note ? " sel" : ""}${marks.reviewed ? " is-read" : ""}${marks.dismissed ? " is-dismissed" : ""}${marks.flagged ? " is-flagged" : ""}${opts.static ? " static" : ""}" data-note="${i}">
    ${n.q ? `<div class="q">${n.qid?.startsWith("c") ? "Your comment " + esc(n.qid) : "You asked" + (n.qid ? " " + esc(n.qid) : "")}: ${prose(n.q)}</div>` : ""}
    <span class="nnum k-${esc(noteStyle(n)[2])}">${i + 1}</span><span class="nk">${esc(noteLabel(n))}</span>${tags ? `<span class="tags">${esc(tags)}</span>` : ""}
    ${state.fresh.has(n.key) ? `<span class="badge fresh">new</span>` : ""}${changed ? `<span class="badge fresh" title="the agent changed this note since you read it">Δ changed</span>` : ""}
    ${n.problem ? "" : where ? `<span class="tags"> · ${esc(where)}</span>` : ""}
    <div>${prose(n.text)}</div>
    ${n.evidence ? `<div class="ev">Evidence: ${prose(n.evidence)}</div>` : ""}
    ${n.changed ? `<div class="ev">Changed: ${prose(n.changed)}</div>` : ""}
    ${n.problem ? `<div class="ev problems">${esc(n.problem)} — anchored at “${esc(n.at)}”</div>` : ""}
    <div class="note-actions"></div></div>`);
  card.onclick = () => {
    state.fresh.delete(n.key);
    if (opts.jump) return opts.jump();
    focusNote(i);
  };
  const actions = card.querySelector(".note-actions");
  const btn = (label, title, on, fn) => {
    const b = el(`<button class="na${on ? " on" : ""}" title="${title}">${label}</button>`);
    b.onclick = (e) => {
      e.stopPropagation();
      fn();
    };
    actions.append(b);
  };
  if (n.kind !== "decide") btn(marks.reviewed ? "✓ Read" : "Mark read", "You read and understood it (space)", marks.reviewed, () => act("MarkNote", n.key, "reviewed"));
  if (n.kind === "issue" && !n.unbacked) btn(marks.flagged ? "⚑ Agreed" : "Agree", "Agree it is a problem: it blocks approval (?)", marks.flagged, () => act("MarkNote", n.key, "flagged"));
  if (n.kind === "decide") btn(marks.reviewed ? "☑ Decided" : "☐ Decided", "You made this call (enter)", marks.reviewed, () => act("MarkNote", n.key, "reviewed"));
  if ((n.kind === "question" || n.unbacked) && !n.q && n.part >= 0) {
    btn(answered ? "✓ Answered" : "Answer", "Answer the agent's question in a comment", answered, () => openComposer(s.id, n.part, n.line, { answers: n.key }));
  }
  btn(marks.dismissed ? "Restore" : "Dismiss", "Not useful (x)", marks.dismissed, () => act("MarkNote", n.key, "dismissed"));
  btn("Copy", "Copy the note (y)", false, () => copy(noteText(s, i), "note " + (i + 1)));
  return card;
}

export function focusNote(i) {
  state.note = i;
  const s = cur();
  const n = s?.notes[i];
  if (n) state.fresh.delete(n.key);
  if (n?.problem) toast(`note ${i + 1}: ${n.problem}`);
  if (n && walking() && n.part >= 0) {
    const si = stepOf(s, n.part, n.line);
    if (si >= 0 && si !== state.step) {
      state.step = si;
      renderStation();
    }
  }
  if (n && n.part >= 0) {
    state.sel = { station: s.id, part: n.part, line: n.line };
    $$("#station .row.sel, #station .kcur").forEach((r) => r.classList.remove("sel", "kcur"));
    document.querySelector(`#station .row[data-part="${n.part}"][data-line="${n.line}"]`)?.classList.add("sel");
    renderStatus();
  }
  $$("#station .note.sel").forEach((x) => x.classList.remove("sel"));
  const card = document.querySelector(`#station .note[data-note="${i}"]`);
  if (!card) return;
  card.classList.add("sel");
  card.scrollIntoView({ block: "center", behavior: "smooth" });
}

function stepNote(dir) {
  const s = cur();
  if (!s) return;
  const order = s.notes.map((n, i) => [n, i]).filter(([n]) => noteVisible(s, n)).sort(([a], [b]) => a.part - b.part || a.line - b.line).map(([, i]) => i);
  const at = order.indexOf(state.note);
  const next = at < 0 ? (dir > 0 ? 0 : order.length - 1) : at + dir;
  if (next >= 0 && next < order.length) return focusNote(order[next]);
  for (let si = state.station + dir; si >= 0 && si < state.view.stations.length; si += dir) {
    const t = state.view.stations[si];
    const vis = t.notes.map((n, i) => [n, i]).filter(([n]) => noteVisible(t, n)).sort(([a], [b]) => a.part - b.part || a.line - b.line);
    if (!vis.length) continue;
    goStation(si);
    return focusNote(dir > 0 ? vis[0][1] : vis[vis.length - 1][1]);
  }
}

function selectedNote() {
  const s = cur();
  return s?.notes[state.note];
}

function noteMark(mark) {
  const n = selectedNote();
  if (!n) return toast("No note selected: click one, or step to one with ] and [.");
  const was = reader()[mark]?.includes(n.key);
  act("MarkNote", n.key, mark).then((ok) => {
    if (!ok || mark !== "dismissed") return;
    toast(was ? `note ${state.note + 1} restored` : `note ${state.note + 1} dismissed as not useful`);
  });
}

function xKey() {
  const k = document.querySelector("#station .kcur[data-comment]");
  if (k) {
    const c = reader().comments.find((x) => x.id === k.dataset.comment);
    if (!c?.draft) return toast(`${c?.id} was sent already; ask the agent to drop it`);
    return act("DeleteComment", c.id).then((ok) => ok && toast(`${c.id} deleted`));
  }
  noteMark("dismissed");
}

function noteText(s, i) {
  const n = s.notes[i];
  const where = n.part >= 0 ? `${s.parts[n.part].file}:${n.line + 1}` : s.id;
  const lines = [`${where} · note ${i + 1} · ${noteLabel(n)}`];
  if (n.q) lines.push("Q: " + n.q.trim());
  lines.push(n.text.trim());
  if (n.evidence) lines.push("evidence: " + n.evidence.trim());
  if (n.changed) lines.push("changed: " + n.changed.trim());
  return lines.join("\n") + "\n";
}

function stopText(s) {
  const blocks = [s.id + " · " + s.title + (s.lede ? "\n" + s.lede.trim() : "")];
  for (const [label, t] of [["Scope", s.scope], ["What", s.what], ["Why", s.why], ["Risk", s.risk ? s.risk + (s.riskWhy ? ": " + s.riskWhy : "") : ""], ["Concern", s.concern]]) {
    if (t?.trim()) blocks.push(label + "\n" + t.trim());
  }
  for (const p of s.parts) if (p.about?.trim() && (p.hunk ?? 0) <= 1) blocks.push(base(p.file) + " · " + p.label + ": " + p.about.trim());
  s.notes.forEach((n, i) => {
    if (noteVisible(s, n) && !reader().dismissed.includes(n.key)) blocks.push(noteText(s, i).trimEnd());
  });
  return blocks.join("\n\n") + "\n";
}

async function copy(text, what) {
  try {
    await Clipboard.SetText(text);
    toast(`${what} copied`);
  } catch (e) {
    toast(errText(e), true);
  }
}

function yank(all) {
  const s = cur();
  if (blind(s)) return toast("The notes are hidden on this stop until you reveal them (v).");
  if (all) {
    if (s.kind !== "code") return toast("Y copies a code stop; open one first.");
    return copy(stopText(s), "stop " + s.id);
  }
  if (!selectedNote()) return toast("No note selected.");
  copy(noteText(s, state.note), "note " + (state.note + 1));
}

function commentCard(c, opts = {}) {
  const body = c.text.replace(/^[a-z]+(?: \([a-z, -]+\))?: /, "");
  const card = el(`<div class="comment${c.draft ? " draft" : ""}${c.blocking ? " blocking" : ""}${opts.static ? " static" : ""}">
    ${opts.where ? `<div class="muted small">${esc(c.file)}:${c.line}</div>` : ""}
    <span class="ck">${esc(c.id)} · you${c.label ? " · " + esc(c.label) : ""}${c.blocking ? " (blocking)" : ""}</span>
    <span class="badge">${c.resolved ? "resolved" : c.draft ? "draft" : "sent"}</span>
    <div>${prose(body)}</div></div>`);
  if (!c.draft) {
    const ans = el(`<button class="na" title="Read the agent's answer, reply to it (A)">Answer</button>`);
    ans.onclick = (e) => {
      e.stopPropagation();
      openAnswers(c.id);
    };
    const actions = el(`<div class="note-actions"></div>`);
    actions.append(ans);
    card.append(actions);
  }
  if (c.draft) {
    const del = el(`<button class="na" title="Delete this draft (x in the recap)">Delete</button>`);
    del.onclick = (e) => {
      e.stopPropagation();
      act("DeleteComment", c.id);
    };
    const actions = el(`<div class="note-actions"></div>`);
    actions.append(del);
    card.append(actions);
  }
  if (opts.jump) card.onclick = opts.jump;
  return card;
}

function questionCard(q, opts = {}) {
  const card = el(`<div class="comment question${opts.static ? " static" : ""}">
    ${opts.where ? `<div class="muted small">${esc(q.file)}:${q.line}</div>` : ""}
    <span class="ck">${esc(q.id)} · you asked</span><span class="badge">${q.answered ? "answered" : state.agentLive ? "waiting for the agent" : "saved; start the agent"}</span>
    <div>${prose(q.text)}</div></div>`);
  if (opts.jump) card.onclick = opts.jump;
  return card;
}

// Selecting lines, commenting and asking

function selectRow(station, part, line, compose) {
  state.sel = { station, part, line };
  $$("#station .row.sel, #station .kcur").forEach((r) => r.classList.remove("sel", "kcur"));
  setTimeout(renderStatus);
  if (compose) return openComposer(station, part, line);
  document.querySelector(`#station .row[data-part="${part}"][data-line="${line}"]`)?.classList.add("sel");
}


function needLine(what) {
  toast(walking() ? `Go to a change first (space or b), then click its line to ${what}.` : `Click a code line first to ${what}.`);
}

function openComposer(station, part, line, opts = {}) {
  const keep = state.compose && state.compose.text ? state.compose : null;
  if (keep && !(keep.station === station && keep.part === part && keep.line === line)) {
    toast("Finish or cancel what you are writing first.");
    return;
  }
  state.sel = { station, part, line };
  const mode = opts.mode ?? (opts.answers ? "answer" : "comment");
  state.compose = keep ?? { station, part, line, answers: opts.answers ?? "", mode, label: "", blocking: false, text: "", picking: !!opts.picking && mode === "comment" && !opts.answers };
  if (!keep) Object.assign(state.compose, { mode, answers: opts.answers ?? "" });
  renderKeepScroll();
  const ta = document.querySelector("#station .composer textarea");
  if (state.compose.picking) document.querySelector("#station .composer")?.focus();
  else ta?.focus();
  ta?.closest(".composer")?.scrollIntoView({ block: "nearest", behavior: "smooth" });
}

function composer(s, p) {
  const cp = state.compose;
  const labels = reader().labels ?? [];
  const note = cp.answers ? s.notes.find((n) => n.key === cp.answers) : null;
  const ask = cp.mode === "ask";
  const head = ask
    ? `Ask the agent about <code>${esc(p.file)}:${cp.line + 1}</code>`
    : note
      ? `Answer to: <span class="muted">${esc(oneLine(note.text).slice(0, 90))}${note.text.length > 90 ? "…" : ""}</span>`
      : `Comment on <code>${esc(p.file)}:${cp.line + 1}</code>`;
  const foot = ask
    ? state.agentLive
      ? "Sent to the agent right away; its answer appears on this line. Enter sends, Shift+Enter breaks the line, Esc cancels."
      : "The agent is not running: the question is saved for it (margin questions). Enter saves, Esc cancels."
    : "Kept as a draft until you send the comments to the agent. Ctrl+Enter saves, Esc cancels.";
  const box = el(`<div class="composer${ask ? " ask" : ""}">
    <div class="cp-head">${head}</div>
    <div class="cp-labels"></div>
    <textarea rows="3"></textarea>
    <div class="cp-foot"><span class="muted small">${foot}</span>
      <span class="spacer"></span><button class="small cp-cancel">Cancel</button><button class="primary small cp-save">${ask ? "Ask" : "Save draft"}</button></div></div>`);
  const ta = box.querySelector("textarea");
  ta.value = cp.text;
  const tip = () => (ask ? "what you want to know about this line" : labels.find((l) => l.name === cp.label)?.tip ?? "your comment");
  ta.placeholder = tip();
  if (cp.picking) {
    box.tabIndex = 0;
    box.querySelector(".cp-head").insertAdjacentHTML("beforeend", `<div class="pick-hint">comment kind: ${labels.map((l) => `<kbd>${esc(l.key)}</kbd> ${esc(l.name)}`).join(" ")} · <kbd>!</kbd> ${cp.blocking ? "blocking flipped" : "issue blocks"} · <kbd>enter</kbd> none · <kbd>esc</kbd> cancels</div>`);
    box.addEventListener("keydown", (e) => {
      if (e.target !== box) return;
      e.preventDefault();
      e.stopPropagation();
      const l = labels.find((x) => x.key === e.key);
      if (e.key === "Escape") return cancel();
      if (e.key === "!") {
        cp.blocking = !cp.blocking;
        renderKeepScroll();
        return document.querySelector("#station .composer")?.focus();
      }
      if (!l && e.key !== "Enter") return;
      cp.picking = false;
      cp.label = l ? l.name : "";
      if (!l) cp.blocking = false;
      renderKeepScroll();
      document.querySelector("#station .composer textarea")?.focus();
    });
  }
  if (!note && !ask) {
    const row = box.querySelector(".cp-labels");
    const chips = () => {
      row.innerHTML = "";
      for (const l of [...labels, { name: "" }]) {
        const c = el(`<button class="chip${cp.label === l.name ? " on" : ""}${l.name === "issue" ? " chip-issue" : ""}" title="${esc(l.tip ?? "")}">${l.name || "no label"}</button>`);
        c.onclick = () => {
          cp.label = l.name;
          cp.blocking = false;
          chips();
          ta.placeholder = tip();
          ta.focus();
        };
        row.append(c);
      }
      const def = labels.find((l) => l.name === cp.label)?.blocking ?? false;
      const t = el(`<label class="blocking"><input type="checkbox"${def !== cp.blocking ? " checked" : ""}${cp.label ? "" : " disabled"}> blocking</label>`);
      t.querySelector("input").onchange = (e) => (cp.blocking = e.target.checked !== def);
      row.append(t);
    };
    chips();
  }
  ta.oninput = () => (cp.text = ta.value);
  const save = async () => {
    let ok;
    if (ask) {
      ok = await act("Ask", { station: cp.station, part: cp.part, line: cp.line, text: ta.value });
      if (ok) toast(state.agentLive ? "Question sent to the agent." : "Question saved; start the agent to have it answered.");
    } else {
      const def = labels.find((l) => l.name === cp.label)?.blocking ?? false;
      ok = await act("AddComment", { station: cp.station, part: cp.part, line: cp.line, label: cp.label, blocking: cp.label ? def !== cp.blocking : false, text: ta.value, answers: cp.answers || "" });
      const last = reader().comments[reader().comments.length - 1];
      if (ok && last) toast(`${last.id} saved as a draft; S sends every draft to the agent`);
    }
    if (ok) {
      state.compose = null;
      renderKeepScroll();
    }
  };
  const cancel = () => {
    state.compose = null;
    renderKeepScroll();
  };
  ta.onkeydown = (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey || (ask && !e.shiftKey))) {
      e.preventDefault();
      save();
    }
    if (e.key === "Escape") {
      e.preventDefault();
      cancel();
    }
  };
  box.querySelector(".cp-save").onclick = save;
  box.querySelector(".cp-cancel").onclick = cancel;
  return box;
}

function startOnLine(mode) {
  const s = cur();
  if (!state.sel || state.sel.station !== s.id) return needLine(mode === "ask" ? "ask about it" : "comment on it");
  openComposer(state.sel.station, state.sel.part, state.sel.line, { mode, picking: true });
}

export async function sendComments() {
  if (!state.agentLive) return toast("Start the agent first; your drafts stay until then.", true);
  if (!(reader().drafts > 0)) return toast("No draft comments; c writes one on the selected line.");
  if (await act("SendComments")) toast("Comments sent to the agent. It answers each one and resolves it.");
}

// Verdicts

function verdictBar(s, end = false) {
  const curV = reader().verdicts?.[s.id];
  const bar = el(`<div class="verdict-bar${end ? " end" : ""}"><span class="vb-label">${end ? "How does this stop look to you?" : "Your verdict on this stop"}</span></div>`);
  for (const v of verdicts) {
    const b = el(`<button class="vb ${v.cls}${curV === v.id ? " on" : ""}" title="${v.label} (${v.key})"><kbd>${v.key}</kbd>${v.label}</button>`);
    b.onclick = () => setVerdict(v.id);
    bar.append(b);
  }
  return bar;
}

async function setVerdict(id) {
  const s = cur();
  if (!s || s.kind !== "code") return;
  const ok = await act("SetVerdict", s.id, id);
  if (!ok) return;
  if (walking()) {
    state.step = s.steps.length - 1;
    renderStation();
  }
  const v = reader().verdicts[s.id];
  if (v === "changes" && !reader().comments.some((c) => c.station === s.id && !c.resolved)) toast("Needs changes: say what with c on the line.");
  else toast(v ? `${s.id}: ${verdicts.find((x) => x.id === v).label.toLowerCase()}` : "Verdict cleared");
}

// The guided walk

function stepEnd(s, st) {
  const p = partData(s, st.part);
  return st.to === p.end && st.to + 1 === p.len ? st.to + 1 : st.to;
}

function stepShows(s, st, part, line) {
  if (st.kind === "mech") return (st.subs ?? []).some((x) => stepShows(s, x, part, line));
  if (st.part !== part || (st.kind !== "change" && st.kind !== "background")) return false;
  return line >= st.from && line <= stepEnd(s, st);
}

function stepOf(s, part, line) {
  return (s.steps ?? []).findIndex((st) => stepShows(s, st, part, line));
}

function stepNotes(s, st) {
  if (blind(s)) return [];
  if (st.kind === "mech") return (st.subs ?? []).flatMap((x) => stepNotes(s, x));
  if (st.from < 0) return [];
  const end = stepEnd(s, st);
  return s.notes.map((n, i) => [n, i]).filter(([n]) => n.part === st.part && n.line >= st.from && n.line <= end && noteVisible(s, n)).sort(([a], [b]) => a.line - b.line).map(([, i]) => i);
}

function nextStep() {
  const s = cur();
  const steps = s.steps;
  readStep(s, steps[state.step]);
  if (state.step < steps.length - 1) {
    state.step++;
    return enterStep();
  }
  if (!reader().verdicts[s.id] && !state.nudged) {
    state.nudged = true;
    return toast("Give this stop a verdict first: 1 looks good · 2 needs changes · 3 not sure (space again skips)");
  }
  goStation(state.station + 1);
}

function prevStep() {
  if (state.step > 0) {
    state.step--;
    return enterStep();
  }
  if (state.station === 0) return;
  const prev = state.view.stations[state.station - 1];
  goStation(state.station - 1, -1, prev.kind === "code" && guided() ? prev.steps.length - 1 : 0);
}

function readStep(s, st) {
  if (!st || (st.kind !== "change" && st.kind !== "background" && st.kind !== "mech")) return;
  const rd = reader();
  const keys = stepNotes(s, st).map((i) => s.notes[i]).filter((n) => n.kind !== "decide" && !rd.reviewed.includes(n.key)).map((n) => n.key);
  if (keys.length) act("ReadNotes", keys);
}

function enterStep() {
  state.nudged = false;
  state.compose = null;
  renderStation();
  $("#station").scrollTop = 0;
  const s = cur();
  const rows = $$("#station .row[data-line]");
  const first = rows.find((r) => /\b(add|chg|del)\b/.test(r.className) || r.classList.contains("has-note")) ?? rows[0];
  if (first) {
    selectRow(s.id, +first.dataset.part, +first.dataset.line, false);
    first.scrollIntoView({ block: "nearest" });
  }
}

function toggleGuide() {
  const s = cur();
  const full = !reader().full;
  if (!full && s?.kind === "code" && state.sel?.station === s.id) {
    const i = stepOf(s, state.sel.part, state.sel.line);
    state.step = i >= 0 ? i : 0;
  } else {
    state.step = 0;
  }
  reader().full = full;
  act("SetFull", full).then(() => {
    const sel = state.sel;
    if (sel) document.querySelector(`#station .row[data-part="${sel.part}"][data-line="${sel.line}"]`)?.scrollIntoView({ block: "center" });
  });
  toast(full ? "Whole stop · w goes back to the guided walk" : "Guided: one step at a time · space next · b back · w whole stop");
}

function progress(s) {
  const steps = s.steps;
  const st = steps[state.step];
  const label = { brief: "what this stop is", background: "know this first", change: `change ${st.n} of ${st.of}`, mech: "housekeeping", wrap: "your verdict" }[st.kind];
  const [at, of] = codeStops();
  const box = el(`<div class="progress"></div>`);
  box.append(phaseRow(2, `stop ${at} of ${of} · step ${state.step + 1} of ${steps.length} · ${label}`));
  const bar = el(`<div class="stepbar"></div>`);
  steps.forEach((x, i) => {
    const seg = el(`<span class="${i < state.step ? "done" : i === state.step ? "now" : ""}" title="${esc(x.kind === "change" ? `change ${x.n} of ${x.of}` : x.kind)}"></span>`);
    seg.onclick = () => {
      state.step = i;
      enterStep();
    };
    bar.append(seg);
  });
  box.append(bar);
  return box;
}

function renderWalk(box, s) {
  const steps = s.steps;
  state.step = Math.min(Math.max(state.step, 0), steps.length - 1);
  const st = steps[state.step];
  box.append(progress(s));
  const body = el(`<div class="walk"></div>`);
  box.append(body);
  switch (st.kind) {
    case "brief":
      briefStep(body, s, steps);
      break;
    case "background":
      body.append(el(`<h3 class="wh">Know this first</h3>`));
      body.append(el(`<p class="muted">Unchanged code the change relies on. Read it so the change makes sense; there is nothing to judge in it.</p>`));
      blockStep(body, s, st);
      break;
    case "change":
      blockStep(body, s, st);
      break;
    case "mech":
      body.append(el(`<h3 class="wh">Housekeeping</h3>`));
      body.append(el(`<p class="muted">Import lines that follow from the changes you just read. Glance over them; there is nothing to judge unless something looks out of place.</p>`));
      for (const sub of st.subs) body.append(partBox(s, sub.part, sub.from, stepEnd(s, sub), sub.title, { about: false, step: true }));
      body.append(navHint("Space: on to your verdict · c comments on the selected line"));
      break;
    case "wrap":
      wrapStep(body, s);
      break;
  }
  const nav = el(`<div class="walk-nav"><button class="small" ${state.station === 0 && state.step === 0 ? "disabled" : ""}>← Back <kbd>b</kbd></button><span class="spacer"></span>
    <button class="primary small">${state.step < steps.length - 1 ? "Next" : "Next stop"} <kbd>space</kbd></button></div>`);
  const [back, next] = nav.querySelectorAll("button");
  back.onclick = prevStep;
  next.onclick = nextStep;
  box.append(nav);
}

function navHint(text) {
  return el(`<p class="hint-line">${esc(text)}</p>`);
}

function card(rows) {
  const c = el(`<dl class="card"></dl>`);
  for (const [label, html, cls] of rows) c.append(el(`<div class="card-row"><dt>${esc(label)}</dt><dd class="${cls ?? ""}">${html}</dd></div>`));
  return c;
}

function briefStep(body, s, steps) {
  body.append(el(`<h2>${esc(s.title)}</h2>`));
  if (s.lede) body.append(el(`<p class="lede">${prose(s.lede)}</p>`));
  if (s.auto) body.append(el(`<p class="problems">These changes are in no stop of the tour, so nobody has explained them. Read them one by one, or ask the agent to place them in a stop or in skip.</p>`));
  const rows = [];
  if (s.riskWhy) rows.push(["Risk", prose(oneLine((s.risk ? s.risk + ": " : "") + s.riskWhy)), "risk-" + (s.risk || "low")]);
  if (s.orderWhy) rows.push(["Why now", prose(oneLine(s.orderWhy))]);
  const checks = s.checksAll ?? [];
  if (checks.length) rows.push(["Check", prose(checks[0]) + (checks.length > 1 ? `<span class="muted"> (+${checks.length - 1} more at the end)</span>` : ""), "m-chg"]);
  if (s.decisions) rows.push(["Your call", plural(s.decisions, "decision only you can make comes", "decisions only you can make come") + " up on the way", "m-decide"]);
  if (s.cov?.stale) rows.push(["Tests", "coverage is stale: the code changed after it was measured", "m-chg"]);
  else if (s.cov?.measured && s.cov.run + s.cov.missed) rows.push(["Tests", `${s.cov.run} of ${s.cov.run + s.cov.missed} changed lines run in a test`, s.cov.missed ? "m-chg" : "m-add"]);
  else if (s.needsTests) rows.push(["Tests", "the agent named no tests that cover this stop", "m-del"]);
  body.append(card(rows));
  let bg = 0, changes = 0, mech = 0;
  for (const x of steps) {
    if (x.kind === "background") bg++;
    if (x.kind === "change") changes++;
    if (x.kind === "mech") mech = x.subs.length;
  }
  let ahead = plural(changes, "change", "changes");
  if (bg) ahead = plural(bg, "piece of code to know first", "pieces of code to know first") + ", then " + ahead;
  if (mech) ahead += ", then " + plural(mech, "import edit", "import edits") + " at a glance";
  body.append(el(`<p class="muted">Ahead: ${ahead}, ${s.loc} lines. Space starts.</p>`));
  const more = [];
  if (((s.what ?? "") + (s.why ?? "")).trim()) more.push("what and why");
  if (s.flow) more.push("the flow");
  if (s.examples?.length) more.push(plural(s.examples.length, "before/after example", "before/after examples"));
  if (checks.length > 1) more.push("all the checks");
  if (s.scope) more.push("the scope");
  if (!more.length) return;
  const toggleMore = el(`<button class="link-btn">${state.more ? "Hide" : "Show"} ${shortList(more)} <kbd>e</kbd></button>`);
  toggleMore.onclick = () => {
    state.more = !state.more;
    renderKeepScroll();
  };
  body.append(toggleMore);
  if (!state.more) return;
  const m = el(`<div class="more"></div>`);
  for (const [h, t] of [["Scope", s.scope], ["What it does", s.what], ["Why it changed", s.why]]) if (t?.trim()) m.append(el(`<div><h4>${h}</h4><div class="prose">${prose(t)}</div></div>`));
  if (s.flow) m.append(el(`<div><h4>Flow</h4><pre class="flow">${esc(s.flow)}</pre></div>`));
  if (s.examples?.length) m.append(examplesTable(s));
  if (checks.length > 1) m.append(el(`<div><h4>All the checks</h4><ul>${checks.map((c) => `<li>${prose(c)}</li>`).join("")}</ul></div>`));
  body.append(m);
}

function examplesTable(s) {
  return el(`<table class="examples"><tr><th>input</th><th>before</th><th>after</th><th></th></tr>
    ${s.examples.map((e) => `<tr><td>${esc(e.Input)}</td><td>${esc(e.Before)}</td><td>${esc(e.After)}</td><td class="muted">${prose(e.Note)}</td></tr>`).join("")}</table>`);
}

function blockStep(body, s, st) {
  const p = partData(s, st.part);
  const notes = stepNotes(s, st);
  const shown = new Set();
  const showAbout = st.kind === "background" || st.from <= s.parts[st.part].start;
  if (st.from < 0) {
    body.append(partBox(s, st.part, 0, -1, null));
    return;
  }
  body.append(partBox(s, st.part, st.from, stepEnd(s, st), st.title, { about: showAbout, step: true, shown }));
  if (blind(s)) {
    const b = el(`<p class="m-chg">Your pass first: the agent's notes stay hidden on this high-risk stop. Decide what you think, then reveal them to compare. <button class="small">Reveal notes (v)</button></p>`);
    b.querySelector("button").onclick = reveal;
    body.append(b);
  } else if (!notes.length && !s.auto && st.kind === "change") {
    body.append(el(`<p class="muted">The agent left no note on this change, so read it on your own.</p>`));
  }
  const left = notes.filter((i) => !shown.has(i));
  if (left.length && state.showNotes) {
    const l = el(`<div class="loose"></div>`);
    left.forEach((i) => l.append(noteCard(s, s.notes[i], i, { static: true })));
    body.append(l);
  }
  settle(body, s, notes, "Settle here");
  callers(body, s, st);
  if (st.kind === "background") return body.append(navHint("Space: on to the change."));
  body.append(el(`<p class="step-q">▸ ${prose(stepQuestion(s, st, notes))}</p>`));
  body.append(navHint("Space: yes, go on · c comment on the selected line · a ask the agent · b back"));
}

function stepQuestion(s, st, notes) {
  const c = s.parts[st.part]?.check?.trim();
  if (c && !blind(s)) return oneLine(c);
  const kinds = new Set(notes.map((i) => s.notes[i].kind));
  if (kinds.has("issue")) return "The agent sees a problem here. Do you agree? ? agrees and flags it, x dismisses it.";
  if (kinds.has("decide")) return "There is a call to make here. Decide, then tick it.";
  if (kinds.has("question")) return "The agent asks you something here. Answer it below, or dismiss it.";
  if (notes.length) return "Does the code do what the notes say, and is that the right thing to do?";
  return "Can you say what each changed line does, and is it right?";
}

// settle lists what notes ask of the reader, right where they read them: calls to make, problems to
// agree with or dismiss, and questions to answer.
function settle(body, s, notes, heading) {
  const rd = reader();
  const items = notes.map((i) => [s.notes[i], i]).filter(([n]) => !rd.dismissed.includes(n.key) && asks(n));
  if (!items.length) return;
  const box = el(`<div class="settle"><h4>${esc(heading)}</h4></div>`);
  for (const [n, i] of items) box.append(settleRow(s, n, i));
  body.append(box);
}

function settleRow(s, n, i, where = "") {
  const rd = reader();
  const row = el(`<div class="settle-row navable ${n.unbacked ? "question" : n.kind}"></div>`);
  const text = esc(oneLine(n.text).slice(0, 160)) + (n.text.length > 160 ? "…" : "") + (where ? `<span class="muted">${esc(where)}</span>` : "");
  const btn = (label, fn, on = false) => {
    const b = el(`<button class="na${on ? " on" : ""}">${label}</button>`);
    b.onclick = (e) => {
      e.stopPropagation();
      fn();
    };
    return b;
  };
  if (n.kind === "decide") {
    const done = rd.reviewed.includes(n.key);
    row.append(el(`<span class="sr-mark">${done ? "☑" : "☐"} ◆</span>`), el(`<span class="sr-text">your call: ${text}</span>`), btn(done ? "Decided" : "Mark decided", () => act("MarkNote", n.key, "reviewed"), done));
  } else if (n.kind === "question" || n.unbacked) {
    const answered = rd.comments.some((c) => c.answers === n.key);
    row.append(el(`<span class="sr-mark">${answered ? "✓" : "?"}</span>`), el(`<span class="sr-text">${text}</span>`));
    if (n.part >= 0) row.append(btn(answered ? "Answered" : "Answer", () => {
      if (walking()) {
        const si = stepOf(s, n.part, n.line);
        if (si >= 0) state.step = si;
      }
      openComposer(s.id, n.part, n.line, { answers: n.key });
    }, answered));
    row.append(btn("Dismiss", () => act("MarkNote", n.key, "dismissed")));
  } else {
    const flagged = rd.flagged.includes(n.key);
    row.append(el(`<span class="sr-mark">${flagged ? "⚑" : "!"}</span>`), el(`<span class="sr-text">${text}</span>`), btn(flagged ? "Agreed" : "Agree", () => act("MarkNote", n.key, "flagged"), flagged), btn("Dismiss", () => act("MarkNote", n.key, "dismissed")));
  }
  row.onclick = () => {
    if (walking() && n.part >= 0) {
      const si = stepOf(s, n.part, n.line);
      if (si >= 0 && si !== state.step) {
        state.step = si;
        renderStation();
      }
    }
    focusNote(i);
  };
  return row;
}

// The end of a stop: what is still open, the checks, your comments and your verdict.
function wrapStep(body, s) {
  const rd = reader();
  body.append(el(`<h2>End of stop: ${esc(s.title)}</h2>`));
  if (blind(s)) {
    const b = el(`<p class="m-chg">The agent's notes are still hidden. Reveal them to compare with what you found, before you decide. <button class="small">Reveal notes (v)</button></p>`);
    b.querySelector("button").onclick = reveal;
    body.append(b);
  }
  body.append(el(`<div class="checks"><h4>You should be able to answer</h4><ul>${(s.checksAll ?? []).map((c) => `<li>${prose(c)}</li>`).join("")}</ul></div>`));
  const open = s.notes.map((n, i) => [n, i]).filter(([n]) => noteVisible(s, n) && !rd.dismissed.includes(n.key) && (
    (n.kind === "decide" && !rd.reviewed.includes(n.key)) ||
    (n.kind === "issue" && !n.unbacked && !rd.flagged.includes(n.key)) ||
    (n.kind === "question" && !n.q && !rd.comments.some((c) => c.answers === n.key))));
  if (open.length) {
    const box = el(`<div class="settle"><h4>Still open</h4></div>`);
    for (const [n, i] of open) box.append(settleRow(s, n, i, n.part >= 0 ? ` (${base(s.parts[n.part].file)}:${n.line + 1})` : ""));
    body.append(box);
  }
  const mine = rd.comments.filter((c) => c.station === s.id);
  if (mine.length) {
    const box = el(`<div class="settle"><h4>Your comments here</h4></div>`);
    mine.forEach((c) => box.append(commentCard(c, { static: true, where: true })));
    body.append(box);
  }
  body.append(verdictBar(s, true));
  const v = rd.verdicts[s.id];
  const next = {
    good: "Space: on to the next stop.",
    changes: "Say what needs to change: b goes back to the code, c comments on a line. Send your comments to the agent (S). Then space moves on.",
    unsure: "Not sure is a fine answer. b goes back and a asks the agent about the line that bothers you; w shows the whole stop at once. Then space moves on.",
  }[v] ?? "Press 1, 2 or 3.";
  body.append(navHint(next));
}

// Callers and history around a stop

async function lookAround(s) {
  if (state.around[s.id]) return;
  state.around[s.id] = { loading: true };
  try {
    state.around[s.id] = await api("Reviews.Around", s.id);
  } catch (e) {
    state.around[s.id] = { err: errText(e), symbols: [], history: [] };
  }
  if (cur()?.id === s.id) renderKeepScroll();
}

const maxCallers = 5;

function callers(body, s, st) {
  const a = state.around[s.id];
  if (!a || a.loading || st.from < 0) return;
  const p = s.parts[st.part];
  for (const sym of a.symbols ?? []) {
    if (sym.file !== p.file || sym.line - 1 < st.from || sym.line - 1 > st.to || sym.tooMany > 0) continue;
    if (!sym.uses.length || sym.uses.length > maxCallers) continue;
    const verb = (sym.type ? "use" : "call") + (sym.uses.length === 1 ? "s" : "");
    const box = el(`<div class="callers"><div class="m-chg">Outside this review, ${plural(sym.uses.length, "place", "places")} ${verb} <code>${esc(sym.name)}</code>. Do they still work? Click to peek:</div></div>`);
    for (const u of sym.uses) box.append(peekLink(u));
    body.append(box);
  }
}

function peekLink(u) {
  const l = el(`<div class="peek-link navable"><code>${esc(u.File)}:${u.Line}</code> <span class="muted">${esc(u.Text.trim())}</span></div>`);
  l.onclick = () => openPeek(u.File, u.Line - 1);
  return l;
}

function aroundSection(box, s) {
  const a = state.around[s.id];
  if (!a) return;
  if (a.loading) return box.append(el(`<p class="muted">Around this stop: looking for callers and history…</p>`));
  if (!a.symbols?.length && !a.history?.length && !a.err) return;
  const sec = el(`<div class="around"><h3>Around this stop</h3></div>`);
  if (a.err) sec.append(el(`<p class="problems">${esc(a.err)}</p>`));
  for (const sym of a.symbols ?? []) {
    const head = `${sym.type ? "type" : "func"} <code>${esc(sym.name)}</code>, ${sym.removed ? "removed" : "changed"} (${esc(base(sym.file))}:${sym.line})`;
    if (sym.tooMany > 0) {
      sec.append(el(`<div class="muted">${head}: used in ${sym.tooMany}+ places, too common a name to list</div>`));
      continue;
    }
    if (!sym.uses.length) {
      sec.append(el(`<div class="${sym.removed ? "m-add" : "muted"}">${head}: ${sym.removed ? "nothing else uses it" : "no other users outside the tour"}</div>`));
      continue;
    }
    sec.append(el(`<div class="${sym.removed ? "m-del" : "m-chg"}">${head}: ${sym.uses.length} outside the tour${sym.inside ? `, ${sym.inside} inside` : ""}</div>`));
    sym.uses.slice(0, 8).forEach((u) => sec.append(peekLink(u)));
    if (sym.uses.length > 8) sec.append(el(`<div class="muted">… ${sym.uses.length - 8} more</div>`));
  }
  if (a.history?.length) {
    sec.append(el(`<div class="muted" style="margin-top:8px">The replaced code was written in · click reads the commit</div>`));
    for (const c of a.history) {
      const l = el(`<div class="peek-link navable"><code>${esc(c.SHA)}</code> <span class="muted">${esc(c.Date)} ${esc(c.Author)}</span> ${esc(c.Subject)}</div>`);
      l.onclick = () => openCommit(c.SHA);
      sec.append(l);
    }
  }
  box.append(sec);
}

// The whole stop at once

function renderWhole(box, s) {
  const rd = reader();
  box.append(el(`<h2>${esc(s.title)}</h2>`));
  box.append(el(`<p class="muted small">${[s.concern, s.loc + " lines"].filter(Boolean).map(esc).join(" · ")}</p>`));
  box.append(verdictBar(s));
  if (s.lede) box.append(el(`<p class="lede">${prose(s.lede)}</p>`));
  if (s.auto) box.append(el(`<p class="problems">These changes are in no stop of the tour, so nobody has explained them. Read them one by one, or ask the agent to place them in a stop or in skip.</p>`));
  const rat = [["scope", s.scope && prose(s.scope)], ["what", s.what && prose(s.what)], ["why", s.why && prose(s.why)],
    ["risk", s.risk ? `<span class="risk-${esc(s.risk)}">${esc(s.risk)}</span>${s.riskWhy ? " — " + prose(s.riskWhy) : ""}` : ""],
    ["concern", s.concern ? esc(s.concern) : ""], ["order", s.orderWhy ? prose(s.orderWhy) : ""],
    ["tests", s.testNames?.length ? esc(s.testNames.join(", ")) : s.needsTests ? `<span class="m-del">none named</span>` : ""]];
  const dl = rat.filter(([, v]) => v).map(([k, v]) => `<dt>${k}</dt><dd>${v}</dd>`);
  if (dl.length) box.append(el(`<dl class="rationale">${dl.join("")}</dl>`));
  if (!s.scope && !s.what && !s.why && !s.auto) box.append(el(`<p class="muted">No rationale yet: the agent has not said why this is a stop of its own, what it does, or why.</p>`));
  if (s.flow) box.append(el(`<pre class="flow">${esc(s.flow)}</pre>`));
  if (s.checksAll?.length) box.append(el(`<div class="checks"><h4>After this stop you should be able to answer</h4><ol>${s.checksAll.map((c) => `<li>${prose(c)}</li>`).join("")}</ol></div>`));
  if (s.examples?.length) box.append(examplesTable(s));
  const calls = s.notes.map((n, i) => [n, i]).filter(([n]) => n.kind === "decide" && noteVisible(s, n));
  if (calls.length) {
    const c = el(`<div class="settle"><h4>Your calls in this stop</h4></div>`);
    for (const [n, i] of calls) c.append(settleRow(s, n, i, n.part >= 0 ? ` (${base(s.parts[n.part].file)}:${n.line + 1})` : ""));
    box.append(c);
  }
  aroundSection(box, s);
  if (blind(s)) {
    const b = el(`<p class="m-chg">Your pass first: the agent's notes stay hidden on this high-risk stop. Decide what you think, then reveal them to compare. <button class="small">Reveal notes (v)</button></p>`);
    b.querySelector("button").onclick = reveal;
    box.append(b);
  }
  const shown = new Set();
  s.parts.forEach((p, pi) => {
    const d = partData(s, pi);
    box.append(partBox(s, pi, d.start, d.end, null, { shown }));
  });
  const loose = s.notes.map((n, i) => [n, i]).filter(([n, i]) => !shown.has(i) && noteVisible(s, n) && n.part < 0);
  if (loose.length && state.showNotes) {
    const l = el(`<div class="loose"><h4>Notes without an anchor</h4></div>`);
    loose.forEach(([n, i]) => l.append(noteCard(s, n, i, { static: true })));
    box.append(l);
  }
  if (!s.notes.length) box.append(el(`<p class="muted small">No notes on this stop yet. ${state.agentLive ? "The agent is working on it." : "Start the agent to have it annotate the change."}</p>`));
  box.append(verdictBar(s, true));
  if (rd.verdicts[s.id] === "changes" && !rd.comments.some((c) => c.station === s.id && !c.resolved)) box.append(navHint("Needs changes: say what with c on the line."));
}

function noGap(g) {
  const t = (g ?? "").trim().replace(/^[.!]+|[.!]+$/g, "").toLowerCase();
  return t === "none" || t === "no gap" || t === "nothing";
}

function stopMeta(s) {
  const rd = reader();
  const meta = [];
  if (s.risk) meta.push(s.risk + " risk");
  if (s.concern) meta.push(s.concern);
  meta.push(`${s.loc} lines`);
  if (s.issues) meta.push(`! ${s.issues}`);
  if (s.questions) meta.push(`? ${s.questions}`);
  if (s.decisions) meta.push(`◆ ${s.decisions}`);
  meta.push(`✓ ${s.notes.filter((n) => rd.reviewed.includes(n.key)).length}/${s.notes.length}`);
  if (s.testNames?.length) meta.push("tests: " + s.testNames.join(", "));
  else if (s.needsTests) meta.push("no tests");
  return meta.join(" · ");
}

function covLine(s) {
  const c = s.cov;
  if (!c) return "";
  if (c.stale) return `<span class="m-chg">coverage stale: the code changed after it was measured</span>`;
  if (!c.measured && c.current) return `<span class="muted">coverage: these files were not measured</span>`;
  const total = c.run + c.missed;
  if (!c.measured || !total) return "";
  const w = 12;
  let filled = Math.round((c.run * w) / total);
  if (c.run) filled = Math.max(filled, 1);
  if (c.missed) filled = Math.min(filled, w - 1);
  const cls = !c.missed ? "m-add" : !c.run ? "m-del" : "m-chg";
  return `<span class="${cls}">tested ${"█".repeat(filled)}${"░".repeat(w - filled)} ${c.run}/${total} changed lines${c.missed ? ` · ${c.missed} untested` : ""}</span>`;
}

// Orient: what the change is for, the route, and one question before any code

function renderOverview(box) {
  const v = state.view;
  const rd = reader();
  const ov = el(`<div class="ov"></div>`);
  const [, stops] = codeStops();
  ov.append(phaseRow(1, `then ${plural(stops, "stop", "stops")}, then your decision`));
  ov.append(el(`<h2>${esc(v.title)}</h2>`));
  ov.append(el(`<p class="muted small">${esc(v.kicker || "")}</p>`));
  ov.append(el(`<p class="muted small">${v.hasBase ? `base: ${esc(v.baseDesc)} · showing: ${esc(v.headDesc || "working tree")}` : "no base: plain code, no diff marks"}</p>`));
  if (v.problems?.length) ov.append(el(`<div class="problems-box"><h3 class="m-del">Problems with the review file (${v.problems.length})</h3><div class="problems">${v.problems.map(esc).join("<br>")}</div></div>`));
  const cap = state.more ? "" : " capped";
  const rows = [];
  const forText = v.asked?.trim() || v.motivation?.trim();
  rows.push(["For", forText ? `<div class="clamp${cap}">${prose(forText)}</div>${v.askedFrom ? `<span class="muted small">from ${esc(v.askedFrom)}</span>` : ""}` : `<span class="muted">not recorded; margin --intent "…" says it next time</span>`]);
  const does = v.did || v.summary;
  rows.push(["Does", does ? `<div class="clamp${cap}">${prose(does)}</div>` : `<span class="muted">the agent is still reading the change; this fills in by itself</span>`]);
  if (v.gap) rows.push(["Gap", noGap(v.gap) ? `<span class="m-add">none: it does what was asked</span>` : `<div class="clamp${cap}">${prose(v.gap)}</div>`, "m-chg"]);
  ov.append(card(rows));
  if (!v.did && !v.gap) ov.append(el(`<p class="muted small">The agent has not compared the change with what was asked yet.</p>`));
  renderOrient(ov);
  const route = el(`<div><h3>The route · click opens a stop</h3><div class="route"></div></div>`);
  let total = 0, read = 0, n = 0;
  v.stations.forEach((s, i) => {
    if (s.kind !== "code") return;
    n++;
    total += s.loc;
    const vd = verdicts.find((x) => x.id === rd.verdicts[s.id]);
    if (vd) read += s.loc;
    const dot = vd ? `<span class="vdot ${vd.cls}"></span>` : rd.visited.includes(s.id) ? `<span class="vdot half"></span>` : `<span class="vdot"></span>`;
    const r = el(`<div class="route-row navable${s.auto ? " auto" : ""}"><span class="rn">${n}</span>${dot}<span class="risk-${esc(s.risk || "low")} rr">${esc(s.risk || "–")}</span>
      <span class="rl">${s.loc} lines</span><span class="rt">${esc(s.title)}
      ${s.riskWhy ? `<div class="small risk-${esc(s.risk || "low")}">because ${esc(oneLine(s.riskWhy))}</div>` : ""}
      ${s.orderWhy ? `<div class="muted small">comes now: ${esc(oneLine(s.orderWhy))}</div>` : ""}
      <div class="muted small mono">${esc(stopMeta(s))}</div>${covLine(s) ? `<div class="small mono">${covLine(s)}</div>` : ""}</span></div>`);
    r.onclick = () => goStation(i);
    route.lastElementChild.append(r);
  });
  ov.append(route);
  const left = total - read;
  const mins = Math.max(Math.round((left * 60) / 400), 1);
  ov.append(el(`<p class="muted">${total} lines to read, ${read} read so far. At about 400 lines an hour that is ${mins >= 90 ? (mins / 60).toFixed(1) + " hours" : mins + " minutes"}; reviews catch less after 60 to 90 minutes, so take breaks.${left > 400 ? " More than one sitting: stop after about 400 lines; margin picks up where you left." : ""}</p>`));
  if (!v.coverage) ov.append(el(`<p class="muted small">Test coverage is not measured yet; the agent can add it with margin coverage.</p>`));
  if (v.instructions || v.guidance) {
    const g = el(`<div><h3>The agent was asked to look at</h3></div>`);
    if (v.instructions) g.append(el(`<div class="prose">you: ${prose(v.instructions)}</div>`));
    if (v.guidance) g.append(el(`<div class="muted small">the repository's .margin/instructions, from the base side:</div>`), el(`<pre class="flow">${esc(v.guidance)}</pre>`));
    ov.append(g);
  }
  const focus = [];
  v.stations.forEach((s, si) => s.notes.forEach((nn, ni) => nn.focus && !rd.dismissed.includes(nn.key) && focus.push([nn, s, si, ni])));
  if (focus.length) {
    const f = el(`<div><h3>Focus areas · F filters by them</h3></div>`);
    focus.sort(([a], [b]) => a.focus.localeCompare(b.focus));
    for (const [nn, s, si, ni] of focus) {
      const where = nn.part >= 0 ? `${s.id} · ${base(s.parts[nn.part].file)}:${nn.line + 1}` : s.id;
      const l = el(`<div class="peek-link navable"><span class="k-${esc(noteStyle(nn)[2])}">${esc(nn.focus)}</span> · <span class="muted">${esc(where)}</span> — ${esc(oneLine(nn.text).slice(0, 140))}</div>`);
      l.onclick = () => goStation(si, ni);
      f.append(l);
    }
    ov.append(f);
  }
  const more = el(`<button class="link-btn">${state.more ? "Hide" : "Show"} everything the agent wrote <kbd>e</kbd></button>`);
  more.onclick = () => {
    state.more = !state.more;
    renderKeepScroll();
  };
  ov.append(more);
  if (state.more) {
    const sec = (h, body, pre = false) => body?.trim() && ov.append(el(`<div><h3>${h}</h3>${pre ? `<pre class="flow">${esc(body)}</pre>` : `<div class="prose">${prose(body)}</div>`}</div>`));
    sec("Summary", v.summary);
    sec("Motivation", v.motivation);
    sec("Outcome", v.outcome);
    if (v.complexity) ov.append(el(`<p class="muted small">complexity: ${esc(v.complexity)}</p>`));
    sec("Flow", v.flow, true);
  }
  const list = (h, items, cls = "") => items?.length && ov.append(el(`<div><h3>${h}</h3><div class="small ${cls}">${items.map(esc).join("<br>")}</div></div>`));
  list("Renames", v.renames);
  list("Safe to skip", v.skip);
  list(`Left out by .margin/ignore (${v.ignored?.length ?? 0})`, v.ignored, "muted mono");
  list(`Changed, but not shown as text (${v.unshown?.length ?? 0})`, v.unshown, "muted");
  box.append(ov);
}

function renderOrient(ov) {
  const rd = reader();
  const box = el(`<div class="orient"><h3>Before any code</h3><p>Does this change make sense, done this way?</p><div class="or-btns"></div></div>`);
  const answers = [["yes", "y", "Yes, walk it", "v-good"], ["no", "n", "No — say why; that is the review, and it blocks the change", "v-changes"], ["unsure", "?", "Not sure yet, walk it anyway", "v-unsure"]];
  for (const [id, key, label, cls] of answers) {
    const b = el(`<button class="vb ${cls}${rd.orient === id ? " on" : ""}"><kbd>${key}</kbd>${label}</button>`);
    b.onclick = () => orient(id);
    box.querySelector(".or-btns").append(b);
  }
  const design = el(`<div class="design${rd.orient === "no" || state.designing ? "" : " hidden"}">
    <textarea rows="3" placeholder="What is wrong with the approach, for the author. Enter saves, Esc cancels."></textarea>
    <div class="cp-foot"><span class="spacer"></span><button class="primary small">Save</button></div></div>`);
  const ta = design.querySelector("textarea");
  ta.value = rd.design ?? "";
  const save = async () => {
    state.designing = false;
    if (await act("SetOrient", "no", ta.value)) {
      toast("Saved: this blocks the change. You can still walk the stops with }.");
      const i = state.view.stations.findIndex((s) => s.kind === "recap");
      if (i >= 0) goStation(i);
    }
  };
  ta.onkeydown = (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      save();
    }
    if (e.key === "Escape") {
      state.designing = false;
      renderKeepScroll();
    }
  };
  design.querySelector("button").onclick = save;
  box.append(design);
  if (rd.orient === "no" && rd.design) box.append(el(`<p class="m-del small">you wrote: ${prose(rd.design)}</p>`));
  if (rd.orient && rd.orient !== "no") box.append(el(`<p class="muted small">Space walks the first stop.</p>`));
  ov.append(box);
}

async function orient(id) {
  if (id === "no") {
    state.designing = true;
    renderKeepScroll();
    document.querySelector(".design textarea")?.focus();
    return;
  }
  if (await act("SetOrient", id, "")) goStation(state.station + 1);
}

// Decide: the checklist before approving, and the recommendation

function renderRecap(box) {
  const ov = el(`<div class="ov"></div>`);
  if (guided()) ov.append(phaseRow(3, ""));
  ov.append(el(`<h2>Before you approve</h2>`));
  ov.append(el(`<p class="lede">The checklist before you approve: the calls only you can make, every problem and open question, and your own comments. Click (or enter) jumps to the line. A problem without evidence is listed as a question until the agent backs it up.</p>`));
  const rd = reader();
  const o = rd.outcome ?? {};
  const dcls = { approve: "v-good", "request changes": "v-changes", "not finished": "v-unsure" }[o.decision] ?? "";
  const title = { approve: "Recommendation: approve", "request changes": "Recommendation: request changes" }[o.decision] ?? "Not finished yet";
  const rec = el(`<div class="recommend ${dcls}"><div class="rec-head"><strong>${title}</strong></div></div>`);
  const list = (h, items) => items?.length && rec.append(el(`<div><div class="rec-sub">${h}</div><ul>${items.map((x) => `<li>${esc(x)}</li>`).join("")}</ul></div>`));
  list("Blocks it", o.blockers);
  list("Still open", o.left);
  list("Goes with it", o.notes);
  ov.append(rec);
  const exp = el(`<p><button class="small">Export for posting…</button> <span class="muted small">your comments, flagged problems and calls, as Markdown or GitHub review text. margin never posts.</span></p>`);
  exp.querySelector("button").onclick = openExport;
  ov.append(exp);
  const where = (s, n) => (n.part < 0 ? s.id : `${s.id} · ${base(s.parts[n.part].file)}:${n.line + 1}`);
  const link = (cls, html, si, ni) => {
    const l = el(`<div class="peek-link navable ${cls}">${html}</div>`);
    l.onclick = () => goStation(si, ni);
    return l;
  };
  const all = [];
  state.view.stations.forEach((s, si) => s.notes.forEach((n, ni) => !rd.dismissed.includes(n.key) && all.push([s, si, n, ni])));
  const calls = all.filter(([, , n]) => n.kind === "decide");
  if (calls.length) {
    const c = el(`<div><h3>Your calls · tick one off at the end of its stop</h3></div>`);
    for (const [s, si, n, ni] of calls) c.append(link("k-decide", `${rd.reviewed.includes(n.key) ? "☑" : "☐"} ${esc(where(s, n))} — ${esc(oneLine(n.text).slice(0, 160))}`, si, ni));
    ov.append(c);
  }
  const groups = [
    ["Problems", "k-issue", (n) => n.kind === "issue" && !n.unbacked],
    ["Problems without evidence", "k-question", (n) => n.unbacked],
    ["Questions", "k-question", (n) => n.kind === "question"],
  ];
  let any = calls.length > 0;
  for (const [h, cls, test] of groups) {
    const items = all.filter(([, , n]) => test(n));
    if (!items.length) continue;
    any = true;
    const g = el(`<div><h3>${h}</h3></div>`);
    for (const [s, si, n, ni] of items) g.append(link(cls, `${rd.reviewed.includes(n.key) || rd.flagged.includes(n.key) ? "✓ " : ""}${esc(where(s, n))} — ${esc(oneLine(n.text).slice(0, 160))}`, si, ni));
    ov.append(g);
  }
  if (!any) ov.append(el(`<p class="muted">Nothing open: every problem and question was dismissed.</p>`));
  const jumpTo = (station, file, line) => () => {
    const si = state.view.stations.findIndex((s) => s.id === station);
    if (si < 0) return;
    const s = state.view.stations[si];
    const pi = s.parts.findIndex((p) => p.file === file && line - 1 >= p.start && line - 1 <= p.end);
    goStation(si, -1, pi >= 0 && guided() ? Math.max(stepOf(s, pi, line - 1), 0) : 0);
    if (pi >= 0) {
      selectRow(s.id, pi, line - 1, false);
      document.querySelector(`#station .row[data-part="${pi}"][data-line="${line - 1}"]`)?.scrollIntoView({ block: "center" });
    }
  };
  if (rd.comments.length) {
    const c = el(`<div><h3>Your comments · S sends the drafts, x deletes one</h3></div>`);
    if (rd.drafts) {
      const b = el(`<button class="primary small" style="margin-bottom:8px">Send ${rd.drafts} draft${rd.drafts > 1 ? "s" : ""} to the agent</button>`);
      b.onclick = sendComments;
      c.append(b);
    }
    rd.comments.forEach((q) => {
      const card = commentCard(q, { static: true, where: true, jump: jumpTo(q.station, q.file, q.line) });
      card.classList.add("navable");
      card.dataset.comment = q.id;
      c.append(card);
    });
    ov.append(c);
  }
  if (rd.questions.length) {
    const c = el(`<div><h3>Questions you asked</h3></div>`);
    rd.questions.forEach((q) => c.append(questionCard(q, { static: true, where: true, jump: jumpTo(q.station, q.file, q.line) })));
    ov.append(c);
  }
  const stops = state.view.stations.map((s, i) => [s, i]).filter(([s]) => s.kind === "code");
  if (stops.length) {
    const t = el(`<div><h3>Stops</h3><div class="vlist"></div></div>`);
    for (const [s, i] of stops) {
      const vd = verdicts.find((x) => x.id === rd.verdicts?.[s.id]);
      const r = el(`<div class="vrow navable"><span class="vdot ${vd?.cls ?? ""}"></span><a href="#">${esc(s.id)} · ${vd ? vd.label.toLowerCase() : "no verdict yet"} — ${esc(s.title)}</a></div>`);
      r.onclick = (e) => {
        e.preventDefault();
        goStation(i);
      };
      t.lastElementChild.append(r);
    }
    ov.append(t);
  }
  box.append(ov);
}

// Tests and coverage

function renderTests(box) {
  const s = cur();
  const v = state.view;
  const ov = el(`<div class="ov"><h2>Tests</h2></div>`);
  for (const g of s.tests ?? []) {
    const sec = el(`<div class="test-group"><h4><code>${esc(g.file)}</code>${g.newFile ? ' <span class="badge">new file</span>' : ""}</h4>${g.why ? `<p class="muted">${prose(g.why)}</p>` : ""}</div>`);
    if (g.err) sec.append(el(`<p class="problems">${esc(g.err)}</p>`));
    const ul = el(`<div class="test-names"></div>`);
    for (const t of g.names) ul.append(el(`<div style="padding-left:${t.depth * 18}px">${esc(t.name)}${t.new ? ' <span class="badge fresh">new</span>' : ""}</div>`));
    sec.append(ul);
    ov.append(sec);
  }
  const c = v.coverage;
  if (!c) {
    ov.append(el(`<p class="muted">No coverage yet: ask the agent to run margin coverage.</p>`));
    return box.append(ov);
  }
  const cols = v.stations.map((x, i) => [x, i]).filter(([x]) => x.kind === "code" && (x.cov?.measured || x.cov?.stale));
  ov.append(el(`<h3>What each test runs · click a test to spotlight it</h3>`));
  ov.append(el(`<p class="muted small">● runs every changed line of the stop, ◐ some, · none. Running a line is not checking it: a test can run a line and assert nothing about the result.</p>`));
  if (!cols.length) {
    ov.append(el(`<p class="muted">None of the stops' files were measured.</p>`));
    return box.append(ov);
  }
  const table = el(`<table class="grid"><tr><th></th>${cols.map(([x]) => `<th title="${esc(x.title)}">${esc(x.id)}</th>`).join("")}</tr></table>`);
  const groups = new Set(c.tests.map((t) => t.group));
  c.tests.forEach((t, ti) => {
    if (groups.size > 1 && t.group && (ti === 0 || c.tests[ti - 1].group !== t.group)) table.append(el(`<tr class="grp"><td colspan="${cols.length + 1}">${esc(t.group)}</td></tr>`));
    const cells = cols.map(([x, si]) => {
      if (x.cov?.stale) return `<td class="m-chg">?</td>`;
      const [run, total] = c.grid[ti][si];
      if (t.synthetic) return `<td></td>`;
      const mark = !total ? "" : run >= total ? "●" : run > 0 ? "◐" : "·";
      return `<td class="${run >= total && total ? "m-add" : run ? "m-chg" : "muted"}">${mark}</td>`;
    });
    const tr = el(`<tr class="${state.spot === ti ? "on " : ""}${t.synthetic ? "synthetic" : "navable"}"><td class="tname" style="padding-left:${8 + t.depth * 16}px">${esc(t.display || t.label)}${t.new ? ' <span class="badge fresh">new</span>' : ""}</td>${cells.join("")}</tr>`);
    if (!t.synthetic) tr.onclick = () => spotlight(ti);
    table.append(tr);
  });
  table.append(el(`<tr class="foot"><td class="tname">changed lines no test runs</td>${cols.map(([x]) => (x.cov?.stale ? `<td class="m-chg">stale</td>` : `<td class="${x.cov?.missed ? "m-del" : "m-add"}">${x.cov?.missed ?? 0}</td>`)).join("")}</tr>`));
  ov.append(table);
  if (cols.some(([x]) => x.cov?.stale) || c.stale?.length) ov.append(el(`<p class="m-chg small">? and stale: the code changed after coverage was measured; ask the agent to measure it again.${c.stale?.length ? " Stale files: " + esc(c.stale.join(", ")) : ""}</p>`));
  box.append(ov);
}

async function spotlight(ti) {
  try {
    state.spotLines = await api("Reviews.RunsLines", ti);
  } catch (e) {
    return toast(errText(e), true);
  }
  state.spot = ti;
  const hit = findHit((s, pi, r) => (state.spotLines[partKey(s, pi)] ?? []).includes(r.n - 1) && (r.k === "add" || r.k === "chg")) ??
    findHit((s, pi, r) => (state.spotLines[partKey(s, pi)] ?? []).includes(r.n - 1));
  if (!hit) {
    renderToolbar();
    return toast(`${state.view.coverage.tests[ti].label} runs none of the lines this review shows · esc ends`);
  }
  jump(hit);
  toast("Lines this test never runs are dimmed · esc ends");
}

function endSpot() {
  state.spot = -1;
  state.spotLines = null;
  renderReview();
}

async function openTests() {
  const s = cur();
  if (!state.sel || state.sel.station !== s.id) return needLine("see the tests that run it");
  if (!state.view.coverage) return toast("No coverage yet: ask the agent to run margin coverage.");
  const p = partData(s, state.sel.part);
  const row = p.rows.find((r) => r.n - 1 === state.sel.line);
  if (row?.cov !== "run") return toast(`No test runs line ${state.sel.line + 1}.`);
  let list;
  try {
    list = await api("Reviews.TestsAt", s.id, state.sel.part, state.sel.line);
  } catch (e) {
    return toast(errText(e), true);
  }
  const tests = state.view.coverage.tests;
  list.sort((a, b) => tests[a].label.localeCompare(tests[b].label));
  const line = state.sel.line;
  const needle = (row.s ?? []).map((x) => x.t).join("").trim();
  const body = el(`<div class="tests-pop"></div>`);
  const picked = new Set();
  for (const ti of list) {
    const t = tests[ti];
    const r = el(`<div class="test-row"><label><input type="checkbox"> ${esc(t.label)}${t.new ? ' <span class="badge fresh">new</span>' : ""}</label>
      <span class="muted small">${esc(t.file && t.line ? `${t.file}:${t.line}` : t.group)}</span><span class="spacer"></span>
      <button class="na">Spotlight</button><button class="na">Source</button></div>`);
    r.querySelector("input").onchange = (e) => (e.target.checked ? picked.add(ti) : picked.delete(ti));
    const [spot, src] = r.querySelectorAll("button");
    spot.onclick = () => {
      closeModal();
      spotlight(ti);
    };
    src.onclick = async () => {
      try {
        showPeek(await api("Reviews.TestSource", ti));
      } catch (e) {
        toast(errText(e), true);
      }
    };
    body.append(r);
  }
  const run = el(`<div class="cp-foot"><span class="muted small">margin never runs tests itself: the agent runs the picked ones and reports what each checks about this line.</span><span class="spacer"></span><button class="primary small">Agent runs ${list.length === 1 ? "it" : "the picked tests"}</button></div>`);
  run.querySelector("button").onclick = async () => {
    const ids = picked.size ? [...picked] : list.slice(0, 1);
    try {
      await api("Reviews.RunTests", ids, p.file, line + 1, needle);
      closeModal();
      toast("Sent to the agent.");
    } catch (e) {
      toast(errText(e), true);
    }
  };
  body.append(run);
  openModal(`${plural(list.length, "test runs", "tests run")} ${base(p.file)}:${line + 1}`, body);
}

function untested() {
  const out = [];
  state.view.stations.forEach((s, si) => {
    if (s.kind !== "code") return;
    s.parts.forEach((_, pi) => {
      for (const r of partData(s, pi).rows) if (r.cov === "missed") out.push({ si, pi, line: r.n - 1 });
    });
  });
  return out;
}

function nextUntested(dir) {
  if (!state.view.coverage) return toast("No coverage yet: ask the agent to run margin coverage.");
  const hs = untested();
  if (!hs.length) return toast("A test runs every measured changed line.");
  const [idx, wrapped] = pick(hs, dir);
  jump(hs[idx]);
  toast(`Untested line ${idx + 1} of ${hs.length}${wrapped}`);
}

// Search and jumping

function findHit(test) {
  for (let si = 0; si < state.view.stations.length; si++) {
    const s = state.view.stations[si];
    for (let pi = 0; pi < s.parts.length; pi++) for (const r of partData(s, pi).rows) if (r.n && test(s, pi, r)) return { si, pi, line: r.n - 1 };
  }
  return null;
}

function hits() {
  const q = state.query;
  if (!q) return [];
  const fold = q === q.toLowerCase();
  const out = [];
  state.view.stations.forEach((s, si) => {
    s.parts.forEach((_, pi) => {
      for (const r of partData(s, pi).rows) {
        if (!r.n) continue;
        const t = (r.s ?? []).map((x) => x.t).join("");
        if ((fold ? t.toLowerCase() : t).includes(q)) out.push({ si, pi, line: r.n - 1 });
      }
    });
  });
  return out;
}

function pick(hs, dir) {
  const here = { si: state.station, pi: state.sel?.part ?? -1, line: state.sel?.line ?? -1 };
  const before = (a, b) => a.si - b.si || a.pi - b.pi || a.line - b.line;
  if (dir > 0) {
    const i = hs.findIndex((h) => before(h, here) > 0);
    return i >= 0 ? [i, ""] : [0, " (wrapped)"];
  }
  for (let i = hs.length - 1; i >= 0; i--) if (before(hs[i], here) < 0) return [i, ""];
  return [hs.length - 1, " (wrapped)"];
}

function jump(h) {
  const s = state.view.stations[h.si];
  if (h.si !== state.station) goStation(h.si, -1, walking() || (guided() && s.kind === "code") ? Math.max(stepOf(s, h.pi, h.line), 0) : 0);
  else if (walking()) {
    const si = stepOf(s, h.pi, h.line);
    if (si >= 0 && si !== state.step) {
      state.step = si;
      renderStation();
    }
  }
  if (!document.querySelector(`#station .row[data-part="${h.pi}"][data-line="${h.line}"]`)) {
    const p = partData(s, h.pi);
    p.folds = (p.folds ?? []).filter((f) => h.line < f.start || h.line > f.end);
    renderKeepScroll();
  }
  selectRow(s.id, h.pi, h.line, false);
  const row = document.querySelector(`#station .row[data-part="${h.pi}"][data-line="${h.line}"]`);
  row?.scrollIntoView({ block: "center" });
  row?.querySelector("mark")?.scrollIntoView({ block: "nearest", inline: "center" });
}

function searchNext(dir, fromInput = false) {
  const hs = hits();
  if (!state.query) return;
  if (!hs.length) {
    renderKeepScroll();
    return toast(`No match for “${state.query}”.`);
  }
  const [idx, wrapped] = pick(hs, dir);
  renderKeepScroll();
  jump(hs[idx]);
  toast(`Match ${idx + 1} of ${hs.length}${wrapped} · n next · N previous · esc ends`);
  if (fromInput) renderToolbar();
}

function endSearch() {
  state.query = "";
  renderToolbar();
  renderKeepScroll();
}

function nextFresh() {
  if (!state.fresh.size) return toast("No new notes.");
  const count = state.view.stations.length;
  for (let k = 0; k < count; k++) {
    const si = (state.station + k) % count;
    const s = state.view.stations[si];
    const i = s.notes.findIndex((n, idx) => state.fresh.has(n.key) && (si !== state.station || idx !== state.note));
    if (i >= 0) {
      if (si !== state.station) goStation(si, i);
      else focusNote(i);
      return;
    }
  }
}

// Filters, blind mode

function cycleFilter() {
  state.filter = (state.filter + 1) % filters().length;
  toast("Showing " + filters()[state.filter]);
  renderReview();
}

function reveal() {
  const s = cur();
  if (!blind(s)) return toast("Notes are already shown.");
  act("Reveal", s.id).then((ok) => ok && toast("Notes shown; compare them with what you found."));
}

// Overlays: peek, commit, export, help

async function openPeek(file, line) {
  try {
    showPeek(await api("Reviews.Peek", file, line));
  } catch (e) {
    toast(errText(e), true);
  }
}

function showPeek(pv) {
  const code = el(`<div class="code peek"></div>`);
  for (const r of pv.rows) {
    code.append(el(`<div class="row${r.n - 1 === pv.at ? " sel" : ""}"><span class="mark"></span><span class="cov"></span><span class="ln">${r.n}</span><span class="src">${renderSpans(r.s, "") || " "}</span></div>`));
  }
  openModal(pv.title, code);
  requestAnimationFrame(() => code.querySelector(".row.sel")?.scrollIntoView({ block: "center" }));
}

async function openCommit(sha) {
  try {
    const pv = await api("Reviews.Commit", sha);
    openModal(pv.title, el(`<pre class="commit">${esc(pv.text)}</pre>`));
  } catch (e) {
    toast(errText(e), true);
  }
}

async function openExport() {
  const body = el(`<div class="export"><div class="cp-labels"><button class="chip on" data-f="md">Markdown</button><button class="chip" data-f="github">GitHub review</button>
    <span class="spacer"></span><button class="primary small">Copy</button></div><pre class="commit">Loading…</pre>
    <p class="muted small">margin never posts anything: copy this and post it yourself.</p></div>`);
  const pre = body.querySelector("pre");
  const load = async (f) => {
    body.querySelectorAll("[data-f]").forEach((b) => b.classList.toggle("on", b.dataset.f === f));
    try {
      pre.textContent = await api("Reviews.Export", f);
    } catch (e) {
      pre.textContent = errText(e);
    }
  };
  body.querySelectorAll("[data-f]").forEach((b) => (b.onclick = () => load(b.dataset.f)));
  body.querySelector(".primary").onclick = () => copy(pre.textContent, "export");
  openModal("Export your review", body);
  load("md");
}

const helpKeys = [
  ["Walk", [["space", "next step; at the end of a stop, the next stop"], ["b", "previous step"], ["w", "guided walk or whole stop"], ["e", "more of the stop card or overview"], ["y n ?", "answer the orient question (overview)"]]],
  ["Move", [["} {", "next / previous stop"], ["] [", "next / previous note"], ["j k", "move the selected line"], ["ctrl+d ctrl+u", "half a page down / up"], ["g G", "top / bottom"], ["h l 0", "scroll code sideways / back"], ["N", "next new note (or previous match)"]]],
  ["Judge", [["1 2 3", "verdict: looks good / needs changes / not sure"], ["space", "mark the selected note read (whole stop)"], ["?", "agree with the selected problem"], ["x", "dismiss the selected note"], ["enter", "tick the selected call"], ["v", "reveal notes in blind mode"]]],
  ["Talk", [["c", "comment on the selected line (draft)"], ["S", "send all drafts to the agent"], ["A", "the agent's answers to your comments: read, edit, reply"], ["a", "ask the agent about the selected line"], ["y Y", "copy the note / the whole stop"], ["`", "focus the agent terminal; ctrl+esc leaves it"]]],
  ["See", [["/", "search; n N next / previous"], ["F", "filter notes"], ["n", "show or hide notes"], ["d", "removed lines"], ["s", "side by side"], ["z", "fold unchanged code"], ["+ -", "more / less context on the selected part"], ["f", "whole file of the selected part"], ["t", "tests that run the selected line"], ["u U", "next / previous untested change"], ["esc", "end search or spotlight, close an overlay"], ["r", "reload"], ["\\", "show or hide the sidebar"], ["ctrl+k", "go to a repository, worktree, branch or review"], ["ctrl+,", "settings"]]],
];

function openHelp() {
  const body = el(`<div class="help">${helpKeys.map(([h, ks]) => `<div><h4>${h}</h4>${ks.map(([k, d]) => `<div class="hk"><kbd>${esc(k)}</kbd><span>${esc(d)}</span></div>`).join("")}</div>`).join("")}</div>`);
  openModal("Keys", body);
}

// Keys

function pageMove(dir) {
  const box = $("#station");
  box.scrollTop += (dir * box.clientHeight) / 2;
  const top = box.getBoundingClientRect().top;
  const first = navItems().find((x) => x.getBoundingClientRect().top >= top + (dir > 0 ? 0 : 0));
  if (first) {
    const items = navItems();
    items.forEach((x) => x.classList.remove("kcur"));
    if (first.dataset.line !== undefined) selectRow(cur().id, +first.dataset.part, +first.dataset.line, false);
    else first.classList.add("kcur");
  }
}

export function reviewKey(e) {
  if (!state.view) return false;
  const k = e.key;
  const s = cur();
  if (document.querySelector(".modal-back")) return modalKey(e);
  const ov = s.kind === "overview" && guided();
  const sel = state.sel && state.sel.station === s.id ? state.sel : null;
  const keys = {
    w: toggleGuide,
    e: () => {
      state.more = !state.more;
      renderKeepScroll();
    },
    1: () => setVerdict("good"),
    2: () => setVerdict("changes"),
    3: () => setVerdict("unsure"),
    " ": () => {
      if (walking()) return nextStep();
      if (guided() && s.kind !== "code") return ov && !reader().orient ? toast("Answer first: y yes · n no · ? not sure") : goStation(state.station + 1);
      const n = selectedNote();
      if (!n) return toast("No note selected: ] steps to the next one, } to the next stop.");
      const wasRead = reader().reviewed.includes(n.key);
      return act("MarkNote", n.key, "reviewed").then((ok) => ok && !wasRead && stepNote(1));
    },
    b: () => (walking() ? prevStep() : goStation(state.station - 1)),
    Backspace: () => walking() && prevStep(),
    "}": () => goStation(state.station + 1),
    "{": () => goStation(state.station - 1),
    "]": () => stepNote(1),
    "[": () => stepNote(-1),
    j: () => moveCursor(1),
    k: () => moveCursor(-1),
    ArrowDown: () => moveCursor(1),
    ArrowUp: () => moveCursor(-1),
    g: () => moveCursor(-1, "first"),
    G: () => moveCursor(1, "last"),
    Home: () => moveCursor(-1, "first"),
    End: () => moveCursor(1, "last"),
    PageDown: () => pageMove(1),
    PageUp: () => pageMove(-1),
    ArrowLeft: () => $$("#station .code-scroll").forEach((x) => (x.scrollLeft -= 40)),
    ArrowRight: () => $$("#station .code-scroll").forEach((x) => (x.scrollLeft += 40)),
    h: () => $$("#station .code-scroll").forEach((x) => (x.scrollLeft -= 40)),
    l: () => $$("#station .code-scroll").forEach((x) => (x.scrollLeft += 40)),
    0: () => $$("#station .code-scroll").forEach((x) => (x.scrollLeft = 0)),
    y: () => (ov ? orient("yes") : yank(false)),
    Y: () => yank(true),
    n: () => (ov ? orient("no") : state.query ? searchNext(1) : toggle("showNotes")),
    N: () => (state.query ? searchNext(-1) : nextFresh()),
    "?": () => (ov ? orient("unsure") : noteMark("flagged")),
    x: xKey,
    Enter: activate,
    v: reveal,
    F: cycleFilter,
    d: () => toggle("showGhosts"),
    s: () => toggle("split"),
    z: () => toggle("unfold"),
    "/": () => document.querySelector("#search")?.focus(),
    Escape: () => {
      if (state.query) return endSearch();
      if (state.spot >= 0) return endSpot();
      if (state.compose) {
        state.compose = null;
        renderKeepScroll();
      }
    },
    t: openTests,
    u: () => nextUntested(1),
    U: () => nextUntested(-1),
    "+": () => (sel ? widen(s, sel.part, 1) : needLine("show more around it")),
    "=": () => (sel ? widen(s, sel.part, 1) : needLine("show more around it")),
    "-": () => (sel ? widen(s, sel.part, -1) : needLine("show less around it")),
    f: () => (sel ? wholeFile(s, sel.part) : needLine("see its whole file")),
    a: () => startOnLine("ask"),
    c: () => startOnLine("comment"),
    S: sendComments,
    A: () => openAnswers(),
    r: () => reload().then(() => toast("reloaded")),
    H: openHelp,
  };
  if (e.ctrlKey && (k === "d" || k === "u")) {
    pageMove(k === "d" ? 1 : -1);
    return true;
  }
  if (e.ctrlKey || e.metaKey || e.altKey) return false;
  const fn = keys[k];
  if (!fn) return false;
  fn();
  return true;
}

// The status bar: the counts the terminal viewer keeps in its footer, the session's pace, what the
// selected line's coverage says and the keys that matter right now.

const session = { secs: 0, lines: 0, last: Date.now(), counted: new Set() };
setInterval(() => {
  if (Date.now() - session.last < 5 * 60 * 1000) session.secs += 15;
  if (state.view) renderStatus();
}, 15000);
["keydown", "mousedown", "wheel"].forEach((t) => document.addEventListener(t, () => (session.last = Date.now()), { passive: true }));

function countRead(s) {
  if (s?.kind !== "code" || session.counted.has(s.id)) return;
  session.counted.add(s.id);
  session.lines += s.loc;
}

function pace() {
  const s = `${Math.floor(session.secs / 60)}m · ${session.lines} lines read`;
  if (session.secs >= 75 * 60) return [s + " · time for a break", true];
  if (session.secs >= 10 * 60 && (session.lines * 3600) / session.secs > 500) return [s + " · fast, over 500 lines/h", true];
  return [s, false];
}

function viewHint() {
  const s = cur();
  if (guided() && s.kind === "overview") return "y n ? answer · e more · ";
  if (guided() && s.kind !== "code") return "space next · w whole stops · ";
  if (s.kind !== "code") return "";
  if (walking()) return "space next · b back · e more · w whole stop · ";
  if (state.split) return "s one column · ";
  return state.showGhosts ? "d hide removed · s side by side · " : "d show removed · s side by side · ";
}

function cursorCoverage() {
  const s = cur();
  if (!state.sel || state.sel.station !== s?.id || !state.view.coverage) return "";
  const r = partData(s, state.sel.part)?.rows.find((x) => x.n - 1 === state.sel.line);
  if (r?.cov === "run") return `<span class="pill cov-run">┃ line ${r.n} runs in ${plural(r.tn, "test", "tests")} · t lists them</span>`;
  if (r?.cov === "missed") return `<span class="pill m-del">✗ line ${r.n} changed, and no test runs it</span>`;
  return "";
}

export function renderStatus() {
  const bar = $("#statusbar");
  if (!bar || !state.view) return;
  const c = state.view.counts ?? {};
  const pills = [`<span class="pill">✓ ${c.reviewed ?? 0}/${c.notes ?? 0}</span>`];
  if (c.issues) pills.push(`<span class="pill p-issue">! ${c.issues}</span>`);
  if (c.questions) pills.push(`<span class="pill p-question">? ${c.questions}</span>`);
  if (state.fresh.size) pills.push(`<span class="pill p-new">new ${state.fresh.size}</span>`);
  if (c.lost) pills.push(`<span class="pill p-issue" title="notes whose line the agent's anchor no longer finds">lost ${c.lost}</span>`);
  if (c.calls) pills.push(`<span class="pill p-decide">◆ ${c.calls_made}/${c.calls}</span>`);
  if (c.draft_comments) pills.push(`<span class="pill p-draft">✎ ${plural(c.draft_comments, "draft", "drafts")} · S sends</span>`);
  if (c.open_questions) pills.push(`<span class="pill m-chg">${c.open_questions} waiting for the agent</span>`);
  if (cur()?.kind === "code" && !state.showGhosts && !state.split) pills.push(`<span class="pill p-info">removed lines hidden</span>`);
  if (state.filter > 0) pills.push(`<span class="pill p-info">${esc(filters()[state.filter])}</span>`);
  if (state.query) pills.push(`<span class="pill p-match">/${esc(state.query)} · n N · esc</span>`);
  const [p, warn] = pace();
  pills.push(`<span class="pill${warn ? " m-chg" : " muted"}">${esc(p)}</span>`);
  pills.push(cursorCoverage());
  bar.innerHTML = `${pills.join("")}<span class="spacer"></span><span class="muted">${esc(viewHint())}H help</span>`;
}

// Keyboard reach: j k move over every line and link, enter acts on it, like the terminal cursor.

function navItems() {
  return $$("#station .row[data-line], #station .navable, #station .vb");
}

export function moveCursor(dir, jumpTo) {
  const items = navItems();
  if (!items.length) return;
  let at = items.findIndex((x) => x.classList.contains("kcur"));
  if (at < 0) {
    const s = cur();
    at = items.findIndex((r) => state.sel && r.dataset.line !== undefined && state.sel.station === s.id && +r.dataset.part === state.sel.part && +r.dataset.line === state.sel.line);
  }
  let next = jumpTo === "first" ? 0 : jumpTo === "last" ? items.length - 1 : at < 0 ? (dir > 0 ? 0 : items.length - 1) : Math.max(0, Math.min(items.length - 1, at + dir));
  items.forEach((x) => x.classList.remove("kcur"));
  const it = items[next];
  if (it.dataset.line !== undefined) {
    selectRow(cur().id, +it.dataset.part, +it.dataset.line, false);
  } else {
    $$("#station .row.sel").forEach((r) => r.classList.remove("sel"));
    it.classList.add("kcur");
  }
  it.scrollIntoView({ block: "nearest" });
  renderStatus();
}

function activate() {
  const k = document.querySelector("#station .kcur");
  if (k) return k.click();
  const n = selectedNote();
  if (n?.kind === "decide") return act("MarkNote", n.key, "reviewed");
  if (n && (n.kind === "question" || n.unbacked) && n.part >= 0) return openComposer(cur().id, n.part, n.line, { answers: n.key });
  const s = cur();
  if (state.sel?.station === s.id) {
    const r = partData(s, state.sel.part)?.rows.find((x) => x.n - 1 === state.sel.line);
    const first = (r?.notes ?? []).find((i) => noteVisible(s, s.notes[i]));
    if (first !== undefined) return focusNote(first);
  }
}

// Keys inside overlays: the test picker and peeks, like the terminal viewer's.
function modalKey(e) {
  const own = document.querySelector(".modal-back")?.onkey;
  if (own) return own(e) ?? true;
  const m = document.querySelector(".modal-back");
  const k = e.key;
  const scroller = m.querySelector(".modal > :not(.modal-head)");
  const picker = m.querySelector(".tests-pop");
  if (picker) {
    const rows = [...picker.querySelectorAll(".test-row")];
    let at = rows.findIndex((r) => r.classList.contains("kcur"));
    const go = (i) => {
      rows.forEach((r) => r.classList.remove("kcur"));
      rows[i]?.classList.add("kcur");
      rows[i]?.scrollIntoView({ block: "nearest" });
    };
    if (at < 0 && rows.length) go((at = 0));
    const flip = (r, on) => {
      const i = r?.querySelector("input");
      if (!i) return;
      i.checked = on ?? !i.checked;
      i.dispatchEvent(new Event("change"));
    };
    const act2 = {
      j: () => go(Math.min(at + 1, rows.length - 1)),
      ArrowDown: () => go(Math.min(at + 1, rows.length - 1)),
      k: () => go(Math.max(at - 1, 0)),
      ArrowUp: () => go(Math.max(at - 1, 0)),
      " ": () => flip(rows[at]),
      a: () => {
        const all = rows.every((r) => r.querySelector("input").checked);
        rows.forEach((r) => flip(r, !all));
      },
      p: () => rows[at]?.querySelectorAll("button")[1]?.click(),
      Enter: () => rows[at]?.querySelectorAll("button")[0]?.click(),
      r: () => picker.querySelector(".cp-foot .primary")?.click(),
      Escape: closeModal,
      q: closeModal,
      t: closeModal,
    }[k];
    if (act2) act2();
    return true;
  }
  const step = scroller ? scroller.clientHeight / 2 : 0;
  const scroll = { j: 40, ArrowDown: 40, k: -40, ArrowUp: -40, " ": step, PageDown: step, PageUp: -step }[k];
  if (scroll !== undefined && scroller) scroller.scrollTop += scroll;
  else if (!["Shift", "Control", "Alt", "Meta"].includes(k)) closeModal();
  return true;
}
