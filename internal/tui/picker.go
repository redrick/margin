package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/redrick/margin/internal/render"
	"github.com/redrick/margin/internal/text"
	"github.com/redrick/margin/internal/workspace"
)

// The picker chooses what to review, like margin-gui's sidebar: the repositories and worktrees in
// the reader's folders on the left, what the selected one offers in the middle, details on the
// right. It ends with a Choice for the command line to start.

// Choice is what the reader picked: margin's arguments in a worktree, or a review file to go on with.
type Choice struct {
	Dir    string
	Args   []string
	Review string
}

type pickItem struct {
	head   bool
	text   string
	meta   string
	dir    string
	folder string
	args   []string
	review string
	info   *workspace.ReviewInfo
	branch *workspace.Branch
	commit *workspace.Commit
	pr     *workspace.PullRequest
	// listPRs asks gh for the pull requests when chosen.
	listPRs bool
}

func (it pickItem) key() string { return it.dir + "\x00" + it.text + "\x00" + it.review }

type pickCol struct {
	all      []pickItem
	vis      []pickItem
	cur, top int
	filter   string
}

func (c *pickCol) set(items []pickItem) {
	keep := ""
	if it, ok := c.item(); ok {
		keep = it.key()
	}
	c.all = items
	c.refilter(keep)
}

func (c *pickCol) refilter(keep string) {
	f := strings.ToLower(c.filter)
	c.vis = nil
	if f == "" {
		c.vis = append(c.vis, c.all...)
	}
	var head *pickItem
	for _, it := range c.all {
		if f == "" {
			break
		}
		if it.head {
			h := it
			head = &h
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(it.text+" "+it.meta), f) {
			continue
		}
		if head != nil {
			c.vis = append(c.vis, *head)
			head = nil
		}
		c.vis = append(c.vis, it)
	}
	c.cur = -1
	for i, it := range c.vis {
		if keep != "" && !it.head && it.key() == keep {
			c.cur = i
		}
	}
	if c.cur < 0 {
		c.cur = 0
		c.move(0)
	}
}

func (c *pickCol) item() (pickItem, bool) {
	if c.cur < 0 || c.cur >= len(c.vis) || c.vis[c.cur].head {
		return pickItem{}, false
	}
	return c.vis[c.cur], true
}

// move goes delta selectable items down, or to the nearest one with 0.
func (c *pickCol) move(delta int) {
	if len(c.vis) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	i := c.cur
	if delta == 0 {
		for j := i; j >= 0 && j < len(c.vis); j++ {
			if !c.vis[j].head {
				c.cur = j
				return
			}
		}
		return
	}
	for ; delta > 0; delta-- {
		j := i + step
		for j >= 0 && j < len(c.vis) && c.vis[j].head {
			j += step
		}
		if j < 0 || j >= len(c.vis) {
			break
		}
		i = j
	}
	c.cur = i
}

func (c *pickCol) follow(h int) {
	margin := min(2, h/4)
	if c.cur < c.top+margin {
		c.top = c.cur - margin
	}
	if c.cur > c.top+h-1-margin {
		c.top = c.cur - h + 1 + margin
	}
	c.top = max(min(c.top, len(c.vis)-h), 0)
}

type Picker struct {
	here  string
	paint *render.Painter
	input textinput.Model
	// typing is what the input is for: "", "filter" or "add".
	typing string

	width, height int
	loaded        bool
	folders       []workspace.Folder
	recent        []workspace.ReviewInfo
	dirty         map[string]int
	targets       map[string]*workspace.Targets
	targetErr     map[string]error
	prs           map[string][]workspace.PullRequest
	prErr         map[string]error
	busy          string

	cols  [2]pickCol
	focus int

	status    string
	statusErr bool
	choice    *Choice

	lastClick time.Time
	lastAt    [2]int
	// lastLeft is the left item the middle column was filled for.
	lastLeft string
}

// NewPicker starts the picker; here is the directory it was run in, and its repository is listed
// first even when it is in none of the reader's folders.
func NewPicker(here string) *Picker {
	in := textinput.New()
	in.CharLimit = 1000
	in.Cursor.SetMode(cursor.CursorStatic)
	return &Picker{here: here, paint: render.NewPainter(), input: in, dirty: map[string]int{},
		targets: map[string]*workspace.Targets{}, targetErr: map[string]error{},
		prs: map[string][]workspace.PullRequest{}, prErr: map[string]error{}}
}

