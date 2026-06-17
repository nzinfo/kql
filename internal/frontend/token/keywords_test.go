package token

import "testing"

// TestKeywordRoundTrip guards against the F4 regression where the JOIN keyword
// const existed but had no tokenStrings entry, so Lookup("join") returned IDENT.
// Every keyword must round-trip through Lookup in both lower- and upper-case
// (KQL keywords are case-insensitive, gold-standard per NOTES.md §2.1).
//
// If you add a keyword const, you MUST add its tokenStrings entry too; this
// test enforces that contract.
func TestKeywordRoundTrip(t *testing.T) {
	for tk := keywordBeg + 1; tk < keywordEnd; tk++ {
		s := tokenStrings[tk]
		if s == "" {
			t.Errorf("keyword const %d has empty tokenStrings entry (Lookup will miss it)", tk)
			continue
		}
		if got := Lookup(s); got != tk {
			t.Errorf("Lookup(%q) = %d, want %d", s, got, tk)
		}
		// case-insensitive: upper-case spelling must also resolve
		if got := Lookup(toUpperASCII(s)); got != tk {
			t.Errorf("Lookup(UPPER %q) = %d, want %d", s, got, tk)
		}
	}
}

func toUpperASCII(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		out[i] = c
	}
	return string(out)
}
