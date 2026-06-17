package token

import "fmt"

// Pos represents a byte offset in the source text.
// The zero value (NoPos) is invalid; valid positions start at 1.
type Pos int

// NoPos is the zero value for Pos, representing an invalid position.
const NoPos Pos = 0

// IsValid reports whether the position is valid.
func (p Pos) IsValid() bool { return p > 0 }

// Position describes a position in a source file including line and column.
// It is the human-readable counterpart to the compact Pos.
type Position struct {
	Filename string // Filename, if known
	Offset   int    // Byte offset, starting at 0
	Line     int    // Line number, starting at 1
	Column   int    // Column number, starting at 1 (in bytes)
}

// IsValid reports whether the position is valid.
func (p Position) IsValid() bool { return p.Line > 0 }

// String returns a string representation of the position.
// Format: "file:line:column" or "line:column" if no filename.
func (p Position) String() string {
	if p.Filename != "" {
		return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Column)
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Span represents a half-open range [Start, End) in the source text.
type Span struct {
	Start Pos // Inclusive start position
	End   Pos // Exclusive end position
}

// IsValid reports whether both start and end positions are valid and ordered.
func (s Span) IsValid() bool { return s.Start.IsValid() && s.End.IsValid() && s.Start <= s.End }

// Len returns the length of the span in bytes.
func (s Span) Len() int {
	if !s.IsValid() {
		return 0
	}
	return int(s.End - s.Start)
}

// Contains reports whether the span contains the given position.
func (s Span) Contains(p Pos) bool { return s.Start <= p && p < s.End }

// File provides line/column information for a source file by maintaining a
// mapping from byte offsets to line starts. This is the third layer of the
// go/scanner-style position abstraction (Pos ↔ File → Position).
type File struct {
	name  string
	src   string
	lines []int // Byte offsets of line starts
}

// NewFile creates a new File for the given source text.
func NewFile(name, src string) *File {
	f := &File{
		name:  name,
		src:   src,
		lines: []int{0}, // Line 1 starts at offset 0
	}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			f.lines = append(f.lines, i+1)
		}
	}
	return f
}

// Name returns the filename.
func (f *File) Name() string { return f.name }

// Size returns the size of the source in bytes.
func (f *File) Size() int { return len(f.src) }

// LineCount returns the number of lines.
func (f *File) LineCount() int { return len(f.lines) }

// Position returns the Position for the given Pos.
func (f *File) Position(p Pos) Position {
	if !p.IsValid() || int(p) > len(f.src)+1 {
		return Position{}
	}
	offset := int(p) - 1 // Pos is 1-based, offset is 0-based
	line := f.lineAt(offset)
	column := offset - f.lines[line-1] + 1
	return Position{
		Filename: f.name,
		Offset:   offset,
		Line:     line,
		Column:   column,
	}
}

// lineAt returns the 1-based line number for the given 0-based byte offset.
func (f *File) lineAt(offset int) int {
	lo, hi := 0, len(f.lines)
	for lo < hi {
		mid := (lo + hi) / 2
		if f.lines[mid] <= offset {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo // 1-based line number
}

// Pos returns the Pos for the given 0-based byte offset.
func (f *File) Pos(offset int) Pos {
	if offset < 0 || offset > len(f.src) {
		return NoPos
	}
	return Pos(offset + 1)
}

// LineStart returns the Pos of the first character on the given 1-based line.
func (f *File) LineStart(line int) Pos {
	if line < 1 || line > len(f.lines) {
		return NoPos
	}
	return Pos(f.lines[line-1] + 1)
}
