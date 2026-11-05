import { api, $, $$, state, hooks, esc, el, toast, errText, tail, plural, fuzzy, openModal, closeModal } from "./core.js";
import { openReview, openReviewFile } from "./review.js";
import { openAnswers } from "./answers.js";

// Navigation: the sidebar holds the reviews in progress and the repositories with their worktrees;
// the worktree page lists what there is to review in one worktree; ctrl+k jumps anywhere.

const home = (p) => String(p ?? "").replace(/^\/home\/[^/]+|^\/Users\/[^/]+/, "~");
const nav = { folders: [], recent: [], live: new Set(), status: {}, expanded: new Set(), tab: "branches", remote: false, prs: null, prErr: "", cursor: -1 };

// Sidebar

export async function loadWorkspace() {
  nav.folders = await api("Workspace.Folders");
  renderSide();
  refreshStatus();
  await loadRecent();
  if (!state.opened && !state.worktree) {
    $("#welcome").classList.remove("hidden");
    renderWelcome();
  }
}

export async function loadRecent() {
  try {
    nav.recent = (await api("Workspace.RecentReviews")) ?? [];
    nav.live = new Set((await api("Agents.Live")) ?? []);
  } catch {
    nav.recent = [];
  }
  renderRecent();
}

async function refreshStatus() {
  const dirs = worktrees().map((w) => w.path);
  if (!dirs.length) return;
  nav.status = (await api("Workspace.Status", dirs)) ?? {};
  renderSide();
  if (!$("#welcome").classList.contains("hidden")) renderWelcome();
}

function worktrees() {
  return nav.folders.flatMap((f) => (f.repos ?? []).flatMap((r) => r.worktrees.map((w) => ({ ...w, repo: r }))));
}

function wtLabel(w) {
  return w.branch || (w.detached ? "detached at " + w.head : w.head);
}

function renderRecent() {
  const box = $("#recent-reviews");
  box.innerHTML = "";
  $("#recent-sec").classList.toggle("hidden", !nav.recent.length);
  for (const r of nav.recent.slice(0, 8)) {
    const on = state.opened?.path === r.path;
    const live = nav.live.has(r.path);
    const item = el(`<div class="rv-item${on ? " on" : ""}" title="${esc(r.path)}">
      <div class="l1"><span class="dot${live ? " live" : ""}" title="${live ? "agent running" : ""}"></span><span class="name">${esc(r.title)}</span><button class="icon forget" title="Take off this list (the file stays)">✕</button></div>
      <div class="sub">${esc(r.kicker)}</div>
      <div class="sub">${progress(r)} · ${esc(r.updated)}</div></div>`);
    item.onclick = () => openFile(r.path);
    item.querySelector(".forget").onclick = async (e) => {
      e.stopPropagation();
      await api("Workspace.ForgetReview", r.path);
      loadRecent();
    };
    box.append(item);
  }
}

function progress(r) {
  const parts = [`${r.judged}/${r.stops} stops judged`];
  if (r.drafts) parts.push(plural(r.drafts, "draft", "drafts"));
  return parts.join(" · ");
}

