import { Call } from "/wails/runtime.js";

export const api = (name, ...args) => Call.ByName("main." + name, ...args);
export const $ = (sel) => document.querySelector(sel);
export const $$ = (sel) => [...document.querySelectorAll(sel)];

export const state = {
  root: "",
  repos: [],
  worktree: null,
  targets: null,
  tab: "changes",
  opened: null,
  view: null,
  station: 0,
  note: -1,
  agentLive: false,
  sel: null,
  compose: null,
  // editing is the draft comment being reworded in its card: { id, text }.
  editing: null,
  // The guided walk: the step of the current stop, and whether the reader was told to give a verdict.
  step: 0,
  nudged: false,
  more: false,
  filter: "All notes",
  split: false,
  unfold: false,
  expand: {},
  whole: {},
  parts: {},
  query: "",
  spot: -1,
  spotLines: null,
  around: {},
  known: null,
  fresh: new Set(),
};

// hooks are filled in by app.js, so the review can reach the agent terminal.
// noteRef(id, n) resolves a mention of note n of stop id: { station, note } indexes, null when the stop
// has no such note, undefined when id is not a stop (so file.go:12 stays text).
export const hooks = { fitTerminal() {}, focusTerminal() {}, showReviewer() {}, themeChanged() {}, noteRef() {} };

// The theme setting: "light", "dark", or "" to follow the system.
let themeMode = "";

export async function applyTheme(mode = themeMode) {
  let dark = mode === "dark";
  if (mode === "") {
    try {
      dark = await api("Settings.SystemDark");
    } catch {
      dark = matchMedia("(prefers-color-scheme: dark)").matches;
    }
  }
  const name = dark ? "dark" : "light";
  if (document.documentElement.dataset.theme === name) return;
  document.documentElement.dataset.theme = name;
  hooks.themeChanged(dark);
}

export function setThemeMode(mode) {
  themeMode = mode ?? "";
  return applyTheme();
}

export const followsSystem = () => themeMode === "";

export function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

export function prose(s) {
  return linkRefs(
    esc(s)
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/\n/g, "<br>"),
  );
}

// linkRefs turns mentions of notes (stop:n, as margin goto takes them) into links; html is already
// escaped. The longest tail of the word that is a stop counts: "(resolve:5" links "resolve:5".
function linkRefs(html) {
  return html.replace(/([^\s:<>]+):(\d+)(?!\w)/g, (m, head, num) => {
    for (let off = 0; off < head.length; off++) {
      const t = hooks.noteRef(head.slice(off), +num);
      if (t === undefined) continue;
      const label = `${head.slice(off)}:${num}`;
      const link = t ? `<a class="ref" href="#" data-ref="${t.station}:${t.note}" title="go to note ${label}">${label}</a>` : `<span class="ref broken" title="no such note">${label}</span>`;
      return head.slice(0, off) + link;
    }
    return m;
  });
}

export function oneLine(s) {
  return String(s ?? "").split(/\s+/).filter(Boolean).join(" ");
}

export function el(html) {
  const t = document.createElement("template");
  t.innerHTML = html.trim();
  return t.content.firstElementChild;
}

let toastTimer;
export function toast(msg, err = false) {
  const t = $("#toast");
  t.textContent = msg;
  t.classList.toggle("err", err);
  t.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), err ? 7000 : 3500);
}

export function errText(e) {
  return e?.message ?? String(e);
}

export function tail(path, n = 2) {
  return path.split("/").filter(Boolean).slice(-n).join("/");
}

export function plural(n, one, many) {
  return n === 1 ? "1 " + one : `${n} ${many}`;
}

export function shortList(names) {
  if (names.length > 4) return names.slice(0, 3).join(", ") + ` and ${names.length - 3} more`;
  if (names.length > 1) return names.slice(0, -1).join(", ") + " and " + names[names.length - 1];
  return names[0] ?? "";
}

export function base(path) {
  return path.split("/").pop();
}

