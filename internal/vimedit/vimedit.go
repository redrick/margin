// Package vimedit is a small modal editor for a few lines of code, with the vim keys people reach
// for first: motions, operators with counts and text objects, visual modes, registers, undo, dot
// repeat and a command line for :w and :q.
package vimedit

import (
	"strconv"
	"strings"
	"unicode"
)

type Mode int

const (
	Normal Mode = iota
	Insert
	Visual
	VisualLine
	Command
)

func (m Mode) String() string {
	switch m {
	case Insert:
		return "INSERT"
	case Visual:
		return "VISUAL"
	case VisualLine:
		return "VISUAL LINE"
	case Command:
		return "COMMAND"
	}
	return "NORMAL"
}

// Result is what a key asks of the editor's owner.
type Result int

const (
	None   Result = iota
	Run           // :w, :wq, :x, ctrl+s; Ex holds the command, so the owner can read its ! and arguments
	Cancel        // :q, :q!
	Ex            // a command the editor does not know; Ex holds it for the owner
)

// Key is one key press: Name as Bubble Tea spells it ("esc", "enter", "ctrl+r", "a"), and the runes
// it typed, which a paste makes several.
type Key struct {
	Name  string
	Runes []rune
}

type snapshot struct {
	lines    [][]rune
	row, col int
}

type register struct {
	text  [][]rune
	lines bool
}

type Editor struct {
	Lines    [][]rune
	Row, Col int
	Mode     Mode
	// First is the number the first line has in its file, for :N.
	First int
	Cmd   string // the command line, in Command mode
	Msg   string // what the last command said
	Ex    string // the last command run from the command line

	reg        register
	undo, redo []snapshot
	want       int // column j and k aim for

	count, opCount int
	op             string
	await          string
	vr, vc         int

	rec       []Key
	recording bool
	changed   bool
	last      []Key
	replaying bool
}

func New(text string, first int) *Editor {
	e := &Editor{First: first}
	for l := range strings.SplitSeq(text, "\n") {
		e.Lines = append(e.Lines, []rune(l))
	}
	e.Col = indentOf(e.Lines[0])
	e.want = e.Col
	return e
}

// Text is the lines joined; an editor left with one empty line holds nothing.
func (e *Editor) Text() string {
	if len(e.Lines) == 1 && len(e.Lines[0]) == 0 {
		return ""
	}
	out := make([]string, len(e.Lines))
	for i, l := range e.Lines {
		out[i] = string(l)
	}
	return strings.Join(out, "\n")
}

// Selection is the visual selection, start to end inclusive; ok is false outside visual modes.
func (e *Editor) Selection() (r1, c1, r2, c2 int, lines, ok bool) {
	if e.Mode != Visual && e.Mode != VisualLine {
		return 0, 0, 0, 0, false, false
	}
	r1, c1, r2, c2 = order(e.vr, e.vc, e.Row, e.Col)
	return r1, c1, r2, c2, e.Mode == VisualLine, true
}

// Pending is the keys of a command not finished yet, as vim shows them.
func (e *Editor) Pending() string {
	s := ""
	if e.count > 0 {
		s += strconv.Itoa(e.count)
	}
	s += e.op
	if e.opCount > 0 {
		s += strconv.Itoa(e.opCount)
	}
	return s + strings.TrimPrefix(strings.TrimPrefix(e.await, "obj"), "op")
}

func (e *Editor) Key(k Key) Result {
	if k.Name == "ctrl+s" {
		e.toNormal()
		return Run
	}
	start := e.Mode == Normal && e.idle() && !e.replaying
	if start {
		e.rec, e.recording, e.changed = nil, true, false
	}
	if e.recording {
		e.rec = append(e.rec, k)
	}
	var res Result
	switch e.Mode {
	case Insert:
		e.insert(k)
	case Command:
		res = e.command(k)
	case Visual, VisualLine:
		e.visual(k)
	default:
		e.normal(k)
	}
	if e.recording && e.Mode == Normal && e.idle() {
		if e.changed && !e.replaying {
			e.last = e.rec
		}
		e.recording = false
	}
	e.clamp()
	return res
}

func (e *Editor) idle() bool { return e.count == 0 && e.op == "" && e.await == "" && e.opCount == 0 }

func (e *Editor) reset() { e.count, e.opCount, e.op, e.await = 0, 0, "", "" }

func (e *Editor) toNormal() {
	if e.Mode == Insert && e.Col > 0 {
		e.Col--
	}
	e.Mode = Normal
	e.reset()
}

func (e *Editor) n() int {
	n := max(e.count, 1) * max(e.opCount, 1)
	return n
}

func (e *Editor) line() []rune { return e.Lines[e.Row] }

