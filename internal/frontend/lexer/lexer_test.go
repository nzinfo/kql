package lexer

import (
	"reflect"
	"testing"

	"nzinfo/kql/internal/frontend/token"
)

// tok is a shorthand for building expected token sequences.
type tok struct {
	typ token.Token
	lit string
}

// scanAll runs the lexer to EOF and returns the non-EOF tokens plus any errors.
func scanAll(t *testing.T, src string) []Token {
	t.Helper()
	l := New("test.kql", src)
	var out []Token
	for {
		tk := l.Scan()
		if tk.Type == token.EOF {
			break
		}
		out = append(out, tk)
	}
	return out
}

// toSeq converts scanned tokens to the comparable {typ,lit} form (drops Pos).
func toSeq(toks []Token) []tok {
	out := make([]tok, len(toks))
	for i, tk := range toks {
		out[i] = tok{tk.Type, tk.Lit}
	}
	return out
}

// TestAcceptanceQuery is the F1 acceptance case: the canonical 9-token query.
//   "StormEvents | where State == "TEXAS" | take 10"
// tokens: IDENT PIPE WHERE IDENT EQL STRING PIPE TAKE INT   = 9
func TestAcceptanceQuery(t *testing.T) {
	src := `StormEvents | where State == "TEXAS" | take 10`
	toks := scanAll(t, src)
	want := []tok{
		{token.IDENT, "StormEvents"},
		{token.PIPE, "|"},
		{token.WHERE, "where"},
		{token.IDENT, "State"},
		{token.EQL, "=="},
		{token.STRING, `"TEXAS"`},
		{token.PIPE, "|"},
		{token.TAKE, "take"},
		{token.INT, "10"},
	}
	if len(toks) != 9 {
		t.Fatalf("got %d tokens, want 9: %+v", len(toks), toSeq(toks))
	}
	if got := toSeq(toks); !reflect.DeepEqual(got, want) {
		t.Errorf("tokens =\n  got  %+v\n  want %+v", got, want)
	}
}

// TestPositionsContiguous verifies tokens are laid out contiguously across the
// source (no gaps/overlaps) — F1 acceptance: "位置连续无重叠".
func TestPositionsContiguous(t *testing.T) {
	src := `StormEvents | where State == "TEXAS" | take 10`
	l := New("test.kql", src)
	var prevEnd token.Pos
	for {
		tk := l.Scan()
		if tk.Type == token.EOF {
			break
		}
		if tk.Pos < prevEnd {
			t.Errorf("token %s at pos %d overlaps prev end %d", tk.Type, tk.Pos, prevEnd)
		}
		// advance prevEnd by literal length
		prevEnd = tk.Pos + token.Pos(len(tk.Lit))
	}
}

// TestKeywordCaseInsensitive — gold-standard alignment (NOTES.md §2.1).
func TestKeywordCaseInsensitive(t *testing.T) {
	for _, w := range []string{"WHERE", "where", "Where"} {
		toks := scanAll(t, w)
		if len(toks) != 1 || toks[0].Type != token.WHERE {
			t.Errorf("scan %q: got %+v, want single WHERE", w, toSeq(toks))
		}
	}
}

// TestHyphenatedKeywords — g4 accepts hyphenated query-operator spellings.
func TestHyphenatedKeywords(t *testing.T) {
	cases := map[string]token.Token{
		"make-series":     token.MAKESERIES,
		"mv-expand":       token.MVEXPAND,
		"project-away":    token.PROJECTAWAY,
		"project-rename":  token.PROJECTRENAME,
		"top-hitters":     token.TOPHITTERS,
		"graph-match":     token.GRAPHMATCH,
		"sample-distinct": token.SAMPLEDISTINCT,
	}
	for lit, want := range cases {
		toks := scanAll(t, lit)
		if len(toks) != 1 || toks[0].Type != want {
			t.Errorf("scan %q: got %+v, want single %s", lit, toSeq(toks), want)
		}
	}
}

// TestNegatedOperators — !has, !contains, etc. as single tokens.
func TestNegatedOperators(t *testing.T) {
	cases := map[string]token.Token{
		"!has":        token.NOTHAS,
		"!contains":   token.NOTCONTAINS,
		"!startswith": token.NOTSTARTSWITH,
		"!between":    token.NOTBETWEEN,
		"!in":         token.NOTIN,
		"!in~":        token.NOTINCI,
		"!has_cs":     token.NOTHASCS,
	}
	for lit, want := range cases {
		toks := scanAll(t, lit)
		if len(toks) != 1 || toks[0].Type != want {
			t.Errorf("scan %q: got %+v, want single %s", lit, toSeq(toks), want)
		}
	}
}

// TestMatchesRegex — two-word "matches regex" keyword.
func TestMatchesRegex(t *testing.T) {
	for _, src := range []string{"matches regex", "matches  regex"} { // allow spaces
		toks := scanAll(t, src)
		if len(toks) != 1 || toks[0].Type != token.MATCHESREGEX {
			t.Errorf("scan %q: got %+v, want single MATCHESREGEX", src, toSeq(toks))
		}
	}
}

