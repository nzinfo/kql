package parser

import (
	"testing"

	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parseExprStr is a test helper: parse src as a single expression, failing the
// test if any diagnostics were produced.
func parseExprStr(t *testing.T, src string) ast.Expr {
	t.Helper()
	p := New("test.kql", src)
	e := p.ParseExpr()
	if diags := p.Diagnostics(); diags.HasErrors() {
		t.Fatalf("parse %q produced errors:\n  %v", src, diags.Render())
	}
	return e
}

// litOf asserts e is a BasicLit with the given kind+value.
func litOf(t *testing.T, e ast.Expr, kind token.Token, val string) *ast.BasicLit {
	t.Helper()
	l, ok := e.(*ast.BasicLit)
	if !ok {
		t.Fatalf("got %T, want *BasicLit", e)
	}
	if l.Kind != kind || l.Value != val {
		t.Errorf("BasicLit = {%s %q}, want {%s %q}", l.Kind, l.Value, kind, val)
	}
	return l
}

// binOf asserts e is a BinaryExpr with op, returning it for further inspection.
func binOf(t *testing.T, e ast.Expr, op token.Token) *ast.BinaryExpr {
	t.Helper()
	b, ok := e.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("got %T, want *BinaryExpr (%s)", e, op)
	}
	if b.Op != op {
		t.Errorf("BinaryExpr.Op = %s, want %s", b.Op, op)
	}
	return b
}

// identOf asserts e is an Ident with the given name.
func identOf(t *testing.T, e ast.Expr, name string) *ast.Ident {
	t.Helper()
	id, ok := e.(*ast.Ident)
	if !ok {
		t.Fatalf("got %T, want *Ident (%s)", e, name)
	}
	if id.Name != name {
		t.Errorf("Ident.Name = %q, want %q", id.Name, name)
	}
	return id
}

// TestPrecedenceAdditiveOverMultiplicative: 1 + 2 * 3  →  1 + (2 * 3)
func TestPrecedenceAdditiveOverMultiplicative(t *testing.T) {
	e := parseExprStr(t, `1 + 2 * 3`)
	b := binOf(t, e, token.ADD) // top-level op is +
	litOf(t, b.X, token.INT, "1")
	rhs := binOf(t, b.Y, token.MUL)
	litOf(t, rhs.X, token.INT, "2")
	litOf(t, rhs.Y, token.INT, "3")
}

// TestPrecedenceStringOpTighterThanMulti: KQL string operators bind tighter
// than * (g4 stringOperatorExpression layer). So `a has x * b` is `(a has x) * b`.
// (This is the key gold-standard deviation — see NOTES.md §2.8.)
func TestPrecedenceStringOpTighterThanMulti(t *testing.T) {
	e := parseExprStr(t, `a has "x" * b`)
	b := binOf(t, e, token.MUL) // top-level is *
	has := binOf(t, b.X, token.HAS)
	identOf(t, has.X, "a")
	litOf(t, has.Y, token.STRING, `"x"`)
	identOf(t, b.Y, "b")
}

// TestPrecedenceRelationalOverEquality: `a == b < c` → `a == (b < c)`
func TestPrecedenceRelationalOverEquality(t *testing.T) {
	e := parseExprStr(t, `a == b < c`)
	b := binOf(t, e, token.EQL)
	identOf(t, b.X, "a")
	rel := binOf(t, b.Y, token.LSS)
	identOf(t, rel.X, "b")
	identOf(t, rel.Y, "c")
}

// TestLogicalAndTighterThanOr: `a or b and c` → `a or (b and c)`
func TestLogicalAndTighterThanOr(t *testing.T) {
	e := parseExprStr(t, `a or b and c`)
	b := binOf(t, e, token.OR)
	identOf(t, b.X, "a")
	and := binOf(t, b.Y, token.AND)
	identOf(t, and.X, "b")
	identOf(t, and.Y, "c")
}

// TestStringOps: a sample of string operators parse to the right BinaryExpr op.
func TestStringOps(t *testing.T) {
	cases := map[string]token.Token{
		`a contains "x"`:   token.CONTAINS,
		`a startswith "x"`: token.STARTSWITH,
		`a has_cs "x"`:     token.HASCS,
		`a =~ b`:           token.TILDE,
		`a !~ b`:           token.NTILDE,
		`a matches regex "x.*"`: token.MATCHESREGEX,
		`a !contains "x"`:  token.NOTCONTAINS,
	}
	for src, want := range cases {
		e := parseExprStr(t, src)
		binOf(t, e, want)
	}
}

// TestComparisonOps
func TestComparisonOps(t *testing.T) {
	cases := map[string]token.Token{
		`a < b`:  token.LSS,
		`a >= b`: token.GEQ,
		`a == b`: token.EQL,
		`a != b`: token.NEQ,
		`a <> b`: token.NEQ,
	}
	for src, want := range cases {
		e := parseExprStr(t, src)
		binOf(t, e, want)
	}
}

// TestFunctionCall: count() and bin(TimeGenerated, 1h) and multi-arg iff.
func TestFunctionCall(t *testing.T) {
	// count()
	e := parseExprStr(t, `count()`)
	c, ok := e.(*ast.CallExpr)
	if !ok {
		t.Fatalf("got %T, want *CallExpr", e)
	}
	identOf(t, c.Fun, "count")
	if len(c.Args) != 0 {
		t.Errorf("count() args = %d, want 0", len(c.Args))
	}

	// bin(TimeGenerated, 1h)
	e = parseExprStr(t, `bin(TimeGenerated, 1h)`)
	c = e.(*ast.CallExpr)
	identOf(t, c.Fun, "bin")
	if len(c.Args) != 2 {
		t.Fatalf("bin args = %d, want 2", len(c.Args))
	}
	identOf(t, argExpr(c.Args[0]), "TimeGenerated")
	litOf(t, argExpr(c.Args[1]), token.TIMESPAN, "1h")

	// iff(x > 0, 1, 0) — three args
	e = parseExprStr(t, `iff(x > 0, 1, 0)`)
	c = e.(*ast.CallExpr)
	if len(c.Args) != 3 {
		t.Fatalf("iff args = %d, want 3", len(c.Args))
	}
}

