import { Events, Clipboard } from "/wails/runtime.js";
import { api, $, $$, state, hooks, esc, prose, md, oneLine, el, toast, errText, tail, plural, shortList, base, openModal, closeModal } from "./core.js";
import { openAnswers, hasAnswerDraft } from "./answers.js";
import { agentReply, discussionThreads, localResolution, nextThread, questionReply, questionSelection, questionTurns, readReply, setLocalResolution, unreadReply } from "./discussions.js";
import { editorMode, setEditorMode } from "./editor-mode.js";
import { runTests, tryChange, toggleTestPanel, resetTests, statusOf, statusIcon, testHooks, results } from "./tests.js";
import { reviewToolbarOverflow } from "./toolbar-overflow.js";

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
const fullDisplayLabel = () => state.view?.mode === "file" ? "Full file diff" : "Full group";
const modeButtons = $$("#reading-mode button");
const splitButtons = $$("#diff-layout button");
const noteVisibilityButtons = $$("#notes-visibility button");
const noteFilter = $("#note-filter");
const toolbarOverflow = reviewToolbarOverflow($("#toolbar"));
const search = $("#search");
for (const button of modeButtons) button.onclick = () => setReadingMode(button.dataset.full === "true");
for (const button of splitButtons) button.onclick = () => setSplit(button.dataset.split === "true");
for (const button of noteVisibilityButtons) button.onclick = () => setNoteVisibility(button.dataset.noteVisibility);
noteFilter.onchange = () => setNoteFilter(noteFilter.value);
search.onkeydown = (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    e.stopPropagation();
    state.query = search.value;
    searchNext(e.shiftKey ? -1 : 1, true);
    toolbarOverflow.finishSearch();
  } else if (e.key === "Escape") {
    e.preventDefault();
    e.stopPropagation();
    endSearch();
    toolbarOverflow.finishSearch();
  }
};
const walking = () => guided() && cur()?.kind === "code";
const partKey = (s, pi) => `${s.id}|${pi}`;
const partData = (s, pi) => state.parts[partKey(s, pi)] ?? s.parts[pi];

// Loading

export function openReviewFile(path, restore = false, browsing = false) {
  return openReview("", "", false, path, restore, browsing);
}

let openingReview = Promise.resolve();
export function openReview(dir, arg, fresh = false, file = "", restore = false, browsing = false) {
  const opened = openingReview.then(() => loadReview(dir, arg, fresh, file, restore, browsing));
  openingReview = opened.catch(() => {});
  return opened;
}

async function loadReview(dir, arg, fresh, file, restore, browsing) {
  const previous = state.opened;
  const scroll = $("#station").scrollTop;
  if (previous) previous.restoringPosition = true;
  await flushReadingPosition();
  hooks.showReviewer(true);
  $("#station").innerHTML = `<div class="spinner">Preparing the review…</div>`;
  let opened;
  try {
    opened = file ? await api("Reviews.OpenFile", file) : await api("Reviews.Open", dir, arg, fresh);
  } catch (e) {
    if (previous && state.view) {
      renderReview();
      $("#station").scrollTop = scroll;
      previous.restoringPosition = false;
      await hooks.attachAgent?.(previous, false);
      toast(errText(e), true);
    } else {
      $("#station").innerHTML = `<div class="spinner error">${esc(errText(e))}</div>`;
    }
    return;
  }
  const same = !fresh && state.opened?.path === opened.path;
  state.opened = { ...opened, arg, file };
  const active = state.opened;
  active.restoringPosition = true;
  if (!same) {
    if (!browsing) readingOrigin = null;
    activeThread = "";
    commentSections = {};
    threads = new Map();
    expandedDisclosures.clear();
    resetTests();
    endTry();
    state.view = null;
    diffQuestions = [];
    questionsByRow.clear();
    state.gh = null;
    state.ghErr = "";
    state.ghPull = null;
    document.getElementById("gh-back")?.remove();
    Object.assign(state, { station: 0, note: -1, step: 0, sel: null, compose: null, editing: null, block: null, parts: {}, expand: {}, whole: {}, around: {}, known: null,
      query: "", spot: -1, spotLines: null, filter: "All notes" });
    state.fresh = new Set();
  }
  hooks.reviewOpened?.();
  await reload(!same);
  if (state.view?.path !== opened.path) {
    active.restoringPosition = false;
    return false;
  }
  if (!same) loadGitHub(active);
  const position = same || opened.created ? null : state.view.walkthrough ? state.view.walkthrough.position :
    await api("Reviews.ReadingPosition", opened.path).catch(() => null);
  if (!same && !await restoreReadingPosition(position)) {
    if (state.view.mode === "file") {
      await goToFirstFile();
    } else if ((!guided() || reader().visited.length) && state.view.stations.length > 1) {
      goStation(1);
    }
  }
  if (opened.shared) toast("This review is also open in a terminal viewer: the agent's margin goto and where drive that one, not this window.", true);
  $("#workspace").classList.add("focus");
  hooks.fitTerminal();
  active.restoringPosition = false;
  queueReadingPosition(0, true);
  await flushReadingPosition();
  await hooks.attachAgent?.(active, restore);
  return true;
}

let reloadRequest = 0;
async function reload(top = false) {
  const opened = state.opened;
  if (opened?.switchingWalkthrough) {
    opened.reloadAfterSwitch = true;
    return;
  }
  const request = ++reloadRequest;
  try {
    const view = await api("Reviews.Current");
    if (request === reloadRequest && state.opened === opened && view.path === opened?.path) applyView(view, top);
  } catch (e) {
    if (request !== reloadRequest || state.opened !== opened) return;
    if (!state.view) $("#station").innerHTML = `<div class="spinner error">${esc(errText(e))}</div>`;
    else toast(errText(e), true);
  }
}

function applyView(view, top = false) {
  if (view.path !== state.opened?.path) return;
  const stop = cur();
  const prevId = stop?.id;
  const prev = state.view;
  const sameFileView = !top && prev?.mode === "file" && view.mode === "file";
  const article = $("#station");
  const anchor = sameFileView ? visibleCodeAnchor(article) : null;
  const step = sameFileView && walking() ? currentReadingStep(stop, anchor) : null;
  const noteKey = sameFileView ? stop?.notes[state.note]?.key : undefined;
  if (prev?.path === view.path && prev.mode === view.mode && prev.readerRevision > view.readerRevision) {
    view.reader = prev.reader;
    view.counts = prev.counts;
    view.readerRevision = prev.readerRevision;
  }
  trackFresh(view);
  if (prev && prev !== view && !top) {
    state.around = {};
    if (state.spot >= 0) {
      const test = prev.coverage?.tests[state.spot];
      const spot = test ? view.coverage?.tests.findIndex((t) => t.name === test.name && t.group === test.group && t.file === test.file) ?? -1 : -1;
      state.spot = spot;
      state.spotLines = null;
      if (spot >= 0) {
        api("Reviews.RunsLines", spot, view.version).then((l) => {
          if (state.view !== view || state.spot !== spot) return;
          state.spotLines = l;
          renderKeepScroll();
        }).catch((e) => state.view === view && toast(errText(e), true));
      }
    }
  }
  state.view = view;
  indexDiscussions();
  const i = view.stations.findIndex((s) => s.id === prevId);
  state.station = i >= 0 ? i : Math.min(state.station, view.stations.length - 1);
  if (sameFileView && i >= 0) {
    const current = cur();
    if (step) state.step = Math.max(0, indexOfReadingStep(current, step));
    if (noteKey) state.note = current.notes.findIndex((note) => note.key === noteKey);
  }
  const scroll = article.scrollTop;
  renderReview();
  if (!top) {
    article.scrollTop = scroll;
    restoreCodeAnchor(article, anchor);
  }
  refreshParts();
  refreshDiffQuestions(view);
  refreshCommentReplies(view);
}

function applyReader(update) {
  const v = state.view;
  if (!v || update.path !== v.path || update.mode !== v.mode || update.readerRevision <= v.readerRevision) return;
  v.readerRevision = update.readerRevision;
  if (JSON.stringify(v.reader) === JSON.stringify(update.reader) && JSON.stringify(v.counts) === JSON.stringify(update.counts)) return;
  const sent = sentComments(v);
  const visibilityChanged = v.reader.noteVisibility !== update.reader.noteVisibility;
  v.reader = update.reader;
  v.counts = update.counts;
  if (visibilityChanged && selectedNote() && !navigableNote(cur(), selectedNote())) state.note = -1;
  indexDiscussions();
  const scroll = $("#station").scrollTop;
  renderReview();
  $("#station").scrollTop = scroll;
  if (sent !== sentComments(v)) refreshCommentReplies(v);
}

function visibleCodeAnchor(article) {
  const box = article.getBoundingClientRect();
  for (const row of article.querySelectorAll(".row[data-line][data-part]")) {
    const rect = row.getBoundingClientRect();
    if (rect.bottom > box.top && rect.top < box.bottom) {
      return { part: row.dataset.part, line: row.dataset.line, offset: rect.top - box.top };
    }
  }
  return null;
}

function restoreCodeAnchor(article, anchor) {
  if (!anchor) return;
  const row = rowAt(anchor.part, anchor.line);
  if (!row) return;
  const offset = row.getBoundingClientRect().top - article.getBoundingClientRect().top;
  article.scrollTop += offset - anchor.offset;
}

export function renderWalkthrough() {
  const opened = state.opened;
  const walk = state.view?.path === opened?.path ? state.view?.walkthrough : null;
  $("#walkthrough").classList.toggle("hidden", !walk);
  toolbarOverflow.schedule();
  if (!walk || walk.ready) $("#walkthrough-feedback").textContent = "";
  if (!walk) return;
  const preparing = !walk.ready && (opened.launchingWalkthrough || (opened.walkAgentLive && opened.agent?.walkPreparing));
  const label = walk.ready ? (walk.offer ? "Guided walk is ready" : "") :
    preparing ? "The agent is preparing the guided walk" :
    opened.walkFailure ? "Could not start the guided walk. Retry from the agent controls" :
    opened.walkAgentLive ? "The guided walk is waiting for the agent to finish grouping changes" :
    opened.agent?.walkAttempted ? "The agent stopped before the guided walk was ready" : "Start the agent to prepare the guided walk";
  const status = $("#walkthrough-status");
  if (status.textContent !== label) status.textContent = label;
  $("#walkthrough-progress").classList.toggle("hidden", !preparing);
  for (const mode of ["file", "agent"]) {
    const button = $("#walkthrough-" + mode);
    button.setAttribute("aria-pressed", String(walk.mode === mode));
    button.setAttribute("aria-disabled", String(!!opened.switchingWalkthrough || (mode === "agent" && !walk.ready)));
  }
  $("#walkthrough-agent").title = walk.ready ? "Follow the agent’s groups of related changes across files" : label;
}

let walkthroughFeedbackTimer;
function showWalkthroughFeedback(event) {
  const feedback = $("#walkthrough-feedback");
  const opened = state.opened;
  feedback.textContent = opened.launchingWalkthrough || opened.walkAgentLive || opened.walkFailure
    ? $("#walkthrough-status").textContent
    : "Start the agent first to prepare the guided walk";
  const button = event.currentTarget.getBoundingClientRect();
  const x = event.detail ? event.clientX : button.left + button.width / 2;
  const y = event.detail ? event.clientY : button.bottom;
  const gap = 8;
  const { offsetWidth: width, offsetHeight: height } = feedback;
  feedback.style.left = Math.max(gap, Math.min(x + gap, window.innerWidth - width - gap)) + "px";
  const top = y + gap + height <= window.innerHeight - gap ? y + gap : y - height - gap;
  feedback.style.top = Math.max(gap, top) + "px";
  clearTimeout(walkthroughFeedbackTimer);
  walkthroughFeedbackTimer = setTimeout(() => feedback.textContent = "", 3500);
}

async function selectWalkthrough(mode, event) {
  const opened = state.opened;
  const walk = state.view?.walkthrough;
  if (!walk || !opened || opened.restoringPosition || opened.switchingWalkthrough || changingMode === opened) return;
  if (mode === "agent" && !walk.ready) {
    showWalkthroughFeedback(event);
    return;
  }
  const changing = mode !== walk.mode;
  if ((!changing && !walk.offer) || (changing && !beginDiscussionAction())) return;
  const button = event.currentTarget;
  const restoreFocus = document.activeElement === button;
  opened.switchingWalkthrough = true;
  if (changing) {
    $("#review-body").inert = true;
    $("#toolbar").inert = true;
  }
  ++reloadRequest;
  renderWalkthrough();
  try {
    if (changing) {
      queueReadingPosition(0, true);
      await flushReadingPosition();
    }
    if (state.opened !== opened) return;
    opened.restoringPosition = changing;
    const view = await api("Reviews.SelectWalkthrough", opened.path, mode);
    if (state.opened !== opened || view.path !== opened.path) return;
    if (changing) {
      activeThread = "";
      readingOrigin = null;
      state.ghPull = null;
      document.getElementById("gh-back")?.remove();
      Object.assign(state, { station: 0, note: -1, step: 0, sel: null, compose: null, editing: null, block: null,
        parts: {}, around: {}, known: null, fresh: new Set(), spot: -1, spotLines: null });
    }
    applyView(view, changing);
    if (changing) {
      if (!await restoreReadingPosition(view.walkthrough.position)) {
        if (mode === "file") await goToFirstFile();
        else await goStation(0);
      }
      loadThreads(opened);
    }
  } catch (e) {
    if (state.opened === opened) toast(errText(e), true);
  } finally {
    opened.restoringPosition = false;
    opened.switchingWalkthrough = false;
    if (changing) {
      $("#review-body").inert = false;
      $("#toolbar").inert = false;
      discussionBusy = false;
      updateDiscussionControls();
    }
    if (state.opened === opened) {
      if (changing) queueReadingPosition(0, true);
      renderWalkthrough();
      if (restoreFocus && document.activeElement === document.body) button.focus({ preventScroll: true });
      if (opened.reloadAfterSwitch) {
        opened.reloadAfterSwitch = false;
        reload();
      }
    }
  }
}

$("#walkthrough-file").onclick = (e) => selectWalkthrough("file", e);
$("#walkthrough-agent").onclick = (e) => selectWalkthrough("agent", e);

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
  const view = state.view;
  let changed = false;
  for (let pi = 0; pi < s.parts.length; pi++) {
    if (state.view !== view || cur() !== s) return;
    const k = partKey(s, pi);
    const extra = state.expand[k] ?? 0, whole = !!state.whole[k];
    if (!extra && !whole) continue;
    if (await loadPart(s, pi, extra, whole, true)) changed = true;
  }
  if (changed && state.view === view && cur() === s) renderKeepScroll();
}

export async function act(name, ...args) {
  const opened = state.opened;
  try {
    const v = await api("Reviews." + name, ...args);
    if (state.opened !== opened || v?.path !== opened?.path) return false;
    applyReader(v);
    return true;
  } catch (e) {
    if (state.opened === opened) toast(errText(e), true);
    return false;
  }
}

let reloadTimer, pendingReload, reloading = false;
Events.On("review:changed", () => {
  const opened = state.opened;
  clearTimeout(reloadTimer);
  reloadTimer = setTimeout(async () => {
    if (!opened || state.opened !== opened) return;
    pendingReload = opened;
    if (reloading) return;
    reloading = true;
    try {
      while (pendingReload) {
        const target = pendingReload;
        pendingReload = null;
        if (state.opened === target) await reload();
      }
    } finally {
      reloading = false;
    }
  }, 150);
});
Events.On("review:reader", () => {
  if (state.opened && state.view) act("Reader");
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
  return { version: state.view?.version, station: state.station, note: state.note, part: sel?.part ?? -1,
    line: sel?.line ?? -1, visible, spot: state.spot, err };
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
  const st = state.view.stations;
  const exact = st.findIndex((s) => s.id === arg);
  if (exact >= 0) return goStation(exact);
  let head = arg, tail = "";
  const i = arg.lastIndexOf(":");
  if (i >= 0) [head, tail] = [arg.slice(0, i), arg.slice(i + 1)];
  const station = st.findIndex((s) => s.id === head);
  if (station < 0 && tail && /[./]/.test(head) && /^\d+$/.test(tail)) return gotoFileLine(head, +tail);
  const si = station >= 0 ? station : findStation(head);
  goStation(si);
  if (!tail) return;
  const n = Number(tail);
  const s = state.view.stations[si];
  if (!Number.isInteger(n) || n < 1 || n > s.notes.length) throw new Error(`station ${s.id} has ${s.notes.length} notes`);
  gotoNote(si, n - 1);
}

function gotoNote(si, ni) {
  const s = state.view.stations[si];
  const note = s.notes[ni];
  if (si !== state.station) goStation(si);
  focusNote(ni);
  if (note.part >= 0 && note.line >= 0) selectRow(s.id, note.part, note.line, false);
}

// Mentions of other notes in prose (stop:n) are links. Following one remembers where the reader was,
// so ctrl+o comes back, like a jump in vim.

hooks.noteRef = (id, n) => {
  const st = state.view?.stations ?? [];
  const si = st.findIndex((s) => s.kind === "code" && esc(s.id) === id);
  if (si < 0) {
    const ref = id + ":" + n;
    for (let i = 0; i < st.length; i++) {
      const index = st[i].notes.findIndex((note) => esc(note.authoredRef) === ref);
      if (index >= 0) return { station: i, note: index };
    }
    return undefined;
  }
  return n >= 1 && n <= st[si].notes.length ? { station: si, note: n - 1 } : null;
};

const jumps = [];

function followRef(si, ni) {
  const s = state.view?.stations[si];
  if (!s?.notes[ni]) return;
  const sel = state.sel && state.sel.station === cur().id ? { ...state.sel } : null;
  jumps.push({ station: cur().id, note: state.note, sel });
  if (jumps.length > 50) jumps.shift();
  gotoNote(si, ni);
  toast(`${s.id}:${ni + 1} · ctrl+o goes back`);
}

function jumpBack() {
  const j = jumps.pop();
  if (!j) return toast("No jump to go back from.");
  const si = state.view.stations.findIndex((s) => s.id === j.station);
  if (si < 0) return;
  if (si !== state.station) goStation(si);
  if (j.note >= 0 && j.note < cur().notes.length) focusNote(j.note);
  if (j.sel) selectRow(j.sel.station, j.sel.part, j.sel.line, false);
}

// followSelectedRef follows the mentions in the selected note's words in turn, one per press.
let refTurn = { key: "", i: 0 };
function followSelectedRef() {
  const n = selectedNote();
  if (!n) return toast("No note selected: ] steps to the next one.");
  const html = prose(n.text) + prose(n.evidence ?? "");
  const refs = [...html.matchAll(/data-ref="(\d+):(\d+)"/g)].map((m) => [+m[1], +m[2]]);
  if (!refs.length) return toast("This note mentions no other note.");
  if (refTurn.key !== n.key) refTurn = { key: n.key, i: 0 };
  const [si, ni] = refs[refTurn.i++ % refs.length];
  followRef(si, ni);
}

document.addEventListener(
  "click",
  (e) => {
    const a = e.target.closest?.("a.ref");
    if (!a) return;
    e.preventDefault();
    e.stopPropagation();
    const [si, ni] = a.dataset.ref.split(":").map(Number);
    followRef(si, ni);
  },
  true,
);

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
          const s = st[si];
          if (!(await loadPart(s, pi, state.expand[partKey(s, pi)] ?? 0, true))) return;
        }
        jump({ si, pi, line: line - 1 });
        return;
      }
    }
  }
  throw new Error(`${file}:${line} is not in any station`);
}

// Notes: blind mode and filters

const noteVisibility = () => reader().noteVisibility ?? "all";

function cardsHidden(s) {
  return s?.kind === "code" && noteVisibility() === "hide" && !reader().revealed.includes(s.id);
}

const filterDescriptions = {
  "All notes": "Show every kind of agent note, including context and positive observations.",
  "Issues, questions & decisions": "Show reported issues and anything the agent asks you to answer or decide.",
  "Issues with evidence": "Show only issues the agent backed with evidence.",
};

function filters() {
  const walk = state.view?.walkthrough;
  const areas = new Set(walk?.focusAreas ?? []);
  if (!walk) {
    for (const s of state.view?.stations ?? []) for (const n of s.notes) if (n.focus) areas.add(n.focus);
  }
  return Object.keys(filterDescriptions).concat([...areas].sort().map((area) => "Focus: " + area));
}

const asks = (n) => ["issue", "question", "decide"].includes(n.kind);

function matchesNoteFilter(n) {
  switch (state.filter) {
    case "All notes":
      return true;
    case "Issues, questions & decisions":
      return asks(n);
    case "Issues with evidence":
      return n.kind === "issue" && !n.unbacked;
  }
  return !!n.focus && "Focus: " + n.focus === state.filter;
}

function noteCardVisible(s, n) {
  return !cardsHidden(s) && (noteVisibility() !== "risky" || n.highRisk) && matchesNoteFilter(n);
}

function filteringNotes(s) {
  return s.kind === "code" && state.filter !== "All notes";
}

function stopHasNoMatchingNotes(s) {
  return filteringNotes(s) && !s.notes.some(matchesNoteFilter);
}

// Header, phases and the stop list

export function renderReview() {
  const v = state.view;
  renderWalkthrough();
  $("#r-kicker").textContent = v.kicker;
  $("#r-title").textContent = v.title;
  $("#r-stats").textContent = v.stats;
  const drafts = reader().drafts ?? 0;
  $("#send-btn").classList.toggle("hidden", !drafts);
  $("#send-btn").textContent = `Send ${drafts} comment${drafts > 1 ? "s" : ""}`;
  renderGitHubButton();
  const toFix = reader().toFix ?? 0;
  $("#fix-btn").classList.toggle("hidden", !toFix);
  $("#fix-btn").textContent = `Ask to fix ${toFix}`;
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
    const ghn = ghOpen().filter((t) => t.station === s.id).length;
    if (ghn) meta.push(`<span class="m-gh">${ghn} on GitHub</span>`);
    const vd = verdicts.find((x) => x.id === reader().verdicts?.[s.id]);
    const visited = !vd && reader().visited.includes(s.id) && s.kind === "code";
    const item = el(`<div class="st${i === state.station ? " on" : ""}"><span class="num">${vd ? `<span class="vdot ${vd.cls}" title="${vd.label}"></span>` : visited ? `<span class="vdot half" title="visited"></span>` : num}</span>
      <span class="t">${esc(s.title || s.id)}</span><div class="meta">${meta.join("")}<span class="m-fresh hidden" data-reply-stop="${esc(s.id)}"></span></div></div>`);
    if (stopHasNoMatchingNotes(s)) {
      item.classList.add("no-note-match");
      item.title = "No matching notes";
    }
    item.onclick = () => goStation(i);
    nav.append(item);
  });
  renderStation();
}