func (e *Editor) clamp() {
	e.Row = min(max(e.Row, 0), len(e.Lines)-1)
	limit := len(e.line())
	if e.Mode != Insert && limit > 0 {
		limit--
	}
	e.Col = min(max(e.Col, 0), limit)
}

func (e *Editor) save() {
	e.undo = append(e.undo, e.snap())
	e.redo = nil
	e.changed = true
}

func (e *Editor) snap() snapshot {
	ls := make([][]rune, len(e.Lines))
	for i, l := range e.Lines {
		ls[i] = append([]rune{}, l...)
	}
	return snapshot{lines: ls, row: e.Row, col: e.Col}
}

func (e *Editor) restore(s snapshot) { e.Lines, e.Row, e.Col = s.lines, s.row, s.col }

func char(k Key) string {
	if len(k.Runes) > 0 {
		return string(k.Runes)
	}
	if k.Name == "space" {
		return " "
	}
	return k.Name
}

func (e *Editor) normal(k Key) {
	ch := char(k)
	if e.await != "" {
		e.awaited(ch)
		return
	}
	if len(ch) == 1 && ch[0] >= '0' && ch[0] <= '9' && (ch != "0" || e.count > 0 || e.opCount > 0) {
		d := int(ch[0] - '0')
		if e.op != "" {
			e.opCount = e.opCount*10 + d
		} else {
			e.count = e.count*10 + d
		}
		return
	}
	if ch == "esc" {
		e.reset()
		return
	}
	if e.op != "" {
		switch {
		case ch == e.op:
			n := e.n()
			e.operate(e.op, e.Row, 0, min(e.Row+n-1, len(e.Lines)-1), 0, true)
		case ch == "i" || ch == "a":
			e.await = "obj" + ch
			return
		case ch == "g" || ch == "f" || ch == "F" || ch == "t" || ch == "T":
			e.await = ch
			return
		default:
			e.opMotion(ch)
		}
		e.reset()
		return
	}
	n := e.n()
	switch ch {
	case "d", "c", "y", ">", "<":
		e.op = ch
		return
	case "g", "f", "F", "t", "T", "r":
		e.await = ch
		return
	case "i", "insert":
		e.startInsert()
	case "a":
		if len(e.line()) > 0 {
			e.Col++
		}
		e.startInsert()
	case "I":
		e.Col = indentOf(e.line())
		e.startInsert()
	case "A":
		e.Col = len(e.line())
		e.startInsert()
	case "o", "O":
		e.save()
		ind := append([]rune{}, e.line()[:indentOf(e.line())]...)
		at := e.Row + 1
		if ch == "O" {
			at = e.Row
		}
		e.Lines = insertLines(e.Lines, at, [][]rune{ind})
		e.Row, e.Col = at, len(ind)
		e.Mode = Insert
	case "x", "delete":
		if len(e.line()) > 0 {
			e.operate("d", e.Row, e.Col, e.Row, min(e.Col+n, len(e.line()))-1, false)
		}
	case "X":
		if e.Col > 0 {
			e.operate("d", e.Row, max(e.Col-n, 0), e.Row, e.Col-1, false)
		}
	case "D", "C":
		if len(e.line()) > 0 {
			e.operate("d", e.Row, e.Col, e.Row, len(e.line())-1, false)
		} else if ch == "C" {
			e.save()
		}
		if ch == "C" {
			e.Col = len(e.line())
			e.Mode = Insert
		}
	case "s":
		if len(e.line()) > 0 {
			e.operate("c", e.Row, e.Col, e.Row, min(e.Col+n, len(e.line()))-1, false)
		} else {
			e.save()
			e.Mode = Insert
		}
	case "S":
		e.operate("c", e.Row, 0, min(e.Row+n-1, len(e.Lines)-1), 0, true)
	case "Y":
		e.operate("y", e.Row, 0, min(e.Row+n-1, len(e.Lines)-1), 0, true)
	case "p", "P":
		e.put(ch == "P", n)
	case "J":
		e.join(max(n, 2))
	case "~":
		e.save()
		l := e.line()
		for i := e.Col; i < min(e.Col+n, len(l)); i++ {
			l[i] = flip(l[i])
		}
		e.Col += n
	case "u":
		for ; n > 0 && len(e.undo) > 0; n-- {
			e.redo = append(e.redo, e.snap())
			e.restore(e.undo[len(e.undo)-1])
			e.undo = e.undo[:len(e.undo)-1]
		}
		e.Msg = ""
	case "ctrl+r":
		for ; n > 0 && len(e.redo) > 0; n-- {
			e.undo = append(e.undo, e.snap())
			e.restore(e.redo[len(e.redo)-1])
			e.redo = e.redo[:len(e.redo)-1]
		}
	case ".":
		e.repeat()
	case "v", "V":
		e.vr, e.vc = e.Row, e.Col
		e.Mode = Visual
		if ch == "V" {
			e.Mode = VisualLine
		}
	case ":":
		e.Mode, e.Cmd = Command, ""
	default:
		if r, c, _, _, ok := e.motion(ch, n); ok {
			e.moveTo(r, c, ch)
		}
	}
	e.reset()
}

