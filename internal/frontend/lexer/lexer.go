// Package lexer implements a lexical scanner for KQL.
//
// Design follows the go/scanner three-layer position abstraction (Pos/File/
// Position, mirrored from cloudygreybeard/kqlparser) and the hand-written
// rune-advancing main loop with a Reset(offset) entry point for parser
// lookahead. The literal-scanning rules are aligned to the **authoritative**
// grammar at .source-projects/Kusto-Query-Language/grammar/KqlTokens.g4 —
// in particular, `<typekeyword>(...)` forms (datetime/guid/timespan/long/...)
// are scanned as a single literal token because their content (hyphens,
// colons, GUID dashes) cannot be re-tokenised. See internal/frontend/NOTES.md
// §2.2 for the rationale and the deviation from the kqlparser template.
package lexer

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"nzinfo/kql/internal/frontend/token"
)

const eof = -1

// Lexer holds the state of the scanner.
type Lexer struct {
	file *token.File // Source file for position info
	src  string      // Source text

	// Scanning state. ch is the current rune; offset is its byte offset;
	// rdOffset is the byte offset of the next rune to read.
	ch       rune
	offset   int
	rdOffset int

	// Error handling — never panics; bad input appends to errors and recovers.
	errors ErrorList
}

// Error represents a lexer error at a position.
type Error struct {
	Pos token.Position
	Msg string
}

// Error implements the error interface.
func (e Error) Error() string { return fmt.Sprintf("%s: %s", e.Pos, e.Msg) }

// ErrorList is a list of lexer errors.
type ErrorList []Error

// Error implements the error interface.
func (el ErrorList) Error() string {
	switch len(el) {
	case 0:
		return "no errors"
	case 1:
		return el[0].Error()
	default:
		return fmt.Sprintf("%s (and %d more errors)", el[0], len(el)-1)
	}
}

// Err returns an error if the list is non-empty, nil otherwise.
func (el ErrorList) Err() error {
	if len(el) == 0 {
		return nil
	}
	return el
}

// Token represents a scanned token with its position and literal text.
type Token struct {
	Type token.Token
	Pos  token.Pos
	Lit  string
}

// New creates a new Lexer for the given source text.
func New(filename, src string) *Lexer {
	l := &Lexer{
		file: token.NewFile(filename, src),
		src:  src,
	}
	l.next() // Initialize first character
	return l
}

// Errors returns any errors encountered during scanning.
func (l *Lexer) Errors() ErrorList { return l.errors }

// File returns the source file (shared with parser/binder for position info).
func (l *Lexer) File() *token.File { return l.file }

// Offset returns the current byte offset in the source.
func (l *Lexer) Offset() int { return l.offset }

// Reset resets the lexer to the given byte offset. Used for parser lookahead
// / backtracking (F3.S1 savedState infrastructure).
func (l *Lexer) Reset(offset int) {
	l.offset = offset
	l.rdOffset = offset
	if offset < len(l.src) {
		r, w := utf8.DecodeRuneInString(l.src[offset:])
		l.ch = r
		l.rdOffset = offset + w
	} else {
		l.ch = eof
	}
}

// next reads the next Unicode character into l.ch.
func (l *Lexer) next() {
	if l.rdOffset >= len(l.src) {
		l.offset = len(l.src)
		l.ch = eof
		return
	}
	l.offset = l.rdOffset
	r, w := utf8.DecodeRuneInString(l.src[l.rdOffset:])
	if r == utf8.RuneError && w == 1 {
		l.error(l.offset, "invalid UTF-8 encoding")
	}
	l.rdOffset += w
	l.ch = r
}

// peek returns the next character without consuming it.
func (l *Lexer) peek() rune {
	if l.rdOffset >= len(l.src) {
		return eof
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.rdOffset:])
	return r
}

func (l *Lexer) error(offset int, msg string) {
	pos := l.file.Position(l.file.Pos(offset))
	l.errors = append(l.errors, Error{Pos: pos, Msg: msg})
}

