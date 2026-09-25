#!/bin/sh
# Regenerates the README screenshots from testdata/example. Needs go, tmux and python3.
# Everything runs on a private tmux server, so your own sessions are not touched.
set -eu
cd "$(dirname "$0")/../.."
out=docs/screenshots
work=$(mktemp -d)
trap 'tmux -L margin-shots kill-server 2>/dev/null || true; rm -rf "$work"' EXIT

go build -o "$work/margin" ./cmd/margin
cp -r testdata/example "$work/ex"
rm -f "$work/ex/example.review.state.yaml"
review="$work/ex/example.review.yaml"

m() { "$work/margin" "$@" --review "$review"; }
T() { tmux -L margin-shots -f /dev/null "$@"; }

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
m answer q1 - >/dev/null <<'EOF'
Only the message. errors.Is still finds ErrInsufficient, and nothing in this repo compares with ==.
EOF

open_viewer() {
	T kill-server 2>/dev/null || true
	while m where >/dev/null 2>&1; do sleep 0.1; done
	T new-session -d -x 150 -y "$1" "env TERM=xterm-256color COLORTERM=truecolor $work/margin open $review" \; set -g status off
	until m where >/dev/null 2>&1; do sleep 0.1; done
	sleep 0.6
}
go_to() { m goto "$1" >/dev/null; sleep 0.3; }
keys() {
	for k in "$@"; do
		T send-keys "$k"
		sleep 0.2
	done
	sleep 0.3
}
text() { T send-keys -l "$1"; sleep 0.3; }
shot() {
	T capture-pane -e -p >"$work/$1.ans"
	python3 "$out/svg.py" "$work/$1.ans" "$out/$1.svg"
}

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
	printf "${dim} %s${rst}\n" "$(printf '─%.0s' $(seq 146))"
	printf "${txt} > ${dim}Ask for a change, or press a on a line in the review…${rst}\n"
} >"$work/agent.ans"

open_viewer 36
go_to reserve:2
T capture-pane -e -p >"$work/hero.ans"
python3 "$out/svg.py" "$work/hero.ans" "$out/hero.svg" --extra "$work/agent.ans"

open_viewer 34
go_to 0
keys k
shot intent

open_viewer 34
go_to 0
keys j j j j j j j j
shot overview

open_viewer 26
go_to store
shot calls

open_viewer 34
go_to reserve:1
keys j j j c
text "Log the rejected quantity too, support will ask about it."
keys Enter
shot comment

open_viewer 22
go_to audit
shot hidden

open_viewer 24
go_to reserve
shot rationale

open_viewer 27
go_to reserve:2
keys a
text "What happens if Record panics while the lock is held?"
shot ask

open_viewer 16
go_to store
keys Tab
shot stops

open_viewer 18
go_to recap
shot recap

open_viewer 26
go_to store
keys /
text record
keys Enter
shot search

open_viewer 22
go_to report
keys u
shot coverage

open_viewer 20
go_to tests
shot grid

open_viewer 30
go_to tests
keys j j j Enter
keys '}' j j j
shot spotlight

open_viewer 40
go_to store
keys F F F
go_to store:2
shot filter

# The comment written for the comment shot above is sent, and the agent resolves it.
T kill-server 2>/dev/null || true
while m where >/dev/null 2>&1; do sleep 0.1; done
sed -i '/draft: true/d' "$work/ex/example.review.state.yaml"
m resolve c1 - >/dev/null <<'EOF'
Fixed: Reserve now records a rejected quantity in the log with a delta of 0, so support can see it. TestReserve covers it.
EOF
open_viewer 44
go_to reserve:5
keys j j
shot resolved

m export | fold -s -w 100 >"$work/export.txt"
python3 "$out/svg.py" "$work/export.txt" "$out/export.svg" --title "margin export"

# A second, smaller review: a helper moved to another package, and a file no stop shows.
demo="$work/demo"
mkdir -p "$demo/base/cart" "$demo/current/cart" "$demo/current/money"
round_body='	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	cents := math.Round(x * 100)
	if cents == 0 {
		return 0
	}
	return cents / 100
}'
cart_head='type Item struct {
	Name  string
	Price float64
	Qty   int
}

func Total(items []Item) float64 {
	sum := 0.0
	for _, it := range items {
		sum += it.Price * float64(it.Qty)
	}'
printf 'package cart\n\nimport "math"\n\n%s\n\treturn roundCents(sum)\n}\n\nfunc roundCents(x float64) float64 {\n%s\n' "$cart_head" "$round_body" >"$demo/base/cart/cart.go"
printf 'package cart\n\nimport "example.com/shop/money"\n\n%s\n\treturn money.Round(sum)\n}\n' "$cart_head" >"$demo/current/cart/cart.go"
printf 'package money\n\nimport "math"\n\n// Round rounds x to whole cents; NaN and infinities become 0.\nfunc Round(x float64) float64 {\n%s\n' "$round_body" >"$demo/current/money/round.go"
printf 'package cart\n\n// Discount takes pct percent off the total.\nfunc Discount(total float64, pct int) float64 {\n\treturn total * float64(100-pct) / 100\n}\n' >"$demo/current/cart/discount.go"
cat >"$demo/demo.review.yaml" <<'EOF'
version: 1
repo: current
base_dir: base
kicker: "shop · current/ vs base/"
title: Rounding moves to the money package
asked: Move cent rounding into a money package so invoices can use it too.
asked_from: the commit message
did: Moves roundCents out of cart into money as Round, and Total calls it from there.
gap: none
stations:
  - id: rounding
    title: roundCents becomes money.Round
    lede: The helper moves package and gets exported; its body is unchanged.
    risk: low
    risk_why: a move with no change in behaviour
    concern: refactor
    scope: Both ends of the move, read together.
    what: Total now calls money.Round, which holds the old roundCents body.
    why: Invoices need the same rounding and cannot import cart.
    parts:
      - file: cart/cart.go
        hunks: true
      - file: money/round.go
        hunks: true
    notes:
      - at: "return money.Round(sum)"
        kind: ok
        text: Same call as before, now through the new package.
EOF
review_demo="$demo/demo.review.yaml"
d() { "$work/margin" "$@" --review "$review_demo"; }
open_demo() {
	T kill-server 2>/dev/null || true
	while d where >/dev/null 2>&1; do sleep 0.1; done
	T new-session -d -x 150 -y "$1" "env TERM=xterm-256color COLORTERM=truecolor $work/margin open $review_demo" \; set -g status off
	until d where >/dev/null 2>&1; do sleep 0.1; done
	sleep 0.6
}
open_demo 42
d goto money/round.go:6 >/dev/null
sleep 0.3
shot moved

open_demo 18
d goto unplaced >/dev/null
sleep 0.3
shot unplaced

echo "wrote $(ls "$out"/*.svg | wc -l) screenshots to $out"
