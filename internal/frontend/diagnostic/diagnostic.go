// Package diagnostic provides structured error/warning reporting for the KQL
// frontend (lexer/parser/binder). Diagnostics carry a stable code (KQLxxx),
// a severity, a source position, a message, and optional fix suggestions.
//
// Design (F6): the frontend never panics on bad input — it records diagnostics
// and keeps going, so a single run can surface many errors. Callers inspect
// List.HasErrors() before trusting the produced AST.
package diagnostic

import (
	"fmt"
	"sort"

	"nzinfo/kql/internal/frontend/token"
)

// Severity ranks a diagnostic's importance. Higher = more severe.
type Severity int

// Severities.
const (
	Info Severity = iota
	Warning
	Error
)

// String returns a human-readable severity name.
func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Info:
		return "info"
	default:
		return "unknown"
	}
}

// Code is a stable diagnostic identifier (e.g. "KQL005"). Centralised in
// codes.go so codes are never invented ad hoc — see the Codes constants.
type Code string

// Diagnostic is a single structured message. Pos carries the source location;
// Suggestions are optional "did you mean" hints for the CLI to render.
type Diagnostic struct {
	Severity    Severity
	Code        Code
	Pos         token.Position
	Message     string
	Suggestions []string // optional fix suggestions
}

// List is an append-only collection of diagnostics with helpers for error
// checking, deduplication, and rendering.
type List struct {
	items []Diagnostic
	// deduped tracks whether the list has been sorted/deduped; further Add
	// calls re-dirty it.
	deduped bool
}

// Add appends a diagnostic.
func (l *List) Add(d Diagnostic) {
	l.items = append(l.items, d)
	l.deduped = false
}

// Items returns the diagnostics, sorted by (Pos, severity-desc, code) after
// deduplication (identical diagnostics collapse to one).
func (l *List) Items() []Diagnostic {
	l.normalise()
	return l.items
}

// Len returns the number of diagnostics.
func (l *List) Len() int { return len(l.items) }

// HasErrors reports whether any diagnostic has Severity Error.
func (l *List) HasErrors() bool {
	for _, d := range l.items {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Has reports whether the list is non-empty.
func (l *List) Has() bool { return len(l.items) > 0 }

// Error implements the error interface, returning the first error diagnostic
// (or a summary). Returns nil if there are no error-severity diagnostics.
func (l *List) Error() error {
	l.normalise()
	var first *Diagnostic
	for i := range l.items {
		if l.items[i].Severity == Error {
			first = &l.items[i]
			break
		}
	}
	if first == nil {
		return nil
	}
	return fmtError(first)
}

// normalise sorts the list by position and drops exact duplicates.
func (l *List) normalise() {
	if l.deduped {
		return
	}
	sort.SliceStable(l.items, func(i, j int) bool {
		a, b := l.items[i], l.items[j]
		if a.Pos.Offset != b.Pos.Offset {
			return a.Pos.Offset < b.Pos.Offset
		}
		// higher severity first at the same position
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		return a.Code < b.Code
	})
	// dedupe exact (Pos, Code, Message) duplicates.
	out := l.items[:0]
	for i, d := range l.items {
		if i > 0 && d.Pos.Offset == l.items[i-1].Pos.Offset && d.Code == l.items[i-1].Code && d.Message == l.items[i-1].Message {
			continue
		}
		out = append(out, d)
	}
	l.items = out
	l.deduped = true
}

// Render returns the diagnostics formatted as CLI-style lines:
//
//	file:line:col: KQL005: message
//
// one per diagnostic. Intended for CLI output (F6.S4); library callers may
// inspect Items() directly for structured access.
func (l *List) Render() []string {
	l.normalise()
	out := make([]string, 0, len(l.items))
	for _, d := range l.items {
		out = append(out, fmt.Sprintf("%s: %s: %s", d.Pos, d.Code, d.Message))
	}
	return out
}

func fmtError(d *Diagnostic) error {
	return fmt.Errorf("%s: %s: %s", d.Pos, d.Code, d.Message)
}