export function renderSide() {
  const q = $("#repo-filter").value.trim().toLowerCase();
  const box = $("#repos");
  box.innerHTML = "";
  const many = nav.folders.length > 1 || nav.folders.some((f) => (f.repos ?? []).length > 1);
  for (const f of nav.folders) {
    const repos = (f.repos ?? []).filter((r) => !q || r.name.toLowerCase().includes(q) || r.worktrees.some((w) => wtLabel(w).toLowerCase().includes(q) || w.path.toLowerCase().includes(q)));
    if (q && !repos.length) continue;
    const single = (f.repos ?? []).length === 1 && f.repos[0].path === f.path;
    if (!single || nav.folders.length > 1) {
      const head = el(`<div class="folder" title="${esc(f.path)}"><span>${esc(single ? "" : f.name)}</span><span class="where">${esc(home(f.path))}</span>
        <button class="icon forget" title="Remove from the sidebar (nothing on disk changes)">✕</button></div>`);
      if (single) head.querySelector("span").remove();
      head.querySelector(".forget").onclick = async () => {
        nav.folders = await api("Workspace.RemoveFolder", f.path);
        renderSide();
      };
      box.append(head);
    }
    if (f.err) box.append(el(`<div class="side-err">${esc(f.err)}</div>`));
    for (const r of repos) {
      const open = !!q || nav.expanded.has(r.path) || !many || r.worktrees.some((w) => w.path === state.worktree?.path);
      const dirty = r.worktrees.reduce((n, w) => n + (nav.status[w.path] ?? 0), 0);
      const multi = r.worktrees.length > 1;
      const head = el(`<div class="repo-name${!multi && state.worktree?.path === r.worktrees[0].path ? " on" : ""}" title="${esc(r.path)}">
        <span class="chev">${multi ? (open ? "▾" : "▸") : ""}</span><span class="rn">${esc(r.name)}</span>
        ${!multi ? `<span class="branch-tag">${esc(wtLabel(r.worktrees[0]))}</span>` : ""}
        <span class="count">${multi ? r.worktrees.length + " worktrees" : ""}${dirty ? ` <span class="dirty" title="changed files">●${dirty}</span>` : ""}</span></div>`);
      head.onclick = () => {
        if (!multi) return selectWorktree(r.worktrees[0], r);
        nav.expanded.has(r.path) ? nav.expanded.delete(r.path) : nav.expanded.add(r.path);
        renderSide();
      };
      box.append(head);
      if (!multi || !open) continue;
      for (const w of r.worktrees) {
        const n = nav.status[w.path] ?? 0;
        const item = el(`<div class="wt${state.worktree?.path === w.path ? " on" : ""}" title="${esc(w.path)}">
          <span class="branch">${esc(wtLabel(w))}${w.main ? "" : ' <span class="badge">worktree</span>'}${n ? ` <span class="dirty" title="changed files">●${n}</span>` : ""}</span>
          <span class="where">${esc(home(w.path))}</span></div>`);
        item.onclick = () => selectWorktree(w, r);
        box.append(item);
      }
    }
  }
}

export async function addFolder() {
  try {
    const p = await api("Workspace.PickFolder");
    if (!p) return;
    nav.folders = await api("Workspace.AddFolder", p);
    $("#welcome").classList.add("hidden");
    renderSide();
    refreshStatus();
    const wt = worktrees().find((w) => w.path === p || w.repo.path === p) ?? (worktrees().length === 1 ? worktrees()[0] : null);
    if (wt) selectWorktree(wt, wt.repo);
  } catch (e) {
    toast(errText(e), true);
  }
}

async function openFile(path) {
  const rv = nav.recent.find((r) => r.path === path);
  const wt = rv && worktrees().find((w) => w.path === rv.repo);
  if (wt) {
    state.worktree = { ...wt, repo: wt.repo };
    renderSide();
  }
  await openReviewFile(path);
}

// The worktree page

export function showHome() {
  $("#reviewer").classList.add("hidden");
  $("#welcome").classList.add("hidden");
  $("#home-pane").classList.remove("hidden");
  $("#back-review").classList.toggle("hidden", !state.opened);
  syncSideButtons();
}

export async function selectWorktree(w, repo) {
  if (state.worktree?.path !== w.path) {
    nav.prs = null;
    nav.prErr = "";
    nav.cursor = -1;
    $$("#wt-body .filter").forEach((f) => (f.value = ""));
  }
  state.worktree = { ...w, repo: repo ?? w.repo };
  renderSide();
  showHome();
  $("#wt-crumbs").textContent = `${state.worktree.repo?.name ?? ""}${w.main ? "" : " · worktree"} · ${home(w.path)}`;
  $("#wt-title").textContent = wtLabel(w);
  $("#wt-body").innerHTML = `<div class="spinner">Reading git…</div>`;
  await loadTargets();
}

async function loadTargets() {
  const dir = state.worktree?.path;
  if (!dir) return;
  try {
    state.targets = await api("Workspace.Targets", dir);
  } catch (e) {
    $("#wt-body").innerHTML = `<div class="spinner error">${esc(errText(e))}</div>`;
    return;
  }
  if (state.worktree?.path !== dir) return;
  renderHome();
  if (nav.tab === "prs") loadPRs();
}

