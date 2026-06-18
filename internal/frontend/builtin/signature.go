// Package builtin — function signatures (F7.S1).
//
// Signature is the rich metadata structure for a builtin function: named
// parameters, return type, and kind (scalar/aggregate/window). It complements
// the existing Spec (which carries emit templates + arity). The Signature is
// used by type inference (F5.S4) and the future PhysicalPlanner to decide
// whether a function needs a UDF, PostProc, or can inline as SQL.
package builtin

import "nzinfo/kql/internal/ir"

// FuncKind classifies a function's execution model.
type FuncKind int

const (
	KindScalar    FuncKind = iota // pure scalar (abs, tostring, ...)
	KindAggregate                 // row-reducing (count, sum, ...)
	KindWindow                    // windowed (series_*, next, prev)
)

// Param is one function parameter.
type Param struct {
	Name       string
	Type       ir.Type // TypeUnknown = any type
	IsOptional bool
}

// Signature is the rich metadata for a builtin function. It carries typed
// parameter info + return type + kind — more detailed than Spec (which tracks
// arity + emit templates). Signatures are registered in a table and looked up
// by name for type inference and capability decisions.
type Signature struct {
	Name       string
	Params     []Param
	ReturnType ir.Type
	IsVariadic bool
	Kind       FuncKind
}

// sigTable is the registry of known signatures. Populated by init() with the
// high-frequency functions that have unambiguous signatures. Functions not in
// this table fall back to the Spec-based lookup (MinArgs/MaxArgs).
var sigTable = map[string]*Signature{}

