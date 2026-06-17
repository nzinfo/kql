package main

import (
	"io"

	"nzinfo/kql/internal/ir"
)

// printIR renders an IR Pipeline as an indented tree to w. Used by `kql explain`.
// Delegates to the library-level ir.Print (I4.S1 — extracted from cmd/kql so
// any importer of ir can pretty-print a pipeline).
func printIR(w io.Writer, pipe *ir.Pipeline) {
	ir.Print(w, pipe)
}