// skipWhitespace skips spaces, tabs, newlines and // comments.
//
// The whitespace set mirrors the gold-standard grammar's WHITESPACE token
// (KqlTokens.g4): ASCII \t space \r \n \f PLUS the Unicode whitespace block
// (NBSP, BOM, and the U+2000–U+3000 / U+1680 / U+180e spaces). Pasted queries
// from rich-text editors / spreadsheets frequently carry NBSP (\u00a0) or a
// leading BOM (\ufeff), so recognising them is a real-world robustness fix,
// not a theoretical one. \u2028/\u2029 (line separators) are NOT whitespace
// per g4 (they only terminate // comments) and are left to the caller.
func (l *Lexer) skipWhitespace() {
	for {
		switch {
		case l.ch == ' ' || l.ch == '\t' || l.ch == '\r' || l.ch == '\n' || l.ch == '\f':
			l.next()
		case isUnicodeSpace(l.ch):
			l.next()
		case l.ch == '/':
			if l.peek() == '/' {
				l.scanComment()
			} else {
				return
			}
		default:
			return
		}
	}
}

// isUnicodeSpace reports whether ch is one of the non-ASCII whitespace runes in
// the g4 WHITESPACE token. Kept as an explicit list (not unicode.IsSpace) so the
// set is identical to the grammar and BOM (\ufeff) is included — unicode.IsSpace
// returns false for BOM.
func isUnicodeSpace(ch rune) bool {
	switch ch {
	case '\u00a0', // NBSP
		'\u1680', // OGHAM SPACE MARK
		'\u180e', // MONGOLIAN VOWEL SEPARATOR
		'\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006',
		'\u2007', '\u2008', '\u2009', '\u200a', '\u200b', // EN QUAD .. ZERO WIDTH SPACE
		'\u202f', // NARROW NO-BREAK SPACE
		'\u205f', // MEDIUM MATHEMATICAL SPACE
		'\u3000', // IDEOGRAPHIC SPACE
		'\ufeff': // BOM / ZERO WIDTH NO-BREAK SPACE
		return true
	}
	return false
}

// scanComment scans a // line comment.
func (l *Lexer) scanComment() {
	l.next() // first /
	l.next() // second /
	for l.ch != '\n' && l.ch != eof {
		l.next()
	}
}

// Scan scans the next token and returns it.
func (l *Lexer) Scan() Token {
	l.skipWhitespace()

	pos := l.file.Pos(l.offset)
	ch := l.ch

	// h/H-prefixed strings: must check BEFORE isLetter, else "has"/"hours"
	// would shadow h"..."/h'...'. g4 STRINGLITERAL allows optional h|H prefix.
	if ch == 'h' || ch == 'H' {
		if next := l.peek(); next == '"' || next == '\'' || next == '`' || next == '~' {
			startOffset := l.offset // remember position of the h/H prefix
			l.next()                // consume h
			if l.ch == '`' || l.ch == '~' {
				return l.scanMultiLineStringFrom(pos, startOffset)
			}
			return l.scanStringFrom(pos, startOffset, l.ch)
		}
	}

	switch {
	case isLetter(ch):
		return l.scanIdentifier(pos)
	case isDigit(ch):
		return l.scanNumber(pos)
	case ch == '"' || ch == '\'':
		return l.scanString(pos, ch)
	case ch == '@' && (l.peek() == '"' || l.peek() == '\''):
		l.next() // consume @
		return l.scanVerbatimString(pos, l.ch)
	case ch == '`' || ch == '~':
		return l.scanMultiLineString(pos)
	default:
		return l.scanOperator(pos)
	}
}