// Choice is what the reader picked, or nil when they left without picking.
func (p *Picker) Choice() *Choice { return p.choice }

type foldersMsg struct {
	folders []workspace.Folder
	recent  []workspace.ReviewInfo
	err     error
}

type dirtyMsg map[string]int

type targetsMsg struct {
	dir string
	t   *workspace.Targets
	err error
}

type prsMsg struct {
	dir string
	prs []workspace.PullRequest
	err error
}

type fetchedMsg struct {
	dir string
	pr  workspace.PullRequest
	err error
}

func loadFolders() tea.Msg {
	return foldersMsg{folders: workspace.Folders(), recent: workspace.RecentReviews()}
}

func (p *Picker) Init() tea.Cmd { return loadFolders }

func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
	case foldersMsg:
		p.loaded = true
		if msg.err != nil {
			p.setStatus(true, "%v", msg.err)
		} else if msg.folders != nil {
			p.folders = msg.folders
		}
		p.recent = msg.recent
		p.cols[0].set(p.leftItems())
		return p, tea.Batch(p.selected(), p.loadDirty())
	case dirtyMsg:
		for k, v := range msg {
			p.dirty[k] = v
		}
		p.cols[0].set(p.leftItems())
	case targetsMsg:
		if msg.err != nil {
			p.targetErr[msg.dir] = msg.err
		} else {
			p.targets[msg.dir] = msg.t
		}
		p.fill()
	case prsMsg:
		p.busy = ""
		if msg.err != nil {
			p.prErr[msg.dir] = msg.err
		} else {
			p.prs[msg.dir] = msg.prs
		}
		p.fill()
	case fetchedMsg:
		p.busy = ""
		if msg.err != nil {
			p.setStatus(true, "fetching #%d: %v", msg.pr.Number, msg.err)
			return p, nil
		}
		p.choice = &Choice{Dir: msg.dir, Args: []string{"--pr", strconv.Itoa(msg.pr.Number)}}
		return p, tea.Quit
	case tea.MouseMsg:
		return p, p.mouse(msg)
	case tea.KeyMsg:
		return p, p.key(msg)
	}
	return p, nil
}

func (p *Picker) setStatus(isErr bool, format string, args ...any) {
	p.status, p.statusErr = fmt.Sprintf(format, args...), isErr
}

// leftItems lists the repository here, the recent reviews, then each folder's repositories and
// their worktrees.
func (p *Picker) leftItems() []pickItem {
	var out []pickItem
	listed := map[string]bool{}
	for _, f := range p.folders {
		for _, r := range f.Repos {
			listed[r.Path] = true
		}
	}
	repo := func(r workspace.Repo, folder string) {
		for _, wt := range r.Worktrees {
			it := pickItem{dir: wt.Path, folder: folder}
			if wt.Main {
				it.text = r.Name
			} else {
				it.text = "  ↳ " + filepath.Base(wt.Path)
			}
			it.meta = wt.Branch
			if wt.Detached || wt.Branch == "" {
				it.meta = wt.Head
			}
			if n := p.dirty[wt.Path]; n > 0 {
				it.meta += fmt.Sprintf(" · %d changed", n)
			}
			it.text, it.meta = text.Line(it.text), text.Line(it.meta)
			out = append(out, it)
		}
	}
	if p.here != "" {
		if r, ok := workspace.RepoAt(p.here); ok && !listed[r.Path] {
			out = append(out, pickItem{head: true, text: "here"})
			repo(r, "")
		}
	}
	if len(p.recent) > 0 {
		out = append(out, pickItem{head: true, text: "recent reviews"})
		for i := range p.recent {
			r := &p.recent[i]
			out = append(out, pickItem{text: text.Line(r.Title), meta: fmt.Sprintf("%d/%d · %s", r.Judged, r.Stops, r.Updated),
				review: r.Path, dir: r.Repo, info: r})
		}
	}
	for _, f := range p.folders {
		out = append(out, pickItem{head: true, text: text.Line(tildePath(f.Path))})
		if f.Err != "" {
			out = append(out, pickItem{head: true, text: "  " + text.Line(f.Err)})
		}
		for _, r := range f.Repos {
			repo(r, f.Path)
		}
	}
	return out
}