function renderToolbar() {
  for (const button of modeButtons) button.setAttribute("aria-pressed", button.dataset.full === String(!guided()));
  for (const group of $$("#toolbar .comparison-option")) group.classList.toggle("hidden", !state.view.hasBase);
  for (const button of splitButtons) button.setAttribute("aria-pressed", String(button.dataset.split === String(state.split)));
  for (const button of noteVisibilityButtons) {
    button.setAttribute("aria-pressed", String(button.dataset.noteVisibility === noteVisibility()));
    button.setAttribute("aria-disabled", String(savingNoteVisibility === state.opened));
  }
  const fullButton = $("#reading-full");
  fullButton.textContent = fullDisplayLabel();
  fullButton.title = state.view.mode === "file" ? "See all changed sections of this file on one page (w)" :
    "See all changes in the current group on one page (w)";
  const choices = filters();
  if (choices.length !== noteFilter.length || choices.some((label, i) => label !== noteFilter.options[i].value)) {
    noteFilter.replaceChildren(...choices.map((label) => {
      const option = new Option(label, label);
      option.title = filterDescriptions[label] ?? `Show only notes tagged ${label.slice("Focus: ".length).replaceAll("-", " ")}.`;
      return option;
    }));
  }
  if (!choices.includes(state.filter)) state.filter = choices[0];
  if (noteFilter.value !== state.filter) noteFilter.value = state.filter;
  const filterHint = noteFilter.selectedOptions[0].title + " Press F to cycle filters.";
  if (noteFilter.title !== filterHint) noteFilter.title = filterHint;

  const s = cur();
  const tb = $("#toolbar-actions");
  toolbarOverflow.clearActions();
  if (document.activeElement !== search && search.value !== state.query) search.value = state.query;
  const btn = (label, title, on, fn, overflow, cls = "") => {
    const b = el(`<button type="button" class="tb${on ? " on" : ""} ${cls}" title="${esc(title)}">${label}</button>`);
    b.dataset.overflow = overflow;
    b.onclick = () => {
      toolbarOverflow.closeForAction(b);
      fn();
    };
    tb.append(b);
    return b;
  };
  if (!guided()) btn("Fold", "Fold unchanged stretches (z)", !state.unfold, () => toggle("unfold"), "layout");
  if (cardsHidden(s)) btn("Reveal notes", "Show note cards and badges on this stop (v)", false, reveal, "notes", "warn");
  if (s?.kind === "code" && state.view.coverage && s.cov?.measured) btn("▶ Tests", "Run the tests that run this stop's changed lines (T without a selected line)", false, () => runFor(s, -1), "tests");
  if (s?.kind === "code" && state.view.coverage && s.cov?.missed) btn("Next untested", "The next changed line no test runs (u, U goes back)", false, () => nextUntested(1), "tests");
  if (state.spot >= 0) btn("Spotlight: " + esc(state.view.coverage?.tests[state.spot]?.label ?? "") + " ✕", "End the spotlight (esc)", true, endSpot, "tests", "warn");
  if (reader().comments.some((c) => !c.draft)) btn("Answers", "The agent's answers to your comments, to read, edit or reply to (A)", false, () => openDiscussionAnswers(), "github");
  const questions = btn("Questions", "Your diff questions and the agent's replies", false, () => openDiffQuestions(), "github");
  questions.id = "diff-questions-btn";
  questions.classList.toggle("hidden", !diffQuestions.length);
  const discussionNav = discussionNavigation();
  discussionNav.dataset.overflow = "github";
  discussionNav.onclick = (e) => {
    if (e.target.closest("button")) toolbarOverflow.closeForAction(discussionNav);
  };
  tb.append(discussionNav);
  if (state.gh) {
    const open = ghOpen().length;
    btn(`GitHub${open ? " · " + open + " open" : ""}`, `Everything said on PR #${state.gh.number}: threads on the code and the conversation (B)`, false, () => openGitHubPanel(true), "github");
    btn("↻", "Pull comments, replies, reactions and resolved state from GitHub again", false, pullGitHub, "github").setAttribute("aria-label", "Refresh GitHub");
  }
  btn("Export", "Your comments, flagged problems and calls, to post yourself", false, openExport, "export");
  btn("?", "All keys (H)", false, openHelp, "help").setAttribute("aria-label", "Help");
  toolbarOverflow.schedule();
}

function cycleToolbarChoice(buttons) {
  const selected = buttons.findIndex((button) => button.getAttribute("aria-pressed") === "true");
  buttons[(selected + 1) % buttons.length].click();
}

function setSplit(split) {
  if (!state.view.hasBase || state.split === split) return;
  state.split = split;
  rerenderKeepScroll();
}

let savingNoteVisibility;
async function setNoteVisibility(mode) {
  const opened = state.opened;
  if (!opened || opened === savingNoteVisibility || mode === noteVisibility() || !allowReviewViewChange()) return;
  savingNoteVisibility = opened;
  renderToolbar();
  try {
    await act("SetNoteVisibility", opened.path, mode);
  } finally {
    if (savingNoteVisibility === opened) savingNoteVisibility = null;
    if (state.opened === opened) renderToolbar();
  }
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
  if (!discussionBusy) activeThread = "";
  state.station = Math.max(0, Math.min(i, state.view.stations.length - 1));
  state.note = noteIdx;
  state.step = step;
  state.nudged = false;
  state.compose = null;
  endTry();
  state.block = null;
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
  return refreshParts();
}

function renderStation() {
  const focus = discussionFocus();
  const compose = state.compose, edit = state.editing, editor = document.activeElement;
  const editorSelector = editor?.matches("#station .edit-box textarea") ? "#station .edit-box textarea" :
    editor?.matches("#station .composer textarea") ? "#station .composer:not(.edit-box) textarea" : "";
  const caret = editorSelector ? [editor.selectionStart, editor.selectionEnd] : null;
  drawStation();
  syncDiscussionUI();
  restoreDiscussionFocus(focus);
  if (caret && state.compose === compose && state.editing === edit) {
    const textarea = $(editorSelector + ":not(:disabled)");
    textarea?.focus({ preventScroll: true });
    textarea?.setSelectionRange(...caret);
  }
  covHint();
  queueMicrotask(() => queueReadingPosition());
}