// scanIdentifier scans an identifier or keyword.
func (l *Lexer) scanIdentifier(pos token.Pos) Token {
	start := l.offset
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.next()
	}
	lit := l.src[start:l.offset]
	tok := token.Lookup(lit)

	// Hyphenated keywords (make-series, project-away, graph-shortest-paths, …).
	// g4 accepts both hyphenated and collapsed spellings; try greedily.
	if l.ch == '-' && isLetter(l.peek()) {
		savedOffset := l.offset
		savedRdOffset := l.rdOffset
		savedCh := l.ch
		bestLit := lit
		bestTok := tok
		bestOffset := l.offset
		bestRdOffset := l.rdOffset
		bestCh := l.ch

		combined := lit
		for l.ch == '-' && isLetter(l.peek()) {
			l.next() // consume -
			combined += "-"
			wordStart := l.offset
			for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
				l.next()
			}
			combined += l.src[wordStart:l.offset]
			if kwTok := token.Lookup(combined); kwTok != token.IDENT {
				bestLit = combined
				bestTok = kwTok
				bestOffset = l.offset
				bestRdOffset = l.rdOffset
				bestCh = l.ch
			}
		}

		if bestTok != token.IDENT {
			l.offset = bestOffset
			l.rdOffset = bestRdOffset
			l.ch = bestCh
			lit = bestLit
			tok = bestTok
		} else {
			// No hyphenated keyword matched; restore.
			l.offset = savedOffset
			l.rdOffset = savedRdOffset
			l.ch = savedCh
		}
	}

	// "in~" is the case-insensitive IN (g4 IN_CI: 'in~'). The base lookup
	// resolved "in" → IN; upgrade to INCI if a '~' immediately follows.
	if tok == token.IN && l.ch == '~' {
		l.next() // consume ~
		return Token{Type: token.INCI, Pos: pos, Lit: "in~"}
	}

	// "matches regex" is a two-word keyword (g4 MATCHES_REGEX).
	if lit == "matches" && l.skipSpacesAndCheck("regex") {
		return Token{Type: token.MATCHESREGEX, Pos: pos, Lit: "matches regex"}
	}

	// <typekeyword>(...) forms are a single literal token per the authoritative
	// grammar (KqlTokens.g4 DATETIMELITERAL/GUIDLITERAL/TIMESPANLITERAL/...).
	// Content is swallowed via LparenGooRparen because it is not safely
	// re-tokenisable (hyphens in GUIDs/dates, colons in timespans). See NOTES.md.
	if tok == token.DATETIMETYPE && l.ch == '(' {
		return l.scanTypeLiteral(pos, start, token.DATETIME)
	}
	if tok == token.TIMESPANTYPE && l.ch == '(' {
		return l.scanTypeLiteral(pos, start, token.TIMESPAN)
	}
	if tok == token.GUIDTYPE && l.ch == '(' {
		return l.scanTypeLiteral(pos, start, token.GUID)
	}
	if (tok == token.LONGTYPE || tok == token.INTTYPE) && l.ch == '(' {
		return l.scanTypeLiteral(pos, start, token.INT)
	}
	if tok == token.REALTYPE && l.ch == '(' {
		return l.scanTypeLiteral(pos, start, token.REAL)
	}
	if tok == token.BOOLTYPE && l.ch == '(' {
		return l.scanTypeLiteral(pos, start, token.BOOL)
	}

	// bool literals: true/True/TRUE/false/... (g4 BOOLEANLITERAL, case-folded by Lookup? no —
	// true/false are not keywords in our table; recognise directly here).
	if tok == token.IDENT {
		switch strings.ToLower(lit) {
		case "true", "false":
			return Token{Type: token.BOOL, Pos: pos, Lit: lit}
		}
	}

	return Token{Type: tok, Pos: pos, Lit: lit}
}