func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	return strconv.Itoa(n) + " files"
}

// tailFit keeps the end of s, which tells paths apart, when s is wider than w.
func tailFit(s string, w int) string {
	if runewidth.StringWidth(s) <= w || w < 2 {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && runewidth.StringWidth(string(r)) > w-1 {
		r = r[1:]
	}
	return "…" + string(r)
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + p[len(home):]
	}
	return p
}

func (p *Picker) loadDirty() tea.Cmd {
	var dirs []string
	for _, it := range p.cols[0].all {
		if it.dir != "" && it.review == "" {
			dirs = append(dirs, it.dir)
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	return func() tea.Msg { return dirtyMsg(workspace.Status(dirs)) }
}

// selected refills the middle column for the item selected on the left, loading its targets.
func (p *Picker) selected() tea.Cmd {
	p.cols[1].filter = ""
	p.cols[1].cur, p.cols[1].top = 0, 0
	p.fill()
	it, ok := p.cols[0].item()
	if !ok || it.review != "" {
		return nil
	}
	if _, ok := p.targets[it.dir]; ok {
		return nil
	}
	dir := it.dir
	return func() tea.Msg {
		t, err := workspace.TargetsIn(dir)
		return targetsMsg{dir: dir, t: t, err: err}
	}
}

func (p *Picker) fill() {
	p.cols[1].set(p.middleItems())
}

func (p *Picker) middleItems() []pickItem {
	left, ok := p.cols[0].item()
	if !ok {
		return nil
	}
	if left.review != "" {
		return []pickItem{{head: true, text: "review"}, {text: "go on with this review", review: left.review, dir: left.dir, info: left.info}}
	}
	dir := left.dir
	if err := p.targetErr[dir]; err != nil {
		return []pickItem{{head: true, text: text.Line(err.Error())}}
	}
	t := p.targets[dir]
	if t == nil {
		return []pickItem{{head: true, text: "reading the repository…"}}
	}
	var out []pickItem
	out = append(out, pickItem{head: true, text: "changes"})
	switch {
	case t.Dirty == 0:
		out = append(out, pickItem{head: true, text: "  nothing uncommitted"})
	default:
		it := pickItem{text: "uncommitted changes · " + files(t.Dirty), dir: dir, info: t.Changes}
		out = append(out, withReview(it, t.Changes))
		if t.Staged > 0 {
			out = append(out, withReview(pickItem{text: "staged changes · " + files(t.Staged), dir: dir, args: []string{"--staged"}}, t.StagedReview))
		}
	}
	if len(t.Reviews) > 0 {
		out = append(out, pickItem{head: true, text: "reviews kept"})
		for i := range t.Reviews {
			r := &t.Reviews[i]
			out = append(out, pickItem{text: text.Line(r.Title), meta: fmt.Sprintf("%d/%d · %s", r.Judged, r.Stops, r.Updated), dir: dir, review: r.Path, info: r})
		}
	}
	if t.GH {
		out = append(out, pickItem{head: true, text: "pull requests"})
		switch prs, err := p.prs[dir], p.prErr[dir]; {
		case err != nil:
			out = append(out, pickItem{head: true, text: "  " + text.Line(err.Error())})
		case prs == nil && p.busy == "prs":
			out = append(out, pickItem{head: true, text: "  asking gh…"})
		case prs == nil:
			out = append(out, pickItem{text: "list the open pull requests", meta: "gh, read-only", dir: dir, listPRs: true})
		case len(prs) == 0:
			out = append(out, pickItem{head: true, text: "  no open pull requests"})
		}
		for i := range p.prs[dir] {
			pr := &p.prs[dir][i]
			meta := pr.Author + " · " + pr.Updated
			if !pr.Fetched {
				meta = "not fetched · " + meta
			}
			it := pickItem{text: text.Line(fmt.Sprintf("#%d %s", pr.Number, pr.Title)), meta: text.Line(meta), dir: dir,
				args: []string{"--pr", strconv.Itoa(pr.Number)}, pr: pr, info: pr.Review}
			out = append(out, it)
		}
	}
	if len(t.Branches) > 0 {
		out = append(out, pickItem{head: true, text: "branches"})
		for i := range t.Branches {
			b := &t.Branches[i]
			if b.Current {
				continue
			}
			meta := b.When
			if b.Ahead > 0 || b.Behind > 0 {
				meta = fmt.Sprintf("+%d −%d · %s", b.Ahead, b.Behind, b.When)
			}
			out = append(out, withReview(pickItem{text: text.Line(b.Name), meta: text.Line(meta), dir: dir, args: []string{b.Name}, branch: b}, b.Review))
		}
	}
	if len(t.Commits) > 0 {
		out = append(out, pickItem{head: true, text: "commits"})
		for i := range t.Commits {
			c := &t.Commits[i]
			out = append(out, pickItem{text: text.Line(c.Short + " " + c.Subject), meta: text.Line(c.When), dir: dir, args: []string{c.SHA}, commit: c})
		}
	}
	return out
}

// withReview marks an item that already has a review, which picking it goes on with.
func withReview(it pickItem, r *workspace.ReviewInfo) pickItem {
	if r != nil {
		it.info = r
		it.meta = strings.TrimPrefix(it.meta+fmt.Sprintf(" · ● %d/%d", r.Judged, r.Stops), " · ")
	}
	return it
}

func (p *Picker) bodyHeight() int { return max(p.height-2, 1) }

func (p *Picker) key(msg tea.KeyMsg) tea.Cmd {
	if p.typing != "" {
		return p.typed(msg)
	}
	// Keys typed fast, or sent by tmux, can come as one message.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
		var cmds []tea.Cmd
		for _, r := range msg.Runes {
			cmds = append(cmds, p.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}))
			if p.typing != "" || p.choice != nil {
				break
			}
		}
		return tea.Batch(cmds...)
	}
	c := &p.cols[p.focus]
	h := p.bodyHeight()
	p.status = ""
	switch msg.String() {
	case "ctrl+c", "q":
		return tea.Quit
	case "esc":
		if c.filter != "" {
			c.filter = ""
			c.refilter("")
			if p.focus == 0 {
				return p.leftMoved()
			}
			return nil
		}
		if p.focus == 1 {
			p.focus = 0
			return nil
		}
		return tea.Quit
	case "j", "down":
		c.move(1)
	case "k", "up":
		c.move(-1)
	case "ctrl+d", "pgdown":
		c.move(h / 2)
	case "ctrl+u", "pgup":
		c.move(-h / 2)
	case "g", "home":
		c.cur = 0
		c.move(0)
	case "G", "end":
		c.move(len(c.vis))
	case "h", "left", "shift+tab":
		p.focus = 0
		return nil
	case "l", "right", "tab":
		if _, ok := p.cols[1].item(); ok {
			p.focus = 1
		}
		return nil
	case "enter":
		return p.choose(false)
	case "F":
		return p.choose(true)
	case "f":
		return p.fetch()
	case "/":
		p.typing = "filter"
		p.input.Prompt = " filter › "
		p.input.Placeholder = "part of a name, branch, subject or author"
		p.input.SetValue(c.filter)
		return p.input.Focus()
	case "a":
		p.typing = "add"
		p.input.Prompt = " add folder › "
		p.input.Placeholder = "a repository, or a folder of repositories (two levels deep)"
		p.input.SetValue(tildePath(p.here) + string(filepath.Separator))
		p.input.CursorEnd()
		return p.input.Focus()
	case "X":
		it, ok := p.cols[0].item()
		if !ok || it.folder == "" {
			p.setStatus(false, "X takes a folder off the list; the repository under the cursor is not from one")
			return nil
		}
		fs, err := workspace.RemoveFolder(it.folder)
		if err != nil {
			p.setStatus(true, "%v", err)
			return nil
		}
		p.folders = fs
		p.cols[0].set(p.leftItems())
		p.setStatus(false, "took %s off the list; nothing on disk changed", tildePath(it.folder))
		return p.selected()
	case "r", "ctrl+r":
		p.targets, p.targetErr = map[string]*workspace.Targets{}, map[string]error{}
		p.prs, p.prErr = map[string][]workspace.PullRequest{}, map[string]error{}
		return loadFolders
	}
	c.follow(h)
	if p.focus == 0 {
		return p.leftMoved()
	}
	return nil
}