// TestTypeLiteralGrouping — gold-standard alignment (NOTES.md §2.2).
// <typekeyword>(...) forms must scan as a single literal token because their
// content is not safely re-tokenisable.
func TestTypeLiteralGrouping(t *testing.T) {
	cases := []struct {
		src string
		typ token.Token
	}{
		{`datetime(2020-01-01T00:00:00Z)`, token.DATETIME},
		{`guid(12345678-1234-1234-1234-123456789012)`, token.GUID},
		{`timespan(1.02:03:04)`, token.TIMESPAN},
		{`long(9223372036854775807)`, token.INT},
		{`int(42)`, token.INT},
		{`real(1.5)`, token.REAL},
		{`bool(true)`, token.BOOL},
	}
	for _, c := range cases {
		toks := scanAll(t, c.src)
		if len(toks) != 1 {
			t.Errorf("scan %q: got %d tokens %+v, want 1", c.src, len(toks), toSeq(toks))
			continue
		}
		if toks[0].Type != c.typ {
			t.Errorf("scan %q: type %s, want %s", c.src, toks[0].Type, c.typ)
		}
		if toks[0].Lit != c.src {
			t.Errorf("scan %q: lit %q, want whole form", c.src, toks[0].Lit)
		}
	}
}

// TestDateTimeWithoutParens — bare "datetime" is the type keyword, not a literal.
func TestDateTimeWithoutParens(t *testing.T) {
	toks := scanAll(t, "datetime")
	if len(toks) != 1 || toks[0].Type != token.DATETIMETYPE {
		t.Errorf("got %+v, want DATETIMETYPE", toSeq(toks))
	}
}

func TestNumbers(t *testing.T) {
	cases := []struct {
		src string
		typ token.Token
	}{
		{"123", token.INT},
		{"0x1F", token.INT},
		{"1.23", token.REAL},
		{"1.23e10", token.REAL},
		{"1e-4", token.REAL},
		{"100L", token.INT}, // L is letter — but 100 is INT; suffix handled later
	}
	for _, c := range cases {
		toks := scanAll(t, c.src)
		// Note: "100L" scans INT "100" then IDENT "L" — but timespan-suffix check
		// only triggers on d/h/m/s/t. L is not a timespan start, so 100 is INT,
		// then L is a separate IDENT.
		if c.src == "100L" {
			if len(toks) != 2 || toks[0].Type != token.INT || toks[1].Type != token.IDENT {
				t.Errorf("scan %q: got %+v, want INT then IDENT(L)", c.src, toSeq(toks))
			}
			continue
		}
		if len(toks) != 1 || toks[0].Type != c.typ {
			t.Errorf("scan %q: got %+v, want single %s", c.src, toSeq(toks), c.typ)
		}
	}
}

func TestTimespanLiterals(t *testing.T) {
	cases := []string{"1d", "1.5d", "1day", "2h", "3hour", "1hr", "1hrs", "5m", "5min", "5minute", "10s", "10sec", "10seconds", "100ms", "2tick", "2ticks"}
	for _, src := range cases {
		toks := scanAll(t, src)
		if len(toks) != 1 || toks[0].Type != token.TIMESPAN {
			t.Errorf("scan %q: got %+v, want single TIMESPAN", src, toSeq(toks))
		}
	}
}

func TestStrings(t *testing.T) {
	cases := map[string]string{
		`"abc"`:         `"abc"`,
		`'abc'`:         `'abc'`,
		`"a\"b"`:        `"a\"b"`,
		`@"C:\path"`:    `@"C:\path"`,
		`@'say ""hi""'`: `@'say ""hi""'`,
		`h"hashed"`:     `h"hashed"`,
	}
	for src, wantLit := range cases {
		toks := scanAll(t, src)
		if len(toks) != 1 || toks[0].Type != token.STRING {
			t.Errorf("scan %q: got %+v, want single STRING", src, toSeq(toks))
			continue
		}
		if toks[0].Lit != wantLit {
			t.Errorf("scan %q: lit %q, want %q", src, toks[0].Lit, wantLit)
		}
	}
}

func TestMultiLineString(t *testing.T) {
	src := "```\nhello\nworld\n```"
	toks := scanAll(t, src)
	if len(toks) != 1 || toks[0].Type != token.STRING {
		t.Errorf("got %+v, want single STRING", toSeq(toks))
	}
}

func TestComments(t *testing.T) {
	src := "a // comment\nb"
	toks := scanAll(t, src)
	if len(toks) != 2 || toks[0].Type != token.IDENT || toks[1].Type != token.IDENT {
		t.Errorf("got %+v, want two IDENTs (comment skipped)", toSeq(toks))
	}
}

