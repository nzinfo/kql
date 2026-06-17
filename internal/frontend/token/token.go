// Package token defines KQL lexical token types and source positions.
//
// Token kinds are grouped into contiguous ranges (literal/operator/keyword)
// using sentinel constants (literalBeg..literalEnd, …) so that IsLiteral /
// IsOperator / IsKeyword can be range checks — the go/scanner style, mirrored
// from the cloudygreybeard/kqlparser reference (Apache-2.0).
package token

import "strconv"

// Token represents a lexical token type.
type Token int

// Token constants. Order matters: literals, operators and keywords each occupy
// a contiguous block bounded by the xxxBeg / xxxEnd sentinels so the IsXxx
// predicates below are simple range checks.
const (
	// Special tokens
	ILLEGAL Token = iota
	EOF
	COMMENT

	literalBeg
	// Literals
	IDENT    // identifier
	INT      // 123, 0x1F
	REAL     // 1.23, 1.23e10
	STRING   // "abc", 'abc', @"abc"
	DATETIME // datetime(2023-01-01)
	TIMESPAN // 1d, 2h, 3m, timespan(1.02:03:04)
	GUID     // guid(...)
	BOOL     // true, false
	DYNAMIC  // dynamic([1,2,3])
	TYPE     // typeof(string)
	literalEnd

	operatorBeg
	// Operators and delimiters
	ADD // +
	SUB // -
	MUL // *
	QUO // /
	REM // %

	EQL    // ==
	NEQ    // != or <>
	LSS    // <
	GTR    // >
	LEQ    // <=
	GEQ    // >=
	TILDE  // =~
	NTILDE // !~

	PIPE         // |
	ASSIGN       // =
	COLON        // :
	SEMI         // ;
	COMMA        // ,
	DOT          // .
	DOTDOT       // ..
	ARROW        // =>
	DASHDASH     // --   (graph: undirected edge)        [reserved, MVP unused]
	DASHGT       // -->  (graph: directed edge forward)  [reserved, MVP unused]
	LTDASH       // <--  (graph: directed edge backward) [reserved, MVP unused]
	DASHLBRACK   // -[   (graph: edge name start)        [reserved, MVP unused]
	LTDASHLBRACK // <-[  (graph: edge name start back)   [reserved, MVP unused]
	RBRACKDASH   // ]-   (graph: edge name end)          [reserved, MVP unused]
	RBRACKDASHGT // ]->  (graph: edge name end forward)  [reserved, MVP unused]

	LPAREN   // (
	RPAREN   // )
	LBRACKET // [
	RBRACKET // ]
	LBRACE   // {
	RBRACE   // }
	operatorEnd

	keywordBeg
	// Keywords - Query operators
	AS
	BY
	CONSUME
	COUNT
	DISTINCT
	EVALUATE
	EXTEND
	FACET
	FILTER
	FIND
	FORK
	GETSCHEMA
	INVOKE
	ASSERTSCHEMA
	EXECUTEANDCACHE
	GRAPHMATCH
	GRAPHMARKCOMPONENTS
	GRAPHSHORTESTPATHS
	GRAPHTOTABLE
	GRAPHWHEREEDGES
	GRAPHWHERENODES
	JOIN
	MACROEXPAND
	PARTITIONBY
	LIMIT
	LOOKUP
	MAKEGRAPH
	MAKESERIES
	MVAPPLY
	MVEXPAND
	ORDER
	PARSE
	PARSEKV
	PARSEWHERE
	PARTITION
	PRINT
	PROJECT
	PROJECTAWAY
	PROJECTKEEP
	PROJECTRENAME
	PROJECTREORDER
	PROJECTSMART
	RANGE
	REDUCE
	RENDER
	SAMPLE
	SAMPLEDISTINCT
	SCAN
	SEARCH
	SERIALIZE
	SORT
	SUMMARIZE
	TAKE
	TOP
	TOPHITTERS
	TOPNESTED
	UNION
	WHERE

	// Keywords - Statements
	ALIAS
	DECLARE
	ENTITYGROUP
	LET
	PATTERN
	RESTRICT
	SET

	// Keywords - Clauses
	ACCESS
	ASC
	BETWEEN
	DATABASE
	DATASCOPE
	DATATABLE
	DEFAULT
	DESC
	EDGES
	EXTERNALDATA
	FIRST
	FROM
	HOTCACHE
	IN
	KIND
	LAST
	MATERIALIZE
	NODES
	NOOPTIMIZATION
	NULLS
	OF
	ON
	PARTITIONEDBY
	STEP
	TO
	TOSCALAR
	TOTABLE
	VIEW
	WITHNODEID
	WITH
	WITHSOURCE

	// Keywords - Logical
	AND
	NOT
	OR

	// Keywords - Types
	BOOLTYPE
	DATETIMETYPE
	DECIMALTYPE
	DYNAMICTYPE
	GUIDTYPE
	INTTYPE
	LONGTYPE
	REALTYPE
	STRINGTYPE
	TIMESPANTYPE

	// Keywords - String operators (positive)
	CONTAINS
	CONTAINSCS
	ENDSWITH
	ENDSWITHCS
	HAS
	HASALL
	HASANY
	HASCS
	HASPREFIX
	HASPREFIXCS
	HASSUFFIX
	HASSUFFIXCS
	LIKE
	LIKECS
	MATCHESREGEX
	STARTSWITH
	STARTSWITHCS

	// Keywords - String operators (negated)
	NOTCONTAINS    // !contains
	NOTCONTAINSCS  // !contains_cs
	NOTENDSWITH    // !endswith
	NOTENDSWITHCS  // !endswith_cs
	NOTHAS         // !has
	NOTHASCS       // !has_cs
	NOTHASPREFIX   // !hasprefix
	NOTHASPREFIXCS // !hasprefix_cs
	NOTHASSUFFIX   // !hassuffix
	NOTHASSUFFIXCS // !hassuffix_cs
	NOTLIKE        // notlike
	NOTLIKECS      // notlikecs
	NOTSTARTSWITH  // !startswith
	NOTSTARTSWITCS // !startswith_cs

	// Keywords - Negated list/range operators
	NOTBETWEEN // !between
	NOTIN      // !in
	NOTINCI    // !in~ (case-insensitive)
	INCI       // in~ (case-insensitive) — g4 IN_CI

	// Keywords - Misc
	CLUSTER
	NULL
	PACK
	TYPEOF
	keywordEnd
)

