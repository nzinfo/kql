package ast

// Visitor walks an AST. The Walk function dispatches to the appropriate Visit
// method based on the concrete node type. Visit is called on entry; a non-nil
// return value replaces the visitor used for that node's children.
//
// Design mirrors go/ast.Visitor (adapted from the cloudygreybeard/kqlparser
// template). BaseVisitor (in visit_base.go) provides no-op defaults so callers
// implement only the node kinds they care about.
type Visitor interface {
	Visit(node Node) (w Visitor)
}

// Walk traverses the AST in depth-first order, calling v.Visit for each node
// and recursing into children with the returned visitor. A nil node yields no
// call. Unknown node types are silently skipped so adding a node type does not
// break existing visitors until they opt in.
func Walk(v Visitor, node Node) {
	if node == nil {
		return
	}
	if v == nil {
		return
	}
	w := v.Visit(node)
	if w == nil {
		return
	}

	switch n := node.(type) {
	// Statements / top-level
	case *Script:
		for _, s := range n.Statements {
			Walk(w, s)
		}
	case *QueryStmt:
		Walk(w, n.Pipeline)
	case *LetStmt:
		Walk(w, n.Name)
		Walk(w, n.Expr)
	case *ExprStmt:
		Walk(w, n.Expr)

	// Pipeline
	case *Pipeline:
		Walk(w, n.Source)
		for _, op := range n.Ops {
			Walk(w, op)
		}

	// Literals & references
	case *BasicLit: // leaf
	case *DynamicLit:
		Walk(w, n.Value)
	case *Ident: // leaf
	case *StarExpr: // leaf
	case *NamedExpr:
		Walk(w, n.Name)
		for _, nm := range n.Names {
			Walk(w, nm)
		}
		Walk(w, n.Expr)

	// Expressions
	case *BinaryExpr:
		Walk(w, n.X)
		Walk(w, n.Y)
	case *UnaryExpr:
		Walk(w, n.X)
	case *ParenExpr:
		Walk(w, n.X)
	case *CallExpr:
		Walk(w, n.Fun)
		for _, a := range n.Args {
			Walk(w, a)
		}
	case *SelectorExpr:
		Walk(w, n.X)
		Walk(w, n.Sel)
	case *IndexExpr:
		Walk(w, n.X)
		Walk(w, n.Index)
	case *ListExpr:
		for _, e := range n.Elems {
			Walk(w, e)
		}
	case *BetweenExpr:
		Walk(w, n.X)
		Walk(w, n.Low)
		Walk(w, n.High)
	case *ConditionalExpr:
		Walk(w, n.Cond)
		Walk(w, n.Then)
		Walk(w, n.Else)
	case *CastExpr:
		Walk(w, n.X)

	// Operators (P0)
	case *WhereOp:
		Walk(w, n.Predicate)
	case *ProjectOp:
		for _, c := range n.Columns {
			Walk(w, c)
		}
	case *ExtendOp:
		for _, c := range n.Columns {
			Walk(w, c)
		}
	case *TakeOp:
		Walk(w, n.Count)
	case *SortOp:
		for _, o := range n.Orders {
			Walk(w, o.Expr)
		}
	case *SummarizeOp:
		for _, a := range n.Aggregates {
			Walk(w, a)
		}
		for _, g := range n.GroupBy {
			Walk(w, g)
		}
	case *JoinOp:
		Walk(w, n.Right)
		for _, c := range n.OnExpr {
			Walk(w, c)
		}
	case *UnionOp:
		for _, t := range n.Tables {
			Walk(w, t)
		}
	case *DistinctOp:
		for _, c := range n.Columns {
			Walk(w, c)
		}
	case *CountOp: // leaf
	case *TopOp:
		Walk(w, n.Count)
		for _, o := range n.Orders {
			Walk(w, o.Expr)
		}

	case *Bad, *BadExpr: // leaves
		// no children
	}
}