// scanTypeLiteral scans a <typekeyword>(...) literal as a single token.
// The identifier (keyword) has already been scanned from start..l.offset;
// l.ch is currently '('. Mirrors g4's `LparenGooRparen: '(' (~')')* ')'`.
func (l *Lexer) scanTypeLiteral(pos token.Pos, start int, typ token.Token) Token {
	// l.ch == '('; consume up to the matching ')'.
	for l.ch != ')' && l.ch != eof {
		l.next()
	}
	if l.ch == ')' {
		l.next() // consume )
	} else {
		l.error(l.offset, "unterminated type literal")
	}
	return Token{Type: typ, Pos: pos, Lit: l.src[start:l.offset]}
}

// skipSpacesAndCheck checks if the next word after optional spaces matches the
// expected string (case-insensitive). Restores state on mismatch.
func (l *Lexer) skipSpacesAndCheck(expected string) bool {
	savedOffset := l.offset
	savedRdOffset := l.rdOffset
	savedCh := l.ch

	for l.ch == ' ' || l.ch == '\t' {
		l.next()
	}
	start := l.offset
	for isLetter(l.ch) {
		l.next()
	}
	word := l.src[start:l.offset]
	if strings.EqualFold(word, expected) {
		return true
	}
	l.offset = savedOffset
	l.rdOffset = savedRdOffset
	l.ch = savedCh
	return false
}

// scanNegatedOperator scans a negated operator like !has, !contains, !between.
// The '!' has already been consumed.
func (l *Lexer) scanNegatedOperator(pos token.Pos) Token {
	start := l.offset
	for isLetter(l.ch) || l.ch == '_' {
		l.next()
	}
	keyword := l.src[start:l.offset]

	// case-sensitive variants with _cs suffix
	if l.ch == '_' {
		savedOffset := l.offset
		savedRdOffset := l.rdOffset
		savedCh := l.ch
		l.next() // consume _
		suffixStart := l.offset
		for isLetter(l.ch) {
			l.next()
		}
		if suffix := l.src[suffixStart:l.offset]; suffix == "cs" {
			keyword += "_cs"
		} else {
			l.offset = savedOffset
			l.rdOffset = savedRdOffset
			l.ch = savedCh
		}
	}

	// case-insensitive suffix ~ (e.g. !in~)
	if l.ch == '~' {
		keyword += "~"
		l.next()
	}

	fullLit := "!" + keyword
	tok := negatedOperatorLookup(keyword)
	if tok != token.ILLEGAL {
		return Token{Type: tok, Pos: pos, Lit: fullLit}
	}
	l.error(int(pos), fmt.Sprintf("unknown negated operator '!%s'", keyword))
	return Token{Type: token.ILLEGAL, Pos: pos, Lit: fullLit}
}

// negatedOperatorLookup maps a keyword (without !) to its negated token.
func negatedOperatorLookup(keyword string) token.Token {
	switch keyword {
	case "has":
		return token.NOTHAS
	case "has_cs":
		return token.NOTHASCS
	case "hasprefix":
		return token.NOTHASPREFIX
	case "hasprefix_cs":
		return token.NOTHASPREFIXCS
	case "hassuffix":
		return token.NOTHASSUFFIX
	case "hassuffix_cs":
		return token.NOTHASSUFFIXCS
	case "contains":
		return token.NOTCONTAINS
	case "contains_cs":
		return token.NOTCONTAINSCS
	case "startswith":
		return token.NOTSTARTSWITH
	case "startswith_cs":
		return token.NOTSTARTSWITCS
	case "endswith":
		return token.NOTENDSWITH
	case "endswith_cs":
		return token.NOTENDSWITHCS
	case "between":
		return token.NOTBETWEEN
	case "in":
		return token.NOTIN
	case "in~":
		return token.NOTINCI
	default:
		return token.ILLEGAL
	}
}

// Helper functions

func isLetter(ch rune) bool { return unicode.IsLetter(ch) || ch == '_' || ch == '$' }
func isDigit(ch rune) bool  { return '0' <= ch && ch <= '9' }
func isHexDigit(ch rune) bool {
	return isDigit(ch) || ('a' <= ch && ch <= 'f') || ('A' <= ch && ch <= 'F')
}
