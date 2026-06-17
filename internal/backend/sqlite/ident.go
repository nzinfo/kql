package sqlite

import "strings"

// quoteIdent quotes a SQL identifier for SQLite using double quotes, doubling
// any embedded quotes. SQLite identifiers are case-sensitive when quoted, which
// matches KQL's identifier semantics. This avoids collisions with SQL reserved
// words that KQL allows as names (order, count, group, …).
func quoteIdent(name string) string {
	// Replace embedded " with "" and wrap. Empty name → "" (valid placeholder).
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
