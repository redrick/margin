package ignore

import "testing"

func TestMatch(t *testing.T) {
	m := Parse(`
# generated
*.pb.go
dist/
/vendor
docs/**/*.svg
go.sum
!keep.pb.go
`)
	for path, want := range map[string]bool{
		"api/v1/user.pb.go":      true,
		"user.pb.go":             true,
		"keep.pb.go":             false,
		"dist/app.js":            true,
		"web/dist/app.js":        true,
		"dist":                   false,
		"vendor/x/y.go":          true,
		"internal/vendor/y.go":   false,
		"docs/screenshots/a.svg": true,
		"docs/a.svg":             true,
		"docs/a.png":             false,
		"go.sum":                 true,
		"tools/go.sum":           true,
		"main.go":                false,
		"api/v1/user.pb.go.orig": false,
	} {
		if got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
	var none *Matcher
	if none.Match("a") || !none.Empty() || !Parse("# only a comment\n").Empty() {
		t.Error("an empty matcher should match nothing")
	}
}
