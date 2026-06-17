package lexer

import (
	"fmt"

	"nzinfo/kql/internal/frontend/token"
)

// scanOperator scans an operator or delimiter. Punctuation tokens map 1:1 to
// the authoritative Kusto-Query-Language/grammar/KqlTokens.g4 punctuation
// section. Multi-char operators are matched greedily (longest match first).
func (l *Lexer) scanOperator(pos token.Pos) Token {
	ch := l.ch
	l.next()

	switch ch {
	case eof:
		return Token{Type: token.EOF, Pos: pos}
	case '+':
		return Token{Type: token.ADD, Pos: pos, Lit: "+"}
	case '-':
		switch l.ch {
		case '-':
			l.next()
			if l.ch == '>' {
				l.next()
				return Token{Type: token.DASHGT, Pos: pos, Lit: "-->"}
			}
			return Token{Type: token.DASHDASH, Pos: pos, Lit: "--"}
		case '[':
			l.next()
			return Token{Type: token.DASHLBRACK, Pos: pos, Lit: "-["}
		}
		return Token{Type: token.SUB, Pos: pos, Lit: "-"}
	case '*':
		return Token{Type: token.MUL, Pos: pos, Lit: "*"}
	case '/':
		return Token{Type: token.QUO, Pos: pos, Lit: "/"}
	case '%':
		return Token{Type: token.REM, Pos: pos, Lit: "%"}
	case '|':
		return Token{Type: token.PIPE, Pos: pos, Lit: "|"}
	case ':':
		return Token{Type: token.COLON, Pos: pos, Lit: ":"}
	case ';':
		return Token{Type: token.SEMI, Pos: pos, Lit: ";"}
	case ',':
		return Token{Type: token.COMMA, Pos: pos, Lit: ","}
	case '(':
		return Token{Type: token.LPAREN, Pos: pos, Lit: "("}
	case ')':
		return Token{Type: token.RPAREN, Pos: pos, Lit: ")"}
	case '[':
		return Token{Type: token.LBRACKET, Pos: pos, Lit: "["}
	case ']':
		if l.ch == '-' {
			l.next()
			if l.ch == '>' {
				l.next()
				return Token{Type: token.RBRACKDASHGT, Pos: pos, Lit: "]->"}
			}
			return Token{Type: token.RBRACKDASH, Pos: pos, Lit: "]-"}
		}
		return Token{Type: token.RBRACKET, Pos: pos, Lit: "]"}
	case '{':
		return Token{Type: token.LBRACE, Pos: pos, Lit: "{"}
	case '}':
		return Token{Type: token.RBRACE, Pos: pos, Lit: "}"}
	case '.':
		if l.ch == '.' {
			l.next()
			return Token{Type: token.DOTDOT, Pos: pos, Lit: ".."}
		}
		return Token{Type: token.DOT, Pos: pos, Lit: "."}
	case '=':
		switch l.ch {
		case '=':
			l.next()
			return Token{Type: token.EQL, Pos: pos, Lit: "=="}
		case '~':
			l.next()
			return Token{Type: token.TILDE, Pos: pos, Lit: "=~"}
		case '>':
			l.next()
			return Token{Type: token.ARROW, Pos: pos, Lit: "=>"}
		}
		return Token{Type: token.ASSIGN, Pos: pos, Lit: "="}
	case '!':
		switch l.ch {
		case '=':
			l.next()
			return Token{Type: token.NEQ, Pos: pos, Lit: "!="}
		case '~':
			l.next()
			return Token{Type: token.NTILDE, Pos: pos, Lit: "!~"}
		}
		// Negated operators: !has, !contains, !between, !in, etc.
		if isLetter(l.ch) {
			return l.scanNegatedOperator(pos)
		}
		l.error(l.offset-1, "unexpected character '!'")
		return Token{Type: token.ILLEGAL, Pos: pos, Lit: "!"}
	case '<':
		switch l.ch {
		case '=':
			l.next()
			return Token{Type: token.LEQ, Pos: pos, Lit: "<="}
		case '>':
			l.next()
			return Token{Type: token.NEQ, Pos: pos, Lit: "<>"}
		case '-':
			l.next() // consume -
			switch l.ch {
			case '-':
				l.next()
				return Token{Type: token.LTDASH, Pos: pos, Lit: "<--"}
			case '[':
				l.next()
				return Token{Type: token.LTDASHLBRACK, Pos: pos, Lit: "<-["}
			}
			// Bare <- is not a valid token; report and recover.
			l.error(l.offset, "unexpected '<-'")
			return Token{Type: token.ILLEGAL, Pos: pos, Lit: "<-"}
		}
		return Token{Type: token.LSS, Pos: pos, Lit: "<"}
	case '>':
		if l.ch == '=' {
			l.next()
			return Token{Type: token.GEQ, Pos: pos, Lit: ">="}
		}
		return Token{Type: token.GTR, Pos: pos, Lit: ">"}
	default:
		l.error(l.offset-1, fmt.Sprintf("unexpected character %q", ch))
		return Token{Type: token.ILLEGAL, Pos: pos, Lit: string(ch)}
	}
}