func (e *Editor) moveTo(r, c int, motion string) {
	vertical := motion == "j" || motion == "k" || motion == "down" || motion == "up"
	e.Row = r
	if vertical {
		e.Col = e.want
	} else {
		e.Col = c
		if motion == "$" || motion == "end" {
			e.want = 1 << 30
		} else {
			e.want = c
		}
	}
}

func (e *Editor) startInsert() {
	e.save()
	e.Mode = Insert
}

func (e *Editor) awaited(ch string) {
	a := e.await
	e.await = ""
	n := e.n()
	switch {
	case a == "r":
		if ch == "esc" || len([]rune(ch)) != 1 || len(e.line()) < e.Col+n {
			break
		}
		e.save()
		for i := 0; i < n; i++ {
			e.line()[e.Col+i] = []rune(ch)[0]
		}
		e.Col += n - 1
	case a == "obji" || a == "obja":
		if r1, c1, r2, c2, lines, ok := e.object(a[3:], ch); ok && e.op != "" {
			e.operate(e.op, r1, c1, r2, c2, lines)
		}
	case a == "g":
		if ch == "g" {
			r := 0
			if e.count > 0 || e.opCount > 0 {
				r = min(n-1, len(e.Lines)-1)
			}
			if e.op != "" {
				r1, r2 := min(r, e.Row), max(r, e.Row)
				e.operate(e.op, r1, 0, r2, 0, true)
			} else {
				e.Row, e.Col = r, indentOf(e.Lines[r])
			}
		}
	default:
		if ch == "esc" {
			break
		}
		r, c, ok := e.find(a, []rune(ch)[0], n)
		if !ok {
			break
		}
		if e.op != "" {
			if a == "F" || a == "T" {
				e.operate(e.op, r, c, e.Row, e.Col-1, false)
			} else {
				e.operate(e.op, e.Row, e.Col, r, c, false)
			}
		} else {
			e.Col, e.want = c, c
		}
	}
	e.reset()
}

func (e *Editor) find(kind string, ch rune, n int) (int, int, bool) {
	l := e.line()
	c := e.Col
	for ; n > 0; n-- {
		found := false
		switch kind {
		case "f", "t":
			from := c + 1
			if kind == "t" {
				from = c + 2
			}
			for i := from; i < len(l); i++ {
				if l[i] == ch {
					c, found = i, true
					if kind == "t" {
						c = i - 1
					}
					break
				}
			}
		case "F", "T":
			from := c - 1
			if kind == "T" {
				from = c - 2
			}
			for i := from; i >= 0; i-- {
				if l[i] == ch {
					c, found = i, true
					if kind == "T" {
						c = i + 1
					}
					break
				}
			}
		}
		if !found {
			return 0, 0, false
		}
	}
	return e.Row, c, true
}

// motion says where a motion key goes from the cursor, whether it moves whole lines and whether the
// character it lands on belongs to an operator's range.
func (e *Editor) motion(ch string, n int) (r, c int, lines, inclusive, ok bool) {
	r, c = e.Row, e.Col
	switch ch {
	case "h", "left", "backspace":
		c = max(c-n, 0)
	case "l", "right", " ":
		c = min(c+n, max(len(e.line())-1, 0))
	case "j", "down", "enter", "+":
		r, lines = min(r+n, len(e.Lines)-1), true
		if ch == "enter" || ch == "+" {
			c = indentOf(e.Lines[r])
		}
	case "k", "up", "-":
		r, lines = max(r-n, 0), true
		if ch == "-" {
			c = indentOf(e.Lines[r])
		}
	case "0", "home":
		c = 0
	case "^":
		c = indentOf(e.line())
	case "$", "end":
		r = min(r+n-1, len(e.Lines)-1)
		c, inclusive = max(len(e.Lines[r])-1, 0), true
	case "G":
		r, lines = len(e.Lines)-1, true
		if e.count > 0 {
			r = min(e.count, len(e.Lines)) - 1
		}
		c = indentOf(e.Lines[r])
	case "w", "W":
		for ; n > 0; n-- {
			r, c = e.nextWord(r, c, ch == "W")
		}
	case "b", "B":
		for ; n > 0; n-- {
			r, c = e.prevWord(r, c, ch == "B")
		}
	case "e", "E":
		for ; n > 0; n-- {
			r, c = e.wordEnd(r, c, ch == "E")
		}
		inclusive = true
	case "}", "{":
		d := 1
		if ch == "{" {
			d = -1
		}
		for ; n > 0; n-- {
			r += d
			for r > 0 && r < len(e.Lines)-1 && len(strings.TrimSpace(string(e.Lines[r]))) > 0 {
				r += d
			}
		}
		r = min(max(r, 0), len(e.Lines)-1)
		c = 0
	default:
		return 0, 0, false, false, false
	}
	return r, c, lines, inclusive, true
}