func init() {
	register := func(s *Signature) { sigTable[s.Name] = s }

	// --- Aggregates ---
	register(&Signature{Name: "count", ReturnType: ir.TypeLong, Kind: KindAggregate})
	register(&Signature{Name: "countif", Params: []Param{{Name: "pred", Type: ir.TypeBool}}, ReturnType: ir.TypeLong, Kind: KindAggregate})
	register(&Signature{Name: "dcount", Params: []Param{{Name: "expr"}, {Name: "accuracy", IsOptional: true}}, ReturnType: ir.TypeLong, Kind: KindAggregate})
	register(&Signature{Name: "sum", Params: []Param{{Name: "expr", Type: ir.TypeLong}}, ReturnType: ir.TypeLong, Kind: KindAggregate})
	register(&Signature{Name: "avg", Params: []Param{{Name: "expr", Type: ir.TypeReal}}, ReturnType: ir.TypeReal, Kind: KindAggregate})
	register(&Signature{Name: "min", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeUnknown, Kind: KindAggregate})
	register(&Signature{Name: "max", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeUnknown, Kind: KindAggregate})
	register(&Signature{Name: "percentile", Params: []Param{{Name: "expr"}, {Name: "p", Type: ir.TypeReal}}, ReturnType: ir.TypeReal, Kind: KindAggregate})
	register(&Signature{Name: "stdev", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeReal, Kind: KindAggregate})
	register(&Signature{Name: "variance", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeReal, Kind: KindAggregate})
	register(&Signature{Name: "make_set", Params: []Param{{Name: "expr"}, {Name: "maxSize", IsOptional: true}}, ReturnType: ir.TypeDynamic, Kind: KindAggregate})
	register(&Signature{Name: "make_list", Params: []Param{{Name: "expr"}, {Name: "maxSize", IsOptional: true}}, ReturnType: ir.TypeDynamic, Kind: KindAggregate})

	// --- Scalar: string ---
	register(&Signature{Name: "tostring", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeString})
	register(&Signature{Name: "strcat", Params: []Param{{Name: "args"}}, ReturnType: ir.TypeString, IsVariadic: true})
	register(&Signature{Name: "substring", Params: []Param{{Name: "s", Type: ir.TypeString}, {Name: "start", Type: ir.TypeLong}, {Name: "length", Type: ir.TypeLong, IsOptional: true}}, ReturnType: ir.TypeString})
	register(&Signature{Name: "tolower", Params: []Param{{Name: "s", Type: ir.TypeString}}, ReturnType: ir.TypeString})
	register(&Signature{Name: "toupper", Params: []Param{{Name: "s", Type: ir.TypeString}}, ReturnType: ir.TypeString})
	register(&Signature{Name: "strlen", Params: []Param{{Name: "s", Type: ir.TypeString}}, ReturnType: ir.TypeLong})
	register(&Signature{Name: "split", Params: []Param{{Name: "s", Type: ir.TypeString}, {Name: "delim", Type: ir.TypeString}}, ReturnType: ir.TypeDynamic})
	register(&Signature{Name: "trim", Params: []Param{{Name: "s", Type: ir.TypeString}}, ReturnType: ir.TypeString})
	register(&Signature{Name: "replace_string", Params: []Param{{Name: "s", Type: ir.TypeString}, {Name: "find", Type: ir.TypeString}, {Name: "replace", Type: ir.TypeString}}, ReturnType: ir.TypeString})
	register(&Signature{Name: "extract", Params: []Param{{Name: "s", Type: ir.TypeString}, {Name: "regex", Type: ir.TypeString}, {Name: "group", Type: ir.TypeLong, IsOptional: true}}, ReturnType: ir.TypeString})

	// --- Scalar: conversion ---
	register(&Signature{Name: "toint", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeInt})
	register(&Signature{Name: "tolong", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeLong})
	register(&Signature{Name: "toreal", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "tobool", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeBool})
	register(&Signature{Name: "tohex", Params: []Param{{Name: "n", Type: ir.TypeLong}}, ReturnType: ir.TypeString})

	// --- Scalar: conditional ---
	register(&Signature{Name: "iff", Params: []Param{{Name: "cond", Type: ir.TypeBool}, {Name: "then"}, {Name: "else"}}, ReturnType: ir.TypeUnknown})
	register(&Signature{Name: "iif", Params: []Param{{Name: "cond", Type: ir.TypeBool}, {Name: "then"}, {Name: "else"}}, ReturnType: ir.TypeUnknown})
	register(&Signature{Name: "coalesce", Params: []Param{{Name: "args"}}, ReturnType: ir.TypeUnknown, IsVariadic: true})
	register(&Signature{Name: "isnull", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeBool})
	register(&Signature{Name: "isnotnull", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeBool})
	register(&Signature{Name: "isempty", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeBool})
	register(&Signature{Name: "isnotempty", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeBool})

	// --- Scalar: datetime ---
	register(&Signature{Name: "now", ReturnType: ir.TypeDateTime})
	register(&Signature{Name: "ago", Params: []Param{{Name: "timespan", Type: ir.TypeTimeSpan}}, ReturnType: ir.TypeDateTime})
	register(&Signature{Name: "bin", Params: []Param{{Name: "expr"}, {Name: "roundTo"}, {Name: "offset", IsOptional: true}}, ReturnType: ir.TypeUnknown})
	register(&Signature{Name: "year", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}}, ReturnType: ir.TypeInt})
	register(&Signature{Name: "month", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}}, ReturnType: ir.TypeInt})
	register(&Signature{Name: "dayofmonth", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}}, ReturnType: ir.TypeInt})
	register(&Signature{Name: "dayofweek", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}}, ReturnType: ir.TypeInt})
	register(&Signature{Name: "dayofyear", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}}, ReturnType: ir.TypeInt})
	register(&Signature{Name: "hour", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}}, ReturnType: ir.TypeInt})
	register(&Signature{Name: "startofday", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}, {Name: "offset", IsOptional: true}}, ReturnType: ir.TypeDateTime})
	register(&Signature{Name: "startofmonth", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}, {Name: "offset", IsOptional: true}}, ReturnType: ir.TypeDateTime})
	register(&Signature{Name: "format_datetime", Params: []Param{{Name: "dt", Type: ir.TypeDateTime}, {Name: "format", Type: ir.TypeString}}, ReturnType: ir.TypeString})

	// --- Scalar: math ---
	register(&Signature{Name: "abs", Params: []Param{{Name: "expr"}}, ReturnType: ir.TypeUnknown})
	register(&Signature{Name: "sqrt", Params: []Param{{Name: "x", Type: ir.TypeReal}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "pow", Params: []Param{{Name: "x", Type: ir.TypeReal}, {Name: "y", Type: ir.TypeReal}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "exp", Params: []Param{{Name: "x", Type: ir.TypeReal}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "log", Params: []Param{{Name: "x", Type: ir.TypeReal}, {Name: "base", Type: ir.TypeReal, IsOptional: true}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "floor", Params: []Param{{Name: "x", Type: ir.TypeReal}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "ceiling", Params: []Param{{Name: "x", Type: ir.TypeReal}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "round", Params: []Param{{Name: "x", Type: ir.TypeReal}, {Name: "digits", Type: ir.TypeLong, IsOptional: true}}, ReturnType: ir.TypeReal})
	register(&Signature{Name: "sign", Params: []Param{{Name: "x", Type: ir.TypeReal}}, ReturnType: ir.TypeInt})

	// --- Scalar: dynamic/JSON ---
	register(&Signature{Name: "parse_json", Params: []Param{{Name: "s", Type: ir.TypeString}}, ReturnType: ir.TypeDynamic})
	register(&Signature{Name: "dynamic", Params: []Param{{Name: "json", Type: ir.TypeString}}, ReturnType: ir.TypeDynamic})
	register(&Signature{Name: "array_length", Params: []Param{{Name: "arr", Type: ir.TypeDynamic}}, ReturnType: ir.TypeLong})

	// --- Scalar: misc ---
	register(&Signature{Name: "column_ifexists", Params: []Param{{Name: "name", Type: ir.TypeString}, {Name: "default"}}, ReturnType: ir.TypeUnknown})
	register(&Signature{Name: "between", Params: []Param{{Name: "value"}, {Name: "lower"}, {Name: "upper"}}, ReturnType: ir.TypeBool})
	register(&Signature{Name: "hash_sha256", Params: []Param{{Name: "s", Type: ir.TypeString}}, ReturnType: ir.TypeString})
	register(&Signature{Name: "new_guid", ReturnType: ir.TypeString})
}

// LookupSignature returns the Signature for a function name (case-insensitive),
// or nil if not found.
func LookupSignature(name string) *Signature {
	if s, ok := sigTable[normalize(name)]; ok {
		return s
	}
	return nil
}
