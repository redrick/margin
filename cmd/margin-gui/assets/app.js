import { Events } from "/wails/runtime.js";
import { api, $, state, hooks, toast, errText, tail, closeModal } from "./core.js";
import { openReview, openReviewFile, reviewKey, renderReview, sendComments } from "./review.js";
import { loadWorkspace, loadRecent, renderSide, addFolder, selectWorktree, homeKey, backToReview, syncSideButtons, openSwitcher, switcherOpen, openSettings, navState } from "./nav.js";

// Agent terminal

const term = new Terminal({
  fontFamily: getComputedStyle(document.body).getPropertyValue("--mono"),
  fontSize: 13,
  cursorBlink: true,
  allowProposedApi: true,
  scrollback: 5000,
  theme: { background: "#0a0d12", foreground: "#d7dde4", cursor: "#7aa2f7", selectionBackground: "#264f78" },
});
const fit = new FitAddon.FitAddon();
term.loadAddon(fit);
term.open($("#terminal"));

function b64(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function fitTerminal() {
  if ($("#agent-pane").classList.contains("collapsed") || $("#reviewer").classList.contains("hidden")) return;
  try {
    fit.fit();
  } catch {}
  if (state.agentLive && state.opened) api("Agents.Resize", state.opened.path, term.cols, term.rows);
}

function setAgentLive(live) {
  const was = state.agentLive;
  if (was !== live) loadRecent();
  state.agentLive = live;
  if (was !== live && state.view) renderReview();
  $("#agent-dot").classList.toggle("live", live);
  $("#agent-label").textContent = live ? `Agent · ${tail(state.opened?.repo ?? "", 1)}` : "Agent not running";
  $("#agent-btn").textContent = live ? "Stop agent" : "Start agent";
}

async function attachAgent() {
  term.reset();
  const live = await api("Agents.Running", state.opened.path);
  setAgentLive(live);
  setAgentCollapsed(!live);
  if (live) {
    const buf = await api("Agents.Start", state.opened.path, state.opened.repo, false, term.cols, term.rows);
    if (buf) term.write(b64(buf));
  } else {
    term.write("\x1b[2mPress Start agent: it reads the change, shapes the tour and writes notes here as it goes.\x1b[0m\r\n");
  }
}

async function toggleAgent() {
  if (!state.opened) return;
  if (state.agentLive) {
    await api("Agents.Stop", state.opened.path);
    return;
  }
  setAgentCollapsed(false);
  term.reset();
  try {
    await api("Agents.Start", state.opened.path, state.opened.repo, state.opened.created, term.cols, term.rows);
    state.opened.created = false;
    setAgentLive(true);
    term.focus();
  } catch (e) {
    toast(errText(e), true);
  }
}

Events.On("agent:data", (ev) => {
  const d = ev.data;
  if (d && state.opened && d.review === state.opened.path) term.write(b64(d.data));
});

Events.On("agent:exit", (ev) => {
  if (state.opened && ev.data === state.opened.path) {
    setAgentLive(false);
    term.write("\r\n\x1b[2m[agent exited]\x1b[0m\r\n");
  }
});

term.onData((d) => {
  if (state.agentLive && state.opened) api("Agents.Write", state.opened.path, d);
});

// Layout and keys

function togglePicker() {
  $("#workspace").classList.toggle("focus");
  syncSideButtons();
  fitTerminal();
}

function showReviewer() {
  $("#welcome").classList.add("hidden");
  $("#home-pane").classList.add("hidden");
  $("#reviewer").classList.remove("hidden");
  syncSideButtons();
  fitTerminal();
}

function reviewOpened() {
  const o = state.opened;
  const w = state.worktree;
  const repoName = w?.repo?.name ?? tail(o?.repo ?? "", 1);
  $("#crumb").textContent = `${repoName} ›`;
  $("#crumb").title = `Back to ${repoName}${w?.branch ? " · " + w.branch : ""}: what else to review there (ctrl+k for anything)`;
  $("#workspace").classList.add("focus");
  syncSideButtons();
  loadRecent();
  renderSide();
}

async function crumbBack() {
  const o = state.opened;
  if (!o) return;
  const w = state.worktree;
  if (w && (w.path === o.repo || w.repo?.worktrees?.some((x) => x.path === o.repo))) {
    $("#workspace").classList.remove("focus");
    return selectWorktree(w, w.repo);
  }
  const nav = navState();
  for (const f of nav.folders) for (const r of f.repos ?? []) for (const x of r.worktrees) if (x.path === o.repo) return selectWorktree(x, r);
  toast("This review's repository is not in the sidebar; add it with ＋.");
}

async function pickReviewFile() {
  try {
    const p = await api("Reviews.PickFile");
    if (p) await openReviewFile(p);
  } catch (e) {
    toast(errText(e), true);
  }
}

(function splitter() {
  const pane = $("#agent-pane");
  let startY, startH;
  $("#splitter").addEventListener("mousedown", (e) => {
    startY = e.clientY;
    startH = pane.getBoundingClientRect().height;
    pane.classList.remove("collapsed");
    const move = (ev) => {
      const h = Math.max(80, Math.min(window.innerHeight - 160, startH + startY - ev.clientY));
      pane.style.height = h + "px";
      fitTerminal();
    };
    const up = () => {
      document.removeEventListener("mousemove", move);
      document.removeEventListener("mouseup", up);
      fitTerminal();
    };
    document.addEventListener("mousemove", move);
    document.addEventListener("mouseup", up);
  });
})();

document.addEventListener("keydown", (e) => {
  if (switcherOpen()) return;
  if (document.activeElement?.closest?.(".cm-editor")) return;
  const inTerm = document.activeElement?.closest?.("#terminal");
  const inInput = ["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement?.tagName) && !inTerm;
  if (inTerm) {
    if (e.key === "Escape" && e.ctrlKey) {
      e.preventDefault();
      document.activeElement.blur();
    }
    return;
  }
  if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "p")) {
    e.preventDefault();
    return openSwitcher();
  }
  if ((e.ctrlKey || e.metaKey) && e.key === ",") {
    e.preventDefault();
    return openSettings();
  }
  if (inInput) return;
  if (!e.ctrlKey && !e.metaKey && !e.altKey) {
    if (e.key === "\\") {
      e.preventDefault();
      return togglePicker();
    }
    if (e.key === "`" && state.opened && !$("#reviewer").classList.contains("hidden")) {
      e.preventDefault();
      return term.focus();
    }
  }
  const modal = document.querySelector(".modal-back");
  if (modal && !state.view) {
    if (modal.onkey?.(e) || e.key === "Escape") e.preventDefault();
    if (e.key === "Escape") closeModal();
    return;
  }
  if (!modal && homeKey(e)) return e.preventDefault();
  if (!$("#reviewer").classList.contains("hidden") && reviewKey(e)) e.preventDefault();
});

