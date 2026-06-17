// Package parser implements a hand-written recursive-descent + Pratt parser
// for KQL scalar expressions (F3) and tabular operators (F4).
//
// Grammar authority: .source-projects/Kusto-Query-Language/grammar/Kql.g4.
// The expression precedence ladder is implemented as explicit layers matching
// the g4 rules (see internal/frontend/NOTES.md §2.8) rather than via a flat
// Pratt table, because KQL's string operators bind *tighter than multiplication*
// and in/between carry their own parenthesised/`..` syntax — a flat table
// cannot express that cleanly.
//
// Error model: the parser never panics on bad input. Errors are collected into
// a diagnostic.List and parsing continues after synchronising to the next
// statement boundary (`;`) or pipeline boundary (`|`). This lets a single run
// surface many errors.
package parser

import (
	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/lexer"
	"nzinfo/kql/internal/frontend/token"
)

// Parser holds parsing state. It wraps a lexer and tracks the current and
// lookahead tokens. Backtracking is supported via save/restore of the lexer
// offset (savedState) for the handful of context-sensitive productions
// (operator-prefix disambiguation in F4).
type Parser struct {
	lx    *lexer.Lexer
	file  *token.File
	src   string
	le    lexer.ErrorList // lexer errors, surfaced via diags
	diags *diagnostic.List

	// Current token and its literal text.
	cur token.Token
	pos token.Pos
	lit string

	// trackStmt, when true, makes expect/advance append a diagnostic on
	// mismatch instead of returning a Bad node — used at statement boundaries.
}

// New creates a Parser over the given source text. The filename is used for
// diagnostics only.
func New(filename, src string) *Parser {
	lx := lexer.New(filename, src)
	p := &Parser{
		lx:    lx,
		file:  lx.File(),
		src:   src,
		diags: &diagnostic.List{},
	}
	p.next() // prime cur
	return p
}

// File returns the source *token.File (shared with lexer).
func (p *Parser) File() *token.File { return p.file }

// Diagnostics returns accumulated diagnostics (both parse and lexer errors).
// Callers should check HasErrors() before trusting the produced AST.
func (p *Parser) Diagnostics() *diagnostic.List {
	// Fold lexer errors into diags (idempotent-ish: re-calling re-appends;
	// callers are expected to call once at the end).
	for _, e := range p.lx.Errors() {
		p.diags.Add(diagnostic.Diagnostic{
			Severity: diagnostic.Error,
			Code:     diagnostic.LexerError,
			Pos:      e.Pos,
			Message:  e.Msg,
		})
	}
	return p.diags
}

// next advances to the next token, populating p.cur/pos/lit.
func (p *Parser) next() {
	if len(p.le) == 0 {
		p.le = p.lx.Errors() // pick up lexer errors lazily
	}
	tk := p.lx.Scan()
	p.cur, p.pos, p.lit = tk.Type, tk.Pos, tk.Lit
}

// peek reports whether the current token is t.
func (p *Parser) peek(t token.Token) bool { return p.cur == t }

// peekAny reports whether the current token is any of the given tokens.
func (p *Parser) peekAny(ts ...token.Token) bool {
	for _, t := range ts {
		if p.cur == t {
			return true
		}
	}
	return false
}

// advance consumes the current token and returns it.
func (p *Parser) advance() (token.Token, token.Pos, string) {
	t, pos, lit := p.cur, p.pos, p.lit
	p.next()
	return t, pos, lit
}

// expect consumes the current token if it matches t, returning its position;
// otherwise it records a diagnostic and returns NoPos without consuming.
func (p *Parser) expect(t token.Token) token.Pos {
	if p.cur == t {
		pos := p.pos
		p.next()
		return pos
	}
	p.error(p.pos, "expected "+t.String()+", found "+p.cur.String())
	return token.NoPos
}

// error records a parse error diagnostic at pos.
func (p *Parser) error(pos token.Pos, msg string) {
	p.diags.Add(diagnostic.Diagnostic{
		Severity: diagnostic.Error,
		Code:     diagnostic.SyntaxError,
		Pos:      p.file.Position(pos),
		Message:  msg,
	})
}

// accept consumes the current token if it matches t and returns true.
func (p *Parser) accept(t token.Token) bool {
	if p.cur == t {
		p.next()
		return true
	}
	return false
}

// savedState captures enough parser/lexer state to rewind. We save BOTH the
// parser's buffered token (cur/pos/lit) and the lexer offset, because the
// lexer is always positioned one token *ahead* of cur after each Scan — so to
// re-parse from cur we must rewind the lexer to cur's start and re-scan.
type savedState struct {
	offset int
	cur    token.Token
	pos    token.Pos
	lit    string
}

// save records the current parser state. Call save() BEFORE consuming the token
// you may need to rewind past.
func (p *Parser) save() savedState {
	return savedState{offset: p.lx.Offset(), cur: p.cur, pos: p.pos, lit: p.lit}
}

// restore rewinds to a saved state. The lexer is re-positioned to re-emit the
// saved cur token (by resetting to cur's start offset and re-scanning).
func (p *Parser) restore(s savedState) {
	// p.pos is the byte offset of cur's first char (Pos is 1-based, so the
	// 0-based lexer offset is int(pos)-1). Rewind there and re-scan so cur is
	// reloaded without re-running the whole token stream.
	start := int(s.pos) - 1
	if start < 0 {
		start = 0
	}
	p.lx.Reset(start)
	p.next() // reload cur from the rewound position
}

// synchroniseToStatementBoundary skips tokens until a `;`, EOF, or (optionally)
// `|` is reached, to recover from a parse error. Used after a failed statement
// so subsequent statements can still be parsed.
func (p *Parser) synchroniseToStatementBoundary() {
	for {
		switch p.cur {
		case token.SEMI, token.EOF:
			return
		}
		p.next()
	}
}
