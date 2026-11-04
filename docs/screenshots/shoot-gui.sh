#!/bin/sh
# Regenerates the margin-gui screenshots in docs/screenshots/gui from testdata/example: the twins
# of the margin-tui ones shoot.sh takes, for the page's sliders. Needs go, python3 with Pillow and
# Chrome or Chromium. The window's page runs headless against its real Go services (see
# cmd/margin-gui/shoot_test.go), with its own settings, so nothing opens and nothing of yours is read.
set -eu
cd "$(dirname "$0")/../.."
out=docs/screenshots/gui
work=$(mktemp -d)
port=47123
trap 'kill "$server" 2>/dev/null || true; rm -rf "$work"' EXIT
mkdir -p "$out" "$work/home"

chrome=$(command -v chromium || command -v google-chrome-stable || command -v google-chrome || command -v chromium-browser)
tags=shoot
pkg-config --exists webkitgtk-6.0 2>/dev/null || tags="shoot gtk3"
go test -c -tags "$tags" -o "$work/shoot.test" ./cmd/margin-gui
go build -o "$work/margin" ./cmd/margin-tui

cp -r testdata/example "$work/ex"
rm -f "$work/ex/example.review.state.yaml"
review="$work/ex/example.review.yaml"
cat >"$work/ex/example.review.state.yaml" <<'EOF'
questions:
  - id: q1
    station: reserve
    file: inventory/stock.go
    line: 39
    needle: 'return fmt.Errorf("%w: %s has %d, want %d", ErrInsufficient, sku, available, qty)'
    text: Does wrapping the error change what callers see?
    asked: 2026-09-21T10:00:00Z
EOF
"$work/margin" answer q1 - --review "$review" >/dev/null <<'EOF'
Only the message. errors.Is still finds ErrInsufficient, and nothing in this repo compares with ==.
EOF
# The reserve stop with before/after examples and a background part, as in shoot.sh.
python3 - "$review" <<'EOF'
import sys
p = sys.argv[1]
s = open(p).read()
old = """    parts:
      - file: inventory/stock.go
        func: Store.Reserve
"""
new = """    examples:
      - input: 'Reserve("apple", -2) with 5 in stock'
        before: nil, and apple goes up to 7
        after: ErrInvalidQuantity, stock stays 5
        note: A negative quantity used to add stock; the new check fixes that too.
      - input: 'Reserve("pear", 2) with 1 in stock'
        before: insufficient stock
        after: "insufficient stock: pear has 1, want 2"
      - input: 'Reserve("apple", 3) with 5 in stock'
        before: nil, 2 left
        after: nil, 2 left, and one audit entry
    parts:
      - file: inventory/stock.go
        func: Store.Add
        background: true
        about: The other writer on the same lock, unchanged. Reserve takes out what Add puts in.
      - file: inventory/stock.go
        func: Store.Reserve
"""
assert s.count(old) == 1
open(p, "w").write(s.replace(old, new))
EOF

dim='\033[38;2;139;148;158m'
txt='\033[38;2;201;209;217m'
wht='\033[1;38;2;240;246;252m'
grn='\033[38;2;63;185;80m'
rst='\033[0m'
{
	printf "${dim} > [margin q1] inventory/stock.go:39 in station reserve: Does wrapping the error change what callers see?${rst}\n"
	printf " ${wht}⏺${rst}${txt} Only the message. The new error wraps the sentinel with %%w, so errors.Is(err, ErrInsufficient) is still true and${rst}\n"
	printf "${txt}   callers that branch on it keep working. Nothing in this repository compares with ==, so nothing breaks.${rst}\n"
	printf " ${grn}⏺${rst}${wht} Bash${rst}${txt}(margin answer q1 -)${rst}\n"
	printf "${dim}   ⎿  q1 answered: station reserve note 4${rst}\n"
	printf "${txt} > ${dim}Ask for a change, or press a on a line in the review…${rst}\n"
} >"$work/agent.ans"

HOME="$work/home" XDG_CONFIG_HOME="$work/home/.config" XDG_DATA_HOME="$work/home/.local/share" \
	XDG_STATE_HOME="$work/home/.local/state" MARGIN_SHOOT_ADDR="127.0.0.1:$port" \
	"$work/shoot.test" -test.run TestShootServer -test.timeout 0 >"$work/server.log" 2>&1 &
server=$!
until grep -q . "$work/server.log" 2>/dev/null && python3 -c "import socket; socket.create_connection(('127.0.0.1', $port))" 2>/dev/null; do sleep 0.2; done

# shot NAME WIDTH HEIGHT QUERY: the window, set up as shoot_test.go reads QUERY, with the aspect of
# the margin-tui shot it pairs with. It is drawn 1.4 times larger, so its text reads at the
# terminal's size when the two are laid over each other.
shot() {
	"$chrome" --headless=new --user-data-dir="$work/chrome" --no-first-run --disable-gpu --hide-scrollbars --force-device-scale-factor=2.1 \
		--window-size="$(($2 * 10 / 14)),$(($3 * 10 / 14))" --virtual-time-budget=15000 --screenshot="$work/$1.png" \
		"http://127.0.0.1:$port/?review=$review&side=hide&$4" >/dev/null 2>&1
	python3 -c "import sys; from PIL import Image; Image.open(sys.argv[1]).convert('RGB').save(sys.argv[2], quality=86, method=6)" \
		"$work/$1.png" "$out/$1.webp"
}

shot hero 1462 860 "full=0&do=g:reserve:2&agent=$work/agent.ans"
shot guide-overview 1462 910 "full=0&do=g:0"
shot guide-brief 1462 1036 "full=0&do=g:reserve"
shot guide-change 1462 1120 "full=0&do=g:reserve,Space,Space"
shot guide-settle 1462 910 "full=0&do=g:report:1"
shot comment-kind 1472 700 "full=0&do=g:reserve:1,c"
shot coverage 1462 532 "full=1&do=g:report,u"
shot around 1472 700 "full=1&do=g:reserve,s:around this stop"
shot spotlight 1472 700 "full=1&do=g:tests,j,j,j,Enter,g:reserve:2,s:// Reserve takes"
shot guide-recap 1462 700 "full=0&do=g:store,1,g:reserve,1,g:recap"
shot try-edit 1472 700 "full=1&do=g:reserve,s:// Reserve takes,l:if qty <= 0,J,J,i,z:1500,f,<,l,x"
echo "wrote $(ls "$out"/*.webp | wc -l) screenshots to $out"
