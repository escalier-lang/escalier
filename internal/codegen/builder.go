package codegen

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

type Builder struct {
	tempId int
}

func (b *Builder) NewTempId() string {
	b.tempId += 1
	return "temp" + strconv.Itoa(b.tempId)
}

func (b *Builder) buildExprs(exprs []ast.Expr) ([]Expr, []Stmt) {
	outStmts := []Stmt{}
	outExprs := make([]Expr, len(exprs))
	for i, e := range exprs {
		expr, stmts := b.buildExpr(e)
		outExprs[i] = expr
		outStmts = slices.Concat(outStmts, stmts)
	}
	return outExprs, outStmts
}

func buildIdent(ident *ast.Ident) *Identifier {
	if ident == nil {
		return nil
	}
	return &Identifier{
		Name:   ident.Name,
		span:   nil,
		source: ident,
	}
}

type Pair[T, U any] struct {
	First  T
	Second U
}

// TODO: dedupe with checker/infer.go
func Zip[T, U any](ts []T, us []U) []Pair[T, U] {
	if len(ts) != len(us) {
		panic("slices have different length")
	}
	pairs := make([]Pair[T, U], len(ts))
	for i := range ts {
		pairs[i] = Pair[T, U]{ts[i], us[i]}
	}
	return pairs
}

func (b *Builder) buildPattern(p ast.Pat, target Expr, export bool) ([]Expr, []Stmt) {

	var checks []Expr
	var stmts []Stmt

	var buildPatternRec func(p ast.Pat, target Expr) Pat

	buildPatternRec = func(p ast.Pat, target Expr) Pat {
		switch p := p.(type) {
		case *ast.IdentPat:
			var defExpr Expr
			if p.Default != nil {
				var defStmts []Stmt
				defExpr, defStmts = b.buildExpr(p.Default)
				stmts = slices.Concat(stmts, defStmts)
			}
			return &IdentPat{
				Name:    p.Name,
				Default: defExpr,
				span:    nil,
				source:  p,
			}
		case *ast.ObjectPat:
			var elems []ObjPatElem
			for _, elem := range p.Elems {
				checks = append(checks,
					NewBinaryExpr(
						NewUnaryExpr(TypeOf, target, nil),
						EqualEqual,
						NewLitExpr(NewStrLit("object", nil), nil),
						nil,
					),
				)

				switch e := elem.(type) {
				case *ast.ObjKeyValuePat:
					var newTarget Expr
					if target != nil {
						newTarget = NewMemberExpr(
							target,
							NewIdentifier(e.Key.Name, e), // TODO: replace with Prop
							false,
							nil,
						)
					}

					var defExpr Expr
					if e.Default != nil {
						var defStmts []Stmt
						defExpr, defStmts = b.buildExpr(e.Default)
						stmts = slices.Concat(stmts, defStmts)
					}
					elems = append(elems, NewObjKeyValuePat(
						e.Key.Name,
						buildPatternRec(e.Value, newTarget),
						defExpr,
						e,
					))
				case *ast.ObjShorthandPat:
					var defExpr Expr
					if e.Default != nil {
						var defStmts []Stmt
						defExpr, defStmts = b.buildExpr(e.Default)
						stmts = slices.Concat(stmts, defStmts)
					}
					elems = append(elems, NewObjShorthandPat(
						e.Key.Name,
						defExpr,
						e,
					))
				case *ast.ObjRestPat:
					elems = append(elems, NewObjRestPat(
						buildPatternRec(e.Pattern, target),
						e,
					))
				}
			}
			return NewObjectPat(elems, p)
		case *ast.TuplePat:
			// TODO: replace with Prop
			length := NewIdentifier("length", nil)

			checks = append(
				checks,
				NewBinaryExpr(
					NewMemberExpr(target, length, false, nil),
					EqualEqual,
					NewLitExpr(NewNumLit(float64(len(p.Elems)), nil), nil),
					nil,
				),
			)

			var elems []Pat
			for i, elem := range p.Elems {
				var newTarget Expr
				if target != nil {
					newTarget = NewIndexExpr(
						target,
						NewLitExpr(NewNumLit(float64(i), nil), nil),
						false,
						nil,
					)
				}
				elems = append(elems, buildPatternRec(elem, newTarget))
			}

			return NewTuplePat(elems, p)
		case *ast.ExtractorPat:
			tempVars := []Expr{}
			tempVarPats := []Pat{}

			for _, arg := range p.Args {
				tempId := b.NewTempId()
				tempVar := NewIdentExpr(tempId, nil)

				var init Expr
				switch arg := arg.(type) {
				case *ast.IdentPat:
					if arg.Default != nil {
						var defStmts []Stmt
						defExpr, defStmts := b.buildExpr(arg.Default)
						stmts = slices.Concat(stmts, defStmts)
						init = defExpr
					}
				}
				tempVarPat := NewIdentPat(tempId, init, p)

				tempVarPats = append(tempVarPats, tempVarPat)
				tempVars = append(tempVars, tempVar)
			}
			extractor := NewIdentExpr(p.Name, p)
			subject := target
			receiver := NewIdentExpr("undefined", nil)

			call := NewCallExpr(
				NewIdentExpr("InvokeCustomMatcherOrThrow", nil),
				[]Expr{extractor, subject, receiver},
				false,
				nil, // TODO: source
			)

			decls := []*Declarator{
				{
					Pattern: NewTuplePat(tempVarPats, nil),
					TypeAnn: nil,
					Init:    call,
				},
			}

			decl := &VarDecl{
				Kind:    VariableKind(ast.ValKind),
				Decls:   decls,
				declare: false,
				export:  export,
				span:    nil,
				source:  nil, // TODO
			}

			stmts = append(stmts, &DeclStmt{
				Decl:   decl,
				span:   nil,
				source: nil,
			})

			for _, pair := range Zip(tempVars, p.Args) {
				temp := pair.First
				arg := pair.Second
				argChecks, argStmts := b.buildPattern(arg, temp, export)
				checks = slices.Concat(checks, argChecks)
				stmts = slices.Concat(stmts, argStmts)
			}
			return nil
		case *ast.RestPat:
			return &RestPat{
				Pattern: buildPatternRec(p.Pattern, target),
				span:    nil,
				source:  p,
			}
		case *ast.LitPat:
			// TODO
		case *ast.WildcardPat:
			// TODO
		default:
			// TODO
		}
		panic("TODO - buildPattern - default case")
	}

	// TODO: Assign the target to a temp variable and pass the temp variable
	// to the buildPatternRec function as the target.  This is necessary because
	// the target may be a complex expression that needs to be evaluated only
	// once.
	pat := buildPatternRec(p, target)

	if pat != nil {
		decls := []*Declarator{
			{
				Pattern: pat,
				TypeAnn: nil,
				Init:    target,
			},
		}

		decl := &VarDecl{
			Kind:    VariableKind(ast.ValKind),
			Decls:   decls,
			declare: false, // TODO
			export:  export,
			span:    nil,
			source:  nil,
		}
		stmts = append(stmts, &DeclStmt{
			Decl:   decl,
			span:   nil,
			source: nil,
		})
	}

	return checks, stmts
}