export function openModal(title, body, cls = "") {
  closeModal();
  const m = el(`<div class="modal-back"><div class="modal ${cls}"><div class="modal-head"><span>${esc(title)}</span><span class="spacer"></span><button class="icon" title="Close (esc)">✕</button></div></div></div>`);
  m.querySelector(".modal").append(body);
  m.querySelector("button").onclick = closeModal;
  m.onclick = (e) => {
    if (e.target === m) closeModal();
  };
  document.body.append(m);
  return m;
}

export function closeModal() {
  const m = document.querySelector(".modal-back");
  if (!m) return false;
  m.remove();
  m.onclosed?.();
  return true;
}

// md renders the markdown agents write in notes files: headings, lists, quotes, code blocks,
// tables as text, and inline code, bold, italics and links.
export function md(src) {
  const out = [];
  const lines = String(src ?? "").replace(/\r/g, "").split("\n");
  const inline = (s) =>
    esc(s)
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/(^|[^*\w])\*([^*\n]+)\*(?!\w)/g, "$1<em>$2</em>")
      .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<span class="md-link" title="$2">$1</span>');
  let i = 0;
  while (i < lines.length) {
    const l = lines[i];
    const fence = l.match(/^\s*(```|~~~)/);
    if (fence) {
      const body = [];
      for (i++; i < lines.length && !lines[i].trim().startsWith(fence[1]); i++) body.push(lines[i]);
      i++;
      out.push(`<pre class="md-code">${esc(body.join("\n"))}</pre>`);
      continue;
    }
    const h = l.match(/^(#{1,6})\s+(.*)$/);
    if (h) {
      out.push(`<h${Math.min(h[1].length + 1, 6)} class="md-h">${inline(h[2])}</h${Math.min(h[1].length + 1, 6)}>`);
      i++;
      continue;
    }
    if (/^\s*>/.test(l)) {
      const body = [];
      for (; i < lines.length && /^\s*>/.test(lines[i]); i++) body.push(lines[i].replace(/^\s*>\s?/, ""));
      out.push(`<blockquote>${md(body.join("\n"))}</blockquote>`);
      continue;
    }
    if (/^\s*([-*+]|\d+[.)])\s+/.test(l)) {
      const ordered = /^\s*\d/.test(l);
      const items = [];
      for (; i < lines.length && /^\s*([-*+]|\d+[.)])\s+/.test(lines[i]); i++) {
        let item = lines[i].replace(/^\s*([-*+]|\d+[.)])\s+/, "");
        for (; i + 1 < lines.length && /^\s{2,}\S/.test(lines[i + 1]) && !/^\s*([-*+]|\d+[.)])\s+/.test(lines[i + 1]); i++) item += " " + lines[i + 1].trim();
        items.push(`<li>${inline(item)}</li>`);
      }
      out.push(ordered ? `<ol>${items.join("")}</ol>` : `<ul>${items.join("")}</ul>`);
      continue;
    }
    if (/^\s*(---+|\*\*\*+)\s*$/.test(l)) {
      out.push("<hr>");
      i++;
      continue;
    }
    if (!l.trim()) {
      i++;
      continue;
    }
    const para = [];
    for (; i < lines.length && lines[i].trim() && !/^(#{1,6}\s|\s*>|\s*```|\s*~~~|\s*([-*+]|\d+[.)])\s+)/.test(lines[i]); i++) para.push(lines[i]);
    out.push(`<p>${para.map(inline).join("<br>")}</p>`);
  }
  return out.join("");
}

// fuzzy scores how well query matches text as a subsequence: higher is better, -1 is no match.
export function fuzzy(query, text) {
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  if (!q) return 0;
  const at = t.indexOf(q);
  if (at >= 0) return 1000 - at - t.length / 100;
  let score = 0;
  let last = -1;
  for (const c of q) {
    const j = t.indexOf(c, last + 1);
    if (j < 0) return -1;
    score += j === last + 1 ? 8 : /[\s/._-]/.test(t[j - 1] ?? " ") ? 6 : 1;
    last = j;
  }
  return score - t.length / 100;
}
