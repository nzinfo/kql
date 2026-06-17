package diagnostic

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/frontend/token"
)

func TestListAddAndHasErrors(t *testing.T) {
	var l List
	if l.HasErrors() {
		t.Fatal("empty list should have no errors")
	}
	l.Add(Diagnostic{Severity: Warning, Code: SyntaxError, Message: "m1"})
	if l.HasErrors() {
		t.Error("warning-only list should have no errors")
	}
	l.Add(Diagnostic{Severity: Error, Code: SyntaxError, Message: "boom"})
	if !l.HasErrors() {
		t.Error("list with an Error should HasErrors")
	}
}

func TestListDedup(t *testing.T) {
	var l List
	pos := token.Position{Offset: 5, Line: 1, Column: 6}
	d := Diagnostic{Severity: Error, Code: SyntaxError, Pos: pos, Message: "same"}
	l.Add(d)
	l.Add(d) // exact dup → collapses
	l.Add(Diagnostic{Severity: Error, Code: SyntaxError, Pos: pos, Message: "different msg"}) // kept
	items := l.Items()
	if len(items) != 2 {
		t.Errorf("after dedup got %d items, want 2: %+v", len(items), items)
	}
}

func TestListSortedByPosition(t *testing.T) {
	var l List
	l.Add(Diagnostic{Severity: Error, Code: SyntaxError, Pos: token.Position{Offset: 20}, Message: "late"})
	l.Add(Diagnostic{Severity: Error, Code: SyntaxError, Pos: token.Position{Offset: 5}, Message: "early"})
	items := l.Items()
	if items[0].Pos.Offset != 5 || items[1].Pos.Offset != 20 {
		t.Errorf("not sorted by offset: %+v", items)
	}
}

func TestListErrorReturnsFirstError(t *testing.T) {
	var l List
	if err := l.Error(); err != nil {
		t.Errorf("empty list Error() = %v, want nil", err)
	}
	l.Add(Diagnostic{Severity: Warning, Code: SyntaxError, Pos: token.Position{Offset: 1}, Message: "w"})
	l.Add(Diagnostic{Severity: Error, Code: UnknownColumn, Pos: token.Position{Offset: 2}, Message: "missing col"})
	err := l.Error()
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "KQL001") {
		t.Errorf("error should reference code KQL001, got %v", err)
	}
}

func TestListRender(t *testing.T) {
	var l List
	l.Add(Diagnostic{
		Severity: Error, Code: SyntaxError,
		Pos:     token.Position{Filename: "q.kql", Offset: 0, Line: 3, Column: 5},
		Message: "bad",
	})
	lines := l.Render()
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	want := "q.kql:3:5: KQL005: bad"
	if lines[0] != want {
		t.Errorf("render = %q, want %q", lines[0], want)
	}
}

func TestSeverityString(t *testing.T) {
	if Error.String() != "error" || Warning.String() != "warning" || Info.String() != "info" {
		t.Error("severity string mismatch")
	}
}
