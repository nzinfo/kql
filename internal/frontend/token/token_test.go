package token

import "testing"

func TestTokenPredicates(t *testing.T) {
	cases := []struct {
		tok             Token
		lit, op, kw     bool
	}{
		{INT, true, false, false},
		{STRING, true, false, false},
		{ADD, false, true, false},
		{PIPE, false, true, false},
		{DASHGT, false, true, false}, // graph edge op reserved
		{WHERE, false, false, true},
		{SUMMARIZE, false, false, true},
		{NOTCONTAINS, false, false, true},
		{ILLEGAL, false, false, false},
		{EOF, false, false, false},
	}
	for _, c := range cases {
		if got := c.tok.IsLiteral(); got != c.lit {
			t.Errorf("%s.IsLiteral() = %v, want %v", c.tok, got, c.lit)
		}
		if got := c.tok.IsOperator(); got != c.op {
			t.Errorf("%s.IsOperator() = %v, want %v", c.tok, got, c.op)
		}
		if got := c.tok.IsKeyword(); got != c.kw {
			t.Errorf("%s.IsKeyword() = %v, want %v", c.tok, got, c.kw)
		}
	}
}

func TestTokenString(t *testing.T) {
	cases := map[Token]string{
		WHERE:       "where",
		SUMMARIZE:   "summarize",
		MATCHESREGEX: "matches regex",
		DASHGT:      "-->",
		INT:         "INT",
		ADD:         "+",
	}
	for tok, want := range cases {
		if got := tok.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", tok, got, want)
		}
	}
}

func TestPrecedence(t *testing.T) {
	// OR < AND < comparison < additive < multiplicative
	if OR.Precedence() >= AND.Precedence() {
		t.Error("OR should have lower precedence than AND")
	}
	if AND.Precedence() >= EQL.Precedence() {
		t.Error("AND should have lower precedence than comparison")
	}
	if EQL.Precedence() >= ADD.Precedence() {
		t.Error("comparison should have lower precedence than additive")
	}
	if ADD.Precedence() >= MUL.Precedence() {
		t.Error("additive should have lower precedence than multiplicative")
	}
	// String operators are comparison-level.
	if HAS.Precedence() != EQL.Precedence() {
		t.Error("HAS should share comparison precedence with ==")
	}
	if BETWEEN.Precedence() != EQL.Precedence() {
		t.Error("BETWEEN should share comparison precedence with ==")
	}
	if PROJECT.Precedence() != 0 {
		t.Error("non-operator keyword should have precedence 0")
	}
}

// TestLookupCaseInsensitive verifies the gold-standard-aligned case-insensitive
// keyword lookup (NOTES.md §2.1) — kqlparser's Lookup failed this.
func TestLookupCaseInsensitive(t *testing.T) {
	for _, w := range []string{"where", "WHERE", "Where", "wHeRe"} {
		if got := Lookup(w); got != WHERE {
			t.Errorf("Lookup(%q) = %s, want WHERE", w, got)
		}
	}
	for _, w := range []string{"summarize", "SUMMARIZE", "Summarize"} {
		if got := Lookup(w); got != SUMMARIZE {
			t.Errorf("Lookup(%q) = %s, want SUMMARIZE", w, got)
		}
	}
	// Non-keyword identifiers stay IDENT.
	if got := Lookup("myColumn"); got != IDENT {
		t.Errorf("Lookup(myColumn) = %s, want IDENT", got)
	}
	if got := Lookup("foo_bar"); got != IDENT {
		t.Errorf("Lookup(foo_bar) = %s, want IDENT", got)
	}
}

func TestLookupHyphenated(t *testing.T) {
	// Both hyphenated and collapsed spellings must resolve (g4 accepts both).
	cases := map[string]Token{
		"make-series":     MAKESERIES,
		"mv-expand":       MVEXPAND,
		"project-away":    PROJECTAWAY,
		"graph-match":     GRAPHMATCH,
		"top-hitters":     TOPHITTERS,
		"assert-schema":   ASSERTSCHEMA,
	}
	for lit, want := range cases {
		if got := Lookup(lit); got != want {
			t.Errorf("Lookup(%q) = %s, want %s", lit, got, want)
		}
	}
}

