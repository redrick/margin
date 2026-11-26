const key = "margin.vim";
let mode = "standard";
try {
  if (localStorage.getItem(key) === "on") mode = "vim";
} catch {}

export function editorMode() {
  return mode;
}

export function setEditorMode(next) {
  mode = next;
  try {
    localStorage.setItem(key, mode === "vim" ? "on" : "off");
    return true;
  } catch {
    return false;
  }
}