func (p *Picker) leftMoved() tea.Cmd {
	it, _ := p.cols[0].item()
	if it.key() == p.lastLeft && len(p.cols[1].all) > 0 {
		return nil
	}
	p.lastLeft = it.key()
	return p.selected()
}

func (p *Picker) typed(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if p.typing == "filter" {
			p.cols[p.focus].filter = ""
			p.cols[p.focus].refilter("")
		}
		p.typing = ""
		p.input.Blur()
		if p.focus == 0 {
			return p.leftMoved()
		}
		return nil
	case "enter":
		typing := p.typing
		p.typing = ""
		p.input.Blur()
		if typing == "add" {
			return p.addFolder(p.input.Value())
		}
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.typing == "filter" {
		c := &p.cols[p.focus]
		c.filter = p.input.Value()
		c.refilter("")
		c.follow(p.bodyHeight())
		if p.focus == 0 {
			return tea.Batch(cmd, p.leftMoved())
		}
	}
	return cmd
}

func (p *Picker) addFolder(path string) tea.Cmd {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + path[1:]
		}
	}
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		p.setStatus(true, "%v", err)
		return nil
	}
	fs, err := workspace.AddFolder(abs)
	if err != nil {
		p.setStatus(true, "%v", err)
		return nil
	}
	p.folders = fs
	p.cols[0].set(p.leftItems())
	p.setStatus(false, "added %s; margin-gui lists it too", tildePath(abs))
	return tea.Batch(p.selected(), p.loadDirty())
}