func (b *Builder) buildStmt(stmt ast.Stmt) []Stmt {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		switch s.Expr.(type) {
		case *ast.EmptyExpr:
			// Ignore empty expressions.
			return []Stmt{}
		default:
			expr, exprStmts := b.buildExpr(s.Expr)
			stmt := &ExprStmt{
				Expr:   expr,
				span:   nil,
				source: stmt,
			}
			return append(exprStmts, stmt)
		}
	case *ast.DeclStmt:
		return b.buildDecl(s.Decl)
	case *ast.ReturnStmt:
		stmts := []Stmt{}
		var expr Expr
		if s.Expr != nil {
			var exprStmts []Stmt
			expr, exprStmts = b.buildExpr(s.Expr)
			stmts = slices.Concat(stmts, exprStmts)
		}
		stmt := &ReturnStmt{
			Expr:   expr,
			span:   nil,
			source: stmt,
		}
		return append(stmts, stmt)
	default:
		panic("TransformStmt - default case should never happen")
	}
}

func (b *Builder) BuildScript(mod *ast.Script) *Module {
	var stmts []Stmt
	for _, s := range mod.Stmts {
		stmts = slices.Concat(stmts, b.buildStmt(s))
	}
	return &Module{
		Stmts: stmts,
	}
}

func (b *Builder) BuildModule(mod *ast.Module) *Module {
	var stmts []Stmt
	for _, d := range mod.Decls {
		stmts = slices.Concat(stmts, b.buildDecl(d))
	}
	return &Module{
		Stmts: stmts,
	}
}

