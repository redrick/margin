package coverage

import (
	"strings"
	"testing"
)

func TestDefLine(t *testing.T) {
	goSrc := strings.Split(`package x

func TestReserve(t *testing.T) {
	t.Run("takes stock", func(t *testing.T) {})
	t.Run("insufficient stock", func(t *testing.T) {})
}

func (s *suite) TestMethod() {}`, "\n")
	py := strings.Split("class TestCart:\n    def test_total(self):\n        pass\n", "\n")
	js := strings.Split("describe('cart', () => {\n  it(\"adds items\", () => {})\n})\n", "\n")
	for _, c := range []struct {
		lines []string
		name  string
		want  int
	}{
		{goSrc, "TestReserve", 2},
		{goSrc, "TestReserve/insufficient_stock", 4},
		{goSrc, "TestReserve/missing_one", 2},
		{goSrc, "TestMethod", 7},
		{goSrc, "TestRes", -1},
		{py, "tests/test_cart.py::TestCart::test_total[1]", 1},
		{js, "adds items", 1},
	} {
		if got := DefLine(c.lines, c.name); got != c.want {
			t.Errorf("DefLine(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestFindGo(t *testing.T) {
	file, line, ok := FindGo("../../testdata/example/current", "inventory", "TestReserve/rejects_non-positive_quantity")
	if !ok || file != "inventory/stock_test.go" || line != 17 {
		t.Errorf("FindGo = %s:%d %v", file, line, ok)
	}
}