// argExpr unwraps a NamedExpr that wraps a bare expression argument.
func argExpr(e ast.Expr) ast.Expr {
	if n, ok := e.(*ast.NamedExpr); ok && !n.IsNamed() {
		return n.Expr
	}
	return e
}

// TestNamedArgument: function calls with `name = value` args
// (e.g. join's kind=inner is at the operator level, but scalar named args
// appear in calls like `pack("a", 1, "b", 2)` — here we test the parser path).
func TestNamedArgument(t *testing.T) {
	p := New("", `f(x = 1, y = 2)`)
	e := p.ParseExpr()
	c := e.(*ast.CallExpr)
	if len(c.Args) != 2 {
		t.Fatalf("args = %d, want 2", len(c.Args))
	}
	for _, a := range c.Args {
		n, ok := a.(*ast.NamedExpr)
		if !ok || !n.IsNamed() {
			t.Errorf("arg not a named binding: %T", a)
		}
	}
}

// TestMemberAndIndex: T.col, arr[0], obj.k[1].y
func TestMemberAndIndex(t *testing.T) {
	e := parseExprStr(t, `T.col`)
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		t.Fatalf("got %T, want *SelectorExpr", e)
	}
	identOf(t, sel.X, "T")
	identOf(t, sel.Sel, "col")

	e = parseExprStr(t, `arr[0]`)
	idx, ok := e.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("got %T, want *IndexExpr", e)
	}
	identOf(t, idx.X, "arr")
	litOf(t, idx.Index, token.INT, "0")

	// chained: obj.k[1].y
	e = parseExprStr(t, `obj.k[1].y`)
	// outermost is .y
	sel = e.(*ast.SelectorExpr)
	identOf(t, sel.Sel, "y")
	idx = sel.X.(*ast.IndexExpr)
	litOf(t, idx.Index, token.INT, "1")
}

// TestLiterals: each literal kind parses to a BasicLit with the right Kind.
func TestLiterals(t *testing.T) {
	cases := map[string]token.Token{
		`42`:                            token.INT,
		`3.14`:                          token.REAL,
		`"hello"`:                       token.STRING,
		`'hello'`:                       token.STRING,
		`1h`:                            token.TIMESPAN,
		`datetime(2020-01-01T00:00:00Z)`: token.DATETIME,
		`guid(12345678-1234-1234-1234-123456789012)`: token.GUID,
		`true`:  token.BOOL,
		`false`: token.BOOL,
	}
	for src, want := range cases {
		e := parseExprStr(t, src)
		litOf(t, e, want, src)
	}
}

// TestParenExpr
func TestParenExpr(t *testing.T) {
	e := parseExprStr(t, `(1 + 2) * 3`)
	b := binOf(t, e, token.MUL)
	par, ok := b.X.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("got %T, want *ParenExpr", b.X)
	}
	binOf(t, par.X, token.ADD)
	litOf(t, b.Y, token.INT, "3")
}

// TestUnary
func TestUnary(t *testing.T) {
	e := parseExprStr(t, `-5`)
	u, ok := e.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("got %T, want *UnaryExpr", e)
	}
	if u.Op != token.SUB {
		t.Errorf("op = %s, want SUB", u.Op)
	}
	litOf(t, u.X, token.INT, "5")
}

// TestInList: x in (1, 2, 3)
func TestInList(t *testing.T) {
	e := parseExprStr(t, `x in (1, 2, 3)`)
	b := binOf(t, e, token.IN)
	identOf(t, b.X, "x")
	list, ok := b.Y.(*ast.ListExpr)
	if !ok {
		t.Fatalf("got %T, want *ListExpr", b.Y)
	}
	if len(list.Elems) != 3 {
		t.Errorf("in-list len = %d, want 3", len(list.Elems))
	}
}

// TestBetween: x between (1 .. 10)
func TestBetween(t *testing.T) {
	e := parseExprStr(t, `x between (1 .. 10)`)
	b, ok := e.(*ast.BetweenExpr)
	if !ok {
		t.Fatalf("got %T, want *BetweenExpr", e)
	}
	if b.Not {
		t.Error("between should not be negated")
	}
	identOf(t, b.X, "x")
	litOf(t, b.Low, token.INT, "1")
	litOf(t, b.High, token.INT, "10")

	// !between
	e = parseExprStr(t, `x !between (1 .. 10)`)
	b = e.(*ast.BetweenExpr)
	if !b.Not {
		t.Error("!between should be negated")
	}
}

// TestErrorRecovery: bad input records diagnostics, never panics.
func TestErrorRecovery(t *testing.T) {
	p := New("", `1 + + +`)
	_ = p.ParseExpr()
	if !p.Diagnostics().HasErrors() {
		t.Error("expected diagnostics for malformed input")
	}
}

// TestKeywordAsName: KQL permits keywords as identifiers in name position
// (e.g. a column named `count`).
func TestKeywordAsName(t *testing.T) {
	e := parseExprStr(t, `count`)
	id := e.(*ast.Ident)
	if id.Name != "count" {
		t.Errorf("name = %q, want count", id.Name)
	}
	// But `count()` is still a call.
	e = parseExprStr(t, `count()`)
	if _, ok := e.(*ast.CallExpr); !ok {
		t.Errorf("count() should be a CallExpr, got %T", e)
	}
}