// tokenStrings maps each Token to its canonical textual form. Literal and
// keyword entries use lowercase KQL spelling; operator entries use the symbol.
var tokenStrings = [...]string{
	ILLEGAL: "ILLEGAL",
	EOF:     "EOF",
	COMMENT: "COMMENT",

	IDENT:    "IDENT",
	INT:      "INT",
	REAL:     "REAL",
	STRING:   "STRING",
	DATETIME: "DATETIME",
	TIMESPAN: "TIMESPAN",
	GUID:     "GUID",
	BOOL:     "BOOL",
	DYNAMIC:  "DYNAMIC",
	TYPE:     "TYPE",

	ADD: "+",
	SUB: "-",
	MUL: "*",
	QUO: "/",
	REM: "%",

	EQL:    "==",
	NEQ:    "!=",
	LSS:    "<",
	GTR:    ">",
	LEQ:    "<=",
	GEQ:    ">=",
	TILDE:  "=~",
	NTILDE: "!~",

	PIPE:         "|",
	ASSIGN:       "=",
	COLON:        ":",
	SEMI:         ";",
	COMMA:        ",",
	DOT:          ".",
	DOTDOT:       "..",
	ARROW:        "=>",
	DASHDASH:     "--",
	DASHGT:       "-->",
	LTDASH:       "<--",
	DASHLBRACK:   "-[",
	LTDASHLBRACK: "<-[",
	RBRACKDASH:   "]-",
	RBRACKDASHGT: "]->",

	LPAREN:   "(",
	RPAREN:   ")",
	LBRACKET: "[",
	RBRACKET: "]",
	LBRACE:   "{",
	RBRACE:   "}",

	AS:                  "as",
	BY:                  "by",
	CONSUME:             "consume",
	COUNT:               "count",
	DISTINCT:            "distinct",
	EVALUATE:            "evaluate",
	EXTEND:              "extend",
	FACET:               "facet",
	FILTER:              "filter",
	FIND:                "find",
	FORK:                "fork",
	GETSCHEMA:           "getschema",
	INVOKE:              "invoke",
	ASSERTSCHEMA:        "assert-schema",
	EXECUTEANDCACHE:     "execute-and-cache",
	GRAPHMATCH:          "graph-match",
	GRAPHMARKCOMPONENTS: "graph-mark-components",
	GRAPHSHORTESTPATHS:  "graph-shortest-paths",
	GRAPHTOTABLE:        "graph-to-table",
	GRAPHWHEREEDGES:     "graph-where-edges",
	GRAPHWHERENODES:     "graph-where-nodes",
	MACROEXPAND:         "macro-expand",
	PARTITIONBY:         "__partitionby",
	LIMIT:               "limit",
	LOOKUP:              "lookup",
	MAKEGRAPH:           "make-graph",
	MAKESERIES:          "make-series",
	MVAPPLY:             "mv-apply",
	MVEXPAND:            "mv-expand",
	ORDER:               "order",
	PARSE:               "parse",
	PARSEKV:             "parse-kv",
	PARSEWHERE:          "parse-where",
	PARTITION:           "partition",
	PRINT:               "print",
	PROJECT:             "project",
	JOIN:                "join",
	PROJECTAWAY:         "project-away",
	PROJECTKEEP:         "project-keep",
	PROJECTRENAME:       "project-rename",
	PROJECTREORDER:      "project-reorder",
	PROJECTSMART:        "project-smart",
	RANGE:               "range",
	REDUCE:              "reduce",
	RENDER:              "render",
	SAMPLE:              "sample",
	SAMPLEDISTINCT:      "sample-distinct",
	SCAN:                "scan",
	SEARCH:              "search",
	SERIALIZE:           "serialize",
	SORT:                "sort",
	SUMMARIZE:           "summarize",
	TAKE:                "take",
	TOP:                 "top",
	TOPHITTERS:          "top-hitters",
	TOPNESTED:           "top-nested",
	UNION:               "union",
	WHERE:               "where",
	ALIAS:               "alias",
	DECLARE:             "declare",
	LET:                 "let",
	PATTERN:             "pattern",
	ENTITYGROUP:         "entity_group",
	RESTRICT:            "restrict",
	SET:                 "set",
	ACCESS:              "access",
	ASC:                 "asc",
	BETWEEN:             "between",
	DATABASE:            "database",
	DATASCOPE:           "datascope",
	DATATABLE:           "datatable",
	DEFAULT:             "default",
	DESC:                "desc",
	EDGES:               "edges",
	EXTERNALDATA:        "externaldata",
	FIRST:               "first",
	FROM:                "from",
	HOTCACHE:            "hotcache",
	IN:                  "in",
	KIND:                "kind",
	LAST:                "last",
	MATERIALIZE:         "materialize",
	NODES:               "nodes",
	NOOPTIMIZATION:      "nooptimization",
	NULLS:               "nulls",
	OF:                  "of",
	ON:                  "on",
	PARTITIONEDBY:       "partitionedby",
	STEP:                "step",
	TO:                  "to",
	TOSCALAR:            "toscalar",
	TOTABLE:             "totable",
	WITHNODEID:          "with_node_id",
	VIEW:                "view",
	WITH:                "with",
	WITHSOURCE:          "withsource",
	AND:                 "and",
	NOT:                 "not",
	OR:                  "or",
	BOOLTYPE:            "bool",
	DATETIMETYPE:        "datetime",
	DECIMALTYPE:         "decimal",
	DYNAMICTYPE:         "dynamic",
	GUIDTYPE:            "guid",
	INTTYPE:             "int",
	LONGTYPE:            "long",
	REALTYPE:            "real",
	STRINGTYPE:          "string",
	TIMESPANTYPE:        "timespan",
	CONTAINS:            "contains",
	CONTAINSCS:          "contains_cs",
	ENDSWITH:            "endswith",
	ENDSWITHCS:          "endswith_cs",
	HAS:                 "has",
	HASALL:              "has_all",
	HASANY:              "has_any",
	HASCS:               "has_cs",
	HASPREFIX:           "hasprefix",
	HASPREFIXCS:         "hasprefix_cs",
	HASSUFFIX:           "hassuffix",
	HASSUFFIXCS:         "hassuffix_cs",
	LIKE:                "like",
	LIKECS:              "likecs",
	MATCHESREGEX:        "matches regex",
	STARTSWITH:          "startswith",
	STARTSWITHCS:        "startswith_cs",

	// Negated string operators
	NOTCONTAINS:    "!contains",
	NOTCONTAINSCS:  "!contains_cs",
	NOTENDSWITH:    "!endswith",
	NOTENDSWITHCS:  "!endswith_cs",
	NOTHAS:         "!has",
	NOTHASCS:       "!has_cs",
	NOTHASPREFIX:   "!hasprefix",
	NOTHASPREFIXCS: "!hasprefix_cs",
	NOTHASSUFFIX:   "!hassuffix",
	NOTHASSUFFIXCS: "!hassuffix_cs",
	NOTLIKE:        "notlike",
	NOTLIKECS:      "notlikecs",
	NOTSTARTSWITH:  "!startswith",
	NOTSTARTSWITCS: "!startswith_cs",

	// Negated list/range operators
	NOTBETWEEN: "!between",
	NOTIN:      "!in",
	NOTINCI:    "!in~",
	INCI:       "in~",

	CLUSTER: "cluster",
	NULL:    "null",
	PACK:    "pack",
	TYPEOF:  "typeof",
}

