//go:build shoot

package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestShootServer serves the window's page and services over plain HTTP, so a headless browser
// can take the screenshots in docs/screenshots (see shoot-gui.sh). It runs until killed.
func TestShootServer(t *testing.T) {
	addr := os.Getenv("MARGIN_SHOOT_ADDR")
	if addr == "" {
		t.Skip("MARGIN_SHOOT_ADDR is not set")
	}
	ws, rv, ag := &Workspace{}, &Reviews{}, &Agents{}
	app := application.New(application.Options{Name: "margin"})
	ws.app, rv.app, ag.app = app, app, app
	rv.agents = ag
	services := map[string]any{"Workspace": ws, "Reviews": rv, "Agents": ag, "Settings": &Settings{}}

	page, err := fs.Sub(assets, "assets")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/wails/runtime.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(shootRuntime))
	})
	mux.HandleFunc("/shoot.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(shootScript))
	})
	mux.HandleFunc("/agent", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, r.URL.Query().Get("f"))
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string            `json:"name"`
			Args []json.RawMessage `json:"args"`
		}
		var out struct {
			Result any    `json:"result"`
			Error  string `json:"error,omitempty"`
		}
		err := json.NewDecoder(r.Body).Decode(&in)
		if err == nil {
			out.Result, err = call(services, in.Name, in.Args)
		}
		if err != nil {
			out.Error = err.Error()
		}
		json.NewEncoder(w).Encode(out)
	})
	files := http.FileServerFS(page)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			files.ServeHTTP(w, r)
			return
		}
		html, _ := fs.ReadFile(page, "index.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.Replace(string(html), "</body>", `<script type="module" src="/shoot.js"></script></body>`, 1)))
	})
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal(http.Serve(l, mux))
}

func call(services map[string]any, name string, raw []json.RawMessage) (any, error) {
	svc, method, _ := strings.Cut(strings.TrimPrefix(name, "main."), ".")
	s, ok := services[svc]
	if !ok {
		return nil, errors.New("no service " + svc)
	}
	m := reflect.ValueOf(s).MethodByName(method)
	if !m.IsValid() {
		return nil, errors.New("no method " + name)
	}
	args := make([]reflect.Value, m.Type().NumIn())
	for i := range args {
		p := reflect.New(m.Type().In(i))
		if i < len(raw) {
			if err := json.Unmarshal(raw[i], p.Interface()); err != nil {
				return nil, err
			}
		}
		args[i] = p.Elem()
	}
	var res any
	for _, v := range m.Call(args) {
		if v.Type() != reflect.TypeFor[error]() {
			res = v.Interface()
		} else if !v.IsNil() {
			return nil, v.Interface().(error)
		}
	}
	return res, nil
}

// shootRuntime stands in for Wails' runtime: calls go to /call, events stay in the page, and the
// agent is a recorded transcript (?agent=file) instead of a process.
const shootRuntime = `
const on = {};
const q = new URLSearchParams(location.search);
export const Events = {
  On(name, fn) { (on[name] ??= []).push(fn); return () => {}; },
  Emit(name, data) { (on[name] ?? []).forEach((fn) => fn({ name, data })); },
};
export const Clipboard = { SetText: async () => true, Text: async () => "" };
export const Call = {
  async ByName(name, ...args) {
    const agent = q.get("agent");
    if (name.startsWith("main.Agents.")) {
      const m = name.slice(12);
      if (m === "Running") return !!agent;
      if (m === "Live") return [];
      if (m === "Start") {
        const t = await (await fetch("/agent?f=" + encodeURIComponent(agent))).text();
        return btoa(String.fromCharCode(...new TextEncoder().encode(t.replaceAll("\n", "\r\n"))));
      }
      return null;
    }
    const r = await fetch("/call", { method: "POST", body: JSON.stringify({ name, args }) });
    const j = await r.json();
    if (j.error) throw new Error(j.error);
    return j.result;
  },
};
`

// shootScript puts the window in the state one screenshot needs, from the page's query string:
// review (a file to open), side=hide, full=1|0 (whole stops or the guided walk), and do, a comma
// separated list of actions: g:TARGET is a margin goto, s:TEXT scrolls to the first text in the stop
// that starts with TEXT, l:TEXT clicks the first code line that starts with TEXT, z:MS waits, t:TEXT
// types into the focused field, and anything else is a key ("Space", "comma"). Then it sets the title to "ready".
const shootScript = `
import { Events } from "/wails/runtime.js";
import { state } from "./core.js";
import { openReviewFile } from "./review.js";
const q = new URLSearchParams(location.search);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const key = (k) => (document.activeElement ?? document).dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
await sleep(400);
if (q.get("review")) await openReviewFile(q.get("review"));
await sleep(400);
if (q.get("side") === "hide" && !document.querySelector("#workspace").classList.contains("focus")) key("\\");
if (q.has("full") && !!state.view?.reader?.full !== (q.get("full") === "1")) {
  key("w");
  await sleep(400);
}
for (const a of (q.get("do") ?? "").split(",").filter(Boolean)) {
  if (a.startsWith("g:")) {
    Events.Emit("ctl:request", { id: 0, cmd: "goto", arg: a.slice(2) });
    await sleep(500);
  } else if (a.startsWith("s:")) {
    const want = a.slice(2).toLowerCase();
    [...document.querySelectorAll("#station *")].find((el) => el.childElementCount === 0 && el.textContent.trim().toLowerCase().startsWith(want))?.scrollIntoView({ block: "start" });
  } else if (a.startsWith("l:")) {
    const want = a.slice(2);
    [...document.querySelectorAll("#station .row[data-line] .src")].find((el) => el.textContent.trim().startsWith(want))?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  } else if (a.startsWith("z:")) {
    await sleep(+a.slice(2));
  } else if (a.startsWith("t:")) {
    const el = document.activeElement;
    el.value = a.slice(2);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  } else {
    key(a === "comma" ? "," : a === "Space" ? " " : a);
  }
  await sleep(250);
}
await sleep(600);
document.title = "ready";
`
