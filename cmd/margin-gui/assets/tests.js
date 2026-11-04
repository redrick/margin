import { Events } from "/wails/runtime.js";
import { api, $, state, esc, el, toast, errText, plural, openModal, closeModal } from "./core.js";

// Running tests from the window, with the command the user set up for the repository.

// results holds what the last run said about each test, by test name: pass, fail, skip or run.
export const results = new Map();
export const testHooks = { changed() {}, edit() {} };

let run = null;
const maxLines = 5000;

async function ready(retry) {
  let setup;
  try {
    setup = await api("Reviews.TestSetup");
  } catch (e) {
    toast(errText(e), true);
    return false;
  }
  if (!setup.found) {
    openTestSetup(setup, retry);
    return false;
  }
  return true;
}

function begin(r, extra) {
  const jobs = (list) => list.map((j) => ({ ...j, status: "wait", code: 0 }));
  run = { id: r.run, dir: r.dir, jobs: jobs(r.jobs), out: [], done: false, open: true, stopped: false, ...extra };
  if (r.change) Object.assign(run, { change: r.change, compare: r.compare, base: r.compare ? jobs(r.jobs) : null, before: new Map(), after: new Map(), implied: new Set() });
  const mark = r.change ? (r.compare ? results : null) : results;
  if (mark) for (const j of run.jobs) for (const t of j.tests) mark.set(t, "run");
  renderPanel(true);
  testHooks.changed();
}

export async function runTests(ids, what) {
  if (!ids.length) return toast("No test runs these lines.");
  if (!(await ready(() => runTests(ids, what)))) return;
  try {
    begin(await api("Reviews.RunTestsHere", ids), { what, ids });
  } catch (e) {
    toast(errText(e), true);
  }
}

// tryChange runs the tests of a line against an edit of it; margin puts the line back after.
export async function tryChange(req, what) {
  if (!(await ready(() => tryChange(req, what)))) return false;
  try {
    begin(await api("Reviews.TryChange", req), { what, req });
    return true;
  } catch (e) {
    toast(errText(e), true);
    return false;
  }
}

export function rerun() {
  if (run?.req) tryChange(run.req, run.what);
  else if (run) runTests(run.ids, run.what);
}

export function stopTests() {
  if (!run || run.done) return;
  run.stopped = true;
  api("Reviews.StopTests").catch(() => {});
}

export function resetTests() {
  run = null;
  results.clear();
  $("#testrun")?.classList.add("hidden");
}

export function toggleTestPanel() {
  if (!run) return toast("No test run yet: select a line, then ▶ Run (T) runs its tests and ✎ Try a change (i) edits it.");
  run.open = !run.open;
  renderPanel();
}

