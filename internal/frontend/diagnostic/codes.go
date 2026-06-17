package diagnostic

// Diagnostic codes. Stable identifiers (KQLxxx) used by the CLI and golden
// tests. New codes MUST be registered here — never invent ad hoc.
//
// Numbering convention:
//   KQL0xx — frontend (lexer/parser)
//   KQL1xx — binder (name resolution, types, scope)
//   KQL2xx — IR / translation
//   KQL3xx — backend / SQL emission
//   KQL9xx — runtime / execution
const (
	// LexerError covers lexical failures: invalid characters, unterminated
	// strings, bad numeric/type-literal syntax.
	LexerError Code = "KQL000"

	// SyntaxError covers parse failures: unexpected tokens, missing clauses,
	// malformed operator syntax. The most common frontend error.
	SyntaxError Code = "KQL005"

	// OperatorParamError covers malformed operator parameters, e.g. an unknown
	// `kind=` value on join, or a hint it doesn't recognise.
	OperatorParamError Code = "KQL006"

	// UnknownColumn is emitted by the binder when a column reference can't be
	// resolved in the current schema scope (F5).
	UnknownColumn Code = "KQL001"

	// TypeMismatch is emitted by the binder on type-inference conflicts (F5).
	TypeMismatch Code = "KQL002"

	// UnknownFunction is emitted by the binder for calls not in the builtin
	// table (F5/F7).
	UnknownFunction Code = "KQL003"

	// ArgCount is emitted by the binder on wrong function arity (F5).
	ArgCount Code = "KQL004"

	// ScopeError covers let-scope and shadowing issues (F5).
	ScopeError Code = "KQL007"

	// ResourceNotFound covers missing tables/databases/functions at bind time.
	ResourceNotFound Code = "KQL008"
)