// choose starts what is selected: the middle column's item, or on the left a recent review, or
// moves to the middle column from a worktree. fresh starts the review over.
func (p *Picker) choose(fresh bool) tea.Cmd {
	if p.focus == 0 {
		it, ok := p.cols[0].item()
		if !ok {
			return nil
		}
		if it.review == "" {
			if _, ok := p.cols[1].item(); ok {
				p.focus = 1
			}
			return nil
		}
		return p.pick(it, fresh)
	}
	it, ok := p.cols[1].item()
	if !ok {
		return nil
	}
	return p.pick(it, fresh)
}

func (p *Picker) pick(it pickItem, fresh bool) tea.Cmd {
	switch {
	case it.listPRs:
		p.busy = "prs"
		p.fill()
		dir := it.dir
		return func() tea.Msg {
			prs, err := workspace.PullRequests(dir)
			if prs == nil && err == nil {
				prs = []workspace.PullRequest{}
			}
			return prsMsg{dir: dir, prs: prs, err: err}
		}
	case it.pr != nil && !it.pr.Fetched:
		p.setStatus(false, "#%d is not fetched yet: f fetches its head (git fetch origin pull/%d/head) and starts the review", it.pr.Number, it.pr.Number)
		return nil
	case it.review != "":
		if fresh {
			p.setStatus(false, "F starts a target over; this is a review file, pick its branch or commit instead")
			return nil
		}
		p.choice = &Choice{Dir: it.dir, Review: it.review}
	default:
		args := append([]string(nil), it.args...)
		if fresh {
			args = append(args, "--fresh")
		}
		p.choice = &Choice{Dir: it.dir, Args: args}
	}
	return tea.Quit
}

func (p *Picker) fetch() tea.Cmd {
	it, ok := p.cols[p.focus].item()
	if !ok || it.pr == nil {
		p.setStatus(false, "f fetches a pull request; pick one in the middle column")
		return nil
	}
	if it.pr.Fetched {
		return p.pick(it, false)
	}
	p.busy = "fetch"
	p.setStatus(false, "fetching #%d…", it.pr.Number)
	dir, pr := it.dir, *it.pr
	return func() tea.Msg { return fetchedMsg{dir: dir, pr: pr, err: workspace.FetchPR(dir, pr.Number)} }
}

