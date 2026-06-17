package parser

import "strings"

// letScope tracks let-bound names visible in the current scope. Used to
// recognise calls to user-defined functions (let f = (x) { ... }; f(42)) so
// they don't get flagged as unknown by the binder.
type letScope struct {
	names map[string]bool
}

func newLetScope() *letScope {
	return &letScope{names: map[string]bool{}}
}

func (s *letScope) bind(name string) {
	if s == nil {
		return
	}
	s.names[strings.ToLower(name)] = true
}

func (s *letScope) isLetBound(name string) bool {
	if s == nil {
		return false
	}
	return s.names[strings.ToLower(name)]
}

// unused import guard
var _ = strings.ToLower
