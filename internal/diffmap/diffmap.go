package diffmap

type Kind uint8

const (
	Same Kind = iota
	Added
	Changed
	Removed
)

// Map describes the current lines of a file relative to its base.
// Ghosts[i] holds base lines removed just before current line i; i may equal len(Kinds).
type Map struct {
	Kinds  []Kind
	Ghosts map[int][]string
	// Pairs maps a changed current line to the base line it replaced, for word-level highlighting.
	Pairs   map[int]string
	Added   int
	Changed int
	Removed int
}

func Compute(base, current []string) Map {
	m := Map{Kinds: make([]Kind, len(current)), Ghosts: map[int][]string{}, Pairs: map[int]string{}}
	ops := script(base, current)
	ia, ib := 0, 0
	for i := 0; i < len(ops); {
		if ops[i] == opEq {
			ia, ib, i = ia+1, ib+1, i+1
			continue
		}
		start := ib
		var dels []string
		for ; i < len(ops) && ops[i] != opEq; i++ {
			if ops[i] == opDel {
				dels = append(dels, base[ia])
				ia++
			} else {
				ib++
			}
		}
		kind := Added
		if len(dels) > 0 {
			kind = Changed
			m.Changed += ib - start
			m.Ghosts[start] = dels
			for k := 0; k < len(dels) && start+k < ib; k++ {
				m.Pairs[start+k] = dels[k]
			}
		} else {
			m.Added += ib - start
		}
		m.Removed += len(dels)
		for j := start; j < ib; j++ {
			m.Kinds[j] = kind
		}
	}
	return m
}

func AllAdded(n int) Map {
	m := Map{Kinds: make([]Kind, n), Ghosts: map[int][]string{}, Pairs: map[int]string{}, Added: n}
	for i := range m.Kinds {
		m.Kinds[i] = Added
	}
	return m
}

type op uint8

const (
	opEq op = iota
	opDel
	opIns
)

func script(a, b []string) []op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	mid := myers(a[pre:len(a)-suf], b[pre:len(b)-suf])
	ops := make([]op, 0, pre+len(mid)+suf)
	for range pre {
		ops = append(ops, opEq)
	}
	ops = append(ops, mid...)
	for range suf {
		ops = append(ops, opEq)
	}
	return slide(ops, a, b)
}

// slide moves pure deletion or insertion runs down past equal lines, like git does, so a removed
// function after `}` is shown after that brace rather than before it.
func slide(ops []op, a, b []string) []op {
	ia, ib := 0, 0
	for i := 0; i < len(ops); {
		if ops[i] == opEq {
			ia, ib, i = ia+1, ib+1, i+1
			continue
		}
		j, da, db := i, 0, 0
		for ; j < len(ops) && ops[j] != opEq; j++ {
			if ops[j] == opDel {
				da++
			} else {
				db++
			}
		}
		switch {
		case db == 0:
			for j < len(ops) && ops[j] == opEq && a[ia] == a[ia+da] {
				ops[i], ops[j] = opEq, opDel
				i, j, ia, ib = i+1, j+1, ia+1, ib+1
			}
		case da == 0:
			for j < len(ops) && ops[j] == opEq && b[ib] == b[ib+db] {
				ops[i], ops[j] = opEq, opIns
				i, j, ia, ib = i+1, j+1, ia+1, ib+1
			}
		}
		ia, ib, i = ia+da, ib+db, j
	}
	return ops
}

func myers(a, b []string) []op {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		ops := make([]op, 0, n+m)
		for range n {
			ops = append(ops, opDel)
		}
		for range m {
			ops = append(ops, opIns)
		}
		return ops
	}
	limit := n + m
	off := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v[off-d-1:off+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m)
			}
		}
	}
	return nil
}

func backtrack(trace [][]int, n, m int) []op {
	var rev []op
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		at := func(k int) int { return v[k+d+1] }
		k := x - y
		pk := k - 1
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			rev = append(rev, opEq)
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == px {
				rev = append(rev, opIns)
			} else {
				rev = append(rev, opDel)
			}
		}
		x, y = px, py
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
