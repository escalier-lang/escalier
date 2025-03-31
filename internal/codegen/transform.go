package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

func TransformExprs(exprs []ast.Expr) []*Expr {
	var res []*Expr
	for _, e := range exprs {
		res = append(res, TransformExpr(e))
	}
	return res
}

func TransformIdentifier(ident *ast.Ident) *Identifier {
	if ident == nil {
		return nil
	}
	return &Identifier{
		Name:   ident.Name,
		span:   nil,
		source: ident,
	}
}

func TransformPattern(pattern ast.Pat) Pat {
	switch p := pattern.(type) {
	case *ast.IdentPat:
		return &IdentPat{
			Name:   p.Name,
			span:   nil,
			source: p,
		}
	default:
		panic("TODO")
	}
}

func TransformStmt(stmt ast.Stmt) *Stmt {
	var kind StmtKind

	switch s := stmt.(type) {
	case *ast.ExprStmt:
		kind = &SExpr{
			Expr: TransformExpr(s.Expr),
		}
	case *ast.DeclStmt:
		kind = &SDecl{
			Decl: TransformDecl(s.Decl),
		}
	case *ast.ReturnStmt:
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

func TransformStmts(stmts []ast.Stmt) []*Stmt {
	var res []*Stmt
	for _, s := range stmts {
		res = append(res, TransformStmt(s))
	}
	return res
}

func TransformDecl(decl ast.Decl) *Decl {
	var kind DeclKind

	switch d := decl.(type) {
	case *ast.VarDecl:
		kind = &DVariable{
			Kind:    VariableKind(d.Kind),
			Pattern: TransformPattern(d.Pattern),
			Init:    TransformExpr(d.Init),
		}
	case *ast.FuncDecl:
		var params []*Param
		for _, p := range d.Params {
			params = append(params, &Param{
				Pattern: TransformPattern(p.Pattern),
			})
		}
		kind = &DFunction{
			Name:   TransformIdentifier(d.Name),
			Params: params,
			Body:   TransformStmts(d.Body.Stmts),
		}
	}

	return &Decl{
		Kind:    kind,
		Declare: decl.Declare(),
		Export:  decl.Export(),
		span:    nil,
		source:  decl,
	}
}

func TransformExpr(expr ast.Expr) *Expr {
	var kind ExprKind

	switch e := expr.(type) {
	case *ast.LiteralExpr:
		switch lit := e.Lit.(type) {
		case *ast.BoolLit:
			panic("TODO: bool literal")
		case *ast.NumLit:
			kind = &ENumber{Value: lit.Value}
		case *ast.StrLit:
			kind = &EString{Value: lit.Value}
		case *ast.BigIntLit:
			panic("TODO: big int literal")
		case *ast.NullLit:
			panic("TODO: null literal")
		case *ast.UndefinedLit:
			panic("TODO: undefined literal")
		}
	case *ast.BinaryExpr:
		kind = &EBinary{
			Left:  TransformExpr(e.Left),
			Op:    BinaryOp(e.Op),
			Right: TransformExpr(e.Right),
		}
	case *ast.UnaryExpr:
		kind = &EUnary{
			Op:  UnaryOp(e.Op),
			Arg: TransformExpr(e.Arg),
		}
	case *ast.IdentExpr:
		kind = &EIdentifier{
			Name: e.Name,
		}
	case *ast.CallExpr:
		kind = &ECall{
			Callee:   TransformExpr(e.Callee),
			Args:     TransformExprs(e.Args),
			OptChain: e.OptChain,
		}
	case *ast.IndexExpr:
		kind = &EIndex{
			Object:   TransformExpr(e.Object),
			Index:    TransformExpr(e.Index),
			OptChain: e.OptChain,
		}
	case *ast.MemberExpr:
		kind = &EMember{
			Object:   TransformExpr(e.Object),
			Prop:     TransformIdentifier(e.Prop),
			OptChain: e.OptChain,
		}
	case *ast.TupleExpr:
		kind = &EArray{
			Elems: TransformExprs(e.Elems),
		}
	case *ast.IgnoreExpr:
		panic("TODO")
	case *ast.EmptyExpr:
		panic("TODO")
	}

	return &Expr{
		Kind:   kind,
		span:   nil,
		source: expr,
	}
}
