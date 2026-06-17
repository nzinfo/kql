package ast

// BaseVisitor provides no-op defaults for every node type, so concrete
// visitors implement only the kinds they care about by embedding BaseVisitor
// and overriding specific VisitXxx methods.
//
// Visit dispatches to the type-specific VisitXxx based on the concrete node.
// Override Visit to intercept all nodes (e.g. for collecting positions).
type BaseVisitor struct{}

// Visit is the Visitor interface entry point. The default implementation
// dispatches to a type-specific method; callers embed BaseVisitor and override
// the methods they need, plus optionally Visit for cross-cutting concerns.
func (v *BaseVisitor) Visit(node Node) (w Visitor) {
	switch n := node.(type) {
	case *Script:
		v.VisitScript(n)
	case *QueryStmt:
		v.VisitQueryStmt(n)
	case *LetStmt:
		v.VisitLetStmt(n)
	case *ExprStmt:
		v.VisitExprStmt(n)
	case *SetStmt:
		v.VisitSetStmt(n)
	case *DeclareStmt:
		v.VisitDeclareStmt(n)
	case *Pipeline:
		v.VisitPipeline(n)
	case *BasicLit:
		v.VisitBasicLit(n)
	case *DynamicLit:
		v.VisitDynamicLit(n)
	case *Ident:
		v.VisitIdent(n)
	case *StarExpr:
		v.VisitStarExpr(n)
	case *NamedExpr:
		v.VisitNamedExpr(n)
	case *BinaryExpr:
		v.VisitBinaryExpr(n)
	case *UnaryExpr:
		v.VisitUnaryExpr(n)
	case *ParenExpr:
		v.VisitParenExpr(n)
	case *CallExpr:
		v.VisitCallExpr(n)
	case *SelectorExpr:
		v.VisitSelectorExpr(n)
	case *IndexExpr:
		v.VisitIndexExpr(n)
	case *ListExpr:
		v.VisitListExpr(n)
	case *BetweenExpr:
		v.VisitBetweenExpr(n)
	case *ConditionalExpr:
		v.VisitConditionalExpr(n)
	case *CastExpr:
		v.VisitCastExpr(n)
	case *WhereOp:
		v.VisitWhereOp(n)
	case *ProjectOp:
		v.VisitProjectOp(n)
	case *ExtendOp:
		v.VisitExtendOp(n)
	case *TakeOp:
		v.VisitTakeOp(n)
	case *SortOp:
		v.VisitSortOp(n)
	case *SummarizeOp:
		v.VisitSummarizeOp(n)
	case *JoinOp:
		v.VisitJoinOp(n)
	case *UnionOp:
		v.VisitUnionOp(n)
	case *DistinctOp:
		v.VisitDistinctOp(n)
	case *CountOp:
		v.VisitCountOp(n)
	case *TopOp:
		v.VisitTopOp(n)
	case *AsOp:
		v.VisitAsOp(n)
	case *InvokeOp:
		v.VisitInvokeOp(n)
	case *Bad:
		v.VisitBad(n)
	case *BadExpr:
		v.VisitBadExpr(n)
	}
	return v
}

// All VisitXxx default to no-ops. Concrete visitors embed BaseVisitor and
// override only what they need.

// VisitScript visits a Script.
func (v *BaseVisitor) VisitScript(*Script) {}

// VisitQueryStmt visits a QueryStmt.
func (v *BaseVisitor) VisitQueryStmt(*QueryStmt) {}

// VisitLetStmt visits a LetStmt.
func (v *BaseVisitor) VisitLetStmt(*LetStmt) {}

// VisitExprStmt visits an ExprStmt.
func (v *BaseVisitor) VisitExprStmt(*ExprStmt) {}

// VisitSetStmt visits a SetStmt.
func (v *BaseVisitor) VisitSetStmt(*SetStmt) {}

// VisitDeclareStmt visits a DeclareStmt.
func (v *BaseVisitor) VisitDeclareStmt(*DeclareStmt) {}