func (e *Editor) opMotion(ch string) {
	n := e.n()
	if e.op == "c" && (ch == "w" || ch == "W") {
		// cw changes to the end of the word, as vim does.
		l := e.line()
		if e.Col < len(l) && !unicode.IsSpace(l[e.Col]) {
			ch = map[string]string{"w": "e", "W": "E"}[ch]
			r, c := e.Row, e.Col
			for ; n > 0; n-- {
				if n == e.n() && e.atWordEnd(r, c, ch == "E") {
					continue
				}
				r, c = e.wordEnd(r, c, ch == "E")
			}
			e.operate("c", e.Row, e.Col, r, c, false)
			return
		}
	}
	r, c, lines, inclusive, ok := e.motion(ch, n)
	if !ok {
		return
	}
	if lines {
		r1, r2 := min(r, e.Row), max(r, e.Row)
		e.operate(e.op, r1, 0, r2, 0, true)
		return
	}
	r1, c1, r2, c2 := order(e.Row, e.Col, r, c)
	if !inclusive {
		// An exclusive motion that lands at the start of a later line stops at the end of the one
		// before, so dw on a line's last word leaves the next line alone.
		if r2 > r1 && (ch == "w" || ch == "W") {
			r2--
			c2 = len(e.Lines[r2])
		}
		c2--
	}
	if r2 == r1 && c2 < c1 {
		return
	}
	e.operate(e.op, r1, c1, r2, c2, false)
}

func (e *Editor) atWordEnd(r, c int, big bool) bool {
	l := e.Lines[r]
	return c < len(l) && (c+1 >= len(l) || class(l[c+1], big) != class(l[c], big))
}

func class(ch rune, big bool) int {
	switch {
	case unicode.IsSpace(ch):
		return 0
	case big:
		return 1
	case ch == '_' || unicode.IsLetter(ch) || unicode.IsDigit(ch):
		return 1
	}
	return 2
}

func (e *Editor) nextWord(r, c int, big bool) (int, int) {
	l := e.Lines[r]
	if c < len(l) {
		k := class(l[c], big)
		for c < len(l) && k != 0 && class(l[c], big) == k {
			c++
		}
	}
	for {
		for c < len(e.Lines[r]) && unicode.IsSpace(e.Lines[r][c]) {
			c++
		}
		if c < len(e.Lines[r]) {
			return r, c
		}
		if r == len(e.Lines)-1 {
			return r, max(len(e.Lines[r])-1, 0)
		}
		r, c = r+1, 0
		if len(e.Lines[r]) == 0 {
			return r, 0
		}
	}
}

func (e *Editor) prevWord(r, c int, big bool) (int, int) {
	for {
		c--
		for c >= 0 && unicode.IsSpace(e.Lines[r][c]) {
			c--
		}
		if c >= 0 {
			break
		}
		if r == 0 {
			return 0, 0
		}
		r--
		c = len(e.Lines[r])
		if c == 0 {
			return r, 0
		}
	}
	l := e.Lines[r]
	k := class(l[c], big)
	for c > 0 && class(l[c-1], big) == k {
		c--
	}
	return r, c
}

func (e *Editor) wordEnd(r, c int, big bool) (int, int) {
	for {
		c++
		for c < len(e.Lines[r]) && unicode.IsSpace(e.Lines[r][c]) {
			c++
		}
		if c < len(e.Lines[r]) {
			break
		}
		if r == len(e.Lines)-1 {
			return r, max(len(e.Lines[r])-1, 0)
		}
		r, c = r+1, -1
	}
	l := e.Lines[r]
	k := class(l[c], big)
	for c+1 < len(l) && class(l[c+1], big) == k {
		c++
	}
	return r, c
}

