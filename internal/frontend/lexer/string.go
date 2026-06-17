package lexer

import "nzinfo/kql/internal/frontend/token"

// scanString scans a quoted string literal ("..." or '...') with backslash
// escapes. Aligned to g4 STRINGLITERAL (the ('h'|'H')? prefix is handled by
// Scan before reaching here).
func (l *Lexer) scanString(pos token.Pos, quote rune) Token {
	return l.scanStringFrom(pos, l.offset, quote)
}

// scanStringFrom is like scanString but lets the caller fix the literal start
// offset (used for h/H-prefixed strings, where the prefix char precedes the
// quote and must be included in Lit).
func (l *Lexer) scanStringFrom(pos token.Pos, start int, quote rune) Token {
	l.next() // consume opening quote

	for {
		switch l.ch {
		case quote:
			l.next() // consume closing quote
			return Token{Type: token.STRING, Pos: pos, Lit: l.src[start:l.offset]}
		case '\\':
			l.next() // consume backslash
			if l.ch != eof {
				l.next() // consume escaped char
			}
		case '\n', eof:
			l.error(l.offset, "unterminated string literal")
			return Token{Type: token.STRING, Pos: pos, Lit: l.src[start:l.offset]}
		default:
			l.next()
		}
	}
}

// scanVerbatimString scans a @"..." or @'...' verbatim string.
// Per g4 STRINGLITERAL verbatim forms: doubled quote is the only escape
// (`""` → `"`), backslash is NOT processed.
func (l *Lexer) scanVerbatimString(pos token.Pos, quote rune) Token {
	start := l.offset - 1 // include the @ already consumed
	l.next()              // consume opening quote

	for {
		switch l.ch {
		case quote:
			// Doubled-quote escape?
			if l.peek() == quote {
				l.next() // first quote
				l.next() // second quote
				continue
			}
			l.next() // consume closing quote
			return Token{Type: token.STRING, Pos: pos, Lit: l.src[start:l.offset]}
		case '\n', eof:
			l.error(l.offset, "unterminated verbatim string literal")
			return Token{Type: token.STRING, Pos: pos, Lit: l.src[start:l.offset]}
		default:
			l.next()
		}
	}
}

// scanMultiLineString scans a multi-line string (``` ... ``` or ~~~ ... ~~~).
func (l *Lexer) scanMultiLineString(pos token.Pos) Token {
	return l.scanMultiLineStringFrom(pos, l.offset)
}

// scanMultiLineStringFrom scans a multi-line string starting from a specific
// offset. Used when the string has a prefix like 'h' that was already consumed.
func (l *Lexer) scanMultiLineStringFrom(pos token.Pos, start int) Token {
	delimiter := l.ch // ` or ~

	// Require triple delimiter.
	if l.ch != delimiter {
		l.error(l.offset, "expected multi-line string delimiter")
		return Token{Type: token.ILLEGAL, Pos: pos, Lit: string(l.ch)}
	}
	l.next() // 1st
	if l.ch != delimiter {
		l.error(l.offset, "expected triple delimiter for multi-line string")
		return Token{Type: token.ILLEGAL, Pos: pos, Lit: l.src[start:l.offset]}
	}
	l.next() // 2nd
	if l.ch != delimiter {
		l.error(l.offset, "expected triple delimiter for multi-line string")
		return Token{Type: token.ILLEGAL, Pos: pos, Lit: l.src[start:l.offset]}
	}
	l.next() // 3rd — now inside the string

	for {
		if l.ch == eof {
			l.error(l.offset, "unterminated multi-line string literal")
			return Token{Type: token.STRING, Pos: pos, Lit: l.src[start:l.offset]}
		}
		if l.ch == delimiter && l.peek() == delimiter {
			// Potential closing triple; confirm the third.
			savedOffset := l.offset
			l.next() // 1st delimiter
			if l.ch == delimiter {
				l.next() // 2nd
				if l.ch == delimiter {
					l.next() // 3rd — end
					return Token{Type: token.STRING, Pos: pos, Lit: l.src[start:l.offset]}
				}
			}
			// Not a triple; restore and continue scanning.
			l.offset = savedOffset
			l.rdOffset = savedOffset + 1
			l.ch = rune(l.src[savedOffset])
		}
		l.next()
	}
}
