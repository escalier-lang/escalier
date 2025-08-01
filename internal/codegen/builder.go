package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
)

type Builder struct {
	tempId   int
	depGraph *dep_graph.DepGraph
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

func fullyQualifyName(name, nsName string) string {
	if nsName == "" {
		return name
	}
	return strings.ReplaceAll(nsName, ".", "__") + "__" + name
}

// TODO: return a pattern instead of passing in the VariableKind
func (b *Builder) buildPattern(
	p ast.Pat,
	target Expr,
	export bool,
	kind ast.VariableKind,
	nsName string,
) ([]Expr, []Stmt) {

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
				Name:    fullyQualifyName(p.Name, nsName),
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
						fullyQualifyName(e.Key.Name, nsName),
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
						fullyQualifyName(e.Key.Name, nsName),
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
				tempVar := NewIdentExpr(tempId, "", nil)

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
			extractor := NewIdentExpr(p.Name, "", p)
			subject := target
			receiver := NewIdentExpr("undefined", "", nil)

			call := NewCallExpr(
				NewIdentExpr("InvokeCustomMatcherOrThrow", "", nil),
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
				argChecks, argStmts := b.buildPattern(arg, temp, export, ast.ValKind, nsName)
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
			Kind:    VariableKind(kind),
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
	if ns, ok := mod.Namespaces.Get(""); ok {
		// If the module has a default namespace, we build its declarations.
		for _, d := range ns.Decls {
			stmts = slices.Concat(stmts, b.buildDecl(d))
		}
	} else {
		panic("TODO - TransformModule - default namespace is missing")
	}
	return &Module{
		Stmts: stmts,
	}
}

// declIDs must be sorted according to reverse topological order based on the
// the strongly connected components of the dependency graph.  The reason why
// we pass this in is because we don't want to compute the strongly connected
// components more than once and BuildDefinitions needs this information as well.
func (b *Builder) BuildTopLevelDecls(depGraph *dep_graph.DepGraph) *Module {
	// Set up builder state
	b.depGraph = depGraph

	var stmts []Stmt

	nsStmts := b.buildNamespaceStatements(depGraph)
	stmts = slices.Concat(stmts, nsStmts)

	var topoDeclIDs []dep_graph.DeclID
	for _, component := range depGraph.Components {
		topoDeclIDs = append(topoDeclIDs, component...)
	}

	for _, declID := range topoDeclIDs {
		decl, _ := depGraph.GetDecl(declID)

		// if decl is a type declaration skip it
		if _, ok := decl.(*ast.TypeDecl); ok {
			continue
		}

		nsName, _ := depGraph.GetDeclNamespace(declID)
		stmts = slices.Concat(stmts, b.buildDeclWithNamespace(decl, nsName))

		bindings := depGraph.GetDeclNames(declID)

		for _, name := range bindings {
			if !strings.Contains(name, ".") {
				continue // Skip non-namespaced identifiers
			}

			parts := strings.Split(name, ".")
			dunderName := strings.Join(parts, "__")
			assignExpr := NewBinaryExpr(
				NewIdentExpr(name, "", nil),
				Assign,
				NewIdentExpr(dunderName, "", nil),
				nil,
			)

			stmts = append(stmts, &ExprStmt{
				Expr:   assignExpr,
				span:   nil,
				source: decl,
			})
		}
	}

	// TODO: Fully qualify variables
	// Codegen flattens the namespace hierarchy.  Bindings are given prefixes
	// like `foo__bar__baz` for `foo.bar.baz`.  We need to do the same for the
	// identifiers used in the declaration bodies within the namespaces.
	// We need to be able to look up the declaration for the identifier by their
	// ID.  This means we should probably give each declaration a unique ID field
	// and the Source field on the identifier can just be the declaration ID.

	return &Module{
		Stmts: stmts,
	}
}

// buildNamespaceStatements generates statements to create namespace objects
// for all namespaces used by the given declarations
func (b *Builder) buildNamespaceStatements(depGraph *dep_graph.DepGraph) []Stmt {
	// Track which namespace segments have been defined to avoid redefinition
	definedNamespaces := make(map[string]bool)
	var stmts []Stmt

	// For each namespace, generate the hierarchy of statements
	for _, namespace := range depGraph.Namespaces {
		if namespace == "" {
			continue // Skip the root namespace
		}
		stmts = slices.Concat(stmts, b.buildNamespaceHierarchy(namespace, definedNamespaces))
	}

	return stmts
}

// buildNamespaceHierarchy generates statements to create a namespace hierarchy
// For "foo.bar.baz", it generates: const foo = {}; foo.bar = {}; foo.bar.baz = {};
func (b *Builder) buildNamespaceHierarchy(namespace string, definedNamespaces map[string]bool) []Stmt {
	if namespace == "" {
		return []Stmt{}
	}

	parts := strings.Split(namespace, ".")
	var stmts []Stmt

	// Build each level of the namespace hierarchy
	for i := 1; i <= len(parts); i++ {
		currentNS := strings.Join(parts[:i], ".")

		// Skip if this namespace level has already been defined
		if definedNamespaces[currentNS] {
			continue
		}
		definedNamespaces[currentNS] = true

		if i == 1 {
			// First level: const foo = {};
			pattern := NewIdentPat(parts[0], nil, nil)
			init := NewObjectExpr([]ObjExprElem{}, nil)

			decl := &VarDecl{
				Kind: VariableKind(ast.ValKind),
				Decls: []*Declarator{
					{
						Pattern: pattern,
						TypeAnn: nil,
						Init:    init,
					},
				},
				declare: false,
				export:  false,
				span:    nil,
				source:  nil,
			}

			stmt := &DeclStmt{
				Decl:   decl,
				span:   nil,
				source: nil,
			}
			stmts = append(stmts, stmt)
		} else {
			// Subsequent levels: foo.bar = {}; foo.bar.baz = {};
			// Build the left side (foo.bar.baz)
			var left Expr = NewIdentExpr(parts[0], "", nil)
			for j := 1; j < i; j++ {
				left = NewMemberExpr(left, NewIdentifier(parts[j], nil), false, nil)
			}

			// Right side is an empty object
			right := NewObjectExpr([]ObjExprElem{}, nil)

			// Create assignment expression
			assignExpr := NewBinaryExpr(left, Assign, right, nil)

			// Wrap in expression statement
			stmt := &ExprStmt{
				Expr:   assignExpr,
				span:   nil,
				source: nil,
			}
			stmts = append(stmts, stmt)
		}
	}

	return stmts
}

func (b *Builder) buildStmts(stmts []ast.Stmt) []Stmt {
	var res []Stmt
	for _, s := range stmts {
		res = slices.Concat(res, b.buildStmt(s))
	}
	return res
}

func (b *Builder) buildDecl(decl ast.Decl) []Stmt {
	return b.buildDeclWithNamespace(decl, "")
}

func (b *Builder) buildDeclWithNamespace(decl ast.Decl, nsName string) []Stmt {
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
		_, patStmts := b.buildPattern(d.Pattern, initExpr, d.Export(), d.Kind, nsName)
		return slices.Concat(initStmts, patStmts)
	case *ast.FuncDecl:
		params, allParamStmts := b.buildParams(d.Params)
		if d.Body == nil {
			return []Stmt{}
		}
		fnDecl := &FuncDecl{
			Name: &Identifier{
				Name:   fullyQualifyName(d.Name.Name, nsName),
				span:   nil,
				source: d.Name,
			},
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
		case *ast.RegexLit:
			return NewLitExpr(NewRegexLit(lit.Value, lit), expr), []Stmt{}
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
		var namespaceStr string
		if b.depGraph != nil {
			namespaceStr = b.depGraph.GetNamespaceString(expr.Namespace)
		}
		return NewIdentExpr(expr.Name, namespaceStr, expr), []Stmt{}
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
		return NewIdentExpr(k.Name, "", key), []Stmt{}
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
			_, paramStmts := b.buildPattern(pat.Pattern, NewIdentExpr(id, "", nil), false, ast.ValKind, "")
			outParamStmts = slices.Concat(outParamStmts, paramStmts)
			paramPat = NewRestPat(paramPat, nil)
		default:
			_, paramStmts := b.buildPattern(pat, NewIdentExpr(id, "", nil), false, ast.ValKind, "")
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