func (b *Builder) BuildDecls(decls []ast.Decl) *Module {
	var stmts []Stmt

	for _, decl := range decls {
		stmts = slices.Concat(stmts, b.buildDecl(decl))
	}

	return &Module{
		Stmts: stmts,
	}
}

func (b *Builder) buildStmts(stmts []ast.Stmt) []Stmt {
	var res []Stmt
	for _, s := range stmts {
		res = slices.Concat(res, b.buildStmt(s))
	}
	return res
}

func (b *Builder) buildDecl(decl ast.Decl) []Stmt {
	if decl.Declare() {
		return []Stmt{}
	}

	switch d := decl.(type) {
	case *ast.VarDecl:
		if d.Init == nil {
			panic("TODO - TransformDecl - VarDecl - Init is nil")
		}
		initExpr, initStmts := b.buildExpr(d.Init)
		// Ignore checks returned by buildPattern
		_, patStmts := b.buildPattern(d.Pattern, initExpr, d.Export())
		return slices.Concat(initStmts, patStmts)
	case *ast.FuncDecl:
		params, allParamStmts := b.buildParams(d.Params)
		if d.Body == nil {
			return []Stmt{}
		}
		fnDecl := &FuncDecl{
			Name:    buildIdent(d.Name),
			Params:  params,
			Body:    slices.Concat(allParamStmts, b.buildStmts(d.Body.Stmts)),
			TypeAnn: nil,
			declare: decl.Declare(),
			export:  decl.Export(),
			span:    nil,
			source:  decl,
		}
		stmt := &DeclStmt{
			Decl:   fnDecl,
			span:   nil,
			source: decl,
		}
		return []Stmt{stmt}
	case *ast.TypeDecl:
		return []Stmt{}
	default:
		panic("TODO - TransformDecl - default case")
	}
}

