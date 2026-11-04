import { EditorState, Compartment } from "@codemirror/state";
import { EditorView, keymap, lineNumbers, highlightActiveLine, drawSelection, highlightSpecialChars } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { HighlightStyle, syntaxHighlighting, StreamLanguage, indentUnit, bracketMatching, indentOnInput } from "@codemirror/language";
import { tags as t } from "@lezer/highlight";
import { vim, Vim, getCM } from "@replit/codemirror-vim";
import { go } from "@codemirror/lang-go";
import { javascript } from "@codemirror/lang-javascript";
import { python } from "@codemirror/lang-python";
import { rust } from "@codemirror/lang-rust";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { css } from "@codemirror/lang-css";
import { html } from "@codemirror/lang-html";
import { markdown } from "@codemirror/lang-markdown";
import { java } from "@codemirror/lang-java";
import { cpp } from "@codemirror/lang-cpp";
import { php } from "@codemirror/lang-php";
import { sql } from "@codemirror/lang-sql";
import { ruby } from "@codemirror/legacy-modes/mode/ruby";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { toml } from "@codemirror/legacy-modes/mode/toml";

const byExt = {
  go: () => go(),
  js: () => javascript({ jsx: true }), mjs: () => javascript(), cjs: () => javascript(), jsx: () => javascript({ jsx: true }),
  ts: () => javascript({ typescript: true }), tsx: () => javascript({ typescript: true, jsx: true }),
  py: () => python(), rs: () => rust(), json: () => json(), yaml: () => yaml(), yml: () => yaml(),
  css: () => css(), scss: () => css(), html: () => html(), htm: () => html(), vue: () => html(), md: () => markdown(),
  java: () => java(), kt: () => java(), c: () => cpp(), h: () => cpp(), cc: () => cpp(), cpp: () => cpp(), hpp: () => cpp(),
  php: () => php(), sql: () => sql(),
  rb: () => StreamLanguage.define(ruby), rake: () => StreamLanguage.define(ruby), erb: () => StreamLanguage.define(ruby),
  sh: () => StreamLanguage.define(shell), bash: () => StreamLanguage.define(shell), zsh: () => StreamLanguage.define(shell),
  toml: () => StreamLanguage.define(toml),
};

// github-dark, like the rest of the code in the window.
const style = HighlightStyle.define([
  { tag: [t.keyword, t.modifier, t.operatorKeyword, t.controlKeyword, t.definitionKeyword, t.moduleKeyword], color: "#ff7b72" },
  { tag: [t.string, t.special(t.string), t.regexp, t.character], color: "#a5d6ff" },
  { tag: [t.number, t.bool, t.null, t.atom, t.constant(t.name), t.standard(t.name)], color: "#79c0ff" },
  { tag: [t.function(t.variableName), t.function(t.propertyName), t.definition(t.function(t.variableName))], color: "#d2a8ff" },
  { tag: [t.typeName, t.className, t.namespace], color: "#ffa657" },
  { tag: [t.comment, t.lineComment, t.blockComment, t.docComment], color: "#8b949e", fontStyle: "italic" },
  { tag: [t.propertyName, t.attributeName], color: "#79c0ff" },
  { tag: [t.tagName], color: "#7ee787" },
  { tag: [t.operator, t.punctuation, t.bracket], color: "#c9d1d9" },
  { tag: t.heading, color: "#79c0ff", fontWeight: "bold" },
]);

const theme = EditorView.theme(
  {
    "&": { backgroundColor: "var(--bg-1)", color: "#c9d1d9", fontSize: "12.5px" },
    ".cm-content": { fontFamily: "var(--mono)", caretColor: "#e3b341", padding: "2px 0" },
    ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.5" },
    ".cm-gutters": { backgroundColor: "var(--bg-1)", color: "var(--fg-faint)", border: "none", borderRight: "2px solid #e3b341" },
    ".cm-activeLine": { backgroundColor: "rgba(227, 179, 65, .07)" },
    ".cm-activeLineGutter": { backgroundColor: "rgba(227, 179, 65, .18)", color: "var(--fg)" },
    "&.cm-focused": { outline: "none" },
    ".cm-cursor, .cm-fat-cursor": { borderLeftColor: "#e3b341" },
    "&:not(.cm-focused) .cm-fat-cursor": { outline: "1px solid #e3b341" },
    ".cm-fat-cursor": { background: "rgba(227, 179, 65, .75) !important", color: "#0d1117 !important" },
    ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": { backgroundColor: "rgba(122, 162, 247, .35) !important" },
    ".cm-panels": { backgroundColor: "var(--bg-2)", color: "var(--fg)", fontFamily: "var(--mono)" },
    ".cm-panels input": { color: "var(--fg)", fontFamily: "var(--mono)" },
    ".cm-vim-panel": { padding: "2px 8px" },
  },
  { dark: true },
);

let exDefined = false;
const live = new Set();

// The ex commands reach whichever editor ran them.
function defineEx() {
  if (exDefined) return;
  exDefined = true;
  const find = (cm) => [...live].find((e) => getCM(e.view) === cm);
  const run = (cm) => find(cm)?.opts.onRun?.(find(cm).view.state.doc.toString());
  const quit = (cm) => find(cm)?.opts.onCancel?.();
  Vim.defineEx("write", "w", run);
  Vim.defineEx("wq", "wq", run);
  Vim.defineEx("xit", "x", run);
  Vim.defineEx("quit", "q", quit);
}

// createEditor puts an editor in parent. opts: doc, file, firstLine, vim, onRun(text), onCancel(),
// onChange(text). It returns { view, focus(), set(text, firstLine), destroy() }.
export function createEditor(parent, opts) {
  defineEx();
  const ext = (opts.file ?? "").split(".").pop().toLowerCase();
  const lang = byExt[ext]?.();
  const first = new Compartment();
  const numbers = (n) => lineNumbers({ formatNumber: (i) => String(i + n - 1) });
  const ed = { opts };
  const runKey = (v) => (opts.onRun?.(v.state.doc.toString()), true);
  const state = EditorState.create({
    doc: opts.doc ?? "",
    extensions: [
      opts.vim !== false ? vim({ status: true }) : [],
      first.of(numbers(opts.firstLine ?? 1)),
      highlightSpecialChars(),
      history(),
      drawSelection(),
      highlightActiveLine(),
      bracketMatching(),
      indentOnInput(),
      indentUnit.of("\t"),
      EditorState.tabSize.of(4),
      syntaxHighlighting(style),
      theme,
      lang ?? [],
      keymap.of([
        { key: "Mod-Enter", run: runKey },
        { key: "Mod-s", run: runKey },
        ...(opts.vim === false ? [{ key: "Escape", run: () => (opts.onCancel?.(), true) }] : []),
        indentWithTab,
        ...defaultKeymap,
        ...historyKeymap,
      ]),
      EditorView.updateListener.of((u) => u.docChanged && opts.onChange?.(u.state.doc.toString())),
    ],
  });
  ed.view = new EditorView({ state, parent });
  ed.focus = () => ed.view.focus();
  ed.set = (text, firstLine) => {
    ed.view.dispatch({ changes: { from: 0, to: ed.view.state.doc.length, insert: text }, effects: first.reconfigure(numbers(firstLine)) });
  };
  ed.destroy = () => {
    live.delete(ed);
    ed.view.destroy();
  };
  live.add(ed);
  return ed;
}

export { Vim };