// columns are where each column starts and how wide it is; the details column goes on narrow screens.
func (p *Picker) columns() (x [3]int, w [3]int) {
	w[0] = max(min(p.width*3/10, 48), 24)
	rest := p.width - w[0] - 1
	if p.width >= 100 {
		w[1] = rest * 6 / 10
		w[2] = rest - w[1] - 1
	} else {
		w[1] = rest
	}
	x[1] = w[0] + 1
	x[2] = x[1] + w[1] + 1
	return x, w
}

const doubleClick = 400 * time.Millisecond

func (p *Picker) mouse(msg tea.MouseMsg) tea.Cmd {
	if p.typing != "" {
		return nil
	}
	x, w := p.columns()
	col := 0
	if msg.X >= x[1] {
		col = 1
	}
	if msg.X >= x[2] && w[2] > 0 {
		return nil
	}
	c := &p.cols[col]
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp:
		c.move(-3)
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown:
		c.move(3)
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		i := c.top + msg.Y - 1
		if msg.Y < 1 || msg.Y > p.bodyHeight() || i >= len(c.vis) || c.vis[i].head {
			return nil
		}
		double := time.Since(p.lastClick) < doubleClick && p.lastAt == [2]int{col, i}
		p.lastClick, p.lastAt = time.Now(), [2]int{col, i}
		p.focus = col
		c.cur = i
		if double {
			return p.choose(false)
		}
	default:
		return nil
	}
	c.follow(p.bodyHeight())
	if col == 0 {
		return p.leftMoved()
	}
	return nil
}

func (p *Picker) View() string {
	if p.width == 0 {
		return ""
	}
	h := p.bodyHeight()
	_, w := p.columns()
	left := p.colLines(0, w[0], h)
	mid := p.colLines(1, w[1], h)
	var right []string
	if w[2] > 0 {
		right = p.details(w[2], h)
	}
	div := p.paint.Text("│", 1, colBar, "", false, false)
	lines := make([]string, 0, h+2)
	lines = append(lines, p.header())
	for i := range h {
		l := left[i] + div + mid[i]
		if right != nil {
			l += div + right[i]
		}
		lines = append(lines, l)
	}
	lines = append(lines, p.footer())
	return strings.Join(lines, "\n")
}

func (p *Picker) header() string {
	segs := []seg{{text: " margin › what to review  ", fg: colTitle, bg: colBar, bold: true}}
	if it, ok := p.cols[0].item(); ok && it.review == "" {
		segs = append(segs, seg{text: tildePath(it.dir), fg: colText, bg: colBar})
	}
	return paintBar(p.paint, segs, p.width, "")
}

func (p *Picker) footer() string {
	if p.typing != "" {
		return p.input.View()
	}
	if p.status != "" {
		fg := colDim
		if p.statusErr {
			fg = colProblem
		}
		return paintBar(p.paint, []seg{{text: " " + p.status, fg: fg, bg: colBar}}, p.width, "")
	}
	keys := " enter review · F start over · f fetch PR · / filter · a add folder · X remove · r refresh · q quit"
	return paintBar(p.paint, []seg{{text: keys, fg: colDim, bg: colBar}}, p.width, "")
}

func (p *Picker) colLines(ci, w, h int) []string {
	c := &p.cols[ci]
	c.follow(h)
	out := make([]string, h)
	empty := ""
	if ci == 0 && p.loaded && len(c.vis) == 0 {
		empty = " no repositories yet: a adds one, or a folder of them"
		if c.filter != "" {
			empty = " nothing matches " + c.filter
		}
	}
	for i := range out {
		idx := c.top + i
		switch {
		case idx < len(c.vis):
			out[i] = p.itemLine(c.vis[idx], w, idx == c.cur, ci == p.focus)
		case i == 0 && empty != "":
			out[i] = p.paint.Text(empty, w, colDim, "", false, true)
		default:
			out[i] = strings.Repeat(" ", w)
		}
	}
	if c.filter != "" && h > 0 {
		out[h-1] = p.paint.Text(" /"+c.filter+" · esc clears", w, colInk, colMatch, true, false)
	}
	return out
}