// object finds a text object: iw aw iW aW, quotes and brackets, on the cursor's line.
func (e *Editor) object(kind, ch string) (r1, c1, r2, c2 int, lines, ok bool) {
	l := e.line()
	if len(l) == 0 {
		return 0, 0, 0, 0, false, false
	}
	c := min(e.Col, len(l)-1)
	switch ch {
	case "w", "W":
		big := ch == "W"
		k := class(l[c], big)
		s, t := c, c
		for s > 0 && class(l[s-1], big) == k {
			s--
		}
		for t+1 < len(l) && class(l[t+1], big) == k {
			t++
		}
		if kind == "a" {
			if t+1 < len(l) && unicode.IsSpace(l[t+1]) {
				for t+1 < len(l) && unicode.IsSpace(l[t+1]) {
					t++
				}
			} else {
				for s > 0 && unicode.IsSpace(l[s-1]) {
					s--
				}
			}
		}
		return e.Row, s, e.Row, t, false, true
	case `"`, "'", "`":
		q := []rune(ch)[0]
		var at []int
		for i, x := range l {
			if x == q && (i == 0 || l[i-1] != '\\') {
				at = append(at, i)
			}
		}
		for i := 0; i+1 < len(at); i += 2 {
			if c >= at[i] && c <= at[i+1] || c < at[i] {
				if kind == "i" {
					if at[i+1]-at[i] < 2 {
						return 0, 0, 0, 0, false, false
					}
					return e.Row, at[i] + 1, e.Row, at[i+1] - 1, false, true
				}
				return e.Row, at[i], e.Row, at[i+1], false, true
			}
		}
	case "(", ")", "b", "[", "]", "{", "}", "B", "<", ">":
		pair := pairs[ch]
		open, close := pair[0], pair[1]
		sr, sc, okS := e.scan(e.Row, c, -1, open, close)
		er, ec, okE := e.scan(e.Row, c, 1, close, open)
		if !okS || !okE {
			return 0, 0, 0, 0, false, false
		}
		if kind == "a" {
			return sr, sc, er, ec, false, true
		}
		// Brackets that end and start their lines hold whole lines, and i takes those.
		if er > sr+1 && strings.TrimSpace(string(e.Lines[sr][sc+1:])) == "" && strings.TrimSpace(string(e.Lines[er][:ec])) == "" {
			return sr + 1, 0, er - 1, 0, true, true
		}
		sc++
		if sc >= len(e.Lines[sr]) && sr < er {
			sr, sc = sr+1, 0
		}
		ec--
		if ec < 0 && er > sr {
			er--
			ec = len(e.Lines[er]) - 1
		}
		if sr == er && ec < sc {
			return 0, 0, 0, 0, false, false
		}
		return sr, sc, er, ec, false, true
	}
	return 0, 0, 0, 0, false, false
}

// scan walks from (r, c) in dir to the unmatched want, skipping nested pairs.
func (e *Editor) scan(r, c, dir int, want, other rune) (int, int, bool) {
	depth := 0
	for {
		if r < 0 || r >= len(e.Lines) {
			return 0, 0, false
		}
		l := e.Lines[r]
		for ; c >= 0 && c < len(l); c += dir {
			switch l[c] {
			case want:
				if depth == 0 {
					return r, c, true
				}
				depth--
			case other:
				if !(r == e.Row && c == e.Col) {
					depth++
				}
			}
		}
		r += dir
		if r < 0 || r >= len(e.Lines) {
			return 0, 0, false
		}
		c = 0
		if dir < 0 {
			c = len(e.Lines[r]) - 1
		}
	}
}

// operate applies an operator to the range, start to end inclusive.
func (e *Editor) operate(op string, r1, c1, r2, c2 int, lines bool) {
	switch op {
	case "y":
		e.reg = e.cut(r1, c1, r2, c2, lines, false)
		e.Row, e.Col = r1, c1
		if lines {
			e.Col = min(e.Col, len(e.Lines[r1]))
		}
	case "d":
		e.save()
		e.reg = e.cut(r1, c1, r2, c2, lines, true)
	case "c":
		e.save()
		if lines {
			ind := append([]rune{}, e.Lines[r1][:indentOf(e.Lines[r1])]...)
			e.reg = e.cut(r1, 0, r2, 0, true, false)
			e.Lines = append(e.Lines[:r1], append([][]rune{ind}, e.Lines[r2+1:]...)...)
			e.Row, e.Col = r1, len(ind)
		} else {
			e.reg = e.cut(r1, c1, r2, c2, false, true)
			e.Row, e.Col = r1, c1
		}
		e.Mode = Insert
	case ">", "<":
		e.save()
		for r := r1; r <= r2; r++ {
			l := e.Lines[r]
			if op == ">" {
				if len(l) > 0 {
					e.Lines[r] = append([]rune{'\t'}, l...)
				}
			} else if len(l) > 0 && l[0] == '\t' {
				e.Lines[r] = l[1:]
			} else {
				i := 0
				for i < len(l) && i < 4 && l[i] == ' ' {
					i++
				}
				e.Lines[r] = l[i:]
			}
		}
		e.Row, e.Col = r1, indentOf(e.Lines[r1])
	}
}

