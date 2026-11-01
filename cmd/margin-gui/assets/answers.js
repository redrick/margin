import { Events } from "/wails/runtime.js";
import { api, state, esc, el, toast, errText, md, openModal, closeModal } from "./core.js";

// The comments note: the agent's answers to the reader's comments, kept as markdown beside the
// review. The reader reads it here, edits it, or replies under one answer.

let panel = null;

export async function openAnswers(focus = "") {
  let v;
  try {
    v = await api("Reviews.Answers");
  } catch (e) {
    return toast(errText(e), true);
  }
  panel = { v, focus, editing: false, replying: "" };
  const body = el(`<div class="answers"></div>`);
  const m = openModal("Answers to your comments", body, "wide");
  m.onkey = answersKey;
  render();
}

function render() {
  const box = document.querySelector(".modal .answers");
  if (!box || !panel) return;
  const { v } = panel;
  box.innerHTML = "";
  const head = el(`<div class="ans-head"><span class="muted small path" title="${esc(v.path)}">${esc(v.path)}</span><span class="spacer"></span></div>`);
  box.append(head);
  if (panel.editing) {
    const ta = el(`<textarea class="ans-edit" spellcheck="false"></textarea>`);
    ta.value = v.text;
    const save = el(`<button class="primary small">Save</button>`);
    const cancel = el(`<button class="small">Cancel</button>`);
    save.onclick = () => saveEdit(ta.value);
    cancel.onclick = () => {
      panel.editing = false;
      render();
    };
    ta.onkeydown = (e) => {
      if ((e.ctrlKey || e.metaKey) && (e.key === "s" || e.key === "Enter")) {
        e.preventDefault();
        saveEdit(ta.value);
      }
      if (e.key === "Escape") {
        e.preventDefault();
        cancel.click();
      }
    };
    head.append(cancel, save);
    box.append(ta, el(`<p class="muted small">Ctrl+S saves · Esc cancels. The agent reads this file again when you reply or ask it to.</p>`));
    requestAnimationFrame(() => ta.focus());
    return;
  }
  const edit = el(`<button class="small" title="Edit the note (e)">Edit</button>`);
  edit.onclick = startEdit;
  head.append(edit);
  if (!v.exists) {
    box.append(el(`<p class="muted">The agent has not answered yet. Send your comments (S) and it writes its answers to this file.</p>`));
    return;
  }
  const doc = el(`<div class="md"></div>`);
  const ids = Object.keys(v.sections);
  let rest = v.text;
  // Draw the note in order, each comment's section with a reply box under it.
  const order = ids.map((id) => [id, v.text.indexOf(v.sections[id])]).sort((a, b) => a[1] - b[1]);
  let at = 0;
  for (const [id, start] of order) {
    if (start > at) doc.append(el(`<div>${md(v.text.slice(at, start))}</div>`));
    const sec = el(`<section class="ans-sec" data-id="${esc(id)}">${md(v.sections[id])}</section>`);
    const comment = state.view?.reader?.comments.find((c) => c.id === id);
    const bar = el(`<div class="ans-bar">${comment ? `<span class="badge">${comment.resolved ? "resolved" : "open"}</span>` : ""}<span class="spacer"></span></div>`);
    const reply = el(`<button class="small">Reply</button>`);
    reply.onclick = () => {
      panel.replying = id;
      render();
    };
    bar.append(reply);
    sec.append(bar);
    if (panel.replying === id) sec.append(replyBox(id));
    doc.append(sec);
    at = start + v.sections[id].length;
  }
  rest = v.text.slice(at);
  if (rest.trim()) doc.append(el(`<div>${md(rest)}</div>`));
  box.append(doc);
  const target = panel.focus && box.querySelector(`.ans-sec[data-id="${CSS.escape(panel.focus)}"]`);
  if (target) {
    target.classList.add("focus");
    requestAnimationFrame(() => target.scrollIntoView({ block: "start" }));
    panel.focus = "";
  }
}

function replyBox(id) {
  const box = el(`<div class="ans-reply"><textarea placeholder="Your reply to the answer on ${esc(id)}. It goes under it in the note, and the agent is told to answer there."></textarea>
    <div class="ans-bar"><span class="muted small">Ctrl+Enter sends · Esc cancels</span><span class="spacer"></span><button class="small">Cancel</button><button class="primary small">Send to the agent</button></div></div>`);
  const ta = box.querySelector("textarea");
  const [cancel, send] = box.querySelectorAll("button");
  cancel.onclick = () => {
    panel.replying = "";
    render();
  };
  send.onclick = async () => {
    try {
      panel.v = await api("Reviews.Reply", id, ta.value);
      panel.replying = "";
      panel.focus = id;
      toast(`Reply to ${id} sent to the agent.`);
      render();
    } catch (e) {
      toast(errText(e), true);
    }
  };
  ta.onkeydown = (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      send.click();
    }
    if (e.key === "Escape") {
      e.preventDefault();
      cancel.click();
    }
  };
  requestAnimationFrame(() => ta.focus());
  return box;
}

function startEdit() {
  panel.editing = true;
  render();
}

async function saveEdit(text) {
  try {
    panel.v = await api("Reviews.SaveAnswers", text);
    panel.editing = false;
    toast("Saved the comments note.");
    render();
  } catch (e) {
    toast(errText(e), true);
  }
}

function answersKey(e) {
  const scroller = document.querySelector(".modal .answers");
  const k = e.key;
  if (k === "Escape" || k === "q" || k === "A") return closeModal(), true;
  if (k === "e") return startEdit(), true;
  const step = scroller ? scroller.clientHeight / 2 : 0;
  const scroll = { j: 40, ArrowDown: 40, k: -40, ArrowUp: -40, " ": step, PageDown: step, PageUp: -step }[k];
  if (scroll !== undefined && scroller) scroller.scrollTop += scroll;
  return true;
}

// The agent writes its answers while the panel is open: show them as they land, unless the reader
// is editing or replying.
Events.On("review:changed", async () => {
  if (!panel || !document.querySelector(".modal .answers") || panel.editing || panel.replying) return;
  try {
    panel.v = await api("Reviews.Answers");
    render();
  } catch {}
});

export function answersOpen() {
  return !!document.querySelector(".modal .answers");
}