$("#jump").onclick = openSwitcher;
$("#settings-btn").onclick = openSettings;
$("#add-folder").onclick = addFolder;
$("#open-file").onclick = pickReviewFile;
$("#repo-filter").oninput = renderSide;
$("#hide-side").onclick = togglePicker;
$("#show-side").onclick = togglePicker;
$("#toggle-picker").onclick = togglePicker;
$("#crumb").onclick = crumbBack;
$("#back-review").onclick = backToReview;
$("#wt-refresh").onclick = () => state.worktree && selectWorktree(state.worktree, state.worktree.repo);
$("#agent-btn").onclick = toggleAgent;
$("#send-btn").onclick = sendComments;
$("#agent-submit").onchange = (e) => api("Agents.SetSubmit", e.target.checked);

function setAgentCollapsed(collapsed) {
  $("#agent-pane").classList.toggle("collapsed", collapsed);
  $("#agent-collapse").textContent = collapsed ? "▴" : "▾";
  fitTerminal();
}
$("#agent-collapse").onclick = () => setAgentCollapsed(!$("#agent-pane").classList.contains("collapsed"));
$("#refresh").onclick = () => {
  if (state.opened?.file) return toast("Opened from a file: start it over from its repository instead.");
  if (state.opened && confirmFresh()) openReview(state.opened.repo, state.opened.arg, true);
};
window.addEventListener("resize", fitTerminal);
Object.assign(hooks, { fitTerminal, focusTerminal: () => term.focus(), attachAgent, showReviewer, reviewOpened, pickReviewFile });

let freshArmed = 0;
function confirmFresh() {
  if (Date.now() - freshArmed < 3000) return true;
  freshArmed = Date.now();
  toast("Click ↺ again to throw away this review's stops and notes and start over.");
  return false;
}

api("Reviews.Recovered").then(
  (fixed) => fixed?.length && toast(`Put back ${fixed.length === 1 ? fixed[0] : `${fixed.length} files`}: a tried change was still in ${fixed.length === 1 ? "it" : "them"} from a run margin did not finish.`),
  (e) => toast(errText(e), true),
);

(async () => {
  const init = await api("Workspace.Initial");
  await loadWorkspace();
  if (init?.review) return openReviewFile(init.review);
  if (!init?.root) {
    if (navState().folders.length) $("#workspace").classList.remove("focus");
    return;
  }
  const nav = navState();
  for (const f of nav.folders) for (const r of f.repos ?? []) for (const w of r.worktrees) {
    if (w.path === init.root || r.path === init.root) {
      if (!init.target) return selectWorktree(w, r);
      state.worktree = { ...w, repo: r };
    }
  }
  if (init.target) await openReview(init.root, init.target === "." ? "" : init.target);
})();