// cut copies the range and, with remove, takes it out of the lines.
func (e *Editor) cut(r1, c1, r2, c2 int, lines, remove bool) register {
	if lines {
		var text [][]rune
		for r := r1; r <= r2; r++ {
			text = append(text, append([]rune{}, e.Lines[r]...))
		}
		if remove {
			e.Lines = append(e.Lines[:r1], e.Lines[r2+1:]...)
			if len(e.Lines) == 0 {
				e.Lines = [][]rune{{}}
			}
			e.Row = min(r1, len(e.Lines)-1)
			e.Col = indentOf(e.Lines[e.Row])
		}
		return register{text: text, lines: true}
	}
	c2 = min(c2, len(e.Lines[r2])-1)
	var text [][]rune
	if r1 == r2 {
		text = [][]rune{append([]rune{}, e.Lines[r1][c1:c2+1]...)}
	} else {
		text = append(text, append([]rune{}, e.Lines[r1][c1:]...))
		for r := r1 + 1; r < r2; r++ {
			text = append(text, append([]rune{}, e.Lines[r]...))
		}
		text = append(text, append([]rune{}, e.Lines[r2][:c2+1]...))
	}
	if remove {
		head := append([]rune{}, e.Lines[r1][:c1]...)
		tail := e.Lines[r2][c2+1:]
		e.Lines = append(e.Lines[:r1], append([][]rune{append(head, tail...)}, e.Lines[r2+1:]...)...)
		e.Row, e.Col = r1, c1
	}
	return register{text: text}
}

func (e *Editor) put(before bool, n int) {
	if len(e.reg.text) == 0 {
		return
	}
	e.save()
	if e.reg.lines {
		at := e.Row + 1
		if before {
			at = e.Row
		}
		var ls [][]rune
		for ; n > 0; n-- {
			for _, l := range e.reg.text {
				ls = append(ls, append([]rune{}, l...))
			}
		}
		e.Lines = insertLines(e.Lines, at, ls)
		e.Row, e.Col = at, indentOf(e.Lines[at])
		return
	}
	at := e.Col
	if !before && len(e.line()) > 0 {
		at++
	}
	var text [][]rune
	for ; n > 0; n-- {
		text = joinText(text, e.reg.text)
	}
	l := e.line()
	head, tail := append([]rune{}, l[:at]...), append([]rune{}, l[at:]...)
	if len(text) == 1 {
		e.Lines[e.Row] = append(append(head, text[0]...), tail...)
		e.Col = at + len(text[0]) - 1
		return
	}
	mid := [][]rune{append(head, text[0]...)}
	for _, t := range text[1 : len(text)-1] {
		mid = append(mid, append([]rune{}, t...))
	}
	mid = append(mid, append(append([]rune{}, text[len(text)-1]...), tail...))
	e.Lines = append(e.Lines[:e.Row], append(mid, e.Lines[e.Row+1:]...)...)
	e.Col = at
}

func joinText(a, b [][]rune) [][]rune {
	if len(a) == 0 {
		return append([][]rune{}, b...)
	}
	out := append([][]rune{}, a[:len(a)-1]...)
	out = append(out, append(append([]rune{}, a[len(a)-1]...), b[0]...))
	return append(out, b[1:]...)
}

func (e *Editor) join(n int) {
	if e.Row+1 >= len(e.Lines) {
		return
	}
	e.save()
	for ; n > 1 && e.Row+1 < len(e.Lines); n-- {
		l := []rune(strings.TrimRight(string(e.line()), " \t"))
		next := []rune(strings.TrimLeft(string(e.Lines[e.Row+1]), " \t"))
		col := len(l)
		if len(l) > 0 && len(next) > 0 && next[0] != ')' {
			l = append(l, ' ')
		}
		e.Lines[e.Row] = append(l, next...)
		e.Lines = append(e.Lines[:e.Row+1], e.Lines[e.Row+2:]...)
		e.Col = col
	}
}

func (e *Editor) repeat() {
	if len(e.last) == 0 {
		return
	}
	keys := e.last
	e.reset()
	e.recording = false
	e.replaying = true
	for _, k := range keys {
		e.Key(k)
	}
	if e.Mode == Insert {
		e.Key(Key{Name: "esc"})
	}
	e.replaying = false
}

