import assert from "node:assert/strict";
import test from "node:test";
import { agentReply, discussionThreads, nextThread, questionSelection, readReply, unreadReply } from "./assets/discussions.js";

test("next ask stays in its stop and advances through multiple threads on a line", () => {
  const drafts = [
    { id: "c1", station: "one", part: 0, at: 2, draft: true },
    { id: "c2", station: "one", part: 0, at: 2, draft: true },
    { id: "c3", station: "other", part: 0, at: 3 },
  ];
  const questions = [{ id: "q", station: "one", part: 1, at: 0, answer: "The answer" }];
  const view = { stations: [{ notes: [] }], reader: { comments: drafts, questions: [] } };
  const threads = discussionThreads(view, questions, {});
  const next = (from = "", reply = false, cursor = [0, 0, 2]) => nextThread(threads, "one", reply, from, cursor, (part) => part)?.id;
  assert.equal(next(), "c1");
  assert.equal(next("c1"), "c2");
  assert.equal(next("c2"), "q");
  assert.equal(next("q"), "c1");
  assert.equal(next("", false, [1, 1, 0]), "q");
  assert.equal(next("c1", true), "q");
  assert.equal(next("q", true), undefined);
});

test("unread state belongs to the review and to this version of the reply", async () => {
  const storage = new Map();
  globalThis.localStorage = { getItem: (key) => storage.get(key), setItem: (key, value) => storage.set(key, value) };
  const thread = { id: "c1", asked: "today", reply: { text: "First reply" } };
  assert.equal(unreadReply("one", thread), true);
  readReply("one", thread);
  assert.equal(unreadReply("one", thread), false);
  assert.equal(unreadReply("other", thread), true);
  const reopened = await import("./assets/discussions.js?reopened");
  assert.equal(reopened.unreadReply("one", thread), false);
  assert.equal(reopened.unreadReply("one", { ...thread, reply: { text: "Updated reply" } }), true);
});

test("a reader follow-up does not create or update an agent reply", () => {
  const reader = "**Reply from the reader:** Question\n> More context\n";
  assert.equal(agentReply(`## c1\n\n${reader}`), "");
  assert.equal(agentReply(`## c1\n> Original comment\n\nThe agent replied.\n\n${reader}\n`), "The agent replied.");
  assert.equal(agentReply(`## c1\n> Original comment\n\nThe agent replied.\n\n${reader}\nA follow-up answer.\n`), "The agent replied.\n\nA follow-up answer.");
});

test("question drafts can follow a reload of the same code and side", () => {
  const part = { file: "code.go", rows: [{ n: 3, s: [{ t: "before()", c: "red" }] }, { n: 4, s: [] }] };
  const before = questionSelection(part, 2, 3);
  assert.ok(before);
  assert.equal(questionSelection({ ...part, rows: [{ n: 3, s: [{ t: "before", c: "blue" }, { t: "()" }] }, { n: 4 }] }, 2, 3), before);
  assert.notEqual(questionSelection({ ...part, base: true }, 2, 3), before);
  assert.notEqual(questionSelection({ ...part, rows: [{ n: 3, s: [{ t: "after()" }] }, { n: 4 }] }, 2, 3), before);
  assert.equal(questionSelection(part, 2, 4), null);
});