function drawStation() {
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
    const st = [
      sp.c ? `--d:${sp.c}` : "",
      sp.l ? `--l:${sp.l}` : "",
      sp.b || sp.lb ? `--weight-dark:${sp.b ? 600 : "inherit"};--weight-light:${sp.lb ? 600 : "inherit"}` : "",
      sp.i || sp.li ? `--style-dark:${sp.i ? "italic" : "normal"};--style-light:${sp.li ? "italic" : "normal"}` : "",
    ].filter(Boolean).join(";");
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
    out += st ? `<span class="sx" style="${st}">${piece}</span>` : piece;
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

const covMarks = { run: ["", "cov-run"], missed: ["✗", "cov-missed"], idle: ["·", "cov-idle"] };

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

function ghostRow(text, moved, hl, split, address) {
  text = untab(text);
  const cls = moved ? "ghost moved navable" : "ghost navable";
  const body = (moved ? "← " : "") + renderSpans(text, state.query, hl ? { ranges: [hl], open: '<span class="del-hl">', close: "</span>" } : null);
  return el(`<div class="row ${cls}" data-ghost="${address}"${moved ? ` title="moved to ${esc(moved)}"` : ""}><span class="mark"></span><span class="cov"></span><span class="ln"></span>
    <span class="src${split ? " left" : ""}">${body}</span>${split ? '<span class="src right"></span>' : ""}</div>`);
}

function lineVisible(s, pi, line) {
  return line >= 0 && partData(s, pi)?.rows?.some((row) => row.n === line + 1);
}

function navigableNote(s, note) {
  return noteCardVisible(s, note) && (note.part < 0 || note.line < 0 || lineVisible(s, note.part, note.line));
}

// codeRows draws lines from..to of a part, with the notes, comments and questions on each line below it.
function codeRows(s, pi, from, to, opts = {}) {
  const p = partData(s, pi);
  const split = state.split && (p.diffed || p.base || p.newFile);
  const scroll = el(`<div class="code-scroll"><div class="code code-inner${split ? " split" : ""}"></div></div>`);
  const code = scroll.firstElementChild;
  const shown = opts.shown ?? new Set();
  const spotSet = state.spotLines ? new Set(state.spotLines[partKey(s, pi)] ?? []) : null;
  const folds = state.unfold || opts.step || opts.whole ? [] : (p.folds ?? []);
  const rows = p.rows.filter((r) => (r.n ? r.n - 1 >= from && r.n - 1 <= to : to >= p.len - 1));
  const hlOld = (r) => (r.hl ? [r.hl[0], r.hl[1]] : null);
  // In side by side, removed lines pair with the changed lines that replaced them.
  const pairedLeft = new Map();
  const pairedCounts = new Map();
  if (split) {
    for (let k = 0; k < rows.length; k++) {
      const r = rows[k];
      for (let j = 0; j < (r.g ?? []).length; j++) {
        const c = rows[k + j];
        if (!c || c.k !== "chg" || (j > 0 && (c.g ?? []).length) || r.gm?.[j]) break;
        pairedLeft.set(c.n, { text: r.g[j], hl: hlOld(c), address: `${pi}:${r.n}:${j}` });
        pairedCounts.set(k, j + 1);
      }
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
    const trb = state.trying;
    const inTry = trb && trb.station === s.id && trb.part === pi && r.n && line >= trb.line && line <= trb.end;
    if (inTry && line > trb.line) continue;
    const ghosts = r.g ?? [];
    for (let j = pairedCounts.get(k) ?? 0; j < ghosts.length; j++) {
      const g = ghosts[j];
      const next = rows.slice(k).find((x) => x.k === "chg" && x.was === g);
      code.append(ghostRow(g, r.gm?.[j], next ? hlOld(next) : null, split, `${pi}:${r.n}:${j}`));
    }
    if (!r.n) continue;
    if (inTry) {
      code.append(tryBox(s, p));
      continue;
    }
    const allNotes = (r.notes ?? []).filter((i) => noteCardVisible(s, s.notes[i]));
    const notes = allNotes.filter((i) => !shown.has(i));
    const sel = state.sel && state.sel.station === s.id && state.sel.part === pi && state.sel.line === line;
    const blk = inBlock(s.id, pi, line);
    const [cm, ccls] = covMarks[r.cov] ?? ["", ""];
    const dim = spotSet && !spotSet.has(line);
    const newHl = r.hl ? { ranges: [[r.hl[2], r.hl[3]]], open: '<span class="add-hl">', close: "</span>" } : null;
    const src = renderSpans(r.s, state.query, newHl) || " ";
    let left = "";
    if (split) {
      const pl = pairedLeft.get(r.n);
      const text = pl ? renderSpans(untab(pl.text), state.query, pl.hl ? { ranges: [pl.hl], open: '<span class="del-hl">', close: "</span>" } : null) :
        r.k === "add" || r.k === "chg" ? "" : src;
      left = `<span class="src left"${pl ? ` data-ghost="${pl.address}"` : ""}>${text}</span>`;
    }
    const badge = allNotes.length ? `<span class="nb k-${esc(noteStyle(s.notes[allNotes[0]])[2])}${allNotes.includes(state.note) ? " on" : ""}">${allNotes[0] + 1}${allNotes.length > 1 ? "+" : ""}</span>` : "";
    const moved = r.mv ? " moved" : "";
    const row = el(`<div class="row ${r.k ?? ""}${moved}${notes.length ? " has-note" : ""}${sel ? " sel" : ""}${blk ? " blk" : ""}${dim ? " dim" : ""}" data-part="${pi}" data-line="${line}"${r.mv ? ` title="moved here unchanged from ${esc(r.mv)}"` : ""}>
      <span class="mark">${r.mv ? "→" : ""}</span><span class="cov ${ccls}" title="${r.cov === "run" ? `${r.tn} tests run this line · click lists them (t)` : r.cov === "missed" ? "changed, and no test runs it" : ""}">${cm}</span>
      <span class="ln" title="Comment or ask the agent">${badge}${r.n}</span>${left}<span class="src${split ? " right" : ""}">${split && p.base ? "" : src}</span></div>`);
    if (r.cov === "run") {
      const cv = row.querySelector(".cov");
      cv.onmouseenter = async () => {
        if (cv.dataset.named) return;
        cv.dataset.named = "1";
        const ids = await testsAtLine(s.id, pi, line);
        const tests = state.view.coverage?.tests ?? [];
        cv.title = `${plural(ids.length, "test runs", "tests run")} line ${r.n}:\n` + ids.slice(0, 12).map((ti) => "  " + tests[ti]?.label).join("\n") + (ids.length > 12 ? `\n  and ${ids.length - 12} more` : "") + "\nclick lists them (t) · T runs them";
      };
    }
    row.onclick = (e) => {
      if (window.getSelection()?.toString()) return;
      if (e.target.classList.contains("cov") && r.cov === "run") {
        state.sel = { station: s.id, part: pi, line };
        return openTests();
      }
      if (e.target.classList.contains("nb")) return focusNote(allNotes[0]);
      selectRow(s.id, pi, line, e.target.classList.contains("ln") && !e.shiftKey, e.shiftKey);
    };
    row.onmousedown = (e) => e.shiftKey && e.preventDefault();
    code.append(row);
    const qs = questionsByRow.get(questionRowKey(s.id, pi, line));
    if (qs) code.append(diffQuestionGroup(qs));
    let replies;
    for (const i of notes) {
      shown.add(i);
      const n = s.notes[i];
      const thread = threads.get(n.qid);
      if (thread?.station === s.id && thread.part === pi && thread.at === line && threadResolved(thread) && !reader().dismissed.includes(n.key)) {
        replies ??= new Map();
        const cards = replies.get(thread.id) ?? [];
        cards.push(noteCard(s, n, i, { inThread: true }));
        replies.set(thread.id, cards);
      } else {
        code.append(noteCard(s, n, i));
      }
    }
    for (const c of reader().comments) {
      if (c.station === s.id && c.part === pi && c.at === line) code.append(commentCard(c, { replies: replies?.get(c.id) }));
    }
    for (const t of state.ghPull?.threads ?? []) {
      if (t.station === s.id && t.path === p.file && t.line === r.n && (t.side === "base") === !!p.base) code.append(threadCard(t));
    }
    for (const q of reader().questions) {
      if (q.station !== s.id || q.part !== pi || q.at !== line) continue;
      const keptOpen = localResolution(state.view.path, q) === false;
      if (!q.answered || keptOpen || threadResolved(q) || activeThread === q.id || unreadReply(state.view.path, threads.get(q.id))) {
        code.append(questionCard(q, { replies: replies?.get(q.id) }));
      }
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
  else if (!p.err && !cardsHidden(s) && !s.auto && !s.notes.some((n) => n.part === pi)) bits.push(`<span class="m-chg">no agent notes, read it yourself</span>`);
  return bits.map((b) => (b.startsWith("<") ? b : esc(b))).join(" · ");
}

function partBox(s, pi, from, to, title, opts = {}) {
  const p = partData(s, pi);
  const k = partKey(s, pi);
  const extra = state.parts[k] ? (state.expand[k] ?? 0) : 0;
  const whole = !!state.parts[k] && !!state.whole[k];
  const start = whole ? p.start : opts.step ? Math.max(p.start, from - extra) : from;
  const end = whole ? p.end : opts.step ? Math.min(p.end, to + extra) : to;
  const folded = !opts.step && !state.unfold && p.folds?.length;
  const wholeShown = start === 0 && end >= p.len - 1 && !folded;
  const tryStart = opts.step ? from : s.parts[pi].start;
  const tryEnd = opts.step ? to : s.parts[pi].end;
  const changes = p.base || p.err ? [] : p.rows.filter((r) => r.n && r.n - 1 >= tryStart && r.n - 1 <= tryEnd && (r.k === "add" || r.k === "chg"));
  const part = el(`<section class="part${p.background ? " bgpart" : ""}">
    <div class="part-head"><span class="file">${esc(title ?? p.file)}</span><span class="label">${title ? (p.baseFile ? "renamed from " + esc(p.baseFile) : "") : partTitle(s, pi, p)}</span>
    <span class="spacer"></span>
    ${!p.err && state.view.coverage && p.rows?.some((r) => r.cov === "run") ? `<button class="na run-btn" data-run="1" title="Run the tests that run this part's changed lines">▶ tests</button>` : ""}
    ${changes.length ? `<button class="na" data-try="1" title="Edit the reviewed changes and see whether the tests notice">✎ try a change</button>` : ""}
    ${p.err ? "" : `<button class="na" data-w="-1" title="Less context (-)"${whole || !extra ? " disabled" : ""}>−</button><button class="na" data-w="1" title="More context (+)"${whole || wholeShown ? " disabled" : ""}>+</button>
    <button class="na${whole ? " on" : ""}" data-f="1" aria-pressed="${whole}" title="${whole ? "Return to the reviewed range" : "Show the whole file"} (f)"${!whole && wholeShown ? " disabled" : ""}>${whole ? "Back to change" : wholeShown ? "Whole file shown" : "Whole file"}</button>`}</div></section>`);
  part.querySelectorAll("[data-w]").forEach((b) => (b.onclick = () => widen(s, pi, +b.dataset.w)));
  part.querySelector("[data-f]")?.addEventListener("click", () => wholeFile(s, pi));
  part.querySelector("[data-run]")?.addEventListener("click", () => runFor(s, pi));
  part.querySelector("[data-try]")?.addEventListener("click", () => startTry(null, { station: s.id, part: pi, line: changes[0].n - 1, end: changes.at(-1).n - 1 }));
  if (p.about && opts.about !== false && (p.hunk ?? 0) <= 1) part.append(el(`<div class="part-about">${prose(p.about)}</div>`));
  if (p.err) {
    part.append(el(`<div class="part-err">${esc(p.err)}</div>`));
    return part;
  }
  part.append(codeRows(s, pi, start, end, { ...opts, whole }));
  return part;
}

async function widen(s, pi, dir) {
  const view = state.view;
  try {
    const step = await stepSize();
    if (state.view !== view || cur() !== s) return;
    const { extra, whole } = requestedPart(s, pi);
    if (whole) return toast("Return to the change to adjust its context.");
    const next = Math.max(0, extra + dir * step);
    if (next !== extra) await loadPart(s, pi, next, false);
  } catch (e) {
    if (state.view === view) toast(errText(e), true);
  }
}

function wholeFile(s, pi) {
  const { extra, whole } = requestedPart(s, pi);
  return loadPart(s, pi, extra, !whole);
}

let expandStep;
function stepSize() {
  expandStep ??= api("Reviews.ExpandStep").catch((e) => {
    expandStep = undefined;
    throw e;
  });
  return expandStep;
}

const partLoads = new Map();
function requestedPart(s, pi) {
  const k = partKey(s, pi);
  const pending = partLoads.get(k);
  return pending?.view === state.view ? pending : { extra: state.expand[k] ?? 0, whole: !!state.whole[k] };
}

async function loadPart(s, pi, extra, whole, refresh = false) {
  const view = state.view;
  const k = partKey(s, pi);
  if (refresh && partLoads.get(k)?.view === view) return false;
  const request = { view, extra, whole };
  partLoads.set(k, request);
  try {
    const part = extra || whole ? await api("Reviews.Lines", s.id, pi, extra, whole, view.version) : null;
    if (state.view !== view || partLoads.get(k) !== request) return false;
    state.expand[k] = extra;
    state.whole[k] = whole;
    if (part) state.parts[k] = { ...part, viewVersion: view.version };
    else delete state.parts[k];
    if (!refresh && cur() === s) renderKeepScroll();
    return true;
  } catch (e) {
    if (state.view === view && partLoads.get(k) === request) {
      if (refresh) delete state.parts[k];
      else toast(errText(e), true);
    }
    return false;
  } finally {
    if (partLoads.get(k) === request) partLoads.delete(k);
  }
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
  const card = el(`<div class="note ${esc(kind)}${i === state.note ? " sel" : ""}${marks.reviewed ? " is-read" : ""}${marks.flagged ? " is-flagged" : ""}${opts.static ? " static" : ""}" data-note="${i}" tabindex="-1">
    ${n.q ? `<div class="q">${n.qid?.startsWith("c") ? "Your comment " + esc(n.qid) : "You asked" + (n.qid ? " " + esc(n.qid) : "")}: ${prose(n.q)}</div>` : ""}
    <span class="nnum k-${esc(noteStyle(n)[2])}">${i + 1}</span><span class="nk">${esc(noteLabel(n))}</span>${tags ? `<span class="tags">${esc(tags)}</span>` : ""}
    ${state.fresh.has(n.key) ? `<span class="badge fresh">new</span>` : ""}${changed ? `<span class="badge fresh" title="the agent changed this note since you read it">Δ changed</span>` : ""}
    ${n.problem ? "" : where ? `<span class="tags"> · ${esc(where)}</span>` : ""}
    <div>${prose(n.text)}</div>
    ${n.evidence ? `<div class="ev">Evidence: ${prose(n.evidence)}</div>` : ""}
    ${n.changed ? `<div class="ev">Changed: ${prose(n.changed)}</div>` : ""}
    ${n.refBy?.length ? `<div class="ev refby">Referenced by ${n.refBy.map((r) => prose(r)).join(", ")}</div>` : ""}
    ${n.problem ? `<div class="ev problems">${esc(n.problem)} — anchored at “${esc(n.at)}”</div>` : ""}
    <div class="note-actions"></div></div>`);
  if (n.qid) {
    card.dataset.discussion = n.qid;
    card.dataset.discussionReply = "";
  }
  card.onclick = () => {
    state.fresh.delete(n.key);
    if (opts.jump) return opts.jump();
    focusNote(i);
    const thread = threads.get(n.qid);
    if (thread?.reply?.kind === "note") openedReply(thread);
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
  if (n.kind === "issue" && !n.unbacked && marks.flagged) {
    const sent = fixSent(n.key);
    if (sent) actions.append(el(`<span class="muted small">sent to fix as ${esc(sent.id)}</span>`));
    else btn("Have it fixed", "Ask the agent to fix every problem you agreed with (P)", false, sendFixes);
  }
  if (n.kind === "decide") btn(marks.reviewed ? "☑ Decided" : "☐ Decided", "You made this call (enter)", marks.reviewed, () => act("MarkNote", n.key, "reviewed"));
  if ((n.kind === "question" || n.unbacked) && !n.q && n.part >= 0) {
    btn(answered ? "✓ Answered" : "Answer", "Answer the agent's question in a comment", answered, () => openComposer(s.id, n.part, n.line, { answers: n.key }));
  }
  btn(marks.dismissed ? "Restore" : "Dismiss", "Dismiss or restore this note (x)", marks.dismissed, () => markDismissed(n));
  btn("Copy", "Copy the note (y)", false, () => copy(noteText(s, i), "note " + (i + 1)));
  if (marks.dismissed) return reviewDisclosure(card, `note:${n.key}`, "dismissed note " + (i + 1), { thread: n.qid });
  const thread = threads.get(n.qid);
  if (thread && !opts.inThread) {
    actions.append(localResolveAction(thread));
    return threadDisclosure(card, thread, "answer");
  }
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
  if (n && n.part >= 0 && lineVisible(s, n.part, n.line)) {
    state.sel = { station: s.id, part: n.part, line: n.line };
    $$("#station .row.sel, #station .kcur").forEach((r) => r.classList.remove("sel", "kcur"));
    document.querySelector(`#station .row[data-part="${n.part}"][data-line="${n.line}"]`)?.classList.add("sel");
    renderStatus();
  }
  $$("#station .note.sel").forEach((x) => x.classList.remove("sel"));
  const card = document.querySelector(`#station .note[data-note="${i}"]`);
  if (!card) return;
  card.classList.add("sel");
  const target = card.closest(".review-disclosure:not([open])")?.firstElementChild ?? card;
  target.scrollIntoView({ block: "center", behavior: "smooth" });
}

function stepNote(dir) {
  const s = cur();
  if (!s) return;
  const order = s.notes.map((n, i) => [n, i]).filter(([n]) => navigableNote(s, n)).sort(([a], [b]) => a.part - b.part || a.line - b.line).map(([, i]) => i);
  const at = order.indexOf(state.note);
  const next = at < 0 ? (dir > 0 ? 0 : order.length - 1) : at + dir;
  if (next >= 0 && next < order.length) return focusNote(order[next]);
  for (let si = state.station + dir; si >= 0 && si < state.view.stations.length; si += dir) {
    const t = state.view.stations[si];
    const vis = t.notes.map((n, i) => [n, i]).filter(([n]) => navigableNote(t, n)).sort(([a], [b]) => a.part - b.part || a.line - b.line);
    if (!vis.length) continue;
    goStation(si);
    return focusNote(dir > 0 ? vis[0][1] : vis[vis.length - 1][1]);
  }
}

function selectedNote() {
  const s = cur();
  const n = s?.notes[state.note];
  return n && matchesNoteFilter(n) ? n : undefined;
}

function noteMark(mark) {
  const n = selectedNote();
  if (!n) return toast("No note selected: click one, or step to one with ] and [.");
  if (mark === "dismissed") return markDismissed(n);
  return act("MarkNote", n.key, mark);
}

async function markDismissed(n) {
  const view = state.view, s = cur();
  const key = `note:${n.key}`;
  const was = reader().dismissed.includes(n.key);
  expandedDisclosures.delete(key);
  if (!await act("MarkNote", n.key, "dismissed") || state.view !== view || cur() !== s) return;
  const i = s.notes.indexOf(n);
  if (was) {
    const card = $(`#station .note[data-note="${i}"]`);
    if (card) focusDiscussion(card);
  } else {
    state.note = i;
    disclosureSummary(key)?.focus({ preventScroll: true });
  }
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
    if (noteCardVisible(s, n) && !reader().dismissed.includes(n.key)) blocks.push(noteText(s, i).trimEnd());
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
  if (cardsHidden(s)) return toast("Reveal notes on this stop first (v).");
  if (all) {
    if (s.kind !== "code") return toast("Y copies a code stop; open one first.");
    return copy(stopText(s), "stop " + s.id);
  }
  if (!selectedNote()) return toast("No note selected.");
  copy(noteText(s, state.note), "note " + (state.note + 1));
}

function commentCard(c, opts = {}) {
  const body = c.text.replace(/^[a-z]+(?: \([a-z, -]+\))?: /, "");
  const card = el(`<div class="comment${c.draft ? " draft" : ""}${c.blocking ? " blocking" : ""}${opts.static ? " static" : ""}" data-discussion="${esc(c.id)}" tabindex="-1">
    ${opts.where ? `<div class="muted small">${esc(c.file)}:${c.line}</div>` : ""}
    <span class="ck">${esc(c.id)} · you${c.label ? " · " + esc(c.label) : ""}${c.blocking ? " (blocking)" : ""}</span>
    <span class="badge">${c.fix ? (c.resolved ? "fixed" : "fix sent") : c.resolved ? "resolved" : c.draft ? "draft" : "sent"}</span>
    ${c.github ? `<button class="badge gh-on" title="Open it on GitHub">on GitHub ↗</button>` : c.forGitHub ? `<span class="badge gh-picked">in GitHub review</span>` : ""}
    ${c.github && ghMine(c)?.resolved ? `<span class="badge gh-resolved">resolved on GitHub</span>` : ""}
    <div>${prose(body)}</div></div>`);
  if (c.github) card.append(...githubThread(c));
  const ghLink = card.querySelector(".gh-on");
  if (ghLink) {
    ghLink.onclick = (e) => {
      e.stopPropagation();
      openURL(c.github);
    };
  }
  if (c.draft && state.editing?.id === c.id) {
    card.querySelector(":scope > div:last-of-type").replaceWith(editBox(c));
    card.classList.add("editing");
    if (opts.jump) card.onclick = opts.jump;
    return card;
  }
  const actions = el(`<div class="note-actions"></div>`);
  if (c.draft) {
    if (!c.github) {
      const edit = el(`<button class="na" title="Reword this draft before you send it (e)">Edit</button>`);
      edit.onclick = (e) => {
        e.stopPropagation();
        startEdit(c);
      };
      actions.append(edit);
    }
    const del = el(`<button class="na" title="Delete this draft (x in the recap)">Delete</button>`);
    del.onclick = (e) => {
      e.stopPropagation();
      act("DeleteComment", c.id);
    };
    actions.append(del);
  }
  actions.append(localResolveAction(c), ...githubActions(c), ...resolveActions(c));
  card.append(actions);
  if (opts.jump) card.onclick = opts.jump;
  return threadDisclosure(card, c, "comment", opts);
}

// startEdit opens a draft comment's words in its card; one draft is edited at a time.
function startEdit(c) {
  if (state.editing && state.editing.id !== c.id && state.editing.text !== state.editing.was)
    return toast("Finish or cancel the comment you are editing first.");
  state.editing = { id: c.id, text: c.body ?? c.text, was: c.body ?? c.text };
  renderKeepScroll();
  const ta = document.querySelector(".comment.editing textarea");
  ta?.focus();
  ta?.setSelectionRange(ta.value.length, ta.value.length);
}

function editBox(c) {
  const ed = state.editing;
  const box = el(`<div class="composer edit-box">
    <textarea rows="3"></textarea>
    <div class="cp-foot"><span class="muted small">Still a draft until you send the comments. Ctrl+Enter saves, Esc cancels.</span>
      <span class="spacer"></span><button class="small cp-cancel">Cancel</button><button class="primary small cp-save">Save draft</button></div></div>`);
  box.onclick = (e) => e.stopPropagation();
  const ta = box.querySelector("textarea");
  ta.value = ed.text;
  ta.oninput = () => (ed.text = ta.value);
  const done = () => {
    state.editing = null;
    renderKeepScroll();
  };
  const save = async () => {
    if (await act("EditComment", c.id, ta.value)) {
      toast(`${c.id} reworded; S sends every draft to the agent`);
      done();
    }
  };
  ta.onkeydown = (e) => {
    e.stopPropagation();
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      save();
    }
    if (e.key === "Escape") {
      e.preventDefault();
      done();
    }
  };
  box.querySelector(".cp-save").onclick = save;
  box.querySelector(".cp-cancel").onclick = done;
  return box;
}

// editKey is e on a draft comment: the one picked in the recap, or the last draft on the selected
// line. It reports false when there is none, so e keeps its other job.
function editKey(s, sel) {
  const k = document.querySelector("#station .kcur[data-comment]");
  let c = k ? reader().comments.find((x) => x.id === k.dataset.comment) : null;
  if (c && (!c.draft || c.github)) {
    toast(`${c.id} was ${c.github ? "posted to GitHub" : "sent"} already; write a new comment instead`);
    return true;
  }
  if (!c && sel) {
    const p = s.parts[sel.part];
    c = reader().comments.filter((x) => x.draft && x.station === s.id && x.file === p?.file && x.line === sel.line + 1 && (x.side === "base") === !!p?.base).pop();
  }
  if (!c) return false;
  startEdit(c);
  return true;
}

function questionCard(q, opts = {}) {
  const card = el(`<div class="comment question${opts.static ? " static" : ""}" data-discussion="${esc(q.id)}" tabindex="-1">
    ${opts.where ? `<div class="muted small">${esc(q.file)}:${q.line}</div>` : ""}
    <span class="ck">${esc(q.id)} · you asked</span><span class="badge">${q.answered ? "answered" : state.agentLive ? "waiting for the agent" : "saved; start the agent"}</span>
    <div>${prose(q.text)}</div><div class="note-actions"></div></div>`);
  card.querySelector(".note-actions").append(localResolveAction(q));
  if (opts.jump) card.onclick = opts.jump;
  return threadDisclosure(card, q, "question", opts);
}

const expandedDisclosures = new Set();
const threadKey = (q) => `thread:${q.id}`;
const disclosureSummary = (key, scope = $("#station")) => scope?.querySelector(`[data-disclosure="${CSS.escape(key)}"] > summary`);
const threadResolved = (q) => localResolution(state.view.path, q) ?? (q.resolved || ghMine(q)?.resolved);

function reviewDisclosure(card, key, label, { thread, replies = [] } = {}) {
  const details = el(`<details class="review-disclosure" data-disclosure="${esc(key)}"><summary>
    <span class="disclosure-show">Show</span><span class="disclosure-hide">Hide</span> ${esc(label)}
    ${thread ? '<span class="badge fresh disclosure-unread hidden">New reply</span>' : ""}</summary></details>`);
  if (thread) details.dataset.disclosureThread = thread;
  if (card.matches(".static")) details.classList.add("static");
  details.open = expandedDisclosures.has(key);
  details.ontoggle = () => {
    if (!details.isConnected) return;
    if (details.open) expandedDisclosures.add(key);
    else expandedDisclosures.delete(key);
  };
  details.append(card, ...replies);
  return details;
}

function threadDisclosure(card, q, type, opts = {}) {
  if (!threadResolved(q)) return card;
  const id = /^[cq]\d+$/.test(q.id) ? " " + q.id : "";
  const where = opts.where ? ` · ${base(q.file)}:${q.line}` : "";
  return reviewDisclosure(card, threadKey(q), `resolved ${type}${id}${where}`, { thread: q.id, replies: opts.replies });
}

function localResolveAction(q) {
  const path = state.view.path;
  const resolved = threadResolved(q);
  const button = el(`<button class="na local-resolve">${resolved ? "Unresolve locally" : "Resolve"}</button>`);
  button.title = resolved ? "Keep this discussion expanded locally" : "Collapse this discussion locally";
  button.onclick = (e) => {
    e.stopPropagation();
    if (followUpDrafts.has(JSON.stringify([path, q.id]))) return toast("Finish or cancel your follow-up before resolving.");
    const key = threadKey(q);
    expandedDisclosures.delete(key);
    setLocalResolution(path, q, !resolved);
    const list = button.closest(".diff-questions-list");
    renderKeepScroll();
    if (list) {
      renderDiffQuestionList(list, path, list.dataset.selected);
      syncDiscussionUI();
    }
    const scope = list ?? $("#station");
    const target = disclosureSummary(key, scope) ?? scope.querySelector(`[data-discussion="${CSS.escape(q.id)}"]`);
    target?.focus({ preventScroll: true });
  };
  return button;
}

let diffQuestions = [], questionFetch = 0;
const followUpDrafts = new Map();
const questionsByRow = new Map();
const renderedQuestionGroups = new WeakMap();
const questionRowKey = (station, part, line) => JSON.stringify([station, part, line]);
const rowAt = (part, line) => $(`#station .row[data-part="${part}"][data-line="${line}"]`);
const questionLastLine = (q) => q.at + q.end - q.line;
const questionWhere = (q) => `${q.file}:${q.line}${q.end > q.line ? `–${q.end}` : ""}${q.side === "base" ? " (base)" : ""}`;
const questionStatus = { queued: "Queued", answering: "Answering", answered: "Answered", failed: "Failed", interrupted: "Interrupted" };

let commentSections = {}, threads = new Map(), replyFetch = 0;
let readingOrigin = null, activeThread = "", activeReply = false, discussionBusy = false;
const sentComments = (view) => view.reader.comments.filter((c) => !c.draft).map((c) => c.id).join(",");

function indexDiscussions() {
  threads = discussionThreads(state.view, diffQuestions, commentSections);
  if (activeThread && !threads.has(activeThread)) activeThread = "";
}

async function refreshCommentReplies(view) {
  const request = ++replyFetch;
  if (!sentComments(view)) {
    commentSections = {};
    return;
  }
  try {
    const { sections } = await api("Reviews.Answers");
    if (state.view !== view || request !== replyFetch || JSON.stringify(sections) === JSON.stringify(commentSections)) return;
    commentSections = sections;
    indexDiscussions();
    syncDiscussionUI();
  } catch (e) {
    if (state.view === view && request === replyFetch) toast(errText(e), true);
  }
}

function discussionNavigation() {
  const bar = el(`<span class="discussion-nav hidden">
    <button class="small" data-next-ask title="Next comment or question in this stop">Next ask</button>
    <button class="small" data-next-reply title="Next reply in this stop">Next reply</button>
    <button class="small" data-reading-back>Back to reading</button></span>`);
  bar.querySelector("[data-next-ask]").onclick = () => nextDiscussion(false);
  bar.querySelector("[data-next-reply]").onclick = () => nextDiscussion(true);
  bar.querySelector("[data-reading-back]").onclick = backToReading;
  return bar;
}

function addDiscussionNavigation(modal) {
  const head = modal.querySelector(".modal-head");
  head.insertBefore(discussionNavigation(), head.querySelector("button"));
}

function nextDiscussionTarget(replies) {
  const s = cur();
  if (s?.kind !== "code" || (activeThread && threads.get(activeThread)?.station !== s.id)) return null;
  const rank = (part, line) => !guided() ? 0 : part < 0 ? Infinity :
    [line, s.parts[part]?.start].map((at) => stepOf(s, part, at)).find((i) => i >= 0) ?? Infinity;
  const sel = state.sel?.station === s.id ? state.sel : null;
  const from = sel ? [rank(sel.part, sel.line), sel.part, sel.line] : [guided() ? state.step : 0, -1, -1];
  const path = state.view.path;
  return nextThread(threads, s.id, replies, replies && !activeReply ? "" : activeThread, from, rank,
    (q) => !threadResolved(q) || (replies && unreadReply(path, q)));
}

function updateDiscussionControls() {
  const asks = [...threads.values()].some((q) => q.station === cur()?.id);
  const canAsk = asks && !!nextDiscussionTarget(false);
  const canReply = asks && !!nextDiscussionTarget(true);
  for (const bar of $$(".discussion-nav")) {
    bar.classList.toggle("hidden", !asks && !readingOrigin);
    const back = bar.querySelector("[data-reading-back]");
    back.classList.toggle("hidden", !readingOrigin);
    back.disabled = discussionBusy;
    bar.querySelector("[data-next-ask]").disabled = !canAsk || discussionBusy;
    bar.querySelector("[data-next-reply]").disabled = !canReply || discussionBusy;
  }
  toolbarOverflow.schedule();
}

function syncDiscussionUI() {
  if (!state.view) return;
  const path = state.view.path;
  const unread = new Set(), stops = new Map();
  for (const q of threads.values()) if (unreadReply(path, q)) {
    unread.add(q.id);
    stops.set(q.station, (stops.get(q.station) ?? 0) + 1);
  }
  for (const badge of $$("[data-reply-stop]")) {
    const n = stops.get(badge.dataset.replyStop) ?? 0;
    badge.classList.toggle("hidden", !n);
    badge.textContent = n === 1 ? "New reply" : `${n} new replies`;
  }
  for (const details of $$("[data-disclosure-thread]")) {
    details.querySelector(".disclosure-unread").classList.toggle("hidden", !unread.has(details.dataset.disclosureThread));
  }
  for (const card of $$("[data-discussion]")) {
    const id = card.dataset.discussion, q = threads.get(id);
    if (!q) continue;
    for (const answer of card.querySelectorAll("[data-question-turn]")) {
      const turn = questionReply(path, q, answer.dataset.questionTurn);
      answer.querySelector(".turn-unread").classList.toggle("hidden", !unreadReply(path, turn));
    }
    card.classList.toggle("unread-reply", unread.has(id));
    let badge = card.querySelector(":scope > .reply-badge");
    if (q.kind !== "diff" && unread.has(id) && !badge) {
      badge = el(`<span class="badge fresh reply-badge">New reply</span>`);
      card.querySelector(":scope > .ck, :scope > .nk")?.after(badge);
    } else if (!unread.has(id)) badge?.remove();
    const action = card.querySelector(":scope > .jump-reply");
    if (q.reply && !action) {
      const button = el(`<button class="small jump-reply">Jump to reply</button>`);
      button.onclick = (e) => {
        e.stopPropagation();
        jumpDiscussion(path, id, true);
      };
      card.append(button);
    } else if (!q.reply) action?.remove();
  }
  updateDiscussionControls();
}

function openedReply(q, turnID) {
  if (!q) return;
  const path = state.view.path;
  const target = q.kind === "diff" ? questionReply(path, q, turnID) : q;
  if (!target) return;
  if (activeThread === q.id) activeReply = true;
  for (const alert of $$("#question-alerts .question-alert")) {
    if (alert.dataset.replyReview === path && alert.dataset.replyId === q.id && alert.dataset.replyTurn === target.id) alert.remove();
  }
  if (!unreadReply(path, target)) return updateDiscussionControls();
  readReply(path, target);
  syncDiscussionUI();
}

export async function openDiscussionAnswers(id = "") {
  const view = state.view;
  if (!view) return false;
  const opened = await openAnswers(id, discussionNavigation(), (id, section) => {
    if (state.view?.path !== view.path) return;
    const q = threads.get(id), text = agentReply(section);
    if (q && text) openedReply({ ...q, reply: { kind: "section", text } });
  });
  if (opened) updateDiscussionControls();
  return opened;
}

function nextDiscussion(replies) {
  const q = nextDiscussionTarget(replies);
  if (q) return jumpDiscussion(state.view.path, q.id, replies);
}

function allowReviewViewChange() {
  if (state.compose?.text.trim() || state.compose?.pending || (state.editing && state.editing.text !== state.editing.was) || state.trying || hasAnswerDraft()) {
    toast("Save or cancel your current edit first.");
    return false;
  }
  return true;
}

function beginDiscussionAction() {
  if (discussionBusy || !allowReviewViewChange()) return false;
  discussionBusy = true;
  updateDiscussionControls();
  return true;
}

async function jumpDiscussion(review, id, reply, turnID) {
  if (!beginDiscussionAction()) return false;
  try {
    if (!readingOrigin && state.view && !$("#reviewer").classList.contains("hidden")) {
      queueReadingPosition(0, true);
      readingOrigin = captureReadingOrigin();
    }
    if (state.view?.path !== review && !await openReviewFile(review, false, true)) return false;
    hooks.showReviewer();
    closeModal();
    const view = state.view;
    if (!threads.has(id) || (turnID && !questionReply(review, threads.get(id), turnID))) await refreshDiffQuestions(view);
    const q = state.view === view && threads.get(id);
    const target = reply && q?.kind === "diff" ? questionReply(review, q, turnID) : null;
    if (!q || (turnID && !target)) {
      toast("This discussion could not be opened.", true);
      return false;
    }
    activeThread = id;
    activeReply = false;
    const i = view.stations.findIndex((s) => s.id === q.station);
    if (i >= 0) {
      const s = view.stations[i];
      const part = s.parts[q.part];
      const last = q.kind === "diff" ? questionLastLine(q) : q.at;
      const step = guided() && part ? stepOf(s, q.part, last) : 0;
      const before = step < 0 ? stepOf(s, q.part, part.start) : step;
      await goStation(i, -1, Math.max(0, before));
      if (part && (!rowAt(q.part, q.at) || !rowAt(q.part, last))) await loadPart(s, q.part, 0, true);
      if (state.view !== view || cur()?.id !== s.id) {
        toast("The review changed. Try the jump again.");
        return false;
      }
      if (part && rowAt(q.part, q.at)) {
        selectRow(s.id, q.part, q.at, false);
        if (last > q.at) {
          state.block = { station: s.id, part: q.part, anchor: q.at, from: q.at, to: last };
          markBlock();
        }
      }
    }
    if (reply && q.reply?.kind === "section" && await openDiscussionAnswers(id)) return true;
    const idSelector = `[data-discussion="${CSS.escape(id)}"]`;
    const card = $(reply && q.reply?.kind === "note" ? `#station ${idSelector}[data-discussion-reply]` : `#station ${idSelector}:not([data-discussion-reply])`);
    if (card) {
      focusDiscussion(card, target?.id);
      if (reply) openedReply(q, target?.id);
    } else if (q.kind === "diff") {
      openDiffQuestions(id, target?.id);
      if (reply) openedReply(q, target?.id);
    } else {
      showSavedDiscussion(q);
      if (reply) openedReply(q);
    }
    return true;
  } finally {
    discussionBusy = false;
    updateDiscussionControls();
  }
}

function discussionFocus() {
  const active = document.activeElement;
  if (active?.matches(".review-disclosure > summary")) {
    return { disclosure: active.parentElement.dataset.disclosure, modal: !!active.closest(".diff-questions-list") };
  }
  const card = active?.closest("#station [data-discussion], .diff-questions-list [data-discussion]");
  if (!card) return null;
  const caret = active.matches(".question-follow-up textarea") ? [active.selectionStart, active.selectionEnd] : null;
  const turn = active.dataset.questionTurn;
  if (active !== card && !turn && !caret) return null;
  return { id: card.dataset.discussion, reply: card.hasAttribute("data-discussion-reply"), turn, caret, modal: !!card.closest(".diff-questions-list") };
}

function restoreDiscussionFocus(focus) {
  if (!focus) return;
  const scope = focus.modal ? ".diff-questions-list" : "#station";
  if (focus.disclosure) return disclosureSummary(focus.disclosure, $(scope))?.focus({ preventScroll: true });
  const replySelector = focus.reply ? "[data-discussion-reply]" : ":not([data-discussion-reply])";
  const card = $(`${scope} [data-discussion="${CSS.escape(focus.id)}"]${replySelector}`);
  const active = focus.turn ? card?.querySelector(`[data-question-turn="${CSS.escape(focus.turn)}"]`) :
    focus.caret ? card?.querySelector(".question-follow-up textarea") : card;
  active?.focus({ preventScroll: true });
  if (focus.caret) active?.setSelectionRange(...focus.caret);
}

function focusDiscussion(card, turnID) {
  const details = card.closest(".review-disclosure");
  if (details) {
    expandedDisclosures.add(details.dataset.disclosure);
    details.open = true;
  }
  const target = turnID && card.querySelector(`[data-question-turn="${CSS.escape(turnID)}"]`);
  (target || card).focus({ preventScroll: true });
  (target || card).scrollIntoView({ block: target ? "start" : "center" });
}

function showSavedDiscussion(q) {
  const card = el(`<div class="saved-discussion" data-discussion="${esc(q.id)}" tabindex="-1">
    <div class="muted small">${esc(questionWhere(q))}</div><p>${prose(q.body ?? q.text)}</p>
    ${q.needle ? `<pre class="md-code">${esc(q.needle)}</pre>` : ""}
    ${q.reply ? `<div class="diff-question-answer">${md(q.reply.text)}</div>` : ""}</div>`);
  addDiscussionNavigation(openModal(q.kind === "comment" ? "Comment" : "Question", card, "narrow"));
  syncDiscussionUI();
  focusDiscussion(card);
}

async function refreshDiffQuestions(view) {
  if (!view) return;
  const request = ++questionFetch;
  try {
    const questions = await api("Reviews.DiffQuestions", view.version);
    if (state.view !== view || request !== questionFetch || JSON.stringify(questions) === JSON.stringify(diffQuestions)) return;
    const focus = discussionFocus();
    diffQuestions = questions;
    indexDiscussions();
    questionsByRow.clear();
    for (const q of questions) {
      if (q.part < 0) continue;
      const key = questionRowKey(q.station, q.part, questionLastLine(q));
      if (!questionsByRow.has(key)) questionsByRow.set(key, []);
      questionsByRow.get(key).push(q);
    }
    $("#diff-questions-btn")?.classList.toggle("hidden", !questions.length);
    const groups = new Map($$("#station .diff-question-row").map((g) => [g.dataset.questionRow, g]));
    for (const [key, group] of groups) if (!questionsByRow.has(key)) group.remove();
    for (const [key, qs] of questionsByRow) {
      const q = qs[0];
      if (q.station !== cur()?.id) continue;
      const group = groups.get(key);
      if (group) {
        if (JSON.stringify(renderedQuestionGroups.get(group)) !== JSON.stringify(qs)) group.replaceWith(diffQuestionGroup(qs));
      } else rowAt(q.part, questionLastLine(q))?.after(diffQuestionGroup(qs));
    }
    const list = $(".diff-questions-list");
    if (list?.dataset.review === view.path) renderDiffQuestionList(list, view.path, list.dataset.selected);
    syncDiscussionUI();
    restoreDiscussionFocus(focus);
  } catch (e) {
    if (state.view === view && request === questionFetch && !errText(e).includes("the review changed")) toast(errText(e), true);
  }
}

function diffQuestionGroup(qs) {
  const q = qs[0];
  const group = el(`<div class="diff-question-row"></div>`);
  group.dataset.questionRow = questionRowKey(q.station, q.part, questionLastLine(q));
  renderedQuestionGroups.set(group, qs);
  group.append(...qs.map((item) => diffQuestionCard(item)));
  return group;
}

function diffQuestionCard(q, review = "", selected = false) {
  const path = review || state.view.path;
  const card = el(`<article class="comment question${review ? " static" : ""}" tabindex="-1">
    ${review ? `<div class="muted small">${esc(questionWhere(q))}</div>` : ""}
    <div class="question-thread"></div>
    ${review ? `<details${selected || q.part < 0 ? " open" : ""}><summary>Selected code</summary><pre class="md-code">${esc(q.excerpt)}</pre></details>` : ""}
    </article>`);
  if (!review) card.dataset.diffQuestion = q.id;
  card.dataset.discussion = q.id;
  const turns = questionTurns(q);
  const list = card.querySelector(".question-thread");
  for (const turn of turns) {
    const item = el(`<section class="question-exchange">
      <span class="ck">${turn.id === q.id ? "You asked" : "You replied"}</span>
      ${turn.status === "answered" ? "" : `<span class="badge">${esc(questionStatus[turn.status])}</span>`}
      <div>${prose(turn.text)}</div>
      ${turn.problem ? `<div class="muted small">${esc(turn.problem)}</div>` : ""}
      </section>`);
    if (turn.answer) {
      const answer = el(`<div class="diff-question-answer" tabindex="0" data-question-turn="${esc(turn.id)}">
        <span class="ck">Agent</span><span class="badge fresh turn-unread hidden">New reply</span>${md(turn.answer)}</div>`);
      const read = () => { if (state.view?.path === path) openedReply(threads.get(q.id), turn.id); };
      answer.addEventListener("click", read);
      answer.addEventListener("focus", read);
      item.append(answer);
    }
    list.append(item);
  }
  if (review && !q.answer && !selected) {
    const jump = el(`<button class="small">Jump to question</button>`);
    jump.onclick = () => jumpDiscussion(review, q.id, false);
    card.append(jump);
  }
  const latest = turns.at(-1);
  const actions = el(`<div class="question-thread-actions"></div>`);
  actions.append(localResolveAction(q));
  if (latest.status === "failed" || latest.status === "interrupted") {
    const retry = el(`<button class="small">Retry</button>`);
    retry.onclick = async () => {
      const view = state.view;
      if (retry.disabled || view?.path !== path) return;
      retry.disabled = true;
      try {
        await api("Reviews.RetryQuestionTurn", q.id, latest.id, view.version);
        if (state.view?.path === path) await refreshDiffQuestions(state.view);
      } catch (e) {
        toast(errText(e), true);
      } finally {
        retry.disabled = false;
      }
    };
    actions.append(retry);
  }
  if (q.answer && latest.status !== "queued" && latest.status !== "answering") actions.append(followUpComposer(q.id, path));
  if (actions.childElementCount) card.append(actions);
  return threadDisclosure(card, q, "question", { where: !!review });
}

function followUpComposer(thread, path) {
  const key = JSON.stringify([path, thread]);
  const wrapper = el(`<div class="question-follow-up"></div>`);
  function draw(focus = false) {
    wrapper.replaceChildren();
    const draft = followUpDrafts.get(key);
    if (!draft) {
      const reply = el(`<button class="small">Reply</button>`);
      reply.onclick = () => {
        followUpDrafts.set(key, { text: "", pending: false });
        draw(true);
      };
      wrapper.append(reply);
      if (focus) reply.focus();
      return;
    }
    const form = el(`<form class="ans-reply" data-follow-up-thread="${esc(thread)}">
      <textarea rows="3" aria-label="Follow-up question" placeholder="Ask a follow-up"></textarea>
      <div class="ans-bar"><span class="spacer"></span><button class="small" type="button">Cancel</button>
      <button class="small primary" type="submit">Send</button></div></form>`);
    const textarea = form.querySelector("textarea");
    const [cancel, send] = form.querySelectorAll("button");
    const updateControls = () => {
      textarea.readOnly = cancel.disabled = draft.pending;
      send.disabled = draft.pending || !draft.text.trim();
    };
    textarea.value = draft.text;
    textarea.oninput = () => {
      draft.text = textarea.value;
      updateControls();
    };
    const close = () => {
      if (draft.pending) return;
      followUpDrafts.delete(key);
      draw(true);
    };
    cancel.onclick = close;
    textarea.onkeydown = (e) => {
      if (e.isComposing) return;
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        close();
      } else if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        form.requestSubmit();
      }
    };
    form.onsubmit = async (e) => {
      e.preventDefault();
      const view = state.view;
      if (draft.pending || !draft.text.trim() || view?.path !== path) return;
      draft.pending = true;
      updateControls();
      try {
        await api("Reviews.FollowUpQuestion", thread, draft.text, view.version);
        followUpDrafts.delete(key);
        if (state.view?.path === path) await refreshDiffQuestions(state.view);
      } catch (e) {
        if (state.view?.path === path) toast(errText(e), true);
      } finally {
        draft.pending = false;
        if (state.view?.path === path) {
          for (const current of $$(`[data-follow-up-thread="${CSS.escape(thread)}"]`)) {
            if (followUpDrafts.has(key)) {
              current.querySelector("textarea").readOnly = false;
              current.querySelector('button[type="button"]').disabled = false;
              current.querySelector('button[type="submit"]').disabled = !draft.text.trim();
            } else current.remove();
          }
        }
      }
    };
    updateControls();
    wrapper.append(form);
    if (focus) textarea.focus();
  }
  draw();
  return wrapper;
}

function renderDiffQuestionList(list, review, id = "") {
  list.replaceChildren(...[...diffQuestions].reverse().map((q) => diffQuestionCard(q, review, q.id === id)));
}

function openDiffQuestions(id = "", turnID) {
  const path = state.view.path;
  const list = el(`<div class="diff-questions-list"></div>`);
  list.dataset.review = path;
  list.dataset.selected = id;
  renderDiffQuestionList(list, path, id);
  const modal = openModal("Questions you asked", list, "narrow");
  addDiscussionNavigation(modal);
  syncDiscussionUI();
  if (id) {
    const selected = list.querySelector(`[data-discussion="${CSS.escape(id)}"]`);
    if (selected) focusDiscussion(selected, turnID);
  }
}

Events.On("question:updated", (ev) => {
  const q = ev.data;
  if (state.view?.path === q.review) refreshDiffQuestions(state.view);
  if (q.status === "failed" && q.problem) toast(q.problem, true);
  if (q.status !== "answered") return;
  const alert = el(`<div class="question-alert"><span>Answer ready · ${esc(base(q.file))}:${q.line}</span>
    <button class="small">Jump to reply</button><button class="icon" aria-label="Dismiss answer notification">✕</button></div>`);
  alert.dataset.replyReview = q.review;
  alert.dataset.replyId = q.id;
  alert.dataset.replyTurn = q.turn ?? q.id;
  const [jump, dismiss] = alert.querySelectorAll("button");
  jump.onclick = async () => {
    jump.disabled = true;
    if (await jumpDiscussion(q.review, q.id, true, alert.dataset.replyTurn)) alert.remove();
    else jump.disabled = false;
  };
  dismiss.onclick = () => alert.remove();
  $("#question-alerts").prepend(alert);
});

// Selecting lines, commenting and asking

// extend grows a block of lines from the selected one, to try a change to all of them at once.
function selectRow(station, part, line, compose, extend) {
  if (!discussionBusy) activeThread = "";
  const range = state.block;
  const askRange = range?.station === station && range.part === part && line >= range.from && line <= range.to ? range : null;
  const from = state.sel;
  if (extend && from && from.station === station && from.part === part) {
    const anchor = state.block?.station === station && state.block.part === part ? state.block.anchor : from.line;
    state.block = anchor === line ? null : { station, part, anchor, from: Math.min(anchor, line), to: Math.max(anchor, line) };
  } else state.block = null;
  state.sel = { station, part, line };
  $$("#station .row.sel, #station .kcur").forEach((r) => r.classList.remove("sel", "kcur"));
  markBlock();
  setTimeout(() => {
    renderStatus();
    updateDiscussionControls();
  });
  if (compose) return openComposer(station, part, line, { from: askRange?.from, end: askRange?.to });
  document.querySelector(`#station .row[data-part="${part}"][data-line="${line}"]`)?.classList.add("sel");
}

function inBlock(station, part, line) {
  const b = state.trying ? { station: state.trying.station, part: state.trying.part, from: state.trying.line, to: state.trying.end } : state.block;
  return !!b && b.station === station && b.part === part && line >= b.from && line <= b.to;
}

function markBlock() {
  const s = cur();
  $$("#station .row[data-line]").forEach((r) => r.classList.toggle("blk", inBlock(s?.id, +r.dataset.part, +r.dataset.line)));
}

// block is the selected lines: the block when one is marked, else the selected line.
function block() {
  const s = cur();
  const b = state.block;
  if (b && b.station === s?.id) return { station: b.station, part: b.part, line: b.from, end: b.to };
  if (state.sel && state.sel.station === s?.id) return { ...state.sel, end: state.sel.line };
  return null;
}

async function blockTests(s, part, from, to) {
  const view = state.view;
  const p = partData(s, part);
  const ids = new Set();
  for (const r of p?.rows ?? []) {
    if (r.cov !== "run" || r.n - 1 < from || r.n - 1 > to) continue;
    const lineTests = await testsAtLine(s.id, part, r.n - 1);
    if (state.view !== view) return [];
    for (const ti of lineTests) ids.add(ti);
  }
  return [...ids];
}


function needLine(what) {
  toast(walking() ? `Go to a change first (space or b), then click its line to ${what}.` : `Click a code line first to ${what}.`);
}

function openComposer(station, part, line, opts = {}) {
  const keep = state.compose && state.compose.text ? state.compose : null;
  if (keep?.pending) return;
  const reselect = keep?.mode === "ask" && (!keep.anchor || keep.version !== state.view.version) && !opts.answers;
  if (keep && !reselect && !(keep.station === station && keep.part === part && keep.line === line)) {
    toast("Finish or cancel what you are writing first.");
    return;
  }
  const s = cur();
  if (s?.id !== station || !s.parts[part]) return;
  const p = partData(s, part);
  const from = opts.from ?? line, end = opts.end ?? line;
  const anchor = questionSelection(p.viewVersion === state.view.version ? p : s.parts[part], from, end);
  if ((reselect || opts.mode === "ask") && !anchor) return toast("Wait for the selected code to reload, then choose Ask again.");
  state.sel = { station, part, line };
  const mode = reselect ? "ask" : opts.mode ?? (opts.answers ? "answer" : "comment");
  if (!keep || reselect) {
    state.compose = { station, part, line, from, end, anchor, version: state.view.version,
      mode, answers: opts.answers ?? "", label: "", blocking: false, text: keep?.text ?? "", picking: !!opts.picking && mode === "comment" && !opts.answers };
  }
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
  if (cp.version !== state.view.version && cp.anchor &&
      questionSelection(p.viewVersion === state.view.version ? p : s.parts[cp.part], cp.from, cp.end) === cp.anchor) {
    cp.version = state.view.version;
  }
  const reselect = ask && (!cp.anchor || cp.version !== state.view.version);
  const lines = cp.from === cp.end ? cp.from + 1 : `${cp.from + 1}–${cp.end + 1}`;
  const head = ask
    ? `Ask the agent about <code>${esc(p.file)}:${lines}</code>`
    : note
      ? `Answer to: <span class="muted">${esc(oneLine(note.text).slice(0, 90))}${note.text.length > 90 ? "…" : ""}</span>`
      : `Comment on <code>${esc(p.file)}:${cp.line + 1}</code>`;
  const foot = ask
    ? reselect ? "The code changed. Select the line or range and choose Ask again to keep this draft."
      : state.agentLive ? "Enter asks, Shift+Enter adds a line, Esc cancels."
      : "Start the review agent to ask a question."
    : "Kept as a draft until you send the comments to the agent. Ctrl+Enter saves, Esc cancels.";
  const box = el(`<div class="composer${ask ? " ask" : ""}">
    <div class="cp-head">${head}</div>
    <div class="cp-labels"></div>
    <textarea rows="3"></textarea>
    <div class="cp-foot"><span class="muted small">${foot}</span>
      <span class="spacer"></span><button class="small cp-cancel">Cancel</button><button class="primary small cp-save">${ask ? "Ask" : "Save draft"}</button></div></div>`);
  const ta = box.querySelector("textarea");
  const submit = box.querySelector(".cp-save");
  const cancelButton = box.querySelector(".cp-cancel");
  let switchMode;
  if (!cp.answers && !cp.picking) {
    switchMode = el(`<button class="link-btn cp-mode">${ask ? "Write a comment" : "Ask agent"}</button>`);
    box.querySelector(".cp-head").append(" ", switchMode);
    switchMode.onclick = () => {
      if (cp.pending) return;
      cp.mode = ask ? "comment" : "ask";
      renderKeepScroll();
      $("#station .composer textarea")?.focus();
    };
  }
  const controls = () => {
    ta.disabled = cancelButton.disabled = !!cp.pending;
    submit.disabled = !!cp.pending || reselect || (ask && !state.agentLive);
    if (switchMode) switchMode.disabled = !!cp.pending;
  };
  controls();
  ta.value = cp.text;
  const tip = () => (ask ? "What would you like to know?" : labels.find((l) => l.name === cp.label)?.tip ?? "your comment");
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
    if (cp.pending || (ask && (!state.agentLive || !cp.anchor || cp.version !== state.view.version))) return;
    cp.pending = true;
    controls();
    let ok;
    if (ask) {
      try {
        await api("Reviews.Ask", { station: cp.station, part: cp.part, line: cp.from, end: cp.end, version: cp.version, text: cp.text });
        ok = true;
        refreshDiffQuestions(state.view);
      } catch (e) {
        if (state.compose === cp) toast(errText(e), true);
      }
    } else {
      const def = labels.find((l) => l.name === cp.label)?.blocking ?? false;
      ok = await act("AddComment", { station: cp.station, part: cp.part, line: cp.line, label: cp.label, blocking: cp.label ? def !== cp.blocking : false, text: ta.value, answers: cp.answers || "" });
      const last = reader().comments[reader().comments.length - 1];
      if (ok && last) toast(`${last.id} saved as a draft; S sends every draft to the agent`);
    }
    cp.pending = false;
    if (state.compose !== cp) return;
    if (ok) {
      state.compose = null;
      renderKeepScroll();
    } else {
      renderKeepScroll();
      $("#station .composer textarea")?.focus();
    }
  };
  const cancel = () => {
    if (cp.pending) return;
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
  submit.onclick = save;
  cancelButton.onclick = cancel;
  return box;
}

function startOnLine(mode) {
  const s = cur();
  if (!state.sel || state.sel.station !== s.id) return needLine(mode === "ask" ? "ask about it" : "comment on it");
  const range = mode === "ask" ? block() : null;
  openComposer(state.sel.station, state.sel.part, state.sel.line, { mode, from: range?.line, end: range?.end, picking: true });
}

export async function sendComments() {
  if (!state.agentLive) return toast("Start the agent first; your drafts stay until then.", true);
  if (!(reader().drafts > 0)) return toast("No draft comments; c writes one on the selected line.");
  if (await act("SendComments")) toast("Comments sent to the agent. It answers each one and resolves it.");
}

// sendFixes asks the agent to fix the problems the reader agreed with; each comes back as a
// comment it resolves.
export async function sendFixes() {
  const n = reader().toFix ?? 0;
  if (!n) return toast("Nothing to fix yet: Agree (?) with a problem, or give a stop the verdict 2 needs changes.");
  if (!state.agentLive) return toast("Start the agent first; what you want fixed waits until then.", true);
  if (await act("SendFixes")) toast(`Sent ${n} to the agent to fix. Each comes back as a comment it resolves.`);
}

function fixSent(key) {
  return reader().comments?.find((c) => c.fix === key);
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
  if (st.kind === "mech") return (st.subs ?? []).flatMap((x) => stepNotes(s, x));
  if (st.from < 0) return [];
  const end = stepEnd(s, st);
  return s.notes.map((n, i) => [n, i]).filter(([n]) => n.part === st.part && n.line >= st.from && n.line <= end && matchesNoteFilter(n)).sort(([a], [b]) => a.line - b.line).map(([, i]) => i);
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
  const showsPrompts = st.kind !== "mech";
  const keys = stepNotes(s, st).map((i) => s.notes[i])
    .filter((n) => n.kind !== "decide" && !rd.reviewed.includes(n.key) && (navigableNote(s, n) || (showsPrompts && guidedPrompt(n))))
    .map((n) => n.key);
  if (keys.length) act("ReadNotes", keys);
}

function enterStep() {
  state.nudged = false;
  state.compose = null;
  state.sel = null;
  state.block = null;
  state.note = -1;
  renderStation();
  $("#station").scrollTop = 0;
  const s = cur();
  const rows = $$("#station .row[data-line]");
  const first = rows.find((r) => /\b(add|chg|del)\b/.test(r.className) || r.classList.contains("has-note")) ?? rows[0];
  if (first) {
    selectRow(s.id, +first.dataset.part, +first.dataset.line, false);
    first.scrollIntoView({ block: "nearest" });
  } else {
    renderStatus();
    updateDiscussionControls();
  }
}

let changingMode;

async function setReadingMode(full) {
  const opened = state.opened;
  if (!state.view || !opened || opened.restoringPosition || opened.switchingWalkthrough || full === !guided() || changingMode === opened) return;
  changingMode = opened;
  const ok = await act("SetFull", full);
  if (changingMode === opened) changingMode = undefined;
  if (!ok || state.opened !== opened || full !== !guided()) return;

  const s = cur();
  const sel = state.sel?.station === s?.id ? state.sel : null;
  const step = !full && s?.kind === "code" && sel ? Math.max(0, stepOf(s, sel.part, sel.line)) : 0;
  if (step !== state.step) {
    state.step = step;
    if (!full) renderKeepScroll();
  }
  if (sel) rowAt(sel.part, sel.line)?.scrollIntoView({ block: "center" });
  toast(full ? `${fullDisplayLabel()} · w switches to step by step` : `Step by step · space next · b back · w ${fullDisplayLabel()}`);
}

function progress(s) {
  const steps = s.steps;
  const st = steps[state.step];
  let label = { brief: "what this stop is", background: "know this first", change: `change ${st.n} of ${st.of}`, mech: "housekeeping", wrap: "your verdict" }[st.kind];
  const [at, of] = codeStops();
  const box = el(`<div class="progress"></div>`);
  const bar = el(`<div class="stepbar"></div>`);
  const showMatches = filteringNotes(s);
  steps.forEach((x, i) => {
    const seg = el(`<span class="${i < state.step ? "done" : i === state.step ? "now" : ""}" title="${esc(x.kind === "change" ? `change ${x.n} of ${x.of}` : x.kind)}"></span>`);
    if (showMatches && x.kind !== "brief" && x.kind !== "wrap") {
      const matches = stepNotes(s, x).length > 0;
      seg.classList.add(matches ? "note-match" : "no-note-match");
      seg.title += matches ? " · Matching notes" : " · No matching notes";
      if (!matches && i === state.step) label += " · no matching notes";
    }
    seg.onclick = () => {
      state.step = i;
      enterStep();
    };
    bar.append(seg);
  });
  box.append(phaseRow(2, `stop ${at} of ${of} · step ${state.step + 1} of ${steps.length} · ${label}`), bar);
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
  if (state.view.mode === "file" && st.kind !== "wrap") box.append(verdictBar(s));
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
  if (cardsHidden(s)) body.append(revealNoteCards());
  else if (!notes.length && !s.auto && st.kind === "change") {
    const hasNotes = s.notes.some((n) => stepShows(s, st, n.part, n.line));
    body.append(el(`<p class="muted">${hasNotes ? "No notes match this filter for this step." : "The agent left no note on this change, so read it on your own."}</p>`));
  }
  const left = notes.filter((i) => !shown.has(i) && navigableNote(s, s.notes[i]));
  if (left.length) {
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
  if (c) return oneLine(c);
  const kinds = new Set(notes.map((i) => s.notes[i].kind));
  if (kinds.has("issue")) return "The agent sees a problem here. Do you agree? ? agrees and flags it, x dismisses it.";
  if (kinds.has("decide")) return "There is a call to make here. Decide, then tick it.";
  if (kinds.has("question")) return "The agent asks you something here. Answer it below, or dismiss it.";
  if (notes.length) return "Does the code do what the notes say, and is that the right thing to do?";
  return "Can you say what each changed line does, and is it right?";
}

// settle lists what notes ask of the reader, right where they read them: calls to make, problems to
// agree with or dismiss, and questions to answer.
function guidedPrompt(note) {
  return asks(note) && !reader().dismissed.includes(note.key);
}

function settle(body, s, notes, heading) {
  const items = notes.map((i) => [s.notes[i], i]).filter(([n]) => guidedPrompt(n));
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
    row.append(btn("Dismiss", () => markDismissed(n)));
  } else {
    const flagged = rd.flagged.includes(n.key);
    row.append(el(`<span class="sr-mark">${flagged ? "⚑" : "!"}</span>`), el(`<span class="sr-text">${text}</span>`), btn(flagged ? "Agreed" : "Agree", () => act("MarkNote", n.key, "flagged"), flagged));
    const sent = flagged && fixSent(n.key);
    if (sent) row.append(el(`<span class="muted small">sent to fix as ${esc(sent.id)}</span>`));
    else if (flagged) row.append(btn("Have it fixed (P)", sendFixes));
    row.append(btn("Dismiss", () => markDismissed(n)));
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
  if (cardsHidden(s)) body.append(revealNoteCards());
  body.append(el(`<div class="checks"><h4>You should be able to answer</h4><ul>${(s.checksAll ?? []).map((c) => `<li>${prose(c)}</li>`).join("")}</ul></div>`));
  const open = s.notes.map((n, i) => [n, i]).filter(([n]) => matchesNoteFilter(n) && !rd.dismissed.includes(n.key) && (
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
    changes: fixSent("stop:" + s.id)
      ? `Sent to the agent to change, as ${fixSent("stop:" + s.id).id}. Space moves on.`
      : "Say what needs to change: b goes back to the code, c comments on a line. P asks the agent to make the changes, with your comments on this stop as the brief. Then space moves on.",
    unsure: "Not sure is a fine answer. b goes back and a asks the agent about the line that bothers you; w shows the whole stop at once. Then space moves on.",
  }[v] ?? "Press 1, 2 or 3.";
  body.append(navHint(next));
}

// Callers and history around a stop

async function lookAround(s) {
  const around = state.around;
  if (state.around[s.id]) return;
  state.around[s.id] = { loading: true };
  try {
    around[s.id] = await api("Reviews.Around", s.id);
  } catch (e) {
    around[s.id] = { err: errText(e), symbols: [], history: [] };
  }
  if (state.around === around && cur()?.id === s.id) renderKeepScroll();
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
  const calls = s.notes.map((n, i) => [n, i]).filter(([n]) => n.kind === "decide" && matchesNoteFilter(n) && !reader().dismissed.includes(n.key));
  if (calls.length) {
    const c = el(`<div class="settle"><h4>Your calls in this stop</h4></div>`);
    for (const [n, i] of calls) c.append(settleRow(s, n, i, n.part >= 0 ? ` (${base(s.parts[n.part].file)}:${n.line + 1})` : ""));
    box.append(c);
  }
  aroundSection(box, s);
  if (cardsHidden(s)) box.append(revealNoteCards());
  const shown = new Set();
  s.parts.forEach((p, pi) => {
    const d = partData(s, pi);
    box.append(partBox(s, pi, d.start, d.end, null, { shown }));
  });
  const loose = s.notes.map((n, i) => [n, i]).filter(([n, i]) => !shown.has(i) && noteCardVisible(s, n) && n.part < 0);
  if (loose.length) {
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
  if (does) rows.push(["Does", `<div class="clamp${cap}">${prose(does)}</div>`]);
  else if (!v.walkthrough) rows.push(["Does", `<span class="muted">the agent is still reading the change; this fills in by itself</span>`]);
  if (v.gap) rows.push(["Gap", noGap(v.gap) ? `<span class="m-add">none: it does what was asked</span>` : `<div class="clamp${cap}">${prose(v.gap)}</div>`, "m-chg"]);
  ov.append(card(rows));
  if (!v.walkthrough && !v.did && !v.gap) ov.append(el(`<p class="muted small">The agent has not compared the change with what was asked yet.</p>`));
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
    if (stopHasNoMatchingNotes(s)) {
      r.classList.add("no-note-match");
      r.title = "No matching notes";
    }
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
  v.stations.forEach((s, si) => s.notes.forEach((nn, ni) => nn.focus && noteCardVisible(s, nn) && !rd.dismissed.includes(nn.key) && focus.push([nn, s, si, ni])));
  if (focus.length) {
    const f = el(`<div><h3>Focus areas · F filters by them</h3></div>`);
    focus.sort(([a], [b]) => a.focus.localeCompare(b.focus));
    for (const [nn, s, si, ni] of focus) {
      const where = nn.part >= 0 ? `${s.id} · ${base(s.parts[nn.part].file)}:${nn.line + 1}` : s.id;
      const l = el(`<div class="peek-link navable"><span class="k-${esc(noteStyle(nn)[2])}">${esc(nn.focus)}</span> · <span class="muted">${esc(where)}</span> — ${esc(oneLine(nn.text).slice(0, 140))}</div>`);
      l.onclick = () => gotoNote(si, ni);
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
  ov.append(githubRecap());
  const exp = el(`<p><button class="small">Export for posting…</button> <span class="muted small">your comments, flagged problems and calls, as Markdown or GitHub review text, to paste yourself.</span></p>`);
  exp.querySelector("button").onclick = openExport;
  ov.append(exp);
  if (rd.toFix) {
    const b = el(`<p><button class="primary small">Ask the agent to fix ${rd.toFix} (P)</button> <span class="muted small">the problems you agreed with and the stops you said need changes. Agreeing and verdicts only mark them; this hands them to the agent, which changes the code and resolves each one as a comment.</span></p>`);
    b.querySelector("button").onclick = sendFixes;
    ov.append(b);
  }
  const where = (s, n) => (n.part < 0 ? s.id : `${s.id} · ${base(s.parts[n.part].file)}:${n.line + 1}`);
  const link = (cls, html, si, ni) => {
    const l = el(`<div class="peek-link navable ${cls}">${html}</div>`);
    l.onclick = () => gotoNote(si, ni);
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
    for (const [s, si, n, ni] of items) g.append(link(cls, `${rd.flagged.includes(n.key) && n.kind === "issue" ? (fixSent(n.key) ? "⚑ sent to fix · " : "⚑ ") : rd.reviewed.includes(n.key) ? "✓ " : ""}${esc(where(s, n))} — ${esc(oneLine(n.text).slice(0, 160))}`, si, ni));
    ov.append(g);
  }
  if (!any) ov.append(el(`<p class="muted">Nothing open: every problem and question was dismissed.</p>`));
  const jumpTo = (q) => () => jumpDiscussion(state.view.path, q.id, false);
  if (rd.comments.length) {
    const c = el(`<div><h3>Your comments · S sends the drafts, x deletes one</h3></div>`);
    if (rd.drafts) {
      const b = el(`<button class="primary small" style="margin-bottom:8px">Send ${rd.drafts} draft${rd.drafts > 1 ? "s" : ""} to the agent</button>`);
      b.onclick = sendComments;
      c.append(b);
    }
    rd.comments.forEach((q) => {
      const card = commentCard(q, { static: true, where: true, jump: jumpTo(q) });
      card.classList.add("navable");
      card.dataset.comment = q.id;
      c.append(card);
    });
    ov.append(c);
  }
  if (rd.questions.length) {
    const c = el(`<div><h3>Questions you asked</h3></div>`);
    rd.questions.forEach((q) => c.append(questionCard(q, { static: true, where: true, jump: jumpTo(q) })));
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
  const head = el(`<h3>What each test runs · click a test to spotlight it, ▶ runs it <button class="small run-btn">▶ Run all ${c.tests.filter((t) => !t.synthetic).length}</button></h3>`);
  head.querySelector("button").onclick = () => runTests(c.tests.map((t, i) => (t.synthetic ? -1 : i)).filter((i) => i >= 0), "every measured test");
  ov.append(head);
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
    const tr = el(`<tr class="${state.spot === ti ? "on " : ""}${t.synthetic ? "synthetic" : "navable"}"><td class="tname" style="padding-left:${8 + t.depth * 16}px">${t.synthetic ? "" : `<button class="na run-one" title="Run this test">▶</button>`}${statusIcon(statusOf(ti))}${esc(t.display || t.label)}${t.new ? ' <span class="badge fresh">new</span>' : ""}</td>${cells.join("")}</tr>`);
    if (!t.synthetic) {
      tr.onclick = () => spotlight(ti);
      tr.querySelector(".run-one").onclick = (e) => {
        e.stopPropagation();
        runTests([ti], t.label);
      };
    }
    table.append(tr);
  });
  table.append(el(`<tr class="foot"><td class="tname">changed lines no test runs</td>${cols.map(([x]) => (x.cov?.stale ? `<td class="m-chg">stale</td>` : `<td class="${x.cov?.missed ? "m-del" : "m-add"}">${x.cov?.missed ?? 0}</td>`)).join("")}</tr>`));
  ov.append(table);
  if (cols.some(([x]) => x.cov?.stale) || c.stale?.length) ov.append(el(`<p class="m-chg small">? and stale: the code changed after coverage was measured; ask the agent to measure it again.${c.stale?.length ? " Stale files: " + esc(c.stale.join(", ")) : ""}</p>`));
  box.append(ov);
}

async function spotlight(ti) {
  const view = state.view;
  try {
    const lines = await api("Reviews.RunsLines", ti, view.version);
    if (state.view !== view) return;
    state.spotLines = lines;
  } catch (e) {
    if (state.view === view) toast(errText(e), true);
    return;
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
  const view = state.view, sel = state.sel;
  if (!sel || sel.station !== s.id) return needLine("see the tests that run it");
  if (!view.coverage) return toast("No coverage yet: ask the agent to run margin coverage.");
  const p = partData(s, sel.part);
  const row = p.rows.find((r) => r.n - 1 === sel.line);
  if (row?.cov !== "run") return toast(`No test runs line ${sel.line + 1}.`);
  const ids = await testsAtLine(s.id, sel.part, sel.line);
  if (state.view !== view || state.sel !== sel || !ids.length) return;
  const list = [...ids];
  const tests = view.coverage.tests;
  list.sort((a, b) => tests[a].label.localeCompare(tests[b].label));
  const line = sel.line;
  const needle = (row.s ?? []).map((x) => x.t).join("").trim();
  const body = el(`<div class="tests-pop"></div>`);
  const picked = new Set();
  for (const ti of list) {
    const t = tests[ti];
    const r = el(`<div class="test-row"><label><input type="checkbox"> ${statusIcon(statusOf(ti))}${esc(t.label)}${t.new ? ' <span class="badge fresh">new</span>' : ""}</label>
      <span class="muted small">${esc(t.file && t.line ? `${t.file}:${t.line}` : t.group)}</span><span class="spacer"></span>
      <button class="na">Spotlight</button><button class="na">Source</button></div>`);
    r.querySelector("input").onchange = (e) => (e.target.checked ? picked.add(ti) : picked.delete(ti));
    const [spot, src] = r.querySelectorAll("button");
    spot.onclick = () => {
      closeModal();
      if (state.view !== view) return toast("The review changed. Open Tests again.");
      spotlight(ti);
    };
    src.onclick = async () => {
      try {
        const peek = await api("Reviews.TestSource", ti, view.version);
        if (state.view === view) showPeek(peek);
      } catch (e) {
        toast(errText(e), true);
      }
    };
    body.append(r);
  }
  const run = el(`<div class="cp-foot"><span class="muted small">Run uses your test command for this repository; the agent can instead run them and say what each checks about this line.</span><span class="spacer"></span>
    <button class="small" data-agent>Ask the agent</button><button class="primary small" data-run>▶ Run ${list.length === 1 ? "it" : "picked, or all"} (r)</button></div>`);
  run.querySelector("[data-run]").onclick = () => {
    closeModal();
    runTests(picked.size ? [...picked] : list, `${base(p.file)}:${line + 1}`, view);
  };
  run.querySelector("[data-agent]").onclick = async () => {
    const ids = picked.size ? [...picked] : list.slice(0, 1);
    try {
      await api("Reviews.RunTests", ids, p.file, line + 1, needle, view.version);
      if (state.view !== view) return;
      closeModal();
      toast("Sent to the agent.");
    } catch (e) {
      toast(errText(e), true);
    }
  };
  body.append(run);
  openModal(`${plural(list.length, "test runs", "tests run")} ${base(p.file)}:${line + 1}`, body);
}

// testsAtLine caches which tests run a line, for the hint, the tooltip and the picker.
const atCache = { view: null, map: new Map() };
async function testsAtLine(station, part, line) {
  const view = state.view;
  if (atCache.view !== view) {
    atCache.view = view;
    atCache.map = new Map();
  }
  const k = `${station}|${part}|${line}`;
  const cache = atCache.map;
  if (!cache.has(k)) {
    try {
      cache.set(k, await api("Reviews.TestsAt", station, part, line, view.version));
    } catch (e) {
      if (state.view === view) toast(errText(e), true);
      return [];
    }
  }
  return state.view === view ? cache.get(k) : [];
}

async function runFor(s, pi) {
  const view = state.view;
  if (!view.coverage) return toast("No coverage yet: ask the agent to run margin coverage.");
  let ids;
  try {
    ids = await api("Reviews.TestsFor", s.id, pi, view.version);
  } catch (e) {
    if (state.view === view) toast(errText(e), true);
    return;
  }
  if (state.view !== view) return;
  const what = pi < 0 ? `stop ${s.title ?? s.id}` : `${base(partData(s, pi).file)} ${partData(s, pi).label ?? ""}`.trim();
  runTests(ids, what, view);
}

// runHere runs the tests of the selected line, or of the stop when no line is selected.
async function runHere() {
  const s = cur();
  const view = state.view, sel = state.sel;
  if (s?.kind !== "code") return toast("Go to a stop with code first.");
  if (!state.view.coverage) return toast("No coverage yet: ask the agent to run margin coverage.");
  if (!state.sel || state.sel.station !== s.id) return runFor(s, -1);
  const r = partData(s, state.sel.part)?.rows.find((x) => x.n - 1 === state.sel.line);
  if (r?.cov !== "run") return toast(`No test runs line ${state.sel.line + 1}; ▶ Tests runs the whole stop's.`);
  const ids = await testsAtLine(s.id, sel.part, sel.line);
  if (state.view === view && state.sel === sel && ids.length) {
    runTests(ids, `${base(partData(s, sel.part).file)}:${sel.line + 1}`, view);
  }
}

// covHint puts the selected line's actions right under it, with the tests that run it, like the
// terminal viewer's notes column does.
// It is kept between renders and put back in the same task, and replaced in place, so it never
// leaves the page for a frame and the code under it does not jump.
let hint = null;
function covHint() {
  const c = hintContext();
  const old = document.querySelector("#station .cov-hint");
  if (!c) {
    old?.remove();
    hint = null;
    return;
  }
  if (hint?.dataset.key === c.key) {
    if (old && old !== hint) old.remove();
    if (c.rowEl.nextElementSibling !== hint) c.rowEl.after(hint);
    return;
  }
  const known = idsNow(c);
  if (!known) {
    old?.remove();
    hint = null;
    const ids = c.many ? blockTests(c.s, c.many.part, c.many.line, c.many.end) : testsAtLine(c.s.id, c.sel.part, c.sel.line);
    ids.then((ids) => {
      const now = hintContext();
      if (now?.key !== c.key || hint?.dataset.key === c.key) return;
      document.querySelector("#station .cov-hint")?.remove();
      hint = buildHint(now, [...ids]);
      now.rowEl.after(hint);
    });
    return;
  }
  hint = buildHint(c, known);
  if (old) old.replaceWith(hint);
  else c.rowEl.after(hint);
}

function hintContext() {
  const s = cur();
  const sel = state.sel;
  if (!sel || !s || sel.station !== s.id || state.trying) return null;
  const rowEl = document.querySelector(`#station .row[data-part="${sel.part}"][data-line="${sel.line}"]`);
  const p = rowEl && partData(s, sel.part);
  const r = p?.rows.find((x) => x.n - 1 === sel.line);
  const c = state.compose;
  if (!r || (c && c.station === sel.station && c.part === sel.part && c.line === sel.line)) return null;
  const b = block();
  const many = b && b.end > b.line ? b : null;
  const key = `${state.view.version}|${s.id}|${sel.part}|${sel.line}|${many ? many.line + "-" + many.end : ""}|${r.cov ?? ""}|${[...results.values()].join()}`;
  return { s, sel, rowEl, p, r, many, key };
}

// idsNow is the tests that run the hint's lines when they are known without asking, else null.
function idsNow(c) {
  if (!c.many && c.r.cov !== "run") return [];
  if (atCache.view !== state.view) return null;
  const ids = new Set();
  const lines = c.many ? (c.p.rows ?? []).filter((r) => r.cov === "run" && r.n - 1 >= c.many.line && r.n - 1 <= c.many.end).map((r) => r.n - 1) : [c.sel.line];
  for (const l of lines) {
    const got = atCache.map.get(`${c.s.id}|${c.many?.part ?? c.sel.part}|${l}`);
    if (!got) return null;
    got.forEach((t) => ids.add(t));
  }
  return [...ids];
}

function buildHint(c, ids) {
  const { r, p, many, key } = c;
  const tests = state.view.coverage?.tests ?? [];
  ids.sort((a, b) => tests[a].label.localeCompare(tests[b].label));
  const shown = ids.slice(0, 4);
  const what = many ? `lines ${many.line + 1}–${many.end + 1}` : `line ${r.n}`;
  const head = ids.length
    ? `<span class="ch-head">┃ ${what} run${many ? "" : "s"} in ${plural(ids.length, "test", "tests")}</span>
    ${shown.map((ti) => `<button class="link-btn ch-test" data-ti="${ti}" title="Spotlight: dim what this test never runs">${statusIcon(statusOf(ti))}${esc(tests[ti].label)}</button>`).join("")}
    ${ids.length > shown.length ? `<button class="link-btn" data-more>and ${ids.length - shown.length} more</button>` : ""}`
    : `<span class="ch-head muted">${what}${many ? " · no test runs them" : r.cov === "missed" ? " · changed, and no test runs it" : ""}</span>`;
  const btn = (attr, label, k, title, cls = "") => `<button class="na ${cls}" ${attr} title="${esc(title)}">${label} <kbd>${k}</kbd></button>`;
  const h = el(`<div class="cov-hint${ids.length ? " covered" : ""}" data-key="${esc(key)}">${head}<span class="spacer"></span>
    ${btn("data-comment", "Comment", "c", "Write a draft comment on this line")}
    ${btn("data-ask", "Ask", "a", "Ask the agent about this line")}
    ${ids.length && !many ? btn("data-list", "Tests", "t", "List the tests that run this line, peek at their source") + btn("data-run", "▶ Run", "T", "Run them with your test command", "run-btn") : ""}
    ${p.base ? "" : btn("data-try", many ? `✎ Try a change to ${many.end - many.line + 1} lines` : "✎ Try a change", "i", "Edit or delete these lines and see whether the tests notice; margin puts them back after. Shift+click or J K selects more lines")}
    ${many ? `<button class="na" data-unblock title="Back to one line (esc)">✕</button>` : ""}</div>`);
  h.querySelectorAll("[data-ti]").forEach((b) => (b.onclick = () => spotlight(+b.dataset.ti)));
  h.querySelector("[data-more]")?.addEventListener("click", openTests);
  h.querySelector("[data-list]")?.addEventListener("click", openTests);
  h.querySelector("[data-run]")?.addEventListener("click", runHere);
  h.querySelector("[data-try]")?.addEventListener("click", () => startTry());
  h.querySelector("[data-unblock]")?.addEventListener("click", () => {
    state.block = null;
    markBlock();
    renderStatus();
  });
  h.querySelector("[data-comment]").onclick = () => startOnLine("comment");
  h.querySelector("[data-ask]").onclick = () => startOnLine("ask");
  return h;
}
// Trying a change: the selected line, edited, runs its tests (or the stop's) with the edit in the
// file; margin puts the line back after.

async function startTry(again, range) {
  const s = cur();
  const view = state.view;
  const from = again?.station ? again : range ?? block();
  if (s?.kind !== "code" || !from || from.station !== s.id) return needLine("try a change to it");
  if (state.compose?.text) return toast("Finish or cancel what you are writing first.");
  const p = partData(s, from.part);
  if (p.base) return toast("This part shows the code before the change; try changes on the new side.");
  const end = Math.max(from.line, from.end ?? from.line);
  let text;
  try {
    text = await api("Reviews.LinesText", s.id, from.part, from.line, end, view.version);
  } catch (e) {
    return toast(errText(e), true);
  }
  const lineTests = await blockTests(s, from.part, from.line, end);
  if (state.view !== view || cur() !== s) return;
  state.sel = { station: s.id, part: from.part, line: end };
  state.block = null;
  state.compose = null;
  endTry();
  state.trying = { view, station: s.id, part: from.part, line: from.line, end, old: text, text: again?.text ?? text, lineTests, scope: lineTests.length ? "line" : "stop", compare: again?.compare ?? true };
  renderKeepScroll();
  focusTry();
}

function focusTry() {
  const tr = state.trying;
  if (!tr) return;
  tr.focused = true;
  tr.ed?.focus();
  document.querySelector("#station .try-inline")?.scrollIntoView({ block: "nearest", behavior: "smooth" });
}

// growTry adds the line above or below to the block being tried, keeping what was typed.
async function growTry(dir) {
  const tr = state.trying;
  const s = cur();
  const at = dir < 0 ? tr.line - 1 : tr.end + 1;
  if (at < 0) return toast("That is the first line of the file.");
  let t;
  try {
    t = await api("Reviews.LinesText", s.id, tr.part, at, at, tr.view.version);
  } catch (e) {
    return toast(errText(e), true);
  }
  if (state.trying !== tr || state.view !== tr.view) return;
  const edited = tr.text !== tr.old;
  tr.old = dir < 0 ? t + "\n" + tr.old : tr.old + "\n" + t;
  if (!edited) tr.text = tr.old;
  else if (tr.text !== "") tr.text = dir < 0 ? t + "\n" + tr.text : tr.text + "\n" + t;
  if (dir < 0) tr.line = at;
  else tr.end = at;
  state.sel = { station: s.id, part: tr.part, line: tr.end };
  tr.lineTests = await blockTests(s, tr.part, tr.line, tr.end);
  if (!tr.lineTests.length) tr.scope = "stop";
  renderKeepScroll();
  focusTry();
}

testHooks.edit = (req) => {
  const i = state.view.stations.findIndex((x) => x.id === req.station);
  if (i < 0) return toast("That stop is gone.");
  if (i !== state.station) goStation(i);
  startTry(req);
};

// Load CodeMirror the first time a change is tried.
let cmLib = null;

export function endTry() {
  state.trying?.ed?.destroy();
  state.trying = null;
}

function tryBox(s, p) {
  const tr = state.trying;
  const covered = tr.lineTests.length;
  const many = tr.end > tr.line;
  const vimOn = editorMode() === "vim";
  const runShortcut = "⌘/Ctrl+Enter";
  const box = el(`<div class="try-inline">
    <div class="try-bar">✎ <code>${esc(base(p.file))}:${tr.line + 1}${many ? "–" + (tr.end + 1) : ""}</code>
      <button class="na" data-grow="-1" title="Take the line above in too">+ above</button><button class="na" data-grow="1" title="Take the line below in too">+ below</button>
      <button class="na" data-del title="Empty the editor: the lines are deleted for the run">Delete</button>
      <span class="sep"></span>
      <label class="opt"><input type="radio" name="try-scope" value="line"${tr.scope === "line" ? " checked" : ""}${covered ? "" : " disabled"}> ${covered ? plural(covered, "test", "tests") + " of these lines" : "no test runs these lines"}</label>
      <label class="opt"><input type="radio" name="try-scope" value="stop"${tr.scope === "stop" ? " checked" : ""}> the stop's tests</label>
      <label class="opt" title="Run them on the code as it is first, to compare"><input type="checkbox" data-compare${tr.compare ? " checked" : ""}> compare</label>
      <label class="opt" title="Vim keys in the editor"><input type="checkbox" data-vim${vimOn ? " checked" : ""}> Vim</label>
      <span class="spacer"></span><button class="small" data-cancel>Cancel</button><button class="primary small" data-go title="${runShortcut}${vimOn ? " or :w" : ""}">▶ Run</button></div>
    <div class="try-host"></div>
    <div class="try-help muted small">${vimOn ? "<kbd>i</kbd> insert · <kbd>Esc</kbd> normal · <kbd>:w</kbd> runs · <kbd>:q</kbd> cancels · " : "<kbd>Esc</kbd> cancels · "}<kbd>${runShortcut}</kbd> runs · margin writes this into the file for the run, then puts the lines back · empty deletes them</div></div>`);
  const host = box.querySelector(".try-host");
  const cancel = () => {
    endTry();
    renderKeepScroll();
  };
  const go = async () => {
    if (tr.text === tr.old) return toast("Change something first.");
    if (state.view !== tr.view) return toast("The review changed. Reopen the editor before running.");
    const active = () => state.trying === tr;
    const req = { station: tr.station, part: tr.part, line: tr.line, end: tr.end, old: tr.old, text: tr.text, tests: [], compare: tr.compare };
    if (tr.scope === "stop") {
      try {
        req.tests = await api("Reviews.TestsFor", s.id, -1, tr.view.version);
      } catch (e) {
        if (active()) toast(errText(e), true);
        return;
      }
      if (!active()) return;
      if (!req.tests.length) return toast("No test runs this stop's changes.");
    }
    const label = `${base(p.file)}:${req.line + 1}${req.end > req.line ? "–" + (req.end + 1) : ""}`;
    if (await tryChange(req, label, tr.view, active) && active() && tr.text === req.text && tr.line === req.line && tr.end === req.end) {
      cancel();
    }
  };
  tr.go = go;
  tr.cancel = cancel;
  const mount = (lib) => {
    if (state.trying !== tr) return;
    if (!tr.ed || tr.edVim !== vimOn) {
      tr.ed?.destroy();
      tr.edVim = vimOn;
      tr.ed = lib.createEditor(document.createElement("div"), {
        doc: tr.text,
        file: p.file,
        firstLine: tr.line + 1,
        vim: tr.edVim,
        onChange: (t) => (tr.text = t),
        onRun: (t) => ((tr.text = t), tr.go()),
        onCancel: () => tr.cancel(),
      });
      tr.focused = true;
    } else if (tr.ed.view.state.doc.toString() !== tr.text || tr.edLine !== tr.line) tr.ed.set(tr.text, tr.line + 1);
    tr.edLine = tr.line;
    host.append(tr.ed.view.dom);
    tr.ed.view.dom.onfocusin = () => (tr.focused = true);
    tr.ed.view.dom.onfocusout = () => setTimeout(() => tr.ed?.view.dom.isConnected && (tr.focused = tr.ed.view.hasFocus));
    if (tr.focused) requestAnimationFrame(() => tr.ed?.focus());
  };
  if (cmLib) mount(cmLib);
  else
    import("./vendor/codemirror.js").then((lib) => {
      cmLib = lib;
      if (host.isConnected) mount(lib);
    }, (e) => toast("The editor did not load: " + errText(e), true));
  box.querySelectorAll("[data-grow]").forEach((b) => (b.onclick = () => growTry(+b.dataset.grow)));
  box.querySelector("[data-del]").onclick = () => {
    tr.text = "";
    tr.ed?.set("", tr.line + 1);
    tr.ed?.focus();
  };
  box.querySelectorAll('input[name="try-scope"]').forEach((x) => (x.onchange = () => (tr.scope = x.value)));
  box.querySelector("[data-compare]").onchange = (e) => (tr.compare = e.target.checked);
  box.querySelector("[data-vim]").onchange = (e) => {
    if (!setEditorMode(e.target.checked ? "vim" : "standard")) {
      toast("Editor keybindings changed for this session. Margin could not remember them for next launch.");
    }
    tr.focused = true;
    renderKeepScroll();
  };
  box.querySelector("[data-cancel]").onclick = cancel;
  box.querySelector("[data-go]").onclick = go;
  return box;
}

testHooks.changed = () => {
  covHint();
  if (cur()?.kind === "tests") renderKeepScroll();
};

function untested() {
  const out = [];
  state.view.stations.forEach((s, si) => {
    if (s.kind !== "code") return;
    s.parts.forEach((_, pi) => {
      for (const r of partData(s, pi).rows) if (r.n && r.cov === "missed") out.push({ si, pi, line: r.n - 1 });
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
  const matches = (text) => (fold ? text.toLowerCase() : text).includes(q);
  const out = [];
  state.view.stations.forEach((s, si) => {
    s.parts.forEach((_, pi) => {
      const part = partData(s, pi);
      for (const r of part.rows) {
        const line = r.n ? r.n - 1 : Math.max(0, part.len - 1);
        (r.g ?? []).forEach((text, index) => {
          if (matches(untab(text))) out.push({ si, pi, line, ghost: `${pi}:${r.n}:${index}`, order: r.n ? index - r.g.length : index + 1 });
        });
        if (r.n && matches((r.s ?? []).map((x) => x.t).join(""))) out.push({ si, pi, line });
      }
    });
  });
  return out;
}

function pick(hs, dir, here = { si: state.station, pi: state.sel?.part ?? -1, line: state.sel?.line ?? -1 }) {
  const before = (a, b) => a.si - b.si || a.pi - b.pi || a.line - b.line ||
    (a.order ?? 0) - (b.order ?? 0);
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
  const target = () => h.ghost ? $(`#station [data-ghost="${h.ghost}"]`) : rowAt(h.pi, h.line);
  if (!target()) {
    const p = partData(s, h.pi);
    p.folds = (p.folds ?? []).filter((f) => h.line < f.start || h.line > f.end);
    renderKeepScroll();
  }
  const row = target();
  if (row) placeReviewCursor(row);
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
  const ghost = $("#station .kcur[data-ghost]")?.dataset.ghost;
  const [idx, wrapped] = pick(hs, dir, ghost ? hs.find((h) => h.si === state.station && h.ghost === ghost) : undefined);
  renderKeepScroll();
  jump(hs[idx]);
  toast(`Match ${idx + 1} of ${hs.length}${wrapped} · n next · N previous · esc ends`);
  if (fromInput) renderToolbar();
}

function endSearch() {
  state.query = "";
  search.value = "";
  renderToolbar();
  renderKeepScroll();
}

function nextFresh() {
  if (!state.fresh.size) return toast("No new notes.");
  const count = state.view.stations.length;
  for (let k = 0; k < count; k++) {
    const si = (state.station + k) % count;
    const s = state.view.stations[si];
    const i = s.notes.findIndex((n, idx) => state.fresh.has(n.key) && navigableNote(s, n) && (si !== state.station || idx !== state.note));
    if (i >= 0) {
      if (si !== state.station) goStation(si, i);
      else focusNote(i);
      return;
    }
  }
  toast("No new visible notes.");
}

// Filters, blind mode

function cycleFilter() {
  const choices = filters();
  setNoteFilter(choices[(choices.indexOf(state.filter) + 1) % choices.length]);
}

function setNoteFilter(filter) {
  const article = $("#station");
  const anchor = visibleCodeAnchor(article);
  state.filter = filter;
  toast("Showing " + filter);
  rerenderKeepScroll();
  restoreCodeAnchor(article, anchor);
}

function reveal() {
  const s = cur();
  if (!cardsHidden(s)) return toast("Choose Hide to reveal note cards one stop at a time.");
  act("Reveal", state.view.path, s.id).then((ok) => ok && toast("Note cards shown on this stop."));
}

function revealNoteCards() {
  const prompt = el('<p class="m-chg">Note cards are hidden on this stop. <button type="button" class="small">Reveal notes (v)</button></p>');
  prompt.querySelector("button").onclick = reveal;
  return prompt;
}

// GitHub: the reader posts their own comments to the pull request, one at a time or as one review.
// Every post goes through a confirmation that shows exactly what is sent and where.

async function loadGitHub(opened, refresh = false) {
  state.ghLoading = true;
  try {
    const pr = await api("Reviews.GitHubPR", refresh);
    if (state.opened !== opened) return;
    Object.assign(state, { gh: pr, ghErr: "" });
    loadThreads(opened);
  } catch (e) {
    if (state.opened !== opened) return;
    Object.assign(state, { gh: null, ghErr: errText(e) });
  } finally {
    if (state.opened === opened) state.ghLoading = false;
  }
  if (!state.view) return;
  renderGitHubButton();
  renderKeepScroll();
}

function renderGitHubButton() {
  const b = $("#gh-btn");
  if (!b) return;
  b.classList.toggle("hidden", !state.gh);
  b.onclick = openGitHubReview;
  if (!state.gh) return;
  const n = reader().comments.filter((c) => c.forGitHub && !c.github && !c.fix).length;
  b.textContent = n ? `GitHub review (${n})` : "GitHub review";
  b.title = `Send a review to PR #${state.gh.number}: your picked comments and your decision`;
}

const reactionEmoji = { THUMBS_UP: "👍", THUMBS_DOWN: "👎", LAUGH: "😄", HOORAY: "🎉", CONFUSED: "😕", HEART: "❤️", ROCKET: "🚀", EYES: "👀" };

// loadThreads pulls what is said on the pull request: the threads of the reader's posted comments,
// everyone else's threads, placed on their lines, and the conversation.
let threadsRequest = 0;
async function loadThreads(opened = state.opened, quiet = true) {
  if (!state.gh) return;
  const request = ++threadsRequest;
  const mode = state.view?.mode;
  state.ghThreadsAt = Date.now();
  try {
    const pull = await api("Reviews.GitHubThreads");
    if (state.opened !== opened || state.view?.mode !== mode || request !== threadsRequest) return;
    state.ghPull = pull;
    if (state.view) {
      ghPanelState();
      rerenderKeepScroll();
      if (document.querySelector(".modal.gh-panel")) openGitHubPanel();
    }
    if (!quiet) toast(`Pulled from GitHub: ${plural(pull.threads.length + Object.keys(pull.mine).length, "thread", "threads")}, ${plural(pull.conversation.length, "comment", "comments")} in the conversation`);
  } catch (e) {
    if (!quiet && state.opened === opened) toast(errText(e), true);
  }
}

// rerenderKeepScroll redraws the stop list, the toolbar and the stop, staying where the reader is.
function rerenderKeepScroll() {
  const sc = $("#station").scrollTop;
  renderReview();
  $("#station").scrollTop = sc;
}

// pullGitHub reads the pull request again (its diff may have moved on) and then everything said on it.
async function pullGitHub() {
  const opened = state.opened;
  toast("Pulling from GitHub…");
  await loadGitHub(opened, true);
  if (state.opened !== opened) return;
  if (state.ghErr) return toast(state.ghErr, true);
  await loadThreads(opened, false);
}

window.addEventListener("focus", () => {
  if (state.gh && Date.now() - (state.ghThreadsAt ?? 0) > 30000) loadThreads();
});

const ghMine = (c) => state.ghPull?.mine?.[c.id];
const ghOpen = () => (state.ghPull?.threads ?? []).filter((t) => !t.resolved);

// ghBody shows GitHub markdown without the HTML bots wrap it in.
function ghBody(src) {
  const clean = String(src ?? "")
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/<\/?(details|summary|sup|sub|p|br|div|picture|source|img|a|b|i|em|strong|kbd|span)\b[^>]*>/gi, "")
    .trim();
  return md(clean);
}

function reactionsRow(rs) {
  if (!rs?.length) return null;
  return el(`<div class="gh-reactions">${rs.map((r) => `<span class="gh-react" title="${esc(r.content.toLowerCase().replace("_", " "))}">${reactionEmoji[r.content] ?? esc(r.content)} ${r.count}</span>`).join("")}</div>`);
}

function repliesBox(replies) {
  const box = el(`<div class="gh-replies"></div>`);
  for (const rp of replies) {
    const row = el(`<div class="gh-reply"><div class="gh-reply-head"><b>${esc(rp.author || "ghost")}</b> <button class="linkish small" title="Open it on GitHub">↗</button></div><div class="gh-md">${ghBody(rp.body)}</div></div>`);
    row.querySelector("button").onclick = (e) => {
      e.stopPropagation();
      openURL(rp.url);
    };
    const rr = reactionsRow(rp.reactions);
    if (rr) row.append(rr);
    box.append(row);
  }
  return box;
}

// githubThread adds what happened on GitHub to one of the reader's posted comments.
function githubThread(c) {
  const t = ghMine(c);
  if (!t) return [];
  const out = [];
  const rr = reactionsRow(t.reactions);
  if (rr) out.push(rr);
  if (t.replies.length) out.push(repliesBox(t.replies));
  const check = ghCheckBox(t, true);
  if (check) out.push(check);
  return out;
}

function resolveButton(t, what, key, comment) {
  if (!t || !(t.resolved ? t.canUnresolve : t.canResolve)) return null;
  const b = el(`<button class="na" title="${t.resolved ? "Open the thread on GitHub again" : "Resolve the thread on GitHub"}">${t.resolved ? "Unresolve on GitHub" : "Resolve on GitHub"}</button>`);
  b.onclick = async (e) => {
    e.stopPropagation();
    b.disabled = true;
    const opened = state.opened;
    const mode = state.view?.mode;
    const request = threadsRequest;
    try {
      const pull = await api("Reviews.ResolveGitHubThread", t.id, !t.resolved);
      if (state.opened !== opened) return;
      if (comment && !t.resolved && localResolution(state.view.path, comment) === false) {
        setLocalResolution(state.view.path, comment, undefined);
      }
      expandedDisclosures.delete(key);
      ghUI.keep.add(t.id);
      ghUI.focus = t.id;
      if (state.view?.mode !== mode || request !== threadsRequest) {
        loadThreads(opened);
      } else {
        state.ghPull = pull;
        rerenderKeepScroll();
        if (document.querySelector(".modal.gh-panel")) openGitHubPanel();
        else disclosureSummary(key)?.focus({ preventScroll: true });
      }
      toast(`${what} ${t.resolved ? "opened again" : "resolved"} on GitHub`);
    } catch (err) {
      b.disabled = false;
      toast(errText(err), true);
    }
  };
  return b;
}

function resolveActions(c) {
  if (!c.github || !state.gh) return [];
  const out = [];
  const b = resolveButton(ghMine(c), c.id, threadKey(c), c);
  if (b) out.push(b);
  const pull = el(`<button class="na" title="Pull reactions, replies and resolved state from GitHub">↻ GitHub</button>`);
  pull.onclick = (e) => {
    e.stopPropagation();
    pullGitHub();
  };
  out.push(pull);
  return out;
}

// threadCard draws a thread someone started on GitHub: on its line, or in the GitHub panel.
function threadCard(t, opts = {}) {
  const first = t.comments[0] ?? { author: "", body: "" };
  const where = opts.where ? `<div class="muted small">${esc(t.path)}${t.prLine ? ":" + t.prLine : ""}${t.outdated ? " · outdated, GitHub lost its line" : !t.station && t.prLine ? " · not shown in this review" : ""}</div>` : "";
  const card = el(`<div class="comment gh-thread${t.resolved ? " gh-is-resolved" : ""}${opts.static ? " static" : ""}">${where}
    <span class="ck">GitHub · ${esc(first.author || "ghost")}</span>${t.resolved ? `<span class="badge gh-resolved">resolved</span>` : ""}${t.outdated ? `<span class="badge">outdated</span>` : ""}
    <div class="gh-md">${ghBody(first.body)}</div></div>`);
  const rr = reactionsRow(first.reactions);
  if (rr) card.append(rr);
  if (t.comments.length > 1) card.append(repliesBox(t.comments.slice(1)));
  const check = ghCheckBox(t, true);
  if (check) card.append(check);
  const actions = el(`<div class="note-actions"></div>`);
  const open = el(`<button class="na" title="Open the thread on GitHub">Open on GitHub ↗</button>`);
  open.onclick = (e) => {
    e.stopPropagation();
    openURL(first.url);
  };
  actions.append(open);
  const key = `github:${t.id}`;
  const rb = resolveButton(t, "thread", key);
  if (rb) actions.append(rb);
  if (opts.jump && t.station) {
    const j = el(`<button class="na" title="Go to its line in the review">Go to line</button>`);
    j.onclick = (e) => {
      e.stopPropagation();
      closeModal();
      opts.jump();
    };
    actions.append(j);
  }
  card.append(actions);
  if (t.resolved && !opts.static) return reviewDisclosure(card, key, `resolved GitHub thread by ${first.author || "ghost"}`);
  card.querySelector(".ck").onclick = (e) => {
    e.stopPropagation();
    card.classList.toggle("gh-collapsed");
  };
  return card;
}

function ghPostCard(p) {
  const states = { APPROVED: "approved", CHANGES_REQUESTED: "requested changes", COMMENTED: "review", DISMISSED: "dismissed review" };
  const card = el(`<div class="comment gh-thread static"><span class="ck">${esc(p.author || "ghost")}${p.state ? " · " + esc(states[p.state] ?? p.state.toLowerCase()) : ""}</span>
    <span class="muted small"> ${esc((p.created || "").slice(0, 10))}</span><div class="gh-md">${ghBody(p.body) || `<span class="muted">no text</span>`}</div></div>`);
  if (p.state === "APPROVED") card.classList.add("gh-approved");
  if (p.state === "CHANGES_REQUESTED") card.classList.add("gh-changes");
  const rr = reactionsRow(p.reactions);
  if (rr) card.append(rr);
  const actions = el(`<div class="note-actions"></div>`);
  const open = el(`<button class="na">Open on GitHub ↗</button>`);
  open.onclick = () => openURL(p.url);
  actions.append(open);
  card.append(actions);
  return card;
}

function jumpToLine(station, file, line, base) {
  const si = state.view.stations.findIndex((s) => s.id === station);
  if (si < 0) return;
  const s = state.view.stations[si];
  const pi = s.parts.findIndex((p) => p.file === file && !!p.base === base && line - 1 >= p.start && line - 1 <= p.end);
  goStation(si, -1, pi >= 0 && guided() ? Math.max(stepOf(s, pi, line - 1), 0) : 0);
  if (pi >= 0) {
    selectRow(s.id, pi, line - 1, false);
    document.querySelector(`#station .row[data-part="${pi}"][data-line="${line - 1}"]`)?.scrollIntoView({ block: "center" });
  }
}

// The GitHub panel lists every review thread on the pull request, the reader's own included, with
// what the agent found about each beside it. Its filters, scroll and the thread last worked on
// survive leaving it for the code, so the reader can go back to exactly where they were (B).
const ghUI = { path: "", author: "", show: "open", agent: "", q: "", scroll: 0, focus: "", keep: new Set(), checks: {}, asking: false, extra: "" };

function ghPanelState() {
  if (ghUI.path !== state.view?.path) {
    Object.assign(ghUI, { path: state.view?.path ?? "", author: "", show: "open", agent: "", q: "", scroll: 0, focus: "", keep: new Set(), checks: {}, extra: "" });
    $("#gh-back")?.remove();
    loadChecks();
  }
  return ghUI;
}

async function loadChecks() {
  const path = state.view?.path;
  if (!path) return;
  try {
    const checks = await api("Reviews.GitHubChecks");
    if (state.view?.path !== path) return;
    ghUI.checks = checks ?? {};
    if (document.querySelector(".modal.gh-panel")) openGitHubPanel();
    else if (state.view) rerenderKeepScroll();
  } catch {}
}

Events.On("review:github", () => loadChecks());

const ghStarter = (t) => t.comments[0]?.author || "ghost";
const ghWhere = (t) => t.path + (t.prLine ? ":" + t.prLine : t.originalLine ? ":" + t.originalLine + " (was)" : "");
const ghCheckOf = (t) => ghUI.checks?.[t.id];
const ghStatusLabel = { done: "done", partly: "partly done", "not done": "not done", unclear: "unclear", pending: "agent checking…" };

function ghThreadsAll() {
  const pull = state.ghPull;
  if (!pull) return [];
  const all = [...Object.values(pull.mine ?? {}), ...pull.threads];
  const line = (t) => t.prLine || t.line || 0;
  return all.sort((a, b) => a.path.localeCompare(b.path) || line(a) - line(b) || (a.comments[0]?.created ?? "").localeCompare(b.comments[0]?.created ?? ""));
}

function ghMatches(t, ui) {
  if (ui.author && ghStarter(t).toLowerCase() !== ui.author.toLowerCase()) return false;
  if (!ui.keep.has(t.id)) {
    if (ui.show === "open" && t.resolved) return false;
    if (ui.show === "resolved" && !t.resolved) return false;
  }
  if (ui.agent) {
    const st = ghCheckOf(t)?.status ?? "";
    if (ui.agent === "unchecked" && st) return false;
    if (ui.agent === "work" && !["not done", "partly", "unclear"].includes(st)) return false;
    if (ui.agent === "done" && st !== "done") return false;
  }
  if (ui.q) {
    const hay = [t.path, ...t.comments.map((c) => c.author + " " + c.body), ghCheckOf(t)?.text ?? ""].join("\n").toLowerCase();
    if (!hay.includes(ui.q.toLowerCase())) return false;
  }
  return true;
}

// ghStopFor finds the stop and line that show file:line (1-based, the new side), if any does.
function ghStopFor(file, line) {
  for (const s of state.view?.stations ?? []) {
    for (const p of s.parts ?? []) {
      if (p.file === file && !p.base && line - 1 >= p.start && line - 1 <= p.end) return s.id;
    }
  }
  return "";
}

// ghLeave closes the panel for the code, remembering where the reader was, and offers the way back.
function ghLeave(thread) {
  const scroller = document.querySelector(".modal.gh-panel .gh-panel-body");
  if (scroller) ghUI.scroll = scroller.scrollTop;
  if (thread) ghUI.focus = thread;
  closeModal();
  ghBackButton();
}

function ghBackButton() {
  if ($("#gh-back") || !state.gh) return;
  const b = el(`<button id="gh-back" class="gh-back" title="Back to the GitHub comments, where you left them (B)">← Back to GitHub comments · B</button>`);
  b.onclick = () => openGitHubPanel();
  document.body.append(b);
}

// ghGoTo shows file:line: on its stop when one shows it, else in a peek that returns to the panel.
function ghGoTo(file, line, thread) {
  const station = ghStopFor(file, line);
  if (station) {
    ghLeave(thread);
    return jumpToLine(station, file, line, false);
  }
  if (thread) ghUI.focus = thread;
  const scroller = document.querySelector(".modal.gh-panel .gh-panel-body");
  if (scroller) ghUI.scroll = scroller.scrollTop;
  openPeek(file, Math.max(line - 1, 0), () => openGitHubPanel());
}

function ghGoToThread(t) {
  if (t.station) {
    ghLeave(t.id);
    return jumpToLine(t.station, t.path, t.line, t.side === "base");
  }
  ghGoTo(t.path, t.prLine || t.originalLine || 1, t.id);
}

const refRe = /((?:[\w@.-]+\/)*[\w@.-]+\.[A-Za-z0-9]{1,8}):(\d+)(?:[-–](\d+))?|\b([0-9a-f]{7,40})\b/g;

// linkRefs makes file:line mentions and commit hashes in rendered text clickable.
function linkRefs(root, thread) {
  const files = new Set((state.view?.stations ?? []).flatMap((s) => (s.parts ?? []).map((p) => p.file)));
  for (const t of ghThreadsAll()) files.add(t.path);
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes = [];
  while (walker.nextNode()) if (!walker.currentNode.parentElement.closest("a, button, .gh-ref")) nodes.push(walker.currentNode);
  for (const node of nodes) {
    const text = node.nodeValue;
    refRe.lastIndex = 0;
    if (!refRe.test(text)) continue;
    refRe.lastIndex = 0;
    const frag = document.createDocumentFragment();
    let last = 0;
    for (const m of text.matchAll(refRe)) {
      const [whole, file, line, , sha] = m;
      let link = null;
      if (file) {
        const known = files.has(file) || [...files].find((f) => f.endsWith("/" + file));
        if (known) {
          const path = files.has(file) ? file : known;
          link = el(`<button class="gh-ref linkish" title="Show ${esc(path)}:${line}">${esc(whole)}</button>`);
          link.onclick = (e) => {
            e.stopPropagation();
            ghGoTo(path, +line, thread);
          };
        }
      } else if (sha && /\d/.test(sha) && /[a-f]/.test(sha)) {
        link = el(`<button class="gh-ref linkish" title="Show commit ${esc(sha)}">${esc(whole)}</button>`);
        link.onclick = (e) => {
          e.stopPropagation();
          if (thread) ghUI.focus = thread;
          const scroller = document.querySelector(".modal.gh-panel .gh-panel-body");
          if (scroller) ghUI.scroll = scroller.scrollTop;
          openCommit(sha, document.querySelector(".modal.gh-panel") ? () => openGitHubPanel() : null);
        };
      }
      if (!link) continue;
      frag.append(text.slice(last, m.index), link);
      last = m.index + whole.length;
    }
    if (!last) continue;
    frag.append(text.slice(last));
    node.replaceWith(frag);
  }
  return root;
}

function ghMd(src, thread) {
  return linkRefs(el(`<div class="gh-md">${ghBody(src)}</div>`), thread);
}

// ghCheckBox shows what the agent said about a thread.
function ghCheckBox(t, compact = false) {
  const c = ghCheckOf(t);
  if (!c) return null;
  const cls = c.status.replace(" ", "-");
  const box = el(`<div class="gh-check gh-check-${cls}${compact ? " compact" : ""}"><div class="gh-check-head"><span class="badge gh-status gh-status-${cls}">${esc(ghStatusLabel[c.status] ?? c.status)}</span> <span class="muted small">the agent</span></div></div>`);
  if (c.text) box.append(ghMd(c.text, t.id));
  else if (c.status === "pending") box.append(el(`<div class="muted small">${state.agentLive ? "The agent is looking at the code; its answer shows here." : "Start the agent; it has not answered yet."}</div>`));
  return box;
}

async function askAgentAbout(ids, extra = "") {
  if (!ids.length) return toast("No threads to ask about.");
  if (!state.agentLive) return toast("Start the agent first, so it can check the threads.", true);
  ghUI.asking = true;
  try {
    ghUI.checks = (await api("Reviews.AskGitHubStatus", ids, extra)) ?? ghUI.checks;
    toast(`Asked the agent where ${plural(ids.length, "thread stands", "threads stand")}; answers show beside each thread.`);
  } catch (e) {
    toast(errText(e), true);
  } finally {
    ghUI.asking = false;
  }
  if (document.querySelector(".modal.gh-panel")) openGitHubPanel();
}

function ghPanelCard(t) {
  const first = t.comments[0] ?? { author: "", body: "", url: "" };
  const own = t.comment ? reader().comments.find((c) => c.id === t.comment) : null;
  const card = el(`<div class="gh-card${t.resolved ? " gh-is-resolved" : ""}${ghUI.focus === t.id ? " gh-focus" : ""}" data-thread="${esc(t.id)}">
    <div class="gh-card-main">
      <div class="gh-card-head">
        <button class="gh-loc linkish" title="${t.station ? "Go to its line in the review" : "Show the code around it"}">${esc(ghWhere(t))}</button>
        <span class="badge ${t.resolved ? "gh-resolved" : "gh-open"}">${t.resolved ? "resolved" : "open"}</span>
        ${t.outdated ? `<span class="badge" title="GitHub lost its line: the code moved on since">outdated</span>` : ""}
        ${own ? `<span class="badge gh-picked" title="Your comment ${esc(own.id)} in this review">your ${esc(own.id)}</span>` : ""}
        <span class="spacer"></span><span class="muted small">${esc((first.created || "").slice(0, 10))}</span>
      </div>
      <div class="gh-turn"><b>${esc(first.author || "ghost")}</b></div>
    </div>
    <div class="gh-card-side"></div>
  </div>`);
  const main = card.querySelector(".gh-card-main");
  const firstTurn = card.querySelector(".gh-turn");
  firstTurn.append(ghMd(first.body, t.id));
  const rr = reactionsRow(first.reactions);
  if (rr) firstTurn.append(rr);
  for (const rp of t.comments.slice(1)) {
    const turn = el(`<div class="gh-turn gh-turn-reply"><b>${esc(rp.author || "ghost")}</b> <span class="muted small">${esc((rp.created || "").slice(0, 10))}</span></div>`);
    turn.append(ghMd(rp.body, t.id));
    const r2 = reactionsRow(rp.reactions);
    if (r2) turn.append(r2);
    main.append(turn);
  }
  card.querySelector(".gh-loc").onclick = (e) => {
    e.stopPropagation();
    ghGoToThread(t);
  };
  const actions = el(`<div class="note-actions"></div>`);
  const go = el(`<button class="na" title="Show its code; B brings you back here">Go to code</button>`);
  go.onclick = (e) => {
    e.stopPropagation();
    ghGoToThread(t);
  };
  actions.append(go);
  const rb = resolveButton(t, "thread", `github:${t.id}`, own);
  if (rb) actions.append(rb);
  const ask = el(`<button class="na" title="Ask the agent whether this thread is really done, checking the code and the commits named in it">${ghCheckOf(t) ? "Ask agent again" : "Ask agent"}</button>`);
  ask.onclick = (e) => {
    e.stopPropagation();
    ghUI.focus = t.id;
    askAgentAbout([t.id]);
  };
  actions.append(ask);
  const open = el(`<button class="na" title="Open the thread on GitHub">GitHub ↗</button>`);
  open.onclick = (e) => {
    e.stopPropagation();
    openURL(first.url);
  };
  actions.append(open);
  main.append(actions);
  const side = card.querySelector(".gh-card-side");
  const check = ghCheckBox(t);
  if (check) side.append(check);
  else side.append(el(`<div class="gh-check gh-check-none muted small">Not checked yet. “Ask agent” has it compare the thread with the code.</div>`));
  card.onclick = () => {
    ghUI.focus = t.id;
    document.querySelectorAll(".gh-card.gh-focus").forEach((x) => x.classList.remove("gh-focus"));
    card.classList.add("gh-focus");
  };
  return card;
}

function chip(label, on, onclick, title = "") {
  const b = el(`<button class="chip${on ? " on" : ""}"${title ? ` title="${esc(title)}"` : ""}>${label}</button>`);
  b.onclick = onclick;
  return b;
}

function ghFilterBar(threads, shown) {
  const ui = ghUI;
  const bar = el(`<div class="gh-filters"></div>`);
  const redraw = () => openGitHubPanel();
  const authors = new Map();
  for (const t of threads) authors.set(ghStarter(t), (authors.get(ghStarter(t)) ?? 0) + 1);
  const me = state.gh?.viewer ?? "";
  const row1 = el(`<div class="gh-frow"><span class="gh-flabel">Started by</span></div>`);
  row1.append(chip(`anyone <span class="muted">${threads.length}</span>`, !ui.author, () => ((ui.author = ""), redraw())));
  const names = [...authors.keys()].sort((a, b) => (b === me) - (a === me) || authors.get(b) - authors.get(a) || a.localeCompare(b));
  for (const name of names) {
    row1.append(chip(`${name === me ? "me · " : ""}${esc(name)} <span class="muted">${authors.get(name)}</span>`, ui.author.toLowerCase() === name.toLowerCase(), () => ((ui.author = ui.author === name ? "" : name), redraw())));
  }
  const row2 = el(`<div class="gh-frow"><span class="gh-flabel">Show</span></div>`);
  const byAuthor = threads.filter((t) => !ui.author || ghStarter(t).toLowerCase() === ui.author.toLowerCase());
  for (const [id, label, n] of [["open", "open", byAuthor.filter((t) => !t.resolved).length], ["resolved", "resolved", byAuthor.filter((t) => t.resolved).length], ["all", "all", byAuthor.length]]) {
    row2.append(chip(`${label} <span class="muted">${n}</span>`, ui.show === id, () => ((ui.show = id), ui.keep.clear(), redraw())));
  }
  row2.append(el(`<span class="gh-flabel">Agent says</span>`));
  for (const [id, label, title] of [["", "anything", "Every thread"], ["work", "needs work", "The agent found it not done, partly done or unclear"], ["done", "done", "The agent found it done"], ["unchecked", "not checked", "Not asked yet"]]) {
    row2.append(chip(label, ui.agent === id, () => ((ui.agent = id), redraw()), title));
  }
  const search = el(`<input class="gh-search" type="search" placeholder="Filter by text, file or person" value="${esc(ui.q)}">`);
  search.oninput = () => {
    ui.q = search.value;
    clearTimeout(search._t);
    search._t = setTimeout(() => {
      redraw();
      const s2 = document.querySelector(".modal.gh-panel .gh-search");
      s2?.focus();
      s2?.setSelectionRange(s2.value.length, s2.value.length);
    }, 200);
  };
  search.onkeydown = (e) => {
    if (e.key === "Escape" && search.value) {
      e.stopPropagation();
      search.value = "";
      search.oninput();
    }
  };
  row2.append(el(`<span class="spacer"></span>`), search);
  const askable = shown.filter((t) => !t.resolved || ui.show !== "open");
  const row3 = el(`<div class="gh-frow gh-ask"></div>`);
  const ask = el(`<button class="small primary" title="Have the agent check each shown thread against the code and the commits replies name, and say whether it is really done">Ask agent where these stand (${askable.length})</button>`);
  ask.disabled = !askable.length || ghUI.asking;
  const extra = el(`<input class="gh-extra" type="text" placeholder="Anything to add for the agent (optional)" value="${esc(ui.extra)}">`);
  extra.oninput = () => (ui.extra = extra.value);
  extra.onkeydown = (e) => {
    if (e.key === "Enter") ask.click();
  };
  ask.onclick = () => askAgentAbout(askable.map((t) => t.id), ui.extra);
  row3.append(ask, extra);
  if (!state.agentLive) row3.append(el(`<span class="muted small">start the agent first</span>`));
  bar.append(row1, row2, row3);
  return bar;
}

// openGitHubPanel opens the panel, or redraws it in place, keeping the reader's place in it.
function openGitHubPanel(fresh = false) {
  const ui = ghPanelState();
  const pull = state.ghPull;
  const existing = document.querySelector(".modal.gh-panel .gh-panel-body");
  let anchor = null;
  if (existing) {
    ui.scroll = existing.scrollTop;
    const a = ui.focus && existing.querySelector(`[data-thread="${CSS.escape(ui.focus)}"]`);
    if (a) anchor = { id: ui.focus, off: a.offsetTop - existing.scrollTop };
  } else if (fresh) {
    ui.keep.clear();
  }
  $("#gh-back")?.remove();
  const body = el(`<div class="gh-panel-body"></div>`);
  const head = prHead(state.gh);
  const re = el(`<button class="small">↻ Pull again</button>`);
  re.onclick = pullGitHub;
  head.append(el(`<span> </span>`), re);
  body.append(head);
  if (!pull) {
    body.append(el(`<p class="muted">Nothing pulled yet.</p>`));
  } else {
    const all = ghThreadsAll();
    const shown = all.filter((t) => ghMatches(t, ui));
    body.append(ghFilterBar(all, shown));
    const list = el(`<div class="gh-cards"></div>`);
    if (!shown.length) list.append(el(`<p class="muted">No thread matches.${all.length ? "" : " Nobody commented on the code yet."}</p>`));
    for (const t of shown) list.append(ghPanelCard(t));
    body.append(list);
    const conv = pull.conversation.filter((p) => (!ui.author || (p.author || "").toLowerCase() === ui.author.toLowerCase()) && (!ui.q || (p.author + " " + p.body).toLowerCase().includes(ui.q.toLowerCase())));
    const det = el(`<details class="gh-conv"${ui.convOpen ? " open" : ""}><summary><h4>Conversation (${conv.length})</h4></summary></details>`);
    det.ontoggle = () => (ui.convOpen = det.open);
    if (!conv.length) det.append(el(`<p class="muted small">Nothing said outside the code${ui.author ? " by " + esc(ui.author) : ""}.</p>`));
    for (const p of conv) det.append(ghPostCard(p));
    body.append(det);
  }
  let m = existing?.closest(".modal");
  if (m) {
    existing.replaceWith(body);
  } else {
    m = openModal(`GitHub · PR #${state.gh.number}`, body, "wide gh-panel").querySelector(".modal");
  }
  const a = anchor && body.querySelector(`[data-thread="${CSS.escape(anchor.id)}"]`);
  if (a) body.scrollTop = a.offsetTop - anchor.off;
  else if (!fresh || existing) body.scrollTop = ui.scroll;
  if (!existing && !fresh && ui.focus) {
    const f = body.querySelector(`[data-thread="${CSS.escape(ui.focus)}"]`);
    if (f) requestAnimationFrame(() => f.scrollIntoView({ block: "nearest" }));
  }
  return m;
}

function openURL(url) {
  api("Reviews.OpenURL", url).catch((e) => toast(errText(e), true));
}

function githubActions(c) {
  if (!state.gh || c.fix || c.github) return [];
  const post = el(`<button class="na" title="Post this comment to PR #${state.gh.number} now, after you confirm it">Comment on GitHub</button>`);
  post.onclick = (e) => {
    e.stopPropagation();
    confirmGitHubComment(c);
  };
  const pick = el(`<button class="na${c.forGitHub ? " on" : ""}" title="${c.forGitHub ? "Take it out of the review you send to GitHub" : "Send it with the review you send to GitHub at the end"}">${c.forGitHub ? "✓ In GitHub review" : "Add to GitHub review"}</button>`);
  pick.onclick = (e) => {
    e.stopPropagation();
    act("ToggleForGitHub", c.id).then(renderGitHubButton);
  };
  return [post, pick];
}

function githubRecap() {
  const box = el(`<p></p>`);
  if (state.gh) {
    const n = reader().comments.filter((c) => c.forGitHub && !c.github && !c.fix).length;
    const b = el(`<button class="small">Send review to GitHub…</button>`);
    b.onclick = openGitHubReview;
    box.append(b, el(`<span class="muted small"> to PR #${state.gh.number}: ${n ? plural(n, "picked comment", "picked comments") : "no comments picked yet"} and your decision (comment, approve or request changes). You confirm before anything is posted.</span>`));
  } else if (state.ghErr) {
    const b = el(`<button class="small">Look again</button>`);
    b.onclick = () => loadGitHub(state.opened, true);
    box.append(el(`<span class="muted small">No GitHub pull request for this review: ${esc(state.ghErr)} </span>`), b);
  } else if (state.ghLoading) box.append(el(`<span class="muted small">Looking for the GitHub pull request…</span>`));
  return box;
}

const short = (sha) => (sha ?? "").slice(0, 9);

function prHead(pr) {
  const head = el(`<div class="gh-pr"><button class="linkish" title="Open it on GitHub">PR #${pr.number}</button> ${esc(pr.title)}
    <span class="muted small">by ${esc(pr.author || "?")} · you post as ${esc(pr.viewer || "your gh account")}</span></div>`);
  head.querySelector("button").onclick = () => openURL(pr.url);
  if (pr.state && pr.state !== "OPEN") head.append(el(`<div class="gh-warn">This pull request is ${esc(pr.state.toLowerCase())}.</div>`));
  if (pr.differs)
    head.append(el(`<div class="gh-warn">Your review shows ${esc(short(pr.local) || "local changes")}, the pull request is at ${esc(short(pr.headSha))}. Comments are matched to the pull request's lines by their text; check where each one lands below.</div>`));
  return head;
}

function placeLine(pl) {
  const where = `<code>${esc(pl.path)}:${pl.line}</code>${pl.side === "LEFT" ? " (old code)" : ""}`;
  if (!pl.inFile) return `<div class="gh-place bad">${where} — ${esc(pl.why)}</div>`;
  if (!pl.inline) return `<div class="gh-place warn">${where} — ${esc(pl.why)}</div>`;
  return `<div class="gh-place">${where} — on the line${pl.moved ? `; ${esc(pl.why)}` : ""}</div>`;
}

// postedText is what GitHub will show, so the confirmation shows exactly that.
function postedText(pl, single) {
  if (pl.inline) return pl.body;
  const ref = `${pl.side === "LEFT" ? "old line" : "line"} ${pl.line}`;
  return single ? `${ref}: ${pl.body}` : `**${pl.path}**, ${ref}:\n\n${pl.body}`;
}

async function confirmGitHubComment(c) {
  const body = el(`<div class="gh-confirm"><p class="muted">Reading the pull request…</p></div>`);
  openModal("Comment on GitHub", body, "narrow");
  let plan;
  try {
    plan = await api("Reviews.PlanGitHubComment", c.id);
  } catch (e) {
    body.innerHTML = `<p class="gh-place bad">${esc(errText(e))}</p>`;
    return;
  }
  const pl = plan.comments[0];
  body.innerHTML = "";
  body.append(prHead(plan.pr), el(placeLine(pl)));
  const pre = el(`<pre class="commit gh-text"></pre>`);
  pre.textContent = postedText(pl, true);
  const foot = el(`<div class="cp-foot"><span class="muted small">Posted at once, under your name, for everyone on the pull request to see. margin cannot take it back.</span>
    <span class="spacer"></span><button class="small cp-cancel">Cancel</button><button class="primary small cp-save">Post to GitHub</button></div>`);
  body.append(pre, foot);
  const save = foot.querySelector(".cp-save");
  foot.querySelector(".cp-cancel").onclick = () => closeModal();
  if (!pl.inFile) save.disabled = true;
  save.onclick = async () => {
    save.disabled = true;
    save.textContent = "Posting…";
    if (await act("PostGitHubComment", c.id)) {
      closeModal();
      toast(`${c.id} is on GitHub`);
      loadThreads();
    } else {
      save.disabled = false;
      save.textContent = "Post to GitHub";
    }
  };
  foot.querySelector(".cp-cancel").focus();
}

const ghEvents = [
  { id: "COMMENT", label: "Comment", tip: "General feedback, without approving or blocking" },
  { id: "APPROVE", label: "Approve", tip: "Approve the change for merging" },
  { id: "REQUEST_CHANGES", label: "Request changes", tip: "Feedback that has to be addressed before merging" },
];

async function openGitHubReview() {
  const body = el(`<div class="gh-confirm"><p class="muted">Reading the pull request…</p></div>`);
  openModal("Send review to GitHub", body, "narrow");
  let plan;
  try {
    plan = await api("Reviews.PlanGitHubReview");
  } catch (e) {
    body.innerHTML = `<p class="gh-place bad">${esc(errText(e))}</p>`;
    return;
  }
  const keep = state.ghReview?.path === state.opened?.path ? state.ghReview : null;
  const draft = (state.ghReview = { path: state.opened?.path, summary: keep?.summary ?? "", event: keep?.event ?? plan.suggest });
  if (plan.pr.own && draft.event !== "COMMENT") draft.event = "COMMENT";
  body.innerHTML = "";
  body.append(prHead(plan.pr));
  const list = el(`<div class="gh-list"><h4>Comments (${plan.comments.length})</h4></div>`);
  if (!plan.comments.length) list.append(el(`<p class="muted small">None picked. "Add to GitHub review" on a comment puts it here; you can also send just a summary and a decision.</p>`));
  for (const pl of plan.comments) {
    const item = el(`<div class="gh-item">${placeLine(pl)}</div>`);
    const pre = el(`<pre class="commit gh-text"></pre>`);
    pre.textContent = pl.inline ? pl.body : "(goes into the review text below the summary)\n\n" + pl.body;
    const drop = el(`<button class="na" title="Leave it out of the review">Remove</button>`);
    drop.onclick = async () => {
      if (await act("ToggleForGitHub", pl.id)) openGitHubReview();
    };
    item.append(pre, drop);
    list.append(item);
  }
  const loose = plan.comments.filter((pl) => !pl.inline && pl.inFile);
  body.append(list, el(`<h4>Summary</h4>`));
  const ta = el(`<textarea rows="4" placeholder="The review's overall text (optional for approve)"></textarea>`);
  ta.value = draft.summary;
  ta.oninput = () => {
    draft.summary = ta.value;
  };
  ta.onkeydown = (e) => e.stopPropagation();
  body.append(ta);
  if (loose.length || plan.comments.some((pl) => !pl.inFile))
    body.append(el(`<p class="muted small">Comments GitHub cannot put on a line go into the review text, after your summary, with their file and line.</p>`));
  const dec = el(`<div class="gh-events"><h4>Decision</h4></div>`);
  const sug = ghEvents.find((x) => x.id === plan.suggest);
  for (const ev of ghEvents) {
    const off = plan.pr.own && ev.id !== "COMMENT";
    const row = el(`<label class="gh-event${off ? " off" : ""}" title="${esc(off ? "GitHub does not let you do this on your own pull request" : ev.tip)}">
      <input type="radio" name="gh-event" value="${ev.id}"${draft.event === ev.id ? " checked" : ""}${off ? " disabled" : ""}> ${ev.label}
      <span class="muted small">${esc(ev.tip)}${ev.id === plan.suggest && sug ? " · margin suggests this from your verdicts" : ""}</span></label>`);
    row.querySelector("input").onchange = () => {
      draft.event = ev.id;
      label();
    };
    dec.append(row);
  }
  body.append(dec);
  const foot = el(`<div class="cp-foot"><span class="muted small">Posted at once, under your name, for everyone on the pull request to see. margin cannot take it back.</span>
    <span class="spacer"></span><button class="small cp-cancel">Cancel</button><button class="primary small cp-save"></button></div>`);
  body.append(foot);
  const save = foot.querySelector(".cp-save");
  const label = () => {
    save.textContent = { COMMENT: "Submit comments", APPROVE: "Approve", REQUEST_CHANGES: "Request changes" }[draft.event] + ` on PR #${plan.pr.number}`;
  };
  label();
  foot.querySelector(".cp-cancel").onclick = () => closeModal();
  save.onclick = async () => {
    save.disabled = true;
    save.textContent = "Sending…";
    if (await act("SubmitGitHubReview", draft.event, draft.summary)) {
      state.ghReview = null;
      closeModal();
      renderGitHubButton();
      toast(`Review sent to PR #${plan.pr.number}`);
      loadThreads();
    } else {
      save.disabled = false;
      label();
    }
  };
}

// Overlays: peek, commit, export, help

async function openPeek(file, line, onclosed = null) {
  try {
    const m = showPeek(await api("Reviews.Peek", file, line));
    if (onclosed) m.onclosed = onclosed;
  } catch (e) {
    toast(errText(e), true);
    onclosed?.();
  }
}

function showPeek(pv) {
  const code = el(`<div class="code peek"></div>`);
  for (const r of pv.rows) {
    code.append(el(`<div class="row${r.n - 1 === pv.at ? " sel" : ""}"><span class="mark"></span><span class="cov"></span><span class="ln">${r.n}</span><span class="src">${renderSpans(r.s, "") || " "}</span></div>`));
  }
  const m = openModal(pv.title, code);
  requestAnimationFrame(() => code.querySelector(".row.sel")?.scrollIntoView({ block: "center" }));
  return m;
}

async function openCommit(sha, onclosed = null) {
  try {
    const pv = await api("Reviews.Commit", sha);
    const pre = el(`<pre class="commit">${esc(pv.text)}</pre>`);
    const m = openModal(pv.title, pre);
    if (onclosed) {
      linkRefs(pre);
      m.onclosed = onclosed;
    }
  } catch (e) {
    toast(errText(e), true);
    onclosed?.();
  }
}

async function openExport() {
  const body = el(`<div class="export"><div class="cp-labels"><button class="chip on" data-f="md">Markdown</button><button class="chip" data-f="github">GitHub review</button>
    <span class="spacer"></span><button class="primary small">Copy</button></div><pre class="commit">Loading…</pre>
    <p class="muted small">Copy this and post it yourself, or use the GitHub buttons on your comments.</p></div>`);
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
  ["Walk", [["space", "next step; at the end of a stop, the next stop"], ["b", "previous step"], ["w", "switch between step by step and the full view"], ["e", "more of the stop card or overview; on a draft comment, edit it"], ["y n ?", "answer the orient question (overview)"]]],
  ["Move", [["} {", "next / previous stop"], ["] [", "next / previous note"], ["j k", "move the selected line"], ["J K shift+click", "select a block of lines"], ["ctrl+d ctrl+u", "half a page down / up"], ["g G", "top / bottom"], ["h l 0", "scroll code sideways / back"], ["N", "next new note (or previous match)"], ["L", "follow the note the selected note mentions (stop:n); again for the next one"], ["ctrl+o", "back to where you followed a mention from"]]],
  ["Judge", [["1 2 3", "verdict: looks good / needs changes / not sure"], ["space", "mark the selected note read in the full view"], ["?", "agree with the selected problem"], ["P", "ask the agent to fix the problems you agreed with and the stops that need changes"], ["x", "dismiss the selected note"], ["enter", "tick the selected call"], ["v", "reveal note cards on this stop"]]],
  ["Talk", [["c", "comment on the selected line (draft); Edit on its card rewords it"], ["S", "send all drafts to the agent"], ["A", "the agent's answers to your comments: read, edit, reply"], ["B", "the GitHub panel, back where you left it"], ["a", "ask the agent about the selected line"], ["y Y", "copy the note / the whole stop"], ["`", "focus the agent terminal; ctrl+esc leaves it"]]],
  ["See", [["/", "search; n N next / previous"], ["F", "filter notes by kind or focus"], ["n", "cycle note visibility"], ["s", "switch between inline and split"], ["z", "fold unchanged code"], ["+ -", "more / less context on the selected part"], ["f", "whole file of the selected part"], ["t", "tests that run the selected line"], ["T", "run them (the stop's tests when no line is selected)"], ["i", "try a change to the selected line or block: edit or delete it, its tests run against that, then it is put back"], ["o", "show or hide the test output"], ["u U", "next / previous untested change"], ["esc", "end search or spotlight, close an overlay"], ["r", "reload"], ["\\", "show or hide the sidebar"], ["ctrl+k", "go to a repository, worktree, branch or review"], ["ctrl+,", "settings"]]],
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
  const first = navItems().find((x) => x.getBoundingClientRect().top >= top);
  if (first) placeReviewCursor(first);
}

export function reviewKey(e) {
  if (!state.view || state.opened?.switchingWalkthrough) return false;
  const k = e.key;
  const s = cur();
  if (k === "Tab") return false;
  if (document.querySelector(".modal-back")) return modalKey(e);
  const ov = s.kind === "overview" && guided();
  const sel = state.sel && state.sel.station === s.id ? state.sel : null;
  const keys = {
    w: () => setReadingMode(guided()),
    e: () => {
      if (editKey(s, sel)) return;
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
    J: () => moveCursor(1, null, true),
    K: () => moveCursor(-1, null, true),
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
    n: () => (ov ? orient("no") : state.query ? searchNext(1) : cycleToolbarChoice(noteVisibilityButtons)),
    N: () => (state.query ? searchNext(-1) : nextFresh()),
    "?": () => (ov ? orient("unsure") : noteMark("flagged")),
    x: xKey,
    Enter: activate,
    v: reveal,
    F: cycleFilter,
    s: () => setSplit(!state.split),
    z: () => guided() ? toast(`Switch to ${fullDisplayLabel()} to fold unchanged code.`) : toggle("unfold"),
    "/": toolbarOverflow.focusSearch,
    Escape: () => {
      if (state.query) return endSearch();
      if (state.spot >= 0) return endSpot();
      if (state.block && !state.trying) {
        state.block = null;
        markBlock();
        return renderStatus();
      }
      if (state.compose || state.trying || state.editing) {
        state.compose = null;
        state.editing = null;
        endTry();
        renderKeepScroll();
      }
    },
    t: openTests,
    T: runHere,
    i: () => startTry(),
    o: toggleTestPanel,
    u: () => nextUntested(1),
    U: () => nextUntested(-1),
    "+": () => (sel ? widen(s, sel.part, 1) : needLine("show more around it")),
    "=": () => (sel ? widen(s, sel.part, 1) : needLine("show more around it")),
    "-": () => (sel ? widen(s, sel.part, -1) : needLine("show less around it")),
    f: () => (sel ? wholeFile(s, sel.part) : needLine("see its whole file")),
    a: () => startOnLine("ask"),
    c: () => startOnLine("comment"),
    S: sendComments,
    P: sendFixes,
    A: () => openDiscussionAnswers(),
    B: () => (state.gh ? openGitHubPanel() : toast("No GitHub pull request for this review.")),
    r: () => reload().then(() => toast("reloaded")),
    H: openHelp,
    L: followSelectedRef,
  };
  if (e.shiftKey && (k === "ArrowDown" || k === "ArrowUp")) {
    moveCursor(k === "ArrowDown" ? 1 : -1, null, true);
    return true;
  }
  if (e.ctrlKey && (k === "d" || k === "u")) {
    pageMove(k === "d" ? 1 : -1);
    return true;
  }
  if (e.ctrlKey && k === "o") {
    jumpBack();
    return true;
  }
  if (e.ctrlKey || e.metaKey || e.altKey) return false;
  const fn = keys[k];
  if (!fn) return false;
  fn();
  return true;
}

function stepPosition(s, step, line = null) {
  if (!step) return null;
  if (step.kind === "mech") {
    const sub = step.subs?.find((item) => line?.station === s.id && stepShows(s, item, line.part, line.line)) ?? step.subs?.[0];
    return { ...stepPosition(s, sub), kind: "mech" };
  }
  if (step.kind !== "change" && step.kind !== "background") return { kind: step.kind };
  const part = s.parts[step.part];
  return { kind: step.kind, file: part?.file ?? "", side: part?.base ? "base" : "", from: step.from, to: step.to };
}

function indexOfReadingStep(station, saved) {
  const steps = station.steps ?? [];
  if (!saved.file) return steps.findIndex((step) => step.kind === saved.kind);
  const match = (exact) => steps.findIndex((step) => {
    const candidates = step.kind === "mech" ? step.subs ?? [] : [step];
    return candidates.some((candidate) => {
      const key = stepPosition(station, candidate);
      const kind = saved.kind === "mech" ? "change" : saved.kind;
      return key.kind === kind && key.file === saved.file && key.side === (saved.side ?? "") &&
        (exact ? key.from === saved.from && key.to === saved.to : key.from <= saved.from && key.to >= saved.from);
    });
  });
  const exact = match(true);
  return exact >= 0 ? exact : match(false);
}

function goToFirstFile() {
  const first = state.view.stations.findIndex((s) => s.kind === "code");
  if (first < 0) return goStation(0);
  const step = guided() ? state.view.stations[first].steps?.findIndex((s) => s.kind === "change" || s.kind === "mech") : 0;
  return goStation(first, -1, Math.max(0, step ?? 0));
}

async function restoreReadingPosition(position) {
  if (!position) return false;
  const index = state.view.stations.findIndex((station) => station.id === position.station);
  if (index < 0) {
    await goStation(0);
    return true;
  }
  const s = state.view.stations[index];
  const saved = position.step;
  const step = saved && guided() ? indexOfReadingStep(s, saved) : 0;
  await goStation(index, -1, Math.max(0, step));
  if (step >= 0) $("#station").scrollTop = position.scroll;
  return true;
}

function currentReadingStep(station, visible = null) {
  const step = station.steps?.[state.step];
  if (step?.kind !== "mech") return stepPosition(station, step);
  const composer = state.compose;
  if (composer?.station === station.id) return stepPosition(station, step, composer);
  const draft = state.editing && reader().comments.find((comment) => comment.id === state.editing.id);
  if (draft?.station === station.id) {
    return stepPosition(station, step, { station: station.id, part: draft.part, line: draft.at });
  }
  const row = visible ?? visibleCodeAnchor($("#station"));
  const line = row ? { station: station.id, part: Number(row.part), line: Number(row.line) } : state.sel;
  return stepPosition(station, step, line);
}

function readingPosition(s) {
  return { station: s.id, mode: state.view.mode ?? "", step: walking() ? currentReadingStep(s) : null,
    scroll: $("#station").scrollTop };
}

function captureReadingOrigin() {
  const s = cur();
  const positions = (s.parts ?? []).map((p, i) => {
    const key = partKey(s, i);
    return { file: p.file, base: !!p.base, from: p.start, extra: state.expand[key] ?? 0, whole: !!state.whole[key] };
  });
  return { ...readingPosition(s), path: state.view.path, full: reader().full, positions,
    sel: state.sel?.station === s.id ? { ...state.sel } : null,
    block: state.block?.station === s.id ? { ...state.block } : null,
    note: s.notes[state.note]?.key,
    horizontal: $$("#station .code-scroll").map((x) => x.scrollLeft) };
}

async function backToReading() {
  const origin = readingOrigin;
  if (!origin || !beginDiscussionAction()) return;
  try {
    if (state.view?.path !== origin.path && !await openReviewFile(origin.path, false, true)) return;
    closeModal();
    hooks.showReviewer();
    activeThread = "";
    if (reader().full !== origin.full) await act("SetFull", origin.full);
    const s = state.view.stations.find((station) => station.id === origin.station);
    const matches = (i) => {
      const p = s?.parts[i], saved = origin.positions[i];
      return p && saved && p.file === saved.file && !!p.base === saved.base && p.start === saved.from;
    };
    s?.parts.forEach((_, i) => {
      const key = partKey(s, i);
      delete state.parts[key];
      state.expand[key] = matches(i) ? origin.positions[i].extra : 0;
      state.whole[key] = matches(i) && origin.positions[i].whole;
    });
    await restoreReadingPosition(origin);
    state.sel = origin.sel && matches(origin.sel.part) && rowAt(origin.sel.part, origin.sel.line) ? origin.sel : null;
    state.block = state.sel && origin.block && matches(origin.block.part) ? origin.block : null;
    state.note = origin.note ? s?.notes.findIndex((note) => note.key === origin.note) ?? -1 : -1;
    renderKeepScroll();
    $("#station").scrollTop = origin.scroll;
    $$("#station .code-scroll").forEach((x, i) => { x.scrollLeft = origin.horizontal[i] ?? 0; });
    renderStatus();
    readingOrigin = null;
    queueReadingPosition(0, true);
  } finally {
    discussionBusy = false;
    updateDiscussionControls();
  }
}

let readingTimer, pendingPosition, savingPosition;
function queueReadingPosition(delay = 0, remember = false) {
  const opened = state.opened, station = cur();
  if (!opened || opened.restoringPosition || state.view?.path !== opened.path || !station || readingOrigin) return;
  const position = readingPosition(station);
  position.scroll = Math.round(position.scroll);
  const key = JSON.stringify(position);
  if (!remember && key === opened.savedPosition && pendingPosition?.opened !== opened) return;
  const rememberReview = remember || (pendingPosition?.opened === opened && pendingPosition.remember);
  pendingPosition = { opened, position, key, remember: !!rememberReview };
  clearTimeout(readingTimer);
  if (delay) readingTimer = setTimeout(flushReadingPosition, delay);
  else flushReadingPosition();
}

function flushReadingPosition() {
  clearTimeout(readingTimer);
  if (!pendingPosition || savingPosition) return savingPosition;
  savingPosition = (async () => {
    while (pendingPosition) {
      const { opened, position, key, remember } = pendingPosition;
      pendingPosition = null;
      if (state.opened !== opened || (!remember && key === opened.savedPosition)) continue;
      try {
        await api("Reviews.SaveReadingPosition", opened.path, position, remember);
        opened.savedPosition = key;
      } catch (e) {
        if (state.opened === opened && !opened.positionError) {
          opened.positionError = true;
          toast("Could not save the reading position: " + errText(e), true);
        }
      }
    }
  })().finally(() => {
    savingPosition = null;
    if (pendingPosition) return flushReadingPosition();
  });
  return savingPosition;
}

$("#station").addEventListener("scroll", () => queueReadingPosition(200), { passive: true });
window.addEventListener("pagehide", flushReadingPosition);

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
  if (guided() && s.kind !== "code") return `space next · w ${fullDisplayLabel()} · `;
  if (s.kind !== "code") return "";
  if (walking()) return `space next · b back · e more · w ${fullDisplayLabel()} · `;
  return state.view.hasBase ? `s ${state.split ? "inline" : "split"} · ` : "";
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
  if (c.to_fix) pills.push(`<span class="pill p-issue">⚑ ${c.to_fix} to fix · P sends</span>`);
  if (c.open_questions) pills.push(`<span class="pill m-chg">${c.open_questions} waiting for the agent</span>`);
  if (state.query) pills.push(`<span class="pill p-match">/${esc(state.query)} · n N · esc</span>`);
  const [p, warn] = pace();
  pills.push(`<span class="pill${warn ? " m-chg" : " muted"}">${esc(p)}</span>`);
  pills.push(cursorCoverage());
  covHint();
  bar.innerHTML = `${pills.join("")}<span class="spacer"></span><span class="muted">${esc(viewHint())}H help</span>`;
}

// Keyboard reach: j k move over every line and link, enter acts on it, like the terminal cursor.

function navItems() {
  return $$("#station .row[data-line], #station .navable, #station .vb");
}

function releaseDisclosureFocus(next) {
  const focused = document.activeElement;
  if (focused?.matches("#station .review-disclosure > summary") && !next.contains(focused)) focused.blur();
}

function placeReviewCursor(item, extend) {
  $$("#station .kcur").forEach((x) => x.classList.remove("kcur"));
  releaseDisclosureFocus(item);
  if (item.dataset.line !== undefined) {
    selectRow(cur().id, +item.dataset.part, +item.dataset.line, false, extend);
    return;
  }
  $$("#station .row.sel").forEach((row) => row.classList.remove("sel"));
  if (item.hasAttribute("data-ghost")) {
    state.sel = null;
    state.block = null;
    state.note = -1;
    markBlock();
  }
  item.classList.add("kcur");
  renderStatus();
}

export function moveCursor(dir, jumpTo, extend) {
  const items = navItems();
  if (!items.length) return;
  const cursor = $("#station .kcur");
  const anchor = cursor?.hasAttribute("data-ghost") ? cursor.closest(".row") : cursor;
  let at = items.indexOf(anchor);
  if (at < 0) {
    const s = cur();
    at = items.findIndex((r) => state.sel && r.dataset.line !== undefined && state.sel.station === s.id && +r.dataset.part === state.sel.part && +r.dataset.line === state.sel.line);
  }
  let next = jumpTo === "first" ? 0 : jumpTo === "last" ? items.length - 1 : at < 0 ? (dir > 0 ? 0 : items.length - 1) : Math.max(0, Math.min(items.length - 1, at + dir));
  const it = items[next];
  placeReviewCursor(it, extend);
  it.scrollIntoView({ block: "nearest" });
}

function activate() {
  const k = document.querySelector("#station .kcur");
  if (k?.matches(".review-disclosure")) {
    const summary = k.firstElementChild;
    summary.focus({ preventScroll: true });
    return summary.click();
  }
  if (k) return k.click();
  const n = selectedNote();
  if (n?.kind === "decide") return act("MarkNote", n.key, "reviewed");
  if (n && (n.kind === "question" || n.unbacked) && n.part >= 0) return openComposer(cur().id, n.part, n.line, { answers: n.key });
  const s = cur();
  if (state.sel?.station === s.id) {
    const r = partData(s, state.sel.part)?.rows.find((x) => x.n - 1 === state.sel.line);
    const first = (r?.notes ?? []).find((i) => navigableNote(s, s.notes[i]));
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
