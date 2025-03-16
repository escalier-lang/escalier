package codegen

import "github.com/escalier-lang/escalier/internal/parser"

func TransformExprs(exprs []*parser.Expr) []*Expr {
	var res []*Expr
	for _, e := range exprs {
		res = append(res, TransformExpr(e))
	}
	return res
}

func TransformIdentifier(ident *parser.Identifier) *Identifier {
	if ident == nil {
		return nil
	}
	return &Identifier{
		Name:   ident.Name,
		span:   nil,
		source: ident,
	}
}

func TransformStmt(stmt *parser.Stmt) *Stmt {
	var kind StmtKind

	switch s := stmt.Kind.(type) {
	case *parser.SExpr:
		kind = &SExpr{
			Expr: TransformExpr(s.Expr),
		}
	case *parser.SDecl:
		kind = &SDecl{
			Decl: TransformDecl(s.Decl),
		}
	case *parser.SReturn:
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

func TransformModule(mod *parser.Module) *Module {
	var stmts []*Stmt
	for _, s := range mod.Stmts {
		stmts = append(stmts, TransformStmt(s))
	}
	return &Module{
		Stmts: stmts,
	}
}

func TransformStmts(stmts []*parser.Stmt) []*Stmt {
	var res []*Stmt
	for _, s := range stmts {
		res = append(res, TransformStmt(s))
	}
	return res
}

func TransformDecl(decl *parser.Decl) *Decl {
	var kind DeclKind

	switch d := decl.Kind.(type) {
	case *parser.DVariable:
		kind = &DVariable{
			Kind: VariableKind(d.Kind),
			Name: TransformIdentifier(d.Name),
			Init: TransformExpr(d.Init),
		}
	case *parser.DFunction:
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

func TransformExpr(expr *parser.Expr) *Expr {
	var kind ExprKind

	switch e := expr.Kind.(type) {
	case *parser.ENumber:
		kind = &ENumber{Value: e.Value}
	case *parser.EBinary:
		kind = &EBinary{
			Left:  TransformExpr(e.Left),
			Op:    BinaryOp(e.Op),
			Right: TransformExpr(e.Right),
		}
	case *parser.EUnary:
		kind = &EUnary{
			Op:  UnaryOp(e.Op),
			Arg: TransformExpr(e.Arg),
		}
	case *parser.EString:
		kind = &EString{
			Value: e.Value,
		}
	case *parser.EIdentifier:
		kind = &EIdentifier{
			Name: e.Name,
		}
	case *parser.ECall:
		kind = &ECall{
			Callee:   TransformExpr(e.Callee),
			Args:     TransformExprs(e.Args),
			OptChain: e.OptChain,
		}
	case *parser.EIndex:
		kind = &EIndex{
			Object:   TransformExpr(e.Object),
			Index:    TransformExpr(e.Index),
			OptChain: e.OptChain,
		}
	case *parser.EMember:
		kind = &EMember{
			Object:   TransformExpr(e.Object),
			Prop:     TransformIdentifier(e.Prop),
			OptChain: e.OptChain,
		}
	case *parser.EArray:
		kind = &EArray{
			Elems: TransformExprs(e.Elems),
		}
	case *parser.EIgnore:
		panic("TODO")
	case *parser.EEmpty:
		panic("TODO")
	}

	return &Expr{
		Kind:   kind,
		span:   nil,
		source: expr,
	}
}
