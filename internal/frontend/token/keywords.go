package token

import "strings"

// keywords maps canonical lowercase keyword spellings to their token types.
// Built once at init. KQL keywords are case-insensitive (a property of the
// authoritative grammar, Kusto-Query-Language/grammar/Kql.g4), so Lookup
// lowercases its input before consulting this map.
//
// Only spellings that actually appear in Kusto-Query-Language/grammar/KqlTokens.g4
// are registered. Note that g4 defines *collapsed* (no-hyphen) alternatives
// only for MVAPPLY ('mvapply') and MVEXPAND ('mvexpand') — NOT for
// make-series/make-graph/etc., which the kqlparser template incorrectly added.
// We follow the gold standard here. See internal/frontend/NOTES.md §2.
var keywords map[string]Token

func init() {
	keywords = make(map[string]Token, keywordEnd-keywordBeg)
	for t := keywordBeg + 1; t < keywordEnd; t++ {
		keywords[tokenStrings[t]] = t
	}
	// Collapsed/alternate spellings accepted by g4 KqlTokens.g4.
	// Only these have non-hyphenated alternatives in the grammar:
	keywords["mvapply"] = MVAPPLY
	keywords["mvexpand"] = MVEXPAND
	keywords["assertschema"] = ASSERTSCHEMA
	keywords["executeandcache"] = EXECUTEANDCACHE
	keywords["execute_and_cache"] = EXECUTEANDCACHE
	keywords["macroexpand"] = MACROEXPAND
	keywords["__partitionby"] = PARTITIONBY
	keywords["external_data"] = EXTERNALDATA
	keywords["with_source"] = WITHSOURCE
	// Type aliases accepted by g4 (BOOLEAN, DATE, TIME, INT64).
	keywords["boolean"] = BOOLTYPE
	keywords["date"] = DATETIMETYPE
	keywords["time"] = TIMESPANTYPE
	keywords["int64"] = LONGTYPE

	// Legacy "notcontains"/"notcontainscs" (without leading !) per g4
	// NOTCONTAINS / NOTCONTAINSCS.
	keywords["notcontains"] = NOTCONTAINS
	keywords["notcontainscs"] = NOTCONTAINSCS
}

// Lookup returns the token type for the given identifier.
// KQL keywords are case-insensitive — "WHERE", "where", "Where" are all the
// WHERE keyword (aligned with the authoritative Kusto-Query-Language grammar).
// Returns IDENT if ident is not a keyword.
//
// Note: this deliberately improves on the cloudygreybeard/kqlparser reference,
// whose Lookup did exact-case matching and thus failed to recognise upper- or
// mixed-case keyword spellings.
func Lookup(ident string) Token {
	if tok, ok := keywords[strings.ToLower(ident)]; ok {
		return tok
	}
	return IDENT
}
