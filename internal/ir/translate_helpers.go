package ir

import (
	"fmt"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/token"
)

// sprintf is fmt.Sprintf, isolated so translate.go reads cleanly.
func sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

// posOf turns a token.Pos into a token.Position via a best-effort lookup. Since
// the translator doesn't currently carry a *token.File (it could, via the
// parser), we record the offset-only position; the CLI/binder can enrich it.
// If diags is nil or pos invalid, returns a zero Position.
func posOf(_ *diagnostic.List, pos token.Pos) token.Position {
	if !pos.IsValid() {
		return token.Position{}
	}
	return token.Position{Offset: int(pos) - 1, Line: 0, Column: 0}
}
