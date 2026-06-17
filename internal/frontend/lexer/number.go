package lexer

import "nzinfo/kql/internal/frontend/token"

// scanNumber scans an integer or real number literal, possibly with a timespan
// suffix. Aligned to g4 TIMESPANLITERAL / NonIntegerNumber / IntegerNumber.
//
//	timespan suffixes (g4): d/day(s), h/hour(s)/hr(s), m/min/minute(s)/ms,
//	  milli*, micro*, nano*, s/sec/second(s), tick(s)  — see NOTES.md §2.4.
//	We use a permissive "consume starting letter + trailing letters" strategy:
//	legality of the exact suffix is validated later (parser/semantic layer).
func (l *Lexer) scanNumber(pos token.Pos) Token {
	start := l.offset
	tok := token.INT

	// Hex literal: 0x1F (g4 HexPrefix HexDigit+)
	if l.ch == '0' && (l.peek() == 'x' || l.peek() == 'X') {
		l.next() // 0
		l.next() // x
		for isHexDigit(l.ch) {
			l.next()
		}
		return Token{Type: tok, Pos: pos, Lit: l.src[start:l.offset]}
	}

	// Integer part
	for isDigit(l.ch) {
		l.next()
	}

	// Integer-with-timespan suffix: 1d, 2h, 3m, 1day, ...
	if isTimespanSuffixStart(l.ch) {
		l.next()
		for isLetter(l.ch) {
			l.next()
		}
		return Token{Type: token.TIMESPAN, Pos: pos, Lit: l.src[start:l.offset]}
	}

	// Fractional part: 1.23 (only if '.' is followed by a digit; g4 requires it)
	if l.ch == '.' && isDigit(l.peek()) {
		tok = token.REAL
		l.next() // consume .
		for isDigit(l.ch) {
			l.next()
		}
		// Decimal timespan: 1.5d (g4 TimespanNumber allows the fractional part)
		if isTimespanSuffixStart(l.ch) {
			l.next()
			for isLetter(l.ch) {
				l.next()
			}
			return Token{Type: token.TIMESPAN, Pos: pos, Lit: l.src[start:l.offset]}
		}
	}

	// Exponent: 1e10, 1.23e-4 (g4 Exponent)
	if l.ch == 'e' || l.ch == 'E' {
		tok = token.REAL
		l.next()
		if l.ch == '+' || l.ch == '-' {
			l.next()
		}
		for isDigit(l.ch) {
			l.next()
		}
	}

	return Token{Type: tok, Pos: pos, Lit: l.src[start:l.offset]}
}

// isTimespanSuffixStart reports whether ch could begin a timespan suffix.
// Mirrors kqlparser isTimespanSuffix: permissive start-letter check; the full
// suffix validity is left to later layers (see NOTES.md §2.4).
func isTimespanSuffixStart(ch rune) bool {
	switch ch {
	case 'd', 'h', 'm', 's', 't':
		return true
	}
	return false
}
