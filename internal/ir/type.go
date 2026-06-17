package ir

// Type enumerates KQL scalar types. Mirrors rust-kql's enum Type (ast.rs:9-97),
// including Decimal which the original I1 design had omitted.
type Type int

// Scalar types.
const (
	TypeUnknown Type = iota
	TypeBool
	TypeInt
	TypeLong
	TypeReal
	TypeDecimal
	TypeString
	TypeDateTime
	TypeTimeSpan
	TypeDynamic
)

// String returns the KQL type name.
func (t Type) String() string {
	switch t {
	case TypeBool:
		return "bool"
	case TypeInt:
		return "int"
	case TypeLong:
		return "long"
	case TypeReal:
		return "real"
	case TypeDecimal:
		return "decimal"
	case TypeString:
		return "string"
	case TypeDateTime:
		return "datetime"
	case TypeTimeSpan:
		return "timespan"
	case TypeDynamic:
		return "dynamic"
	default:
		return "unknown"
	}
}

// IsNumeric reports whether the type is numeric (used by the cost model and
// type inference for arithmetic operators).
func (t Type) IsNumeric() bool {
	switch t {
	case TypeInt, TypeLong, TypeReal, TypeDecimal, TypeTimeSpan:
		return true
	}
	return false
}