func TestLookupAliases(t *testing.T) {
	// Only spellings present in g4 KqlTokens.g4 as collapsed/alternate forms.
	cases := map[string]Token{
		"mvapply":        MVAPPLY,        // g4 MVAPPLY
		"mvexpand":       MVEXPAND,       // g4 MVEXPAND
		"int64":          LONGTYPE,       // g4 INT64
		"boolean":        BOOLTYPE,       // g4 BOOLEAN
		"date":           DATETIMETYPE,   // g4 DATE
		"time":           TIMESPANTYPE,   // g4 TIME
		"external_data":  EXTERNALDATA,   // g4 EXTERNAL_DATA
		"with_source":    WITHSOURCE,     // g4 WITH_SOURCE
		"notcontains":    NOTCONTAINS,    // g4 NOTCONTAINS (legacy no-!)
		"assertschema":   ASSERTSCHEMA,   // collapsed
		"macroexpand":    MACROEXPAND,    // collapsed
	}
	for lit, want := range cases {
		if got := Lookup(lit); got != want {
			t.Errorf("Lookup(%q) = %s, want %s", lit, got, want)
		}
	}
	// Sanity: g4 does NOT accept collapsed forms for these — they stay IDENT.
	for _, invalid := range []string{"makeseries", "makegraph", "projectaway"} {
		if got := Lookup(invalid); got != IDENT {
			t.Errorf("Lookup(%q) = %s, want IDENT (g4 has no collapsed form)", invalid, got)
		}
	}
}

func TestPositionFile(t *testing.T) {
	src := "abc\ndefg\nhi"
	f := NewFile("test.kql", src)

	// line table: lines = [0, 4, 9]  (line 1 @ 0, line 2 @ 4, line 3 @ 9)
	if f.LineCount() != 3 {
		t.Errorf("LineCount = %d, want 3", f.LineCount())
	}

	// Pos(0) → 1-based 1 → offset 0 = line 1 col 1
	p := f.Pos(0)
	if p != Pos(1) {
		t.Errorf("Pos(0) = %d, want 1", p)
	}
	pos := f.Position(p)
	if pos.Line != 1 || pos.Column != 1 {
		t.Errorf("Position(Pos(0)) = line %d col %d, want 1:1", pos.Line, pos.Column)
	}

	// offset 4 → line 2 col 1 ('d')
	pos = f.Position(f.Pos(4))
	if pos.Line != 2 || pos.Column != 1 {
		t.Errorf("Position(Pos(4)) = line %d col %d, want 2:1", pos.Line, pos.Column)
	}
	// offset 6 → line 2 col 3 ('f')
	pos = f.Position(f.Pos(6))
	if pos.Line != 2 || pos.Column != 3 {
		t.Errorf("Position(Pos(6)) = line %d col %d, want 2:3", pos.Line, pos.Column)
	}
	// offset 9 → line 3 col 1 ('h')
	pos = f.Position(f.Pos(9))
	if pos.Line != 3 || pos.Column != 1 {
		t.Errorf("Position(Pos(9)) = line %d col %d, want 3:1", pos.Line, pos.Column)
	}

	// NoPos is invalid
	if NoPos.IsValid() {
		t.Error("NoPos should be invalid")
	}
	// Out-of-range offset returns invalid Position
	if (Position{}).IsValid() {
		t.Error("zero Position should be invalid")
	}
}

func TestPositionString(t *testing.T) {
	p := Position{Filename: "a.kql", Line: 3, Column: 5}
	if got := p.String(); got != "a.kql:3:5" {
		t.Errorf("got %q", got)
	}
	p.Filename = ""
	if got := p.String(); got != "3:5" {
		t.Errorf("got %q", got)
	}
}

func TestSpan(t *testing.T) {
	s := Span{Start: Pos(3), End: Pos(7)}
	if !s.IsValid() {
		t.Error("span should be valid")
	}
	if s.Len() != 4 {
		t.Errorf("Len = %d, want 4", s.Len())
	}
	if !s.Contains(Pos(5)) {
		t.Error("span should contain 5")
	}
	if s.Contains(Pos(7)) { // half-open
		t.Error("span should NOT contain 7 (exclusive end)")
	}
}