func (p *Picker) itemLine(it pickItem, w int, cur, focused bool) string {
	if it.head {
		fg := colHeading
		if strings.HasPrefix(it.text, "  ") {
			fg = colDim
		}
		return p.paint.Text(" "+tailFit(it.text, w-1), w, fg, "", !strings.HasPrefix(it.text, "  "), false)
	}
	bg, fg, metaFg := "", colText, colDim
	if cur {
		bg = tintNoteSel
		if focused {
			bg, fg, metaFg = colCursorNum, colInk, colInk
		}
	}
	meta := it.meta
	mw := runewidth.StringWidth(meta)
	if mw > w/2 {
		meta, mw = "", 0
	}
	tw := w - mw - 1
	if mw == 0 {
		tw = w
	}
	s := p.paint.Text("  "+it.text, tw, fg, bg, cur && focused, false)
	if mw > 0 {
		s += p.paint.Text(" "+meta, mw+1, metaFg, bg, false, false)
	}
	return s
}

// details describes the item selected in the focused column, or the middle one's.
func (p *Picker) details(w, h int) []string {
	it, ok := p.cols[1].item()
	if p.focus == 0 || !ok {
		it, ok = p.cols[0].item()
	}
	var ls []struct {
		t  string
		fg string
	}
	add := func(fg, format string, args ...any) {
		for _, l := range wrap(text.Line(fmt.Sprintf(format, args...)), max(w-2, 10)) {
			ls = append(ls, struct {
				t  string
				fg string
			}{l, fg})
		}
	}
	if ok {
		switch {
		case it.pr != nil:
			pr := it.pr
			add(colTitle, "#%d %s", pr.Number, pr.Title)
			add(colDim, "by %s · %s", pr.Author, pr.Updated)
			add(colText, "%s → %s", pr.Branch, pr.Base)
			if pr.Draft {
				add(colChanged, "draft")
			}
			if !pr.Fetched {
				add(colChanged, "")
				add(colChanged, "its head is not fetched: f runs git fetch origin pull/%d/head and starts the review", pr.Number)
			}
		case it.branch != nil:
			b := it.branch
			add(colTitle, "%s", b.Name)
			add(colText, "%s", b.Subject)
			add(colDim, "%s", b.When)
			if t := p.targets[it.dir]; t != nil && t.Base != "" && (b.Ahead > 0 || b.Behind > 0) {
				add(colDim, "%d ahead of %s, %d behind", b.Ahead, t.Base, b.Behind)
			}
			if b.Worktree != "" {
				add(colDim, "checked out in %s", tildePath(b.Worktree))
			}
		case it.commit != nil:
			c := it.commit
			add(colTitle, "%s", c.Subject)
			add(colDim, "%s · %s · %s", c.Short, c.Author, c.When)
		case it.listPRs:
			add(colText, "asks gh for the open pull requests of this repository; nothing is posted")
		case it.review == "" && p.focus == 0:
			add(colTitle, "%s", strings.TrimSpace(strings.TrimPrefix(it.text, "  ↳ ")))
			add(colDim, "%s", tildePath(it.dir))
			add(colText, "%s", it.meta)
		}
		if r := it.info; r != nil {
			if len(ls) > 0 {
				add(colText, "")
			}
			add(colHeading, "review: %s", r.Title)
			if r.Kicker != "" {
				add(colDim, "%s", r.Kicker)
			}
			add(colText, "%d of %d stops judged · %d notes", r.Judged, r.Stops, r.Notes)
			if r.Drafts > 0 {
				add(colComment, "%d comments not sent yet", r.Drafts)
			}
			add(colDim, "updated %s", r.Updated)
			if it.review != "" {
				add(colDim, "%s", tildePath(r.Path))
			}
		}
		if p.focus == 1 || it.review != "" {
			add(colText, "")
			switch {
			case it.listPRs:
				add(colDim, "enter lists them")
			case it.pr != nil && !it.pr.Fetched:
				add(colDim, "f fetch and review")
			case it.info != nil:
				add(colDim, "enter goes on with the review · F starts it over")
			default:
				add(colDim, "enter starts the review, with the agent beside it")
			}
		}
	}
	out := make([]string, h)
	for i := range out {
		if i < len(ls) {
			out[i] = p.paint.Text(" "+ls[i].t, w, ls[i].fg, "", ls[i].fg == colTitle, false)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return out
}