func TestOperators(t *testing.T) {
	cases := map[string]token.Token{
		"+":   token.ADD,
		"-":   token.SUB,
		"*":   token.MUL,
		"/":   token.QUO,
		"%":   token.REM,
		"==":  token.EQL,
		"!=":  token.NEQ,
		"<>":  token.NEQ,
		"<":   token.LSS,
		">":   token.GTR,
		"<=":  token.LEQ,
		">=":  token.GEQ,
		"=~":  token.TILDE,
		"!~":  token.NTILDE,
		"|":   token.PIPE,
		"=":   token.ASSIGN,
		"=>":  token.ARROW,
		"..":  token.DOTDOT,
		"--":  token.DASHDASH,
		"-->": token.DASHGT,
		"<--": token.LTDASH,
		"-[":  token.DASHLBRACK,
		"]->": token.RBRACKDASHGT,
	}
	for src, want := range cases {
		toks := scanAll(t, src)
		if len(toks) != 1 || toks[0].Type != want {
			t.Errorf("scan %q: got %+v, want %s", src, toSeq(toks), want)
		}
	}
}

// TestErrorRecovery — bad input records errors and continues, never panics.
func TestErrorRecovery(t *testing.T) {
	// Unterminated string.
	l := New("", `"unterminated`)
	toks := scanAll(t, `"unterminated`)
	_ = l.Errors()
	if len(toks) != 1 {
		t.Errorf("expected partial token, got %+v", toSeq(toks))
	}

	// Unknown char '@' not followed by quote.
	l2 := New("", "@")
	_ = scanAllFrom(l2)
	if errs := l2.Errors(); len(errs) == 0 {
		t.Error("expected error for lone '@'")
	}
}

func scanAllFrom(l *Lexer) []Token {
	var out []Token
	for {
		tk := l.Scan()
		if tk.Type == token.EOF {
			break
		}
		out = append(out, tk)
	}
	return out
}

// TestReset — lexer can rewind to a byte offset (F3.S1 lookahead foundation).
func TestReset(t *testing.T) {
	src := "abc def"
	l := New("", src)
	first := l.Scan() // abc
	mid := l.Offset() // offset of ' '
	second := l.Scan() // def
	if first.Lit != "abc" || second.Lit != "def" {
		t.Fatalf("got %q %q", first.Lit, second.Lit)
	}
	// Reset to the space and rescan — should get "def" again.
	l.Reset(mid)
	again := l.Scan()
	if again.Lit != "def" {
		t.Errorf("after Reset: got %q, want def", again.Lit)
	}
}

func TestFileAndPosition(t *testing.T) {
	src := "line1\nline2"
	l := New("query.kql", src)
	if l.File().Name() != "query.kql" {
		t.Error("File name mismatch")
	}
	if l.File().LineCount() != 2 {
		t.Errorf("LineCount = %d, want 2", l.File().LineCount())
	}
}

// TestUnicodeIdent — KQL identifiers may contain unicode letters.
func TestUnicodeIdent(t *testing.T) {
	toks := scanAll(t, "café")
	if len(toks) != 1 || toks[0].Type != token.IDENT || toks[0].Lit != "café" {
		t.Errorf("got %+v, want IDENT 'café'", toSeq(toks))
	}
}


// TestUnicodeWhitespace — the gold-standard WHITESPACE token covers non-ASCII
// spaces. Pasted queries from rich-text editors carry NBSP (\u00a0); files
// exported from Windows often start with a BOM (\ufeff). Both must be skipped,
// not error. Mirrors the g4 WHITESPACE character set.
func TestUnicodeWhitespace(t *testing.T) {
	cases := []struct {
		name string
		sep  string // the whitespace rune(s) to insert
	}{
		{"NBSP", "\u00a0"},
		{"BOM", "\ufeff"},
		{"NARROW_NBSP", "\u202f"},
		{"MEDIUM_MATH", "\u205f"},
		{"IDEOGRAPHIC", "\u3000"},
		{"OGHAM", "\u1680"},
		{"FIGURE_SPACE", "\u2007"},
		{"EN_QUAD", "\u2000"},
		{"FormFeed", "\f"},
	}
	for _, c := range cases {
		// "T" + sep + "where" → should lex as IDENT(T) WHERE.
		src := "T" + c.sep + "where"
		toks := scanAll(t, src)
		// Expect: IDENT "T", WHERE, EOF (scanAll may or may not include EOF;
		// filter to the meaningful tokens).
		var got []Token
		for _, tk := range toks {
			if tk.Type == token.EOF {
				continue
			}
			got = append(got, tk)
		}
		if len(got) != 2 || got[0].Type != token.IDENT || got[1].Type != token.WHERE {
			t.Errorf("%s: scan %q got %v, want [IDENT WHERE]", c.name, src, toSeq(got))
		}
	}
}

// TestLeadingBOM — a BOM at the very start of the file is skipped, so the first
// real token (a table name) lexes cleanly.
func TestLeadingBOM(t *testing.T) {
	src := "\ufeffEvents | take 1"
	toks := scanAll(t, src)
	var got []Token
	for _, tk := range toks {
		if tk.Type == token.EOF {
			continue
		}
		got = append(got, tk)
	}
	if len(got) < 2 || got[0].Type != token.IDENT || got[0].Lit != "Events" || got[1].Type != token.PIPE {
		t.Errorf("leading BOM: got %v, want [IDENT(Events) PIPE ...]", toSeq(got))
	}
}
