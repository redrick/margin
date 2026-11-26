import assert from "node:assert/strict";
import test from "node:test";

let launches = 0;
async function launch(storage) {
  Object.defineProperty(globalThis, "localStorage", { value: storage, configurable: true });
  return import(`./assets/editor-mode.js?launch=${++launches}`);
}

test("defaults to Standard and restores existing Vim preferences", async () => {
  for (const [stored, expected] of [[null, "standard"], ["invalid", "standard"], ["off", "standard"], ["on", "vim"]]) {
    const { editorMode } = await launch({ getItem: () => stored });
    assert.equal(editorMode(), expected);
  }
});

test("changing modes uses the existing storage key and survives a relaunch", async () => {
  const stored = new Map();
  const storage = { getItem: (key) => stored.get(key), setItem: (key, value) => stored.set(key, value) };
  const editor = await launch(storage);
  for (const [mode, value] of [["vim", "on"], ["standard", "off"]]) {
    assert.equal(editor.setEditorMode(mode), true);
    assert.equal(editor.editorMode(), mode);
    assert.equal(stored.get("margin.vim"), value);
    assert.equal((await launch(storage)).editorMode(), mode);
  }
});

test("a mode change still works for the session when storage is blocked", async () => {
  const blocked = () => { throw new Error("blocked"); };
  const { editorMode, setEditorMode } = await launch({ getItem: blocked, setItem: blocked });
  assert.equal(editorMode(), "standard");
  assert.equal(setEditorMode("vim"), false);
  assert.equal(editorMode(), "vim");
});
