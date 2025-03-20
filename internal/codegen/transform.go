package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

func TransformExprs(exprs []*ast.Expr) []*Expr {
	var res []*Expr
	for _, e := range exprs {
		res = append(res, TransformExpr(e))
	}
	return res
}

func TransformIdentifier(ident *ast.Identifier) *Identifier {
	if ident == nil {
		return nil
	}
	return &Identifier{
		Name:   ident.Name,
		span:   nil,
		source: ident,
	}
}

func TransformStmt(stmt *ast.Stmt) *Stmt {
	var kind StmtKind

	switch s := stmt.Kind.(type) {
	case *ast.SExpr:
		kind = &SExpr{
			Expr: TransformExpr(s.Expr),
		}
	case *ast.SDecl:
		kind = &SDecl{
			Decl: TransformDecl(s.Decl),
		}
	case *ast.SReturn:
		kind = &SReturn{
			Expr: TransformExpr(s.Expr),
		}
	}

	return &Stmt{
		Kind:   kind,
		span:   nil,
		source: stmt,
	}
}

func TransformModule(mod *ast.Module) *Module {
	var stmts []*Stmt
	for _, s := range mod.Stmts {
		stmts = append(stmts, TransformStmt(s))
	}
	return &Module{
		Stmts: stmts,
	}
}

func TransformStmts(stmts []*ast.Stmt) []*Stmt {
	var res []*Stmt
	for _, s := range stmts {
		res = append(res, TransformStmt(s))
	}
	return res
}

func TransformDecl(decl *ast.Decl) *Decl {
	var kind DeclKind

	switch d := decl.Kind.(type) {
	case *ast.DVariable:
		kind = &DVariable{
			Kind: VariableKind(d.Kind),
			Name: TransformIdentifier(d.Name),
			Init: TransformExpr(d.Init),
		}
	case *ast.DFunction:
		var params []*Param
		for _, p := range d.Params {
			params = append(params, &Param{
				Name: TransformIdentifier(p.Name),
			})
		}
		kind = &DFunction{
			Name:   TransformIdentifier(d.Name),
			Params: params,
			Body:   TransformStmts(d.Body),
		}
	}

	return &Decl{
		Kind:    kind,
		Declare: decl.Declare,
		Export:  decl.Export,
		span:    nil,
		source:  decl,
	}
}

func TransformExpr(expr *ast.Expr) *Expr {
	var kind ExprKind

	switch e := expr.Kind.(type) {
	case *ast.ENumber:
		kind = &ENumber{Value: e.Value}
	case *ast.EBinary:
		kind = &EBinary{
			Left:  TransformExpr(e.Left),
			Op:    BinaryOp(e.Op),
			Right: TransformExpr(e.Right),
		}
	case *ast.EUnary:
		kind = &EUnary{
			Op:  UnaryOp(e.Op),
			Arg: TransformExpr(e.Arg),
		}
	case *ast.EString:
		kind = &EString{
			Value: e.Value,
		}
	case *ast.EIdentifier:
		kind = &EIdentifier{
			Name: e.Name,
		}
	case *ast.ECall:
		kind = &ECall{
			Callee:   TransformExpr(e.Callee),
			Args:     TransformExprs(e.Args),
			OptChain: e.OptChain,
		}
	case *ast.EIndex:
		kind = &EIndex{
			Object:   TransformExpr(e.Object),
			Index:    TransformExpr(e.Index),
			OptChain: e.OptChain,
		}
	case *ast.EMember:
		kind = &EMember{
			Object:   TransformExpr(e.Object),
			Prop:     TransformIdentifier(e.Prop),
			OptChain: e.OptChain,
		}
	case *ast.EArray:
		kind = &EArray{
			Elems: TransformExprs(e.Elems),
		}
	case *ast.EIgnore:
		panic("TODO")
	case *ast.EEmpty:
		panic("TODO")
	}

	return &Expr{
		Kind:   kind,
		span:   nil,
		source: expr,
	}
}
