// The comments note includes a reader's quoted follow-ups. They are not agent replies.
export function agentReply(section = "") {
  const lines = section.trim().split("\n").slice(1);
  let reader = false;
  const text = [];
  for (const line of lines) {
    if (line.startsWith("**Reply from the reader:**")) {
      reader = true;
      continue;
    }
    if (reader && line.startsWith(">")) continue;
    reader = false;
    if (!text.length && (!line.trim() || line.startsWith(">"))) continue;
    text.push(line);
  }
  return text.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

export function discussionThreads(view, questions, sections) {
  const notes = new Map();
  for (const station of view.stations) for (const note of station.notes) {
    if (note.qid && note.text) notes.set(note.qid, note.text);
  }
  const threads = new Map();
  for (const [kind, items] of [["comment", view.reader.comments], ["question", view.reader.questions], ["diff", questions]]) {
    for (const q of items) {
      if (kind === "diff") {
        const replies = questionTurns(q).filter((t) => t.answer).map((t) => ({ ...t, reply: { kind: "inline", text: t.answer } }));
        threads.set(q.id, { ...q, kind, replies, reply: replies.at(-1)?.reply });
        continue;
      }
      const section = agentReply(sections[q.id]);
      const note = notes.get(q.id);
      const reply = section ? { kind: "section", text: section } : note ? { kind: "note", text: note } : null;
      threads.set(q.id, { ...q, kind, reply });
    }
  }
  return threads;
}

export const questionTurns = (q) => [q, ...(q.followUps ?? [])];

export function questionReply(path, thread, id) {
  const replies = thread?.replies ?? [];
  if (id) return replies.find((turn) => turn.id === id);
  for (let i = replies.length - 1; i >= 0; i--) {
    if (unreadReply(path, replies[i])) return replies[i];
  }
  return replies.at(-1);
}

export function questionSelection(part, from, end) {
  if (!part?.rows) return null;
  const rows = part.rows.filter((r) => r.n > 0 && r.n - 1 >= from && r.n - 1 <= end);
  if (rows.length !== end - from + 1) return null;
  return JSON.stringify([part.file, !!part.base, rows.map((r) => (r.s ?? []).map((span) => span.t).join(""))]);
}

export function nextThread(threads, station, replies, activeID, from, stepAt, include = () => true) {
  const order = (x) => [stepAt(x.part, x.at), x.part < 0 ? Infinity : x.part, x.at];
  const compare = (a, b) => {
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1;
    return 0;
  };
  const items = [...threads.values()].filter((q) => q.station === station && (!replies || q.reply) && include(q));
  items.sort((a, b) => compare(order(a), order(b)) || (a.asked ?? "").localeCompare(b.asked ?? "") || a.id.localeCompare(b.id, undefined, { numeric: true }));
  const i = items.findIndex((q) => q.id === activeID);
  if (i >= 0) return items.length > 1 ? items[(i + 1) % items.length] : null;
  return items.find((q) => compare(order(q), from) >= 0) ?? items[0];
}

const seen = new Map();
const localResolutions = new Map();

function resolutionKey(path, q) {
  return "margin.gui.resolved." + JSON.stringify([path, q.id, q.asked ?? ""]);
}

export function localResolution(path, q) {
  const key = resolutionKey(path, q);
  if (!localResolutions.has(key)) {
    let stored;
    try { stored = localStorage.getItem(key); } catch {}
    localResolutions.set(key, stored === "true" ? true : stored === "false" ? false : undefined);
  }
  return localResolutions.get(key);
}

export function setLocalResolution(path, q, value) {
  const key = resolutionKey(path, q);
  localResolutions.set(key, value);
  try {
    if (value === undefined) localStorage.removeItem(key);
    else localStorage.setItem(key, String(value));
  } catch {}
}

function seenKey(path, q) {
  return "margin.gui.reply." + JSON.stringify([path, q.id, q.asked ?? ""]);
}

function replyVersion(text) {
  // Store a fingerprint so the browser's preferences do not duplicate reply text.
  let hash = 2166136261;
  for (let i = 0; i < text.length; i++) hash = Math.imul(hash ^ text.charCodeAt(i), 16777619);
  return `${text.length}:${hash >>> 0}`;
}

export function unreadReply(path, q) {
  if (q?.replies) return q.replies.some((turn) => unreadReply(path, turn));
  if (!q?.reply) return false;
  const key = seenKey(path, q);
  if (!seen.has(key)) {
    let before;
    try { before = localStorage.getItem(key); } catch {}
    seen.set(key, before);
  }
  return seen.get(key) !== replyVersion(q.reply.text);
}

export function readReply(path, q) {
  if (!q?.reply) return;
  const key = seenKey(path, q);
  const version = replyVersion(q.reply.text);
  seen.set(key, version);
  try { localStorage.setItem(key, version); } catch {}
}