// VisitPipeline visits a Pipeline.
func (v *BaseVisitor) VisitPipeline(*Pipeline) {}

// VisitBasicLit visits a BasicLit.
func (v *BaseVisitor) VisitBasicLit(*BasicLit) {}

// VisitDynamicLit visits a DynamicLit.
func (v *BaseVisitor) VisitDynamicLit(*DynamicLit) {}

// VisitIdent visits an Ident.
func (v *BaseVisitor) VisitIdent(*Ident) {}

// VisitStarExpr visits a StarExpr.
func (v *BaseVisitor) VisitStarExpr(*StarExpr) {}

// VisitNamedExpr visits a NamedExpr.
func (v *BaseVisitor) VisitNamedExpr(*NamedExpr) {}

// VisitBinaryExpr visits a BinaryExpr.
func (v *BaseVisitor) VisitBinaryExpr(*BinaryExpr) {}

// VisitUnaryExpr visits a UnaryExpr.
func (v *BaseVisitor) VisitUnaryExpr(*UnaryExpr) {}

// VisitParenExpr visits a ParenExpr.
func (v *BaseVisitor) VisitParenExpr(*ParenExpr) {}

// VisitCallExpr visits a CallExpr.
func (v *BaseVisitor) VisitCallExpr(*CallExpr) {}

// VisitSelectorExpr visits a SelectorExpr.
func (v *BaseVisitor) VisitSelectorExpr(*SelectorExpr) {}

// VisitIndexExpr visits an IndexExpr.
func (v *BaseVisitor) VisitIndexExpr(*IndexExpr) {}

// VisitListExpr visits a ListExpr.
func (v *BaseVisitor) VisitListExpr(*ListExpr) {}

// VisitBetweenExpr visits a BetweenExpr.
func (v *BaseVisitor) VisitBetweenExpr(*BetweenExpr) {}

// VisitConditionalExpr visits a ConditionalExpr.
func (v *BaseVisitor) VisitConditionalExpr(*ConditionalExpr) {}

// VisitCastExpr visits a CastExpr.
func (v *BaseVisitor) VisitCastExpr(*CastExpr) {}

// VisitWhereOp visits a WhereOp.
func (v *BaseVisitor) VisitWhereOp(*WhereOp) {}

// VisitProjectOp visits a ProjectOp.
func (v *BaseVisitor) VisitProjectOp(*ProjectOp) {}

// VisitExtendOp visits an ExtendOp.
func (v *BaseVisitor) VisitExtendOp(*ExtendOp) {}

// VisitTakeOp visits a TakeOp.
func (v *BaseVisitor) VisitTakeOp(*TakeOp) {}

// VisitSortOp visits a SortOp.
func (v *BaseVisitor) VisitSortOp(*SortOp) {}

// VisitSummarizeOp visits a SummarizeOp.
func (v *BaseVisitor) VisitSummarizeOp(*SummarizeOp) {}

// VisitJoinOp visits a JoinOp.
func (v *BaseVisitor) VisitJoinOp(*JoinOp) {}

// VisitUnionOp visits a UnionOp.
func (v *BaseVisitor) VisitUnionOp(*UnionOp) {}

// VisitDistinctOp visits a DistinctOp.
func (v *BaseVisitor) VisitDistinctOp(*DistinctOp) {}

// VisitCountOp visits a CountOp.
func (v *BaseVisitor) VisitCountOp(*CountOp) {}

// VisitTopOp visits a TopOp.
func (v *BaseVisitor) VisitTopOp(*TopOp) {}

// VisitAsOp visits an AsOp.
func (v *BaseVisitor) VisitAsOp(*AsOp) {}

// VisitInvokeOp visits an InvokeOp.
func (v *BaseVisitor) VisitInvokeOp(*InvokeOp) {}

// VisitBad visits a Bad node.
func (v *BaseVisitor) VisitBad(*Bad) {}

// VisitBadExpr visits a BadExpr.
func (v *BaseVisitor) VisitBadExpr(*BadExpr) {}
