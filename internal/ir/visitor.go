package ir

import "reflect"

// Visitor walks an IR tree. Walk dispatches on concrete node types and recurses
// into children. Unknown node types are skipped so adding nodes doesn't break
// existing visitors until they opt in.
type Visitor interface {
	Visit(node Node) (w Visitor)
}

// isNilPointer reports whether node is a typed-nil pointer (e.g. (*Pipeline)(nil)
// wrapped in the Node interface). Such values are non-nil interfaces but
// represent absent children; Walk must skip them to avoid deref panics.
func isNilPointer(node Node) bool {
	if node == nil {
		return true
	}
	v := reflect.ValueOf(node)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

// Walk traverses the IR depth-first, calling v.Visit for each node and
// recursing with the returned visitor. A nil node yields no call.
//
// NOTE: typed-nil pointers (e.g. a *Pipeline field that is nil) are treated as
// absent — checking `node == nil` alone misses them because a nil *T wrapped in
// an interface is a non-nil interface value.
func Walk(v Visitor, node Node) {
	if node == nil || v == nil || isNilPointer(node) {
		return
	}
	w := v.Visit(node)
	if w == nil {
		return
	}
	switch n := node.(type) {
	case *Pipeline:
		Walk(w, n.Source)
		for _, s := range n.Stages {
			Walk(w, s)
		}

	case *SourceTable: // leaf (Columns are data, not nodes)
	case *SourceDatatable:
		for _, row := range n.Rows {
			for _, e := range row {
				Walk(w, e)
			}
		}
	case *SourcePrint:
		for _, c := range n.Cols {
			Walk(w, c)
		}
	case *SourceRange:
		Walk(w, n.From)
		Walk(w, n.To)
		Walk(w, n.Step)

	case *Filter:
		Walk(w, n.Predicate)
	case *Project:
		for _, c := range n.Cols {
			Walk(w, c)
		}
	case *Extend:
		for _, c := range n.Cols {
			Walk(w, c)
		}
	case *Aggregate:
		for _, a := range n.Aggregates {
			Walk(w, a)
		}
		for _, k := range n.Keys {
			Walk(w, k)
		}
	case *Join:
		Walk(w, n.Right)
		for _, c := range n.On {
			Walk(w, c)
		}
	case *Sort:
		for _, k := range n.Keys {
			Walk(w, k.Expr)
		}
	case *Limit:
		Walk(w, n.Count)
	case *Union:
		for _, in := range n.Inputs {
			Walk(w, in)
		}
	case *Distinct:
		for _, c := range n.Cols {
			Walk(w, c)
		}

	case *NamedExpr:
		Walk(w, n.Expr)
	case *Lit: // leaf
	case *Col: // leaf
	case *Star: // leaf
	case *BinOp:
		Walk(w, n.X)
		Walk(w, n.Y)
	case *UnaryOp:
		Walk(w, n.X)
	case *FuncCall:
		for _, a := range n.Args {
			Walk(w, a)
		}
	case *Member:
		Walk(w, n.X)
	case *Index:
		Walk(w, n.X)
		Walk(w, n.Index)
	case *Case:
		Walk(w, n.Cond)
		Walk(w, n.Then)
		Walk(w, n.Else)
	}
}

// BaseVisitor provides no-op Visit so concrete visitors implement only the
// node kinds they care about (embed BaseVisitor, override VisitXxx).
type BaseVisitor struct{}

// Visit dispatches to the type-specific VisitXxx. Override to intercept all.
func (v *BaseVisitor) Visit(node Node) Visitor {
	switch n := node.(type) {
	case *Pipeline:
		v.VisitPipeline(n)
	case *SourceTable:
		v.VisitSourceTable(n)
	case *SourceDatatable:
		v.VisitSourceDatatable(n)
	case *SourcePrint:
		v.VisitSourcePrint(n)
	case *SourceRange:
		v.VisitSourceRange(n)
	case *Filter:
		v.VisitFilter(n)
	case *Project:
		v.VisitProject(n)
	case *Extend:
		v.VisitExtend(n)
	case *Aggregate:
		v.VisitAggregate(n)
	case *Join:
		v.VisitJoin(n)
	case *Sort:
		v.VisitSort(n)
	case *Limit:
		v.VisitLimit(n)
	case *Union:
		v.VisitUnion(n)
	case *Distinct:
		v.VisitDistinct(n)
	case *NamedExpr:
		v.VisitNamedExpr(n)
	case *Lit:
		v.VisitLit(n)
	case *Col:
		v.VisitCol(n)
	case *Star:
		v.VisitStar(n)
	case *BinOp:
		v.VisitBinOp(n)
	case *UnaryOp:
		v.VisitUnaryOp(n)
	case *FuncCall:
		v.VisitFuncCall(n)
	case *Member:
		v.VisitMember(n)
	case *Index:
		v.VisitIndex(n)
	case *Case:
		v.VisitCase(n)
	}
	return v
}

// All VisitXxx default to no-ops.
func (v *BaseVisitor) VisitPipeline(*Pipeline)       {}
func (v *BaseVisitor) VisitSourceTable(*SourceTable) {}
func (v *BaseVisitor) VisitSourceDatatable(*SourceDatatable) {}
func (v *BaseVisitor) VisitSourcePrint(*SourcePrint) {}
func (v *BaseVisitor) VisitSourceRange(*SourceRange) {}
func (v *BaseVisitor) VisitFilter(*Filter)           {}
func (v *BaseVisitor) VisitProject(*Project)         {}
func (v *BaseVisitor) VisitExtend(*Extend)           {}
func (v *BaseVisitor) VisitAggregate(*Aggregate)     {}
func (v *BaseVisitor) VisitJoin(*Join)               {}
func (v *BaseVisitor) VisitSort(*Sort)               {}
func (v *BaseVisitor) VisitLimit(*Limit)             {}
func (v *BaseVisitor) VisitUnion(*Union)             {}
func (v *BaseVisitor) VisitDistinct(*Distinct)       {}
func (v *BaseVisitor) VisitNamedExpr(*NamedExpr)     {}
func (v *BaseVisitor) VisitLit(*Lit)                 {}
func (v *BaseVisitor) VisitCol(*Col)                 {}
func (v *BaseVisitor) VisitStar(*Star)               {}
func (v *BaseVisitor) VisitBinOp(*BinOp)             {}
func (v *BaseVisitor) VisitUnaryOp(*UnaryOp)         {}
func (v *BaseVisitor) VisitFuncCall(*FuncCall)       {}
func (v *BaseVisitor) VisitMember(*Member)           {}
func (v *BaseVisitor) VisitIndex(*Index)             {}
func (v *BaseVisitor) VisitCase(*Case)               {}