func (e *Editor) insert(k Key) {
	switch k.Name {
	case "esc", "ctrl+c", "ctrl+[":
		e.toNormal()
	case "enter":
		l := e.line()
		ind := append([]rune{}, l[:min(indentOf(l), e.Col)]...)
		head, tail := append([]rune{}, l[:e.Col]...), append(ind, l[e.Col:]...)
		e.Lines[e.Row] = head
		e.Lines = insertLines(e.Lines, e.Row+1, [][]rune{tail})
		e.Row, e.Col = e.Row+1, len(ind)
	case "backspace", "ctrl+h":
		switch {
		case e.Col > 0:
			l := e.line()
			e.Lines[e.Row] = append(l[:e.Col-1:e.Col-1], l[e.Col:]...)
			e.Col--
		case e.Row > 0:
			prev := e.Lines[e.Row-1]
			e.Col = len(prev)
			e.Lines[e.Row-1] = append(append([]rune{}, prev...), e.line()...)
			e.Lines = append(e.Lines[:e.Row], e.Lines[e.Row+1:]...)
			e.Row--
		}
	case "delete":
		l := e.line()
		switch {
		case e.Col < len(l):
			e.Lines[e.Row] = append(l[:e.Col:e.Col], l[e.Col+1:]...)
		case e.Row+1 < len(e.Lines):
			e.Lines[e.Row] = append(l, e.Lines[e.Row+1]...)
			e.Lines = append(e.Lines[:e.Row+1], e.Lines[e.Row+2:]...)
		}
	case "ctrl+w":
		l := e.line()
		c := e.Col
		for c > 0 && unicode.IsSpace(l[c-1]) {
			c--
		}
		if c > 0 {
			k := class(l[c-1], false)
			for c > 0 && class(l[c-1], false) == k {
				c--
			}
		}
		e.Lines[e.Row] = append(l[:c:c], l[e.Col:]...)
		e.Col = c
	case "ctrl+u":
		l := e.line()
		ind := indentOf(l)
		from := 0
		if e.Col > ind {
			from = ind
		}
		e.Lines[e.Row] = append(l[:from:from], l[e.Col:]...)
		e.Col = from
	case "left":
		e.Col = max(e.Col-1, 0)
	case "right":
		e.Col = min(e.Col+1, len(e.line()))
	case "up":
		e.Row = max(e.Row-1, 0)
	case "down":
		e.Row = min(e.Row+1, len(e.Lines)-1)
	case "home":
		e.Col = 0
	case "end":
		e.Col = len(e.line())
	case "tab":
		e.type_([]rune{'\t'})
	default:
		rs := k.Runes
		if len(rs) == 0 && k.Name == "space" {
			rs = []rune{' '}
		}
		if len(rs) > 0 {
			e.type_(rs)
		}
	}
}

func (e *Editor) type_(rs []rune) {
	for _, r := range rs {
		if r == '\n' || r == '\r' {
			e.insert(Key{Name: "enter"})
			continue
		}
		l := e.line()
		nl := make([]rune, 0, len(l)+1)
		nl = append(append(append(nl, l[:e.Col]...), r), l[e.Col:]...)
		e.Lines[e.Row] = nl
		e.Col++
	}
}