func (b *Builder) buildExpr(expr ast.Expr) (Expr, []Stmt) {
	if expr == nil {
		return nil, []Stmt{}
	}

	switch expr := expr.(type) {
	case *ast.LiteralExpr:
		switch lit := expr.Lit.(type) {
		case *ast.BoolLit:
			return NewLitExpr(NewBoolLit(lit.Value, lit), expr), []Stmt{}
		case *ast.NumLit:
			return NewLitExpr(NewNumLit(lit.Value, lit), expr), []Stmt{}
		case *ast.StrLit:
			return NewLitExpr(NewStrLit(lit.Value, lit), expr), []Stmt{}
		case *ast.BigIntLit:
			panic("TODO: big int literal")
		case *ast.NullLit:
			return NewLitExpr(NewNullLit(lit), expr), []Stmt{}
		case *ast.UndefinedLit:
			return NewLitExpr(NewUndefinedLit(lit), expr), []Stmt{}
		default:
			panic("TODO: literal type")
		}
	case *ast.BinaryExpr:
		leftExpr, leftStmts := b.buildExpr(expr.Left)
		rightExpr, rightStmts := b.buildExpr(expr.Right)
		stmts := slices.Concat(leftStmts, rightStmts)
		return NewBinaryExpr(leftExpr, BinaryOp(expr.Op), rightExpr, expr), stmts
	case *ast.UnaryExpr:
		argExpr, argStmts := b.buildExpr(expr.Arg)
		return NewUnaryExpr(UnaryOp(expr.Op), argExpr, expr), argStmts
	case *ast.IdentExpr:
		return NewIdentExpr(expr.Name, expr), []Stmt{}
	case *ast.CallExpr:
		calleeExpr, calleeStmts := b.buildExpr(expr.Callee)
		argsExprs, argsStmts := b.buildExprs(expr.Args)
		stmts := slices.Concat(calleeStmts, argsStmts)
		return NewCallExpr(
			calleeExpr,
			argsExprs,
			expr.OptChain,
			expr,
		), stmts
	case *ast.IndexExpr:
		objExpr, objStmts := b.buildExpr(expr.Object)
		indexExpr, indexStmts := b.buildExpr(expr.Index)
		stmts := slices.Concat(objStmts, indexStmts)
		return NewIndexExpr(objExpr, indexExpr, expr.OptChain, expr), stmts
	case *ast.MemberExpr:
		objExpr, objStmts := b.buildExpr(expr.Object)
		propExpr := buildIdent(expr.Prop)
		return NewMemberExpr(objExpr, propExpr, expr.OptChain, expr), objStmts
	case *ast.TupleExpr:
		elemsExprs, elemsStmts := b.buildExprs(expr.Elems)
		return NewArrayExpr(elemsExprs, expr), elemsStmts
	case *ast.ObjectExpr:
		stmts := []Stmt{}
		elems := make([]ObjExprElem, len(expr.Elems))
		for i, elem := range expr.Elems {
			switch elem := elem.(type) {
			case *ast.MethodExpr:
				key, keyStmts := b.buildObjKey(elem.Name)
				stmts = slices.Concat(stmts, keyStmts)
				params, allParamStmts := b.buildParams(elem.Fn.Params)
				stmts = slices.Concat(stmts, allParamStmts)

				elems[i] = NewMethodExpr(
					key,
					params,
					b.buildStmts(elem.Fn.Body.Stmts),
					elem,
				)
			case *ast.GetterExpr:
				key, keyStmts := b.buildObjKey(elem.Name)
				stmts = slices.Concat(stmts, keyStmts)

				elems[i] = NewGetterExpr(
					key,
					b.buildStmts(elem.Fn.Body.Stmts),
					elem,
				)
			case *ast.SetterExpr:
				key, keyStmts := b.buildObjKey(elem.Name)
				stmts = slices.Concat(stmts, keyStmts)
				params, allParamStmts := b.buildParams(elem.Fn.Params)
				stmts = slices.Concat(stmts, allParamStmts)
				elems[i] = NewSetterExpr(
					key,
					params,
					b.buildStmts(elem.Fn.Body.Stmts),
					elem,
				)
			case *ast.PropertyExpr:
				key, keyStmts := b.buildObjKey(elem.Name)
				stmts = slices.Concat(stmts, keyStmts)
				if elem.Value != nil {
					valueExpr, valueStmts := b.buildExpr(elem.Value)
					stmts = slices.Concat(stmts, valueStmts)
					elems[i] = NewPropertyExpr(key, valueExpr, elem)
				} else {
					elems[i] = NewPropertyExpr(key, nil, elem)
				}
			default:
				panic(fmt.Sprintf("TODO - buildExpr - ObjectExpr - default case: %#v", elem))
			}
		}

		return NewObjectExpr(
			elems,
			expr,
		), stmts
	case *ast.FuncExpr:
		params, allParamStmts := b.buildParams(expr.Params)
		return NewFuncExpr(
			params,
			slices.Concat(allParamStmts, b.buildStmts(expr.Body.Stmts)),
			expr,
		), []Stmt{}
	case *ast.IgnoreExpr:
		panic("TODO - buildExpr - IgnoreExpr")
	case *ast.EmptyExpr:
		panic("TODO - buildExpr - EmptyExpr")
	default:
		panic(fmt.Sprintf("TODO - buildExpr - default case: %#v", expr))
	}
}

func (b *Builder) buildObjKey(key ast.ObjKey) (ObjKey, []Stmt) {
	switch k := key.(type) {
	case *ast.IdentExpr:
		return NewIdentExpr(k.Name, key), []Stmt{}
	case *ast.StrLit:
		return NewStrLit(k.Value, key), []Stmt{}
	case *ast.NumLit:
		return NewNumLit(k.Value, key), []Stmt{}
	case *ast.ComputedKey:
		expr, stmts := b.buildExpr(k.Expr)
		return NewComputedKey(expr, key), stmts
	default:
		panic(fmt.Sprintf("TODO - buildObjKey - default case: %#v", k))
	}
}

func (b *Builder) buildParams(inParams []*ast.Param) ([]*Param, []Stmt) {
	var outParams []*Param
	var outParamStmts []Stmt
	for _, p := range inParams {
		id := b.NewTempId()
		var paramPat Pat
		paramPat = NewIdentPat(id, nil, p.Pattern)

		switch pat := p.Pattern.(type) {
		case *ast.RestPat:
			_, paramStmts := b.buildPattern(pat.Pattern, NewIdentExpr(id, nil), false)
			outParamStmts = slices.Concat(outParamStmts, paramStmts)
			paramPat = NewRestPat(paramPat, nil)
		default:
			_, paramStmts := b.buildPattern(pat, NewIdentExpr(id, nil), false)
			outParamStmts = slices.Concat(outParamStmts, paramStmts)
		}

		outParams = append(outParams, &Param{
			// TODO: handle param defaults
			Pattern:  paramPat,
			Optional: p.Optional,
			TypeAnn:  nil,
		})
	}
	return outParams, outParamStmts
}