async function loadPRs() {
  const dir = state.worktree?.path;
  if (!dir || nav.prs) return;
  try {
    nav.prs = (await api("Workspace.PullRequests", dir)) ?? [];
  } catch (e) {
    nav.prErr = errText(e);
    nav.prs = [];
  }
  if (state.worktree?.path === dir) renderHome();
}

function reviewPill(r) {
  if (!r) return "";
  return `<span class="pill-rv" title="${esc(r.path)}">in progress · ${esc(progress(r))}</span>`;
}

export function renderHome() {
  const t = state.targets;
  const body = $("#wt-body");
  if (!t || !body) return;
  const keepFilter = body.querySelector(".pick-filter")?.value ?? "";
  const hadFocus = document.activeElement?.classList.contains("pick-filter");
  body.innerHTML = "";
  const dir = t.dir;

  const cards = el(`<div class="wt-cards"></div>`);
  const card = (title, meta, r, disabled, fn) => {
    const c = el(`<div class="wt-card pick${disabled ? " disabled" : ""}"><div class="c-title">${esc(title)}</div><div class="muted small">${meta}</div>
      <div class="c-foot">${reviewPill(r)}<span class="spacer"></span><button class="primary small"${disabled ? " disabled" : ""}>${r ? "Continue" : "Review"}</button></div></div>`);
    if (!disabled) c.onclick = fn;
    cards.append(c);
  };
  card("Uncommitted changes", t.dirty ? `${plural(t.dirty, "changed file", "changed files")} against HEAD` : "Nothing uncommitted", t.changes, !t.dirty, () => openReview(dir, ""));
  if (t.staged) card("Staged changes", `${plural(t.staged, "file", "files")} in the index: what the next commit holds`, t.stagedReview, false, () => openReview(dir, "--staged"));
  const curBranch = (t.branches ?? []).find((b) => b.current);
  const baseName = (t.base ?? "").replace(/^origin\//, "");
  if (t.branch && t.branch !== baseName) {
    const ab = curBranch && t.base ? `${curBranch.ahead} ahead, ${curBranch.behind} behind ${esc(t.base)}` : `against ${esc(t.base || "the main branch")}`;
    card(`This branch · ${t.branch}`, ab, curBranch?.review, curBranch && t.base && !curBranch.ahead, () => openReview(dir, t.branch));
  }
  body.append(cards);

  if (t.reviews?.length) {
    const sec = el(`<section class="wt-sec"><h3>Reviews of this repository</h3></section>`);
    for (const r of t.reviews) {
      const row = el(`<div class="pick-row pick" title="${esc(r.path)}"><div class="l1"><span class="name">${esc(r.title)}</span>
        ${nav.live.has(r.path) ? '<span class="badge cur">agent running</span>' : ""}<span class="when">${esc(r.updated)}</span></div>
        <div class="sub">${esc(r.kicker)} · ${esc(progress(r))} · ${plural(r.notes, "note", "notes")}</div></div>`);
      row.onclick = () => openReviewFile(r.path);
      sec.append(row);
    }
    body.append(sec);
  }

  const pick = el(`<section class="wt-sec"><div class="tabs-row"><div class="tabs"></div><span class="spacer"></span>
    <label class="small muted remote${nav.tab === "branches" ? "" : " hidden"}"><input type="checkbox"${nav.remote ? " checked" : ""}> remote branches</label></div>
    <input class="filter pick-filter" placeholder="Filter (/)"><div class="pick-list"></div></section>`);
  const tabs = [["branches", `Branches`, "1"], ["commits", "Commits", "2"]];
  if (t.gh) tabs.push(["prs", "Pull requests", "3"]);
  for (const [id, label, key] of tabs) {
    const b = el(`<button class="${nav.tab === id ? "on" : ""}" title="${key}">${label}</button>`);
    b.onclick = () => setTab(id);
    pick.querySelector(".tabs").append(b);
  }
  pick.querySelector(".remote input").onchange = (e) => {
    nav.remote = e.target.checked;
    renderHome();
  };
  const filter = pick.querySelector(".pick-filter");
  filter.value = keepFilter;
  filter.oninput = () => renderList(pick.querySelector(".pick-list"), filter.value.trim().toLowerCase());
  filter.onkeydown = (e) => {
    if (e.key === "Escape" || e.key === "Enter" || e.key === "ArrowDown") {
      e.preventDefault();
      filter.blur();
      if (e.key !== "Escape") moveHome(1, true);
    }
  };
  body.append(pick);
  renderList(pick.querySelector(".pick-list"), keepFilter.toLowerCase());
  if (hadFocus) filter.focus();
  markCursor();
}

function renderList(list, q) {
  const t = state.targets;
  const dir = t.dir;
  list.innerHTML = "";
  const row = (html, fn, cls = "") => {
    const r = el(`<div class="pick-row pick ${cls}">${html}</div>`);
    if (fn) r.onclick = fn;
    list.append(r);
    return r;
  };
  const baseName = (t.base ?? "").replace(/^origin\//, "");
  if (nav.tab === "branches") {
    const bs = (t.branches ?? []).filter((b) => (nav.remote || !b.remote) && (!q || fuzzy(q, b.name + " " + b.subject) >= 0));
    if (!bs.length) list.append(el(`<div class="spinner">No branches${q ? " match" : ""}.</div>`));
    for (const b of bs) {
      const isBase = b.name === t.base || b.name === baseName;
      const where = b.worktree && b.worktree !== dir ? `<span class="badge" title="${esc(b.worktree)}">in ${esc(tail(b.worktree, 1))}</span>` : b.current ? '<span class="badge cur">checked out</span>' : "";
      const ab = isBase ? '<span class="badge">base</span>' : t.base ? `<span class="ab" title="commits ahead / behind ${esc(t.base)}">↑${b.ahead} ↓${b.behind}</span>` : "";
      row(`<div class="l1"><span class="name">${esc(b.name)}</span>${where}${ab}${reviewPill(b.review)}<span class="when">${esc(b.when)}</span></div><div class="sub">${esc(b.subject)}</div>`,
        isBase ? null : () => openReview(b.worktree || dir, b.name), isBase ? "dim" : "");
    }
    return;
  }
  if (nav.tab === "commits") {
    for (const c of t.commits ?? []) {
      if (q && fuzzy(q, c.short + " " + c.subject + " " + c.author) < 0 && !c.sha.startsWith(q)) continue;
      row(`<div class="l1"><span class="sha">${esc(c.short)}</span><span class="name">${esc(c.subject)}</span><span class="when">${esc(c.when)}</span></div><div class="sub">${esc(c.author)}</div>`, () => openReview(dir, c.sha));
    }
    return;
  }
  if (!nav.prs) return list.append(el(`<div class="spinner">Asking gh for open pull requests…</div>`));
  if (nav.prErr) return list.append(el(`<div class="spinner error">${esc(nav.prErr)}</div>`));
  if (!nav.prs.length) return list.append(el(`<div class="spinner">No open pull requests.</div>`));
  for (const p of nav.prs) {
    if (q && fuzzy(q, `#${p.number} ${p.title} ${p.author} ${p.branch}`) < 0) continue;
    const r = row(`<div class="l1"><span class="sha">#${p.number}</span><span class="name">${esc(p.title)}</span>${p.draft ? '<span class="badge">draft</span>' : ""}<span class="when">${esc(p.updated)}</span></div>
      <div class="sub">${esc(p.author)} · ${esc(p.branch)} → ${esc(p.base)}${p.fetched ? "" : " · head not fetched yet"}</div>`,
      p.fetched ? () => openReview(dir, "--pr " + p.number) : null);
    if (!p.fetched) {
      const f = el(`<button class="small" title="git fetch origin pull/${p.number}/head">Fetch and review</button>`);
      f.onclick = async (e) => {
        e.stopPropagation();
        f.disabled = true;
        f.textContent = "Fetching…";
        try {
          await api("Workspace.FetchPR", dir, p.number);
          await openReview(dir, "--pr " + p.number);
          nav.prs = null;
        } catch (err) {
          toast(errText(err), true);
          f.disabled = false;
          f.textContent = "Fetch and review";
        }
      };
      r.querySelector(".l1").append(f);
      r.onclick = () => f.click();
    }
  }
}

function setTab(id) {
  nav.tab = id;
  nav.cursor = -1;
  renderHome();
  if (id === "prs") loadPRs();
}

function homeItems() {
  return $$("#wt-body .pick").filter((x) => !x.classList.contains("disabled"));
}

function markCursor() {
  const items = homeItems();
  items.forEach((x, i) => x.classList.toggle("kcur", i === nav.cursor));
}

function moveHome(dir, first = false) {
  const items = homeItems();
  if (!items.length) return;
  if (first) {
    const list = $("#wt-body .pick-list .pick");
    nav.cursor = Math.max(0, items.indexOf(list));
  } else nav.cursor = Math.max(0, Math.min(items.length - 1, nav.cursor + dir));
  markCursor();
  items[nav.cursor]?.scrollIntoView({ block: "nearest" });
}

// homeKey handles the keys of the worktree page.
export function homeKey(e) {
  if ($("#home-pane").classList.contains("hidden")) return false;
  const k = e.key;
  if (e.ctrlKey || e.metaKey || e.altKey) return false;
  const fn = {
    j: () => moveHome(1),
    ArrowDown: () => moveHome(1),
    k: () => moveHome(-1),
    ArrowUp: () => moveHome(-1),
    g: () => ((nav.cursor = 0), markCursor()),
    Enter: () => homeItems()[nav.cursor]?.click(),
    "/": () => $("#wt-body .pick-filter")?.focus(),
    r: () => loadTargets(),
    1: () => setTab("branches"),
    2: () => setTab("commits"),
    3: () => state.targets?.gh && setTab("prs"),
    Escape: () => state.opened && backToReview(),
  }[k];
  if (!fn) return false;
  fn();
  return true;
}

export function backToReview() {
  if (!state.opened) return;
  hooks.showReviewer();
}

export function syncSideButtons() {
  const hidden = $("#workspace").classList.contains("focus");
  $("#show-side").classList.toggle("hidden", !hidden);
}

// The switcher: one list of everything to go to, ranked by the query and by what was used lately.

const mruKey = "margin.mru";
let switchItems = [];
let switchAt = 0;

function mru() {
  try {
    return JSON.parse(localStorage.getItem(mruKey) ?? "[]");
  } catch {
    return [];
  }
}

function used(id) {
  try {
    localStorage.setItem(mruKey, JSON.stringify([id, ...mru().filter((x) => x !== id)].slice(0, 40)));
  } catch {}
}

function candidates() {
  const items = [];
  const add = (id, kind, label, detail, run) => items.push({ id, kind, label, detail, run });
  for (const r of nav.recent) add("review:" + r.path, "review", r.title, `${r.kicker} · ${progress(r)}`, () => openFile(r.path));
  for (const r of state.targets?.reviews ?? []) {
    if (!nav.recent.some((x) => x.path === r.path)) add("review:" + r.path, "review", r.title, r.kicker, () => openReviewFile(r.path));
  }
  for (const w of worktrees()) add("wt:" + w.path, "worktree", `${w.repo.name} › ${wtLabel(w)}`, home(w.path), () => selectWorktree(w, w.repo));
  const t = state.targets;
  if (t) {
    const wt = state.worktree;
    if (t.dirty) add("changes:" + t.dir, "review", `Uncommitted changes in ${wt?.repo?.name ?? tail(t.dir, 1)}`, `${t.dirty} files · ${home(t.dir)}`, () => openReview(t.dir, ""));
    for (const b of t.branches ?? []) {
      if (b.remote && !nav.remote) continue;
      add(`branch:${t.dir}:${b.name}`, "branch", b.name, `branch in ${wt?.repo?.name ?? ""} · ${b.subject}`, () => openReview(b.worktree || t.dir, b.name));
    }
    for (const p of nav.prs ?? []) if (p.fetched) add(`pr:${t.dir}:${p.number}`, "pull request", `#${p.number} ${p.title}`, `${p.author} · ${p.branch}`, () => openReview(t.dir, "--pr " + p.number));
  }
  add("cmd:settings", "command", "Settings", "where reviews are kept, which agent runs, the agent skill", openSettings);
  add("cmd:add", "command", "Add a repository…", "a repository or a folder of repositories", addFolder);
  add("cmd:file", "command", "Open a review file…", "any .review.yaml", hooks.pickReviewFile);
  if (state.opened) add("cmd:answers", "command", "Answers to your comments", "the comments note of the open review", () => openAnswers());
  if (state.opened && !$("#home-pane").classList.contains("hidden")) add("cmd:back", "command", "Back to the review", state.view?.title ?? "", backToReview);
  return items;
}

export function openSwitcher() {
  closeModal();
  $("#switcher").classList.remove("hidden");
  const q = $("#switch-q");
  q.value = "";
  filterSwitch();
  q.focus();
}

function closeSwitcher() {
  $("#switcher").classList.add("hidden");
}

function filterSwitch() {
  const q = $("#switch-q").value.trim();
  const recent = mru();
  const all = candidates();
  switchItems = all
    .map((it, i) => {
      const s = q ? Math.max(fuzzy(q, it.label), fuzzy(q, it.label + " " + it.detail) - 50) : 0;
      const r = recent.indexOf(it.id);
      return { it, s: s < 0 ? -1 : s + (r >= 0 ? 40 - r : 0) - i / 1000 };
    })
    .filter((x) => x.s >= 0 || !q)
    .sort((a, b) => b.s - a.s)
    .slice(0, 60)
    .map((x) => x.it);
  switchAt = 0;
  renderSwitch();
}

function renderSwitch() {
  const list = $("#switch-list");
  list.innerHTML = "";
  if (!switchItems.length) list.append(el(`<div class="spinner">Nothing matches.</div>`));
  switchItems.forEach((it, i) => {
    const row = el(`<div class="sw-row${i === switchAt ? " on" : ""}"><span class="sw-kind">${esc(it.kind)}</span><span class="sw-label">${esc(it.label)}</span><span class="sw-detail">${esc(it.detail)}</span></div>`);
    row.onmousemove = () => {
      if (switchAt === i) return;
      switchAt = i;
      $$("#switch-list .sw-row").forEach((x, j) => x.classList.toggle("on", j === i));
    };
    row.onclick = () => runSwitch(i);
    list.append(row);
  });
  list.querySelector(".sw-row.on")?.scrollIntoView({ block: "nearest" });
}

function runSwitch(i) {
  const it = switchItems[i];
  if (!it) return;
  closeSwitcher();
  used(it.id);
  it.run();
}

$("#switch-q").oninput = filterSwitch;
$("#switch-q").onkeydown = (e) => {
  const k = e.key;
  if (k === "Escape") return e.preventDefault(), closeSwitcher();
  if (k === "Enter") return e.preventDefault(), runSwitch(switchAt);
  const d = k === "ArrowDown" || (e.ctrlKey && (k === "n" || k === "j")) ? 1 : k === "ArrowUp" || (e.ctrlKey && (k === "p" || k === "k")) ? -1 : 0;
  if (d) {
    e.preventDefault();
    switchAt = Math.max(0, Math.min(switchItems.length - 1, switchAt + d));
    renderSwitch();
  }
};
$("#switcher").onclick = (e) => {
  if (e.target.id === "switcher") closeSwitcher();
};

export function switcherOpen() {
  return !$("#switcher").classList.contains("hidden");
}

// Settings

export async function openSettings() {
  let v;
  try {
    v = await api("Settings.Get");
  } catch (e) {
    return toast(errText(e), true);
  }
  const f = v.file ?? {};
  const where = !f.reviews || f.reviews === "repo" ? "repo" : f.reviews === "state" ? "state" : "dir";
  const agentKnown = v.agents.some((a) => a.command === (f.agent || "claude"));
  const body = el(`<div class="settings">
    ${v.err ? `<p class="error">${esc(v.err)}</p>` : ""}
    <section><h4>Where reviews are kept</h4>
      <label class="opt"><input type="radio" name="where" value="repo"${where === "repo" ? " checked" : ""}><span><b>In the repository</b> · <code>.margin/reviews/</code>, which git ignores. Every worktree of the repository shares it.</span></label>
      <label class="opt"><input type="radio" name="where" value="state"${where === "state" ? " checked" : ""}><span><b>A private folder</b> · <code>~/.local/state/margin</code>, outside every repository.</span></label>
      <label class="opt"><input type="radio" name="where" value="dir"${where === "dir" ? " checked" : ""}><span><b>A folder of your choice</b>, for example a notes vault.</span></label>
      <div class="dir-row${where === "dir" ? "" : " hidden"}">
        <input class="filter" id="set-dir" placeholder="~/notes/{project}/topics/{topic}" value="${esc(where === "dir" ? f.reviews : "")}">
        <p class="muted small"><code>{project}</code> is the repository's name (or its <code>ai.memoryProject</code> git setting), <code>{topic}</code> the branch, <code>{repo}</code> a unique folder per repository.
        With <code>{project}</code> or <code>{topic}</code> it is treated as a notes folder: files are named <code>margin-…</code> and start with frontmatter.</p>
      </div>
      ${v.envDir ? `<p class="warn-line">$MARGIN_DIR is set to ${esc(v.envDir)} and wins over this.</p>` : ""}
    </section>
    <section><h4>Agent</h4>
      <div class="agents"></div>
      <input class="filter" id="set-agent" placeholder="command, e.g. claude --model opus" value="${esc(agentKnown ? "" : f.agent ?? "")}">
      <p class="muted small">The agent starts in the terminal under the review. Its instructions are passed as the last argument, or in place of <code>{prompt}</code>. Any agent that can run shell commands works.</p>
      ${v.envAgent ? `<p class="warn-line">$MARGIN_AGENT is set to ${esc(v.envAgent)} and wins over this.</p>` : ""}
    </section>
    <section><h4>Note for the agent</h4>
      <textarea id="set-note" class="note-input" placeholder="Optional. Added to the agent's instructions, e.g. File the review in the index of {project}.">${esc(f.agent_note ?? "")}</textarea>
      <p class="muted small"><code>{project}</code>, <code>{topic}</code> and <code>{dir}</code> are filled in.</p>
    </section>
    <section><h4>Running tests</h4>
      <p class="muted small">The commands that run a repository's tests when you click ▶ in a review; the first run in a repository asks for one. <code>{dir}</code> <code>{pkg}</code> <code>{run}</code> <code>{tests}</code> <code>{file}</code> are filled in.</p>
      <div class="test-cmds"></div>
    </section>
    <section><h4>Agent skill</h4>
      <p class="muted small">Teaches an agent margin's commands, also in sessions margin did not start, so you can ask it for "a margin review of this branch".</p>
      <div class="skills"></div>
    </section>
    <div class="set-foot"><span class="muted small" title="${esc(v.path)}">${esc(home(v.path))} · shared with margin-tui</span><span class="spacer"></span>
      <button class="small" data-act="cancel">Cancel</button><button class="primary small" data-act="save">Save</button></div>
  </div>`);
  const agents = body.querySelector(".agents");
  for (const a of v.agents) {
    const on = agentKnown && a.command === (f.agent || "claude");
    const c = el(`<label class="opt"><input type="radio" name="agent" value="${esc(a.command)}"${on ? " checked" : ""}><span><code>${esc(a.command)}</code> ${a.found ? '<span class="ok-dot">installed</span>' : '<span class="muted small">not found</span>'}</span></label>`);
    agents.append(c);
  }
  agents.append(el(`<label class="opt"><input type="radio" name="agent" value=""${agentKnown ? "" : " checked"}><span>Another command:</span></label>`));
  let tests = (f.tests ?? []).map((t) => ({ ...t }));
  const cmds = body.querySelector(".test-cmds");
  const drawTests = () => {
    cmds.innerHTML = tests.length ? "" : `<p class="muted small">None yet.</p>`;
    tests.forEach((t, i) => {
      const r = el(`<div class="skill-row"><span class="small" title="${esc(t.repo || "every repository")}">${esc(t.repo ? home(t.repo) : "every repository")}</span>
        <input class="filter mono" spellcheck="false" value="${esc(t.command)}"><button class="small">Remove</button></div>`);
      r.querySelector("input").oninput = (e) => (t.command = e.target.value);
      r.querySelector("button").onclick = () => {
        tests.splice(i, 1);
        drawTests();
      };
      cmds.append(r);
    });
  };
  drawTests();
  const skills = body.querySelector(".skills");
  for (const s of v.skills ?? []) {
    const r = el(`<div class="skill-row"><code>${esc(s.agent)}</code><span class="muted small" title="${esc(s.path)}">${esc(home(s.path))}</span>
      <span class="spacer"></span><span class="small">${s.current ? "installed" : s.installed ? "older version" : "not installed"}</span>
      <button class="small"${s.current ? " disabled" : ""}>${s.installed ? "Update" : "Install"}</button></div>`);
    r.querySelector("button").onclick = async () => {
      try {
        await api("Settings.InstallSkill", s.agent);
        toast(`Installed the margin skill for ${s.agent}.`);
        openSettings();
      } catch (e) {
        toast(errText(e), true);
      }
    };
    skills.append(r);
  }
  body.querySelectorAll('input[name="where"]').forEach((r) => (r.onchange = () => body.querySelector(".dir-row").classList.toggle("hidden", r.value !== "dir" || !r.checked)));
  body.querySelector('[data-act="cancel"]').onclick = closeModal;
  body.querySelector('[data-act="save"]').onclick = async () => {
    const w = body.querySelector('input[name="where"]:checked').value;
    const a = body.querySelector('input[name="agent"]:checked')?.value;
    const cfg = {
      reviews: w === "dir" ? body.querySelector("#set-dir").value.trim() : w === "repo" ? "" : "state",
      agent: a ? (a === "claude" ? "" : a) : body.querySelector("#set-agent").value.trim(),
      agent_note: body.querySelector("#set-note").value.trim(),
      tests,
    };
    try {
      await api("Settings.Save", cfg);
      closeModal();
      toast("Settings saved. New reviews and agents use them; open reviews stay where they are.");
      if (state.worktree) loadTargets();
    } catch (e) {
      toast(errText(e), true);
    }
  };
  const m = openModal("Settings", body, "narrow");
  m.onkey = (e) => (e.key === "Escape" ? (closeModal(), true) : true);
}

// Welcome, when the workspace is empty

async function renderWelcome() {
  const body = $("#welcome-body");
  body.innerHTML = "";
  if (!nav.folders.length) {
    const add = el(`<div><button class="primary">Add a repository…</button>
      <p class="hint">A repository, or a folder holding several. Their worktrees are found on their own.</p></div>`);
    add.querySelector("button").onclick = addFolder;
    body.append(add);
  } else {
    body.append(el(`<p class="hint">Pick a repository or worktree on the left, or press <kbd>ctrl k</kbd> to go anywhere.</p>`));
    const list = el(`<div class="start-list"></div>`);
    for (const r of nav.recent.slice(0, 4)) {
      const b = el(`<div class="pick-row"><div class="l1"><span class="name">${esc(r.title)}</span><span class="when">${esc(r.updated)}</span></div><div class="sub">continue · ${esc(progress(r))}</div></div>`);
      b.onclick = () => openFile(r.path);
      list.append(b);
    }
    for (const w of worktrees().filter((w) => nav.status[w.path]).slice(0, 6)) {
      const b = el(`<div class="pick-row"><div class="l1"><span class="name">${esc(w.repo.name)} › ${esc(wtLabel(w))}</span><span class="dirty">●${nav.status[w.path]}</span></div><div class="sub">uncommitted changes</div></div>`);
      b.onclick = () => selectWorktree(w, w.repo);
      list.append(b);
    }
    if (list.children.length) body.append(list);
  }
  const box = $("#welcome-checks");
  box.innerHTML = "";
  try {
    const v = await api("Settings.Get");
    const agent = v.effect?.agent ?? "claude";
    const found = v.agents.find((a) => a.command === agent)?.found;
    box.append(el(`<p class="small">${found === false ? "⚠" : "✓"} agent: <code>${esc(agent)}</code>${found === false ? " is not installed; pick another in " : " · change it in "}<button class="link-btn" data-s>Settings</button></p>`));
    box.querySelector("[data-s]").onclick = openSettings;
  } catch {}
}

export function navState() {
  return nav;
}