func (e *Editor) visual(k Key) {
	ch := char(k)
	if e.await != "" {
		a := e.await
		e.await = ""
		switch a {
		case "g":
			if ch == "g" {
				e.Row, e.Col = 0, 0
			}
		case "obji", "obja":
			if r1, c1, r2, c2, lines, ok := e.object(a[3:], ch); ok {
				e.vr, e.vc, e.Row, e.Col = r1, c1, r2, c2
				if lines {
					e.Mode, e.Col = VisualLine, 0
				}
			}
		case "vr":
			rs := []rune(ch)
			if len(rs) != 1 || ch == "esc" {
				break
			}
			r1, c1, r2, c2 := order(e.vr, e.vc, e.Row, e.Col)
			e.save()
			for r := r1; r <= r2; r++ {
				l := e.Lines[r]
				from, to := 0, len(l)-1
				if e.Mode == Visual {
					if r == r1 {
						from = c1
					}
					if r == r2 {
						to = min(c2, len(l)-1)
					}
				}
				for i := from; i <= to; i++ {
					l[i] = rs[0]
				}
			}
			e.Row, e.Col = r1, c1
			e.Mode = Normal
		default:
			if r, c, ok := e.find(a, []rune(ch)[0], max(e.count, 1)); ok && ch != "esc" {
				e.Row, e.Col = r, c
			}
		}
		e.count = 0
		return
	}
	if len(ch) == 1 && ch[0] >= '0' && ch[0] <= '9' && (ch != "0" || e.count > 0) {
		e.count = e.count*10 + int(ch[0]-'0')
		return
	}
	r1, c1, r2, c2 := order(e.vr, e.vc, e.Row, e.Col)
	lines := e.Mode == VisualLine
	done := func() {
		e.Mode = Normal
		e.reset()
	}
	switch ch {
	case "esc", "ctrl+c":
		done()
	case "v", "V":
		m := Visual
		if ch == "V" {
			m = VisualLine
		}
		if e.Mode == m {
			done()
		} else {
			e.Mode = m
		}
	case "o":
		e.vr, e.vc, e.Row, e.Col = e.Row, e.Col, e.vr, e.vc
	case "d", "x", "delete", "y", "c", "s", ">", "<", "D", "X", "Y", "C", "S", "J", "~", "u", "U", "r":
		op := map[string]string{"x": "d", "delete": "d", "s": "c", "D": "d", "X": "d", "Y": "y", "C": "c", "S": "c"}[ch]
		if op == "" {
			op = ch
		}
		if ch == "D" || ch == "X" || ch == "Y" || ch == "C" || ch == "S" {
			lines = true
		}
		switch op {
		case "J":
			e.Row = r1
			done()
			e.join(max(r2-r1+1, 2))
			return
		case "~", "u", "U":
			e.save()
			for r := r1; r <= r2; r++ {
				l := e.Lines[r]
				from, to := 0, len(l)-1
				if !lines {
					if r == r1 {
						from = c1
					}
					if r == r2 {
						to = min(c2, len(l)-1)
					}
				}
				for i := from; i <= to && i < len(l); i++ {
					switch op {
					case "~":
						l[i] = flip(l[i])
					case "u":
						l[i] = unicode.ToLower(l[i])
					default:
						l[i] = unicode.ToUpper(l[i])
					}
				}
			}
			e.Row, e.Col = r1, c1
			done()
			return
		case "r":
			e.await = "vr"
			return
		}
		done()
		if lines {
			e.operate(op, r1, 0, r2, 0, true)
		} else {
			e.operate(op, r1, c1, r2, c2, false)
		}
	case "i", "a":
		e.await = "obj" + ch
	case "g", "f", "F", "t", "T":
		e.await = ch
	case ":":
		done()
		e.Mode, e.Cmd = Command, ""
	case "p", "P":
		reg := e.reg
		done()
		if lines {
			e.operate("d", r1, 0, r2, 0, true)
		} else {
			e.operate("d", r1, c1, r2, c2, false)
		}
		e.reg = reg
		e.put(!lines || r1 < len(e.Lines), 1)
	default:
		if r, c, _, _, ok := e.motion(ch, max(e.count, 1)); ok {
			e.moveTo(r, c, ch)
		}
	}
	e.count = 0
}

func (e *Editor) command(k Key) Result {
	switch k.Name {
	case "esc", "ctrl+c":
		e.Mode = Normal
		return None
	case "backspace":
		if e.Cmd == "" {
			e.Mode = Normal
			return None
		}
		r := []rune(e.Cmd)
		e.Cmd = string(r[:len(r)-1])
		return None
	case "enter":
		cmd := strings.TrimSpace(e.Cmd)
		e.Mode, e.Cmd, e.Ex, e.Msg = Normal, "", cmd, ""
		name, _, _ := strings.Cut(cmd, " ")
		switch strings.TrimSuffix(name, "!") {
		case "w", "wq", "x", "up", "update":
			return Run
		case "q", "cq", "qa":
			return Cancel
		case "":
			return None
		}
		if n, err := strconv.Atoi(cmd); err == nil {
			e.Row = min(max(n-e.First, 0), len(e.Lines)-1)
			e.Col = indentOf(e.line())
			return None
		}
		return Ex
	}
	if len(k.Runes) > 0 {
		e.Cmd += string(k.Runes)
	} else if k.Name == "space" {
		e.Cmd += " "
	}
	return None
}

var pairs = map[string][2]rune{"(": {'(', ')'}, ")": {'(', ')'}, "b": {'(', ')'}, "[": {'[', ']'}, "]": {'[', ']'},
	"{": {'{', '}'}, "}": {'{', '}'}, "B": {'{', '}'}, "<": {'<', '>'}, ">": {'<', '>'}}

// Grow takes lines in above and below the ones being edited, keeping the cursor on its line.
func (e *Editor) Grow(above, below []string) {
	var top [][]rune
	for _, l := range above {
		top = append(top, []rune(l))
	}
	e.Lines = append(top, e.Lines...)
	for _, l := range below {
		e.Lines = append(e.Lines, []rune(l))
	}
	e.Row += len(top)
	e.First -= len(top)
}

func order(r1, c1, r2, c2 int) (int, int, int, int) {
	if r2 < r1 || (r2 == r1 && c2 < c1) {
		return r2, c2, r1, c1
	}
	return r1, c1, r2, c2
}

func indentOf(l []rune) int {
	i := 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		i++
	}
	return i
}

func insertLines(ls [][]rune, at int, add [][]rune) [][]rune {
	out := make([][]rune, 0, len(ls)+len(add))
	out = append(out, ls[:at]...)
	out = append(out, add...)
	return append(out, ls[at:]...)
}

func flip(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.ToLower(r)
	}
	return unicode.ToUpper(r)
}