// String returns the canonical string representation of the token.
func (t Token) String() string {
	if 0 <= t && int(t) < len(tokenStrings) {
		if s := tokenStrings[t]; s != "" {
			return s
		}
	}
	return "token(" + strconv.Itoa(int(t)) + ")"
}

// IsLiteral reports whether the token is a literal.
func (t Token) IsLiteral() bool { return literalBeg < t && t < literalEnd }

// IsOperator reports whether the token is an operator or delimiter.
func (t Token) IsOperator() bool { return operatorBeg < t && t < operatorEnd }

// IsKeyword reports whether the token is a keyword.
func (t Token) IsKeyword() bool { return keywordBeg < t && t < keywordEnd }

// Precedence returns the binary-operator precedence for t (0 = not binary).
// Matches KQL semantics: logical < comparison < additive < multiplicative.
func (t Token) Precedence() int {
	switch t {
	case OR:
		return 1
	case AND:
		return 2
	case EQL, NEQ, LSS, GTR, LEQ, GEQ, TILDE, NTILDE,
		// Positive string operators
		CONTAINS, CONTAINSCS, STARTSWITH, STARTSWITHCS,
		ENDSWITH, ENDSWITHCS, HAS, HASCS, HASALL, HASANY,
		HASPREFIX, HASPREFIXCS, HASSUFFIX, HASSUFFIXCS,
		LIKE, LIKECS, MATCHESREGEX,
		// Negated string operators
		NOTCONTAINS, NOTCONTAINSCS, NOTSTARTSWITH, NOTSTARTSWITCS,
		NOTENDSWITH, NOTENDSWITHCS, NOTHAS, NOTHASCS,
		NOTHASPREFIX, NOTHASPREFIXCS, NOTHASSUFFIX, NOTHASSUFFIXCS,
		NOTLIKE, NOTLIKECS,
		// List/range operators
		BETWEEN, NOTBETWEEN, IN, INCI, NOTIN, NOTINCI:
		return 3
	case ADD, SUB:
		return 4
	case MUL, QUO, REM:
		return 5
	default:
		return 0
	}
}