Events.On("tests:batch", (ev) => {
  const evs = Array.isArray(ev.data) ? ev.data : [];
  let lines = false;
  let marks = false;
  for (const e of evs) {
    if (!run || e.run !== run.id) continue;
    const base = e.phase === "base";
    const j = (base ? run.base : run.jobs)?.[e.job];
    // Without a change, and in the unchanged pass, results are the code's own; with the change
    // they are the trial's.
    const into = run.change && !base ? run.after : results;
    const tag = !run.compare ? "" : base ? "unchanged" : "with change";
    switch (e.kind) {
      case "out":
        run.out.push([e.job, e.text]);
        lines = true;
        break;
      case "result":
        into.set(e.test, e.status);
        if (base) run.before.set(e.test, e.status);
        marks = true;
        break;
      case "start":
        if (!j) break;
        j.status = "running";
        run.out.push([e.job, `$ ${j.command}${tag ? `   # ${tag}` : ""}`, "cmd"]);
        lines = true;
        break;
      case "end":
        if (!j) break;
        j.code = e.code;
        j.err = e.err;
        j.status = e.err ? (run.stopped ? "stopped" : "error") : e.code === 0 ? "pass" : "fail";
        for (const t of j.tests) {
          if (into.get(t) === "run" || !into.has(t)) {
            into.set(t, j.status === "pass" ? "pass" : j.status === "fail" ? "fail" : "");
            if (run.change && !base) run.implied.add(t);
          }
          if (base && !run.before.has(t)) run.before.set(t, into.get(t));
        }
        if (e.err && !run.stopped) run.out.push([e.job, e.err, "err"]);
        marks = true;
        break;
      case "applied":
        run.applied = !e.err;
        run.out.push([0, e.err ? `✎ could not make the change: ${e.err}` : `✎ changed ${run.change}`, e.err ? "err" : "try"]);
        lines = true;
        break;
      case "restored":
        run.restored = e.err || "ok";
        run.out.push([0, e.err ? `✎ ${e.err}` : "✎ put the line back", e.err ? "err" : "try"]);
        if (e.err) toast(e.err, true);
        lines = true;
        break;
    }
  }
  if (run && run.out.length > maxLines) run.out.splice(0, run.out.length - maxLines);
  if (lines || marks) renderPanel();
  if (marks) testHooks.changed();
});

Events.On("tests:done", (ev) => {
  const id = ev.data;
  if (!run || id !== run.id) return;
  run.done = true;
  renderPanel();
  testHooks.changed();
  if (run.open) return;
  if (run.change) return toast(`With your change: ${verdict().text}.`);
  const failed = run.jobs.filter((j) => j.status === "fail" || j.status === "error").length;
  toast(failed ? `Tests failed: ${plural(failed, "command", "commands")} of ${run.jobs.length}.` : run.stopped ? "Tests stopped." : "Tests passed.", !!failed);
});

// verdict compares the runs with and without the change, like tryout.Judge.
function verdict() {
  const caught = [];
  const missed = [];
  const already = [];
  // A command that failed without naming a failed test, as one that does not build, caught nothing.
  const after = [...run.after].filter(([t, s]) => !(s === "fail" && run.implied.has(t)));
  for (const [t, s] of after) {
    const b = run.before.get(t) ?? results.get(t);
    if (s === "fail" && b === "fail") already.push(t);
    else if (s === "fail") caught.push(t);
    else if (s === "pass") missed.push(t);
  }
  const broken = run.jobs.filter((j) => (j.status === "fail" || j.status === "error") && !j.tests.some((t) => caught.includes(t) || already.includes(t) || after.some(([n, s]) => s === "fail" && n.startsWith(t + "/")))).map((j) => j.label);
  let text;
  let cls;
  if (broken.length && !caught.length) [text, cls] = ["the tests did not get to run: the change breaks the build, or the command failed", "m-chg"];
  else if (caught.length) [text, cls] = [`${plural(caught.length, "test caught", "tests caught")} it${missed.length ? `, ${missed.length} still pass` : ""}`, "m-add"];
  else if (missed.length) [text, cls] = ["no test noticed the change", "m-del"];
  else [text, cls] = [already.length ? "the tests failed without the change too" : "no test result", "muted"];
  return { caught, missed, already, broken, text, cls };
}

function summary() {
  if (run.change && run.done && !run.stopped && run.applied) {
    const v = verdict();
    return [v.text, v.cls];
  }
  const n = run.jobs.length;
  const fail = run.jobs.filter((j) => j.status === "fail" || j.status === "error").length;
  if (!run.done) {
    const at = run.jobs.findIndex((j) => j.status === "running");
    const bat = run.base?.findIndex((j) => j.status === "running") ?? -1;
    if (bat >= 0 || (run.base && !run.applied)) return [`unchanged ${bat < 0 ? 1 : bat + 1}/${n}`, "m-chg"];
    return [`${run.change ? "with change " : "running "}${at < 0 ? 1 : at + 1}/${n}`, "m-chg"];
  }
  if (run.stopped) return ["stopped", "muted"];
  if (fail) return [`${fail}/${n} failed`, "m-del"];
  return [n > 1 ? `all ${n} passed` : "passed", "m-add"];
}

const icons = { wait: "·", running: "◌", pass: "✓", fail: "✗", error: "!", stopped: "■" };

function renderPanel(scrollEnd) {
  const box = $("#testrun");
  if (!box) return;
  if (!run) return box.classList.add("hidden");
  box.classList.remove("hidden");
  box.classList.toggle("collapsed", !run.open);
  const pre = box.querySelector("pre");
  const atEnd = scrollEnd || !pre || pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 8;
  const keep = pre?.scrollTop;
  const [sum, cls] = summary();
  const tests = [...new Set(run.jobs.flatMap((j) => j.tests))];
  const mine = (t) => tests.includes(t) || tests.some((p) => t.startsWith(p + "/"));
  const chip = (t, s, was) => `<span class="tchip t-${s}" title="${esc(t)}${was !== undefined ? `: ${was || "no result"} unchanged, ${s || "no result"} with your change` : ""}">${was !== undefined ? `${icons[was] ?? "·"}→` : ""}${icons[s] ?? "·"} ${esc(t)}</span>`;
  const chips = run.change
    ? [...run.after.entries()].filter(([t, s]) => s && mine(t)).map(([t, s]) => chip(t, s, run.before.get(t) ?? results.get(t) ?? "")).join("")
    : [...results.entries()].filter(([t, s]) => s && mine(t)).map(([t, s]) => chip(t, s)).join("");
  const jobRows = (list, tag) =>
    list.map((j, i) => `<div class="tr-job j-${j.status}" data-i="${i}" title="${esc(j.command)}"><span class="ji">${icons[j.status]}</span>${tag ? `<span class="muted small">${tag}</span> ` : ""}${esc(j.label)}${j.status === "fail" ? ` <span class="muted small">exit ${j.code}</span>` : ""}</div>`).join("");
  const after = run.change && run.done && run.applied && !run.stopped;
  box.innerHTML = `<div class="tr-head"><button class="icon tr-fold" title="${run.open ? "Collapse" : "Expand"} (o)">${run.open ? "▾" : "▸"}</button>
      <b>${run.change ? "Try a change" : "Tests"}</b><span class="muted small">${esc(run.change ? `✎ ${run.change}` : (run.what ?? ""))}</span><span class="${cls} small">${esc(sum)}</span><span class="spacer"></span>
      <span class="muted small" title="${esc(run.dir)}">in ${esc(run.dir.split("/").slice(-2).join("/"))}</span>
      ${run.done ? "" : `<button class="small" data-a="stop">Stop</button>`}
      ${after ? `<button class="small" data-a="tell" title="Send the agent what the tests did with your change">Tell the agent</button><button class="small" data-a="keep" title="Write the change to the file for good">Keep the change</button>` : ""}
      ${run.req ? `<button class="small" data-a="edit"${run.done ? "" : " disabled"} title="Change the line again (i)">Edit…</button>` : ""}
      <button class="small" data-a="rerun"${run.done ? "" : " disabled"}>Run again</button>
      <button class="small" data-a="setup" title="The command that runs tests in this repository">Command…</button>
      <button class="icon" data-a="close" title="Close">✕</button></div>
    ${run.open ? `${run.change ? `<div class="tr-note muted small">${run.restored === "ok" ? "The file is back as it was." : run.restored ? esc(run.restored) : run.applied ? "Your change is in the file while the tests run; margin puts the line back when they end." : run.compare ? "Running the tests on the code as it is first, to compare." : ""}</div>` : ""}
    <div class="tr-jobs">${run.base ? jobRows(run.base, "unchanged") : ""}${jobRows(run.jobs, run.base ? "with change" : "")}</div>
    ${chips ? `<div class="tr-chips">${chips}</div>` : ""}
    <pre class="tr-out">${run.out.map(([, t, k]) => `<span class="${k ? "o-" + k : lineClass(t)}">${esc(t)}</span>`).join("\n")}</pre>` : ""}`;
  box.querySelector(".tr-fold").onclick = toggleTestPanel;
  box.querySelector('[data-a="stop"]')?.addEventListener("click", stopTests);
  box.querySelector('[data-a="rerun"]').onclick = rerun;
  box.querySelector('[data-a="edit"]')?.addEventListener("click", () => testHooks.edit(run.req));
  box.querySelector('[data-a="keep"]')?.addEventListener("click", async () => {
    try {
      await api("Reviews.KeepChange");
      toast("Change written to the file.");
    } catch (e) {
      toast(errText(e), true);
    }
  });
  box.querySelector('[data-a="tell"]')?.addEventListener("click", async () => {
    const v = verdict();
    try {
      const named = Object.fromEntries([...run.after].filter(([t, s]) => !(s === "fail" && run.implied.has(t))));
      await api("Reviews.TellAgentTryout", Object.fromEntries(run.before.size ? run.before : results), named, v.broken);
      toast("Sent to the agent.");
    } catch (e) {
      toast(errText(e), true);
    }
  });
  box.querySelector('[data-a="close"]').onclick = () => {
    stopTests();
    box.classList.add("hidden");
    run = null;
    testHooks.changed();
  };
  box.querySelector('[data-a="setup"]').onclick = async () => {
    try {
      openTestSetup(await api("Reviews.TestSetup"), run ? () => rerun() : null);
    } catch (e) {
      toast(errText(e), true);
    }
  };
  const out = box.querySelector("pre");
  if (out) out.scrollTop = atEnd ? out.scrollHeight : keep;
}

function lineClass(t) {
  if (/^\s*--- FAIL|^FAIL\b|^\s*FAIL:|panic:|\bFAILED\b/.test(t)) return "o-fail";
  if (/^\s*--- PASS|^ok\s|^PASS$|\bPASSED\b/.test(t)) return "o-pass";
  if (/^\s*--- SKIP/.test(t)) return "o-skip";
  return "";
}

// openTestSetup asks how this repository runs tests, then calls then.
export function openTestSetup(setup, then) {
  const home = (p) => p.replace(/^\/home\/[^/]+|^\/Users\/[^/]+/, "~");
  const scope = setup.found && setup.match !== "" && setup.match !== "*" ? "repo" : setup.found ? "all" : "repo";
  const examples = [
    ["go test", "go test -count=1 -v {dir} -run {run}"],
    ["docker compose", "docker compose run --rm app go test -count=1 -v {dir} -run {run}"],
    ["docker exec", "docker compose exec -T app go test -count=1 -v {dir} -run {run}"],
    ["make", "make test ARGS='-run {run}'"],
    ["pytest", "pytest -v {tests}"],
  ];
  const body = el(`<div class="settings test-setup">
    <p class="small">margin runs this command with <code>sh</code> in <code title="${esc(setup.dir)}">${esc(home(setup.dir))}</code> for each package of the picked tests, and shows what it prints. It is kept in your settings, never in the repository.</p>
    <section><h4>Command</h4>
      <input class="filter mono" id="ts-cmd" spellcheck="false" value="${esc(setup.command || setup.suggest || "")}" placeholder="go test -count=1 -v {dir} -run {run}">
      <div class="ts-examples">${examples.map(([n]) => `<button class="small" data-ex="${esc(n)}">${esc(n)}</button>`).join("")}</div>
      <table class="ts-places">${Object.entries(setup.places ?? {}).map(([k, v]) => `<tr><td><code>${esc(k)}</code></td><td class="muted small">${esc(v)}</td></tr>`).join("")}</table>
      <p class="muted small">Every value is shell-quoted. Go's <code>-v</code> lets margin mark each test passed or failed.</p>
    </section>
    <section><h4>Use it for</h4>
      <label class="opt"><input type="radio" name="ts-scope" value="repo"${scope === "repo" ? " checked" : ""}><span><b>${esc(setup.repo)}</b> · <code>${esc(home(setup.main))}</code> and its worktrees</span></label>
      <label class="opt"><input type="radio" name="ts-scope" value="all"${scope === "all" ? " checked" : ""}><span><b>Every repository</b> without a command of its own</span></label>
    </section>
    <div class="ts-preview muted small"></div>
    <div class="set-foot"><span class="spacer"></span><button class="small" data-act="cancel">Cancel</button><button class="primary small" data-act="save">${then ? "Save and run" : "Save"}</button></div>
  </div>`);
  const input = body.querySelector("#ts-cmd");
  body.querySelectorAll("[data-ex]").forEach((b) => (b.onclick = () => {
    input.value = examples.find(([n]) => n === b.dataset.ex)[1];
    input.focus();
  }));
  body.querySelector('[data-act="cancel"]').onclick = closeModal;
  body.querySelector('[data-act="save"]').onclick = async () => {
    const all = body.querySelector('input[name="ts-scope"]:checked').value === "all";
    try {
      await api("Reviews.SaveTestCommand", all ? "" : setup.main, input.value);
    } catch (e) {
      return toast(errText(e), true);
    }
    closeModal();
    toast("Test command saved.");
    then?.();
  };
  const m = openModal("How tests run here", body, "narrow");
  input.onkeydown = (e) => {
    if (e.key === "Enter") body.querySelector('[data-act="save"]').click();
    if (e.key === "Escape") closeModal();
  };
  m.onkey = (e) => (e.key === "Escape" ? (closeModal(), true) : true);
  setTimeout(() => input.focus());
}

// statusOf is the last run's word on a test of the coverage, by index.
export function statusOf(ti) {
  const t = state.view?.coverage?.tests[ti];
  if (!t) return "";
  return results.get(t.name) ?? "";
}

export const statusIcon = (s) => (s ? `<span class="tst t-${s}">${icons[s] ?? ""}</span>` : "");
