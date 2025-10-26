package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/type_system"
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
		expr, stmts := b.buildExpr(e, nil)
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
				defExpr, defStmts = b.buildExpr(p.Default, nil)
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
						defExpr, defStmts = b.buildExpr(e.Default, nil)
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
						defExpr, defStmts = b.buildExpr(e.Default, nil)
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
						defExpr, defStmts := b.buildExpr(arg.Default, nil)
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
			expr, exprStmts := b.buildExpr(s.Expr, nil)
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
			expr, exprStmts = b.buildExpr(s.Expr, nil)
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
		initExpr, initStmts := b.buildExpr(d.Init, nil)
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
			async:   d.Async,
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
	case *ast.ClassDecl:
		allStmts := []Stmt{}

		// Build class body elements
		classElems, classStmts := b.buildClassElems(d.Body)
		allStmts = slices.Concat(allStmts, classStmts)

		// Use buildParams to handle parameter patterns and generate temp variables
		params, paramStmts := b.buildParams(d.Params)
		var constructorBodyStmts []Stmt

		// Add parameter statements (variable declarations from buildParams)
		constructorBodyStmts = slices.Concat(constructorBodyStmts, paramStmts)

		// For each instance field in the class body, create this.fieldName = fieldName assignment
		for _, elem := range d.Body {
			if fieldElem, ok := elem.(*ast.FieldElem); ok && !fieldElem.Static {
				if fieldElem.Name != nil {
					var lhs Expr
					switch name := fieldElem.Name.(type) {
					case *ast.IdentExpr:
						lhs = NewMemberExpr(
							NewIdentExpr("this", "", nil),
							NewIdentifier(name.Name, fieldElem.Name),
							false,
							nil,
						)
					case *ast.StrLit:
						lhs = NewIndexExpr(
							NewIdentExpr("this", "", nil),
							NewLitExpr(NewStrLit(name.Value, name), nil),
							false,
							nil,
						)
					case *ast.NumLit:
						lhs = NewIndexExpr(
							NewIdentExpr("this", "", nil),
							NewLitExpr(NewNumLit(name.Value, name), nil),
							false,
							nil,
						)
					case *ast.ComputedKey:
						key, keyStmts := b.buildExpr(name.Expr, nil)
						allStmts = slices.Concat(allStmts, keyStmts)

						lhs = NewIndexExpr(
							NewIdentExpr("this", "", nil),
							key,
							false,
							nil,
						)
					}

					var rhs Expr
					if fieldElem.Value != nil {
						value, valueStmts := b.buildExpr(fieldElem.Value, nil)
						allStmts = slices.Concat(allStmts, valueStmts)

						rhs = value
					} else {
						// If the field has no value, assume it's a parameter with the same name
						switch name := fieldElem.Name.(type) {
						case *ast.IdentExpr:
							rhs = NewIdentExpr(name.Name, "", fieldElem.Name)
						case *ast.StrLit:
							rhs = NewIdentExpr(name.Value, "", fieldElem.Name)
						case *ast.NumLit:
							rhs = NewIdentExpr(fmt.Sprintf("%g", name.Value), "", fieldElem.Name)
						case *ast.ComputedKey:
							// Computed keys cannot be constructor parameters
							panic("Computed keys cannot be constructor parameters")
						}
					}

					// Create assignment: this.fieldName = fieldName;
					assignment := &ExprStmt{
						Expr:   NewBinaryExpr(lhs, Assign, rhs, fieldElem.Name),
						span:   nil,
						source: fieldElem,
					}
					constructorBodyStmts = append(constructorBodyStmts, assignment)
				}
			}
		}

		// Create constructor method
		constructorMethod := &MethodElem{
			Name:    NewIdentExpr("constructor", "", d),
			Params:  params,
			Body:    constructorBodyStmts,
			MutSelf: nil,
			Static:  false,
			Private: false,
			Async:   false,
			span:    nil,
			source:  d,
		}

		// Add constructor as the first element in the class body
		classElems = append([]ClassElem{constructorMethod}, classElems...)

		// Create the class declaration
		classDecl := &ClassDecl{
			Name: &Identifier{
				Name:   fullyQualifyName(d.Name.Name, nsName),
				span:   nil,
				source: d.Name,
			},
			Body:    classElems,
			export:  d.Export(),
			declare: d.Declare(),
			span:    nil,
			source:  d,
		}

		stmt := &DeclStmt{
			Decl:   classDecl,
			span:   nil,
			source: d,
		}

		allStmts = append(allStmts, stmt)

		return allStmts
	default:
		panic("TODO - TransformDecl - default case")
	}
}

func (b *Builder) buildExpr(expr ast.Expr, parent ast.Expr) (Expr, []Stmt) {
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
		leftExpr, leftStmts := b.buildExpr(expr.Left, expr)
		rightExpr, rightStmts := b.buildExpr(expr.Right, expr)
		stmts := slices.Concat(leftStmts, rightStmts)
		return NewBinaryExpr(leftExpr, BinaryOp(expr.Op), rightExpr, expr), stmts
	case *ast.UnaryExpr:
		argExpr, argStmts := b.buildExpr(expr.Arg, expr)
		return NewUnaryExpr(UnaryOp(expr.Op), argExpr, expr), argStmts
	case *ast.IdentExpr:
		var namespaceStr string
		if b.depGraph != nil {
			namespaceStr = b.depGraph.GetNamespaceString(expr.Namespace)
		}
		return NewIdentExpr(expr.Name, namespaceStr, expr), []Stmt{}
	case *ast.CallExpr:
		calleeExpr, calleeStmts := b.buildExpr(expr.Callee, expr)
		argsExprs, argsStmts := b.buildExprs(expr.Args)
		stmts := slices.Concat(calleeStmts, argsStmts)

		// Check if the callee is a constructor by examining its inferred type
		calleeType := expr.Callee.InferredType()
		if objType, ok := calleeType.(*type_system.ObjectType); ok {
			// Check if the object type has a constructor elem
			for _, elem := range objType.Elems {
				if _, isConstructor := elem.(*type_system.ConstructorElem); isConstructor {
					return NewNewExpr(
						calleeExpr,
						argsExprs,
						expr,
					), stmts
				}
			}
		}

		return NewCallExpr(
			calleeExpr,
			argsExprs,
			expr.OptChain,
			expr,
		), stmts
	case *ast.IndexExpr:
		objExpr, objStmts := b.buildExpr(expr.Object, expr)
		indexExpr, indexStmts := b.buildExpr(expr.Index, expr)
		stmts := slices.Concat(objStmts, indexStmts)
		return NewIndexExpr(objExpr, indexExpr, expr.OptChain, expr), stmts
	case *ast.MemberExpr:
		objExpr, objStmts := b.buildExpr(expr.Object, expr)
		propExpr := buildIdent(expr.Prop)

		member := NewMemberExpr(objExpr, propExpr, expr.OptChain, expr)
		if _, ok := parent.(*ast.CallExpr); !ok {
			t := expr.InferredType()
			if _, ok := t.(*type_system.FuncType); ok {
				bindIdent := NewIdentifier("bind", nil)
				callee := NewMemberExpr(member, bindIdent, false, expr)
				call := NewCallExpr(callee, []Expr{objExpr}, false, nil)
				return call, objStmts
			}
		}

		return member, objStmts
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
					valueExpr, valueStmts := b.buildExpr(elem.Value, expr)
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
			expr.Async,
			expr,
		), []Stmt{}
	case *ast.DoExpr:
		return b.buildBlockWithTempVar(expr.Body.Stmts, expr)
	case *ast.IfElseExpr:
		// Generate a temporary variable for the if-else result
		tempVar, tempDeclStmt := b.createTempVar(expr)

		stmts := []Stmt{tempDeclStmt}

		// Build the condition
		condExpr, condStmts := b.buildExpr(expr.Cond, expr)
		stmts = slices.Concat(stmts, condStmts)

		// Build the consequent (then branch)
		consStmts := b.buildBlockStmtsWithTempAssignment(expr.Cons.Stmts, tempVar, expr)

		var altStmt Stmt
		if expr.Alt != nil {
			var altStmts []Stmt

			if expr.Alt.Block != nil {
				// Alternative is a block
				altStmts = b.buildBlockStmtsWithTempAssignment(expr.Alt.Block.Stmts, tempVar, expr)
			} else if expr.Alt.Expr != nil {
				// Alternative is an expression
				altExpr, altExprStmts := b.buildExpr(expr.Alt.Expr, expr)
				altStmts = slices.Concat(altStmts, altExprStmts)

				assignment := NewBinaryExpr(tempVar, Assign, altExpr, expr.Alt.Expr)
				altStmts = append(altStmts, &ExprStmt{
					Expr:   assignment,
					span:   nil,
					source: expr.Alt.Expr,
				})
			}

			// Always wrap alternative in a block for proper formatting
			if len(altStmts) > 0 {
				altStmt = NewBlockStmt(altStmts, expr)
			}
		}

		// Create the consequent statement - always wrap in a block for proper formatting
		consStmt := NewBlockStmt(consStmts, expr)

		// Create the if statement
		ifStmt := NewIfStmt(condExpr, consStmt, altStmt, expr)
		stmts = append(stmts, ifStmt)

		return tempVar, stmts
	case *ast.MatchExpr:
		// Convert match expression to a series of if-else statements
		return b.buildMatchExpr(expr)
	case *ast.ThrowExpr:
		// Build the argument expression
		argExpr, argStmts := b.buildExpr(expr.Arg, expr)

		// Create a throw statement
		throwStmt := NewThrowStmt(argExpr, expr)

		// Since throw never returns, we need to create a temporary variable
		// for the expression context, but it will never be reached
		tempVar, tempDeclStmt := b.createTempVar(expr)

		// Return the temp variable (unreachable) and the statements
		allStmts := []Stmt{tempDeclStmt}
		allStmts = slices.Concat(allStmts, argStmts)
		allStmts = append(allStmts, throwStmt)

		return tempVar, allStmts
	case *ast.AwaitExpr:
		// Build the argument expression
		argExpr, argStmts := b.buildExpr(expr.Arg, expr)

		// Create an await expression
		awaitExpr := NewAwaitExpr(argExpr, expr)

		return awaitExpr, argStmts
	case *ast.TypeCastExpr:
		// For type casts, we just build the inner expression since
		// JavaScript doesn't have runtime type casting
		innerExpr, innerStmts := b.buildExpr(expr.Expr, expr)

		// Return the inner expression directly - the type cast is compile-time only
		return innerExpr, innerStmts
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
		expr, stmts := b.buildExpr(k.Expr, nil)
		return NewComputedKey(expr, key), stmts
	default:
		panic(fmt.Sprintf("TODO - buildObjKey - default case: %#v", k))
	}
}

// createTempVar creates a temporary variable declaration and returns the temp variable
// expression and the declaration statement.
func (b *Builder) createTempVar(sourceExpr ast.Expr) (Expr, Stmt) {
	tempId := b.NewTempId()
	tempVar := NewIdentExpr(tempId, "", sourceExpr)

	tempDecl := &VarDecl{
		Kind: VarKind,
		Decls: []*Declarator{
			{
				Pattern: NewIdentPat(tempId, nil, sourceExpr),
				TypeAnn: nil,
				Init:    nil,
			},
		},
		declare: false,
		export:  false,
		span:    nil,
		source:  sourceExpr,
	}

	declStmt := &DeclStmt{
		Decl:   tempDecl,
		span:   nil,
		source: sourceExpr,
	}

	return tempVar, declStmt
}

// buildBlockWithTempVar builds a block of statements and assigns the result of the last
// expression statement to a temporary variable. Returns the temp variable expression
// and the statements needed to declare the temp variable and execute the block.
func (b *Builder) buildBlockWithTempVar(stmts []ast.Stmt, sourceExpr ast.Expr) (Expr, []Stmt) {
	// Generate a temporary variable
	tempVar, tempDeclStmt := b.createTempVar(sourceExpr)

	outStmts := []Stmt{tempDeclStmt}

	// Build block statements
	blockStmts := b.buildBlockStmtsWithTempAssignment(stmts, tempVar, sourceExpr)

	// Create a block statement with the inner statements
	block := NewBlockStmt(blockStmts, sourceExpr)
	outStmts = append(outStmts, block)

	return tempVar, outStmts
}

// buildBlockStmtsWithTempAssignment builds statements for a block, treating the last
// statement specially by assigning its result to the given temp variable.
func (b *Builder) buildBlockStmtsWithTempAssignment(stmts []ast.Stmt, tempVar Expr, sourceExpr ast.Expr) []Stmt {
	blockStmts := []Stmt{}

	if len(stmts) > 0 {
		// Build all statements except the last one
		for _, stmt := range stmts[:len(stmts)-1] {
			blockStmts = slices.Concat(blockStmts, b.buildStmt(stmt))
		}

		// Handle the last statement specially
		lastStmt := stmts[len(stmts)-1]
		if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
			// Convert the last expression statement to an assignment to temp variable
			lastExpr, lastExprStmts := b.buildExpr(exprStmt.Expr, nil)
			blockStmts = slices.Concat(blockStmts, lastExprStmts)

			// Create assignment: tempVar = lastExpr
			assignment := NewBinaryExpr(
				tempVar,
				Assign,
				lastExpr,
				exprStmt.Expr,
			)
			blockStmts = append(blockStmts, &ExprStmt{
				Expr:   assignment,
				span:   nil,
				source: lastStmt,
			})
		} else {
			// Last statement is not an expression, add it as-is
			blockStmts = slices.Concat(blockStmts, b.buildStmt(lastStmt))

			// Only assign undefined if the last statement is not a terminal statement
			// (return statements end execution, so no assignment is needed)
			_, isReturn := lastStmt.(*ast.ReturnStmt)

			if !isReturn {
				// Assign undefined to temp variable
				assignment := NewBinaryExpr(
					tempVar,
					Assign,
					NewLitExpr(NewUndefinedLit(&ast.UndefinedLit{}), nil),
					lastStmt,
				)
				blockStmts = append(blockStmts, &ExprStmt{
					Expr:   assignment,
					span:   nil,
					source: lastStmt,
				})
			}
		}
	} else {
		// Empty block, assign undefined to temp variable
		assignment := NewBinaryExpr(
			tempVar,
			Assign,
			NewLitExpr(NewUndefinedLit(&ast.UndefinedLit{}), nil),
			sourceExpr,
		)
		blockStmts = append(blockStmts, &ExprStmt{
			Expr:   assignment,
			span:   nil,
			source: sourceExpr,
		})
	}

	return blockStmts
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

func (b *Builder) buildClassElems(inElems []ast.ClassElem) ([]ClassElem, []Stmt) {
	var outElems []ClassElem
	var allStmts []Stmt

	for _, elem := range inElems {
		switch e := elem.(type) {
		case *ast.FieldElem:
			// Only handle static fields here; instance fields are handled by the constructor
			if e.Static {
				name, nameStmts := b.buildObjKey(e.Name)
				allStmts = slices.Concat(allStmts, nameStmts)

				var value Expr
				var valueStmts []Stmt
				if e.Value != nil {
					value, valueStmts = b.buildExpr(e.Value, nil)
					allStmts = slices.Concat(allStmts, valueStmts)
				}

				fieldElem := &FieldElem{
					Name:    name,
					Value:   value,
					Static:  e.Static,
					Private: e.Private,
					span:    nil,
					source:  e,
				}
				outElems = append(outElems, fieldElem)
			}
			// Instance fields are skipped and handled by the constructor
		case *ast.MethodElem:
			if e.Fn == nil {
				continue
			}
			params, paramStmts := b.buildParams(e.Fn.Params)
			var bodyStmts []Stmt
			if e.Fn.Body != nil {
				bodyStmts = b.buildStmts(e.Fn.Body.Stmts)
			}

			name, nameStmts := b.buildObjKey(e.Name)
			allStmts = slices.Concat(allStmts, nameStmts)

			methodElem := &MethodElem{
				Name:    name,
				Params:  params,
				Body:    slices.Concat(paramStmts, bodyStmts),
				MutSelf: e.MutSelf,
				Static:  e.Static,
				Private: e.Private,
				Async:   e.Fn.Async,
				span:    nil,
				source:  e,
			}
			outElems = append(outElems, methodElem)

		case *ast.GetterElem:
			if e.Fn == nil {
				continue
			}
			var bodyStmts []Stmt
			if e.Fn.Body != nil {
				bodyStmts = b.buildStmts(e.Fn.Body.Stmts)
			}

			name, nameStmts := b.buildObjKey(e.Name)
			allStmts = slices.Concat(allStmts, nameStmts)

			getterElem := &GetterElem{
				Name:    name,
				Body:    bodyStmts,
				Static:  e.Static,
				Private: e.Private,
				span:    nil,
				source:  e,
			}
			outElems = append(outElems, getterElem)

		case *ast.SetterElem:
			if e.Fn == nil {
				continue
			}
			params, paramStmts := b.buildParams(e.Fn.Params)
			var bodyStmts []Stmt
			if e.Fn.Body != nil {
				bodyStmts = b.buildStmts(e.Fn.Body.Stmts)
			}

			name, nameStmts := b.buildObjKey(e.Name)
			allStmts = slices.Concat(allStmts, nameStmts)

			setterElem := &SetterElem{
				Name:    name,
				Params:  params,
				Body:    slices.Concat(paramStmts, bodyStmts),
				Static:  e.Static,
				Private: e.Private,
				span:    nil,
				source:  e,
			}
			outElems = append(outElems, setterElem)
		}
	}

	return outElems, allStmts
}

// buildMatchExpr converts a match expression into if-else statements with pattern matching
func (b *Builder) buildMatchExpr(expr *ast.MatchExpr) (Expr, []Stmt) {
	// Create a temporary variable to store the match result
	tempVar, tempDeclStmt := b.createTempVar(expr)
	stmts := []Stmt{tempDeclStmt}

	// Build the target expression
	targetExpr, targetStmts := b.buildExpr(expr.Target, expr)
	stmts = slices.Concat(stmts, targetStmts)

	// Create a temporary variable for the target to avoid re-evaluation
	targetTempVar, targetTempDeclStmt := b.createTempVar(expr.Target)
	stmts = append(stmts, targetTempDeclStmt)

	// Assign target to temp variable
	targetAssignment := NewBinaryExpr(targetTempVar, Assign, targetExpr, expr.Target)
	stmts = append(stmts, &ExprStmt{
		Expr:   targetAssignment,
		span:   nil,
		source: expr.Target,
	})

	// Convert each match case to if-else statements
	var currentStmt Stmt
	for i := len(expr.Cases) - 1; i >= 0; i-- {
		matchCase := expr.Cases[i]

		// Build pattern matching conditions and variable bindings
		patternCond, patternBindings := b.buildPatternCondition(matchCase.Pattern, targetTempVar)

		// Build guard condition if present
		var fullCondition Expr = patternCond
		if matchCase.Guard != nil {
			guardExpr, guardStmts := b.buildExpr(matchCase.Guard, expr)
			stmts = slices.Concat(stmts, guardStmts)
			fullCondition = NewBinaryExpr(patternCond, LogicalAnd, guardExpr, matchCase.Guard)
		}

		// Build the case body statements
		var caseStmts []Stmt

		// Add pattern binding statements
		caseStmts = slices.Concat(caseStmts, patternBindings)

		// Add body statements or expression
		if matchCase.Body.Block != nil {
			// Handle block body - assign result of last statement to temp var
			blockStmts := b.buildBlockStmtsWithTempAssignment(matchCase.Body.Block.Stmts, tempVar, expr)
			caseStmts = slices.Concat(caseStmts, blockStmts)
		} else if matchCase.Body.Expr != nil {
			// Handle expression body
			bodyExpr, bodyStmts := b.buildExpr(matchCase.Body.Expr, expr)
			caseStmts = slices.Concat(caseStmts, bodyStmts)

			// Assign result to temp variable
			assignment := NewBinaryExpr(tempVar, Assign, bodyExpr, matchCase.Body.Expr)
			caseStmts = append(caseStmts, &ExprStmt{
				Expr:   assignment,
				span:   nil,
				source: matchCase.Body.Expr,
			})
		}

		// Create block statement for the case
		caseBlock := NewBlockStmt(caseStmts, expr)

		// Create if statement
		if currentStmt == nil {
			// Last case (first in reverse order) - no else clause
			currentStmt = NewIfStmt(fullCondition, caseBlock, nil, expr)
		} else {
			// Previous cases become the else clause
			currentStmt = NewIfStmt(fullCondition, caseBlock, currentStmt, expr)
		}
	}

	if currentStmt != nil {
		// Post-process to convert "else if (true)" to "else"
		currentStmt = simplifyTrueLiterals(currentStmt)
		stmts = append(stmts, currentStmt)
	}

	return tempVar, stmts
}

// simplifyTrueLiterals recursively converts "else if (true)" to "else" in if-else chains
func simplifyTrueLiterals(stmt Stmt) Stmt {
	if ifStmt, ok := stmt.(*IfStmt); ok {
		if ifStmt.Alt != nil {
			// Check if the else clause is an "if (true)" that can be simplified
			if altIfStmt, ok := ifStmt.Alt.(*IfStmt); ok && isTrueLiteral(altIfStmt.Test) {
				// Replace "else if (true) { ... }" with "else { ... }"
				simplifiedAlt := simplifyTrueLiterals(altIfStmt.Cons)
				return NewIfStmt(ifStmt.Test, ifStmt.Cons, simplifiedAlt, ifStmt.Source())
			} else {
				// Recursively simplify the else clause
				simplifiedAlt := simplifyTrueLiterals(ifStmt.Alt)
				return NewIfStmt(ifStmt.Test, ifStmt.Cons, simplifiedAlt, ifStmt.Source())
			}
		}
		return ifStmt
	}
	return stmt
}

// buildPatternCondition builds the condition expression and binding statements for a pattern
func (b *Builder) buildPatternCondition(pattern ast.Pat, targetExpr Expr) (Expr, []Stmt) {
	switch pat := pattern.(type) {
	case *ast.IdentPat:
		// Identifier patterns always match, just create binding as const declaration
		identPat := NewIdentPat(pat.Name, nil, pat)
		declarator := &Declarator{
			Pattern: identPat,
			TypeAnn: nil,
			Init:    targetExpr,
		}
		varDecl := &VarDecl{
			Kind:    ValKind,
			Decls:   []*Declarator{declarator},
			declare: false,
			export:  false,
			span:    nil,
			source:  pat,
		}
		bindingStmt := &DeclStmt{
			Decl:   varDecl,
			span:   nil,
			source: pat,
		}
		var cond Expr = NewLitExpr(NewBoolLit(true, pat), pat)
		if pat.TypeAnn != nil && pat.TypeAnn.InferredType() != nil {
			inferred := pat.TypeAnn.InferredType()
			// Try to match on the type name for basic types
			switch t := inferred.(type) {
			case *type_system.PrimType:
				var typeOfStr string

				switch t.Prim {
				case type_system.NumPrim:
					typeOfStr = "number"
				case type_system.StrPrim:
					typeOfStr = "string"
				case type_system.BoolPrim:
					typeOfStr = "boolean"
				case type_system.BigIntPrim:
					typeOfStr = "bigint"
				case type_system.SymbolPrim:
					typeOfStr = "symbol"
				default:
					panic(fmt.Sprintf("Unknown primitive type in pattern type check: %v", t.Prim))
				}

				if typeOfStr != "" {
					cond = NewBinaryExpr(
						NewUnaryExpr(TypeOf, targetExpr, nil),
						EqualEqual,
						NewLitExpr(NewStrLit(typeOfStr, nil), nil),
						pat,
					)
				}
			default:
				panic(fmt.Sprintf("TODO: handle other inferred types in pattern type check: %T", inferred))
			}
		}
		return cond, []Stmt{bindingStmt}

	case *ast.LitPat:
		// Literal patterns: check for equality
		litExpr, _ := b.buildExpr(&ast.LiteralExpr{Lit: pat.Lit}, nil)
		condition := NewBinaryExpr(targetExpr, EqualEqual, litExpr, pat)
		return condition, []Stmt{}

	case *ast.WildcardPat:
		// Wildcard patterns always match, no bindings
		return NewLitExpr(NewBoolLit(true, pat), pat), []Stmt{}

	case *ast.TuplePat:
		// Tuple patterns: check length and recursively check element conditions only (not bindings)
		var conditions []Expr

		// Check if target is an array and has the right length
		lengthCheck := b.buildArrayLengthCheck(targetExpr, len(pat.Elems), pat)
		conditions = append(conditions, lengthCheck)

		// For each element, recursively build only the condition (ignore bindings)
		for i, elem := range pat.Elems {
			elemTarget := NewIndexExpr(targetExpr, NewLitExpr(NewNumLit(float64(i), pat), pat), false, pat)
			cond, _ := b.buildPatternCondition(elem, elemTarget)
			conditions = append(conditions, cond)
		}

		// Only generate the destructuring binding at this level
		elemPats := []Pat{}
		for _, elem := range pat.Elems {
			elemPats = append(elemPats, b.buildDestructuringPattern(elem))
		}
		tuplePat := NewTuplePat(elemPats, pat)
		declarator := &Declarator{
			Pattern: tuplePat,
			TypeAnn: nil,
			Init:    targetExpr,
		}
		varDecl := &VarDecl{
			Kind:    ValKind,
			Decls:   []*Declarator{declarator},
			declare: false,
			export:  false,
			span:    nil,
			source:  pat,
		}
		bindingStmt := &DeclStmt{
			Decl:   varDecl,
			span:   nil,
			source: pat,
		}

		// Combine all conditions with &&
		finalCondition := combineConditions(conditions, pat)

		return finalCondition, []Stmt{bindingStmt}

	case *ast.ObjectPat:
		// Object patterns: check for object properties and recursively check nested pattern conditions only (not bindings)
		var conditions []Expr

		// Check that target is not null/undefined
		nullCheck := NewBinaryExpr(
			targetExpr,
			NotEqual,
			NewLitExpr(NewNullLit(pat), pat),
			pat,
		)
		conditions = append(conditions, nullCheck)

		objPatElems := []ObjPatElem{}

		for _, elem := range pat.Elems {
			switch objElem := elem.(type) {
			case *ast.ObjKeyValuePat:
				// Check that the property exists: "propName" in object
				propExistsCheck := NewBinaryExpr(
					NewLitExpr(NewStrLit(objElem.Key.Name, objElem.Key), objElem.Key),
					In,
					targetExpr,
					objElem,
				)
				conditions = append(conditions, propExistsCheck)

				// Recursively check the value pattern (condition only)
				propTarget := NewMemberExpr(targetExpr, NewIdentifier(objElem.Key.Name, objElem.Key), false, objElem)
				cond, _ := b.buildPatternCondition(objElem.Value, propTarget)
				conditions = append(conditions, cond)

				valuePat := b.buildDestructuringPattern(objElem.Value)
				objPatElems = append(objPatElems, NewObjKeyValuePat(objElem.Key.Name, valuePat, nil, objElem))

			case *ast.ObjShorthandPat:
				// Check that the property exists: "propName" in object
				propExistsCheck := NewBinaryExpr(
					NewLitExpr(NewStrLit(objElem.Key.Name, objElem.Key), objElem.Key),
					In,
					targetExpr,
					objElem,
				)
				conditions = append(conditions, propExistsCheck)

				objPatElems = append(objPatElems, NewObjShorthandPat(objElem.Key.Name, nil, objElem))

			case *ast.ObjRestPat:
				// TODO: Implement object rest pattern properly
				restPat := b.buildDestructuringPattern(objElem.Pattern)
				objPatElems = append(objPatElems, NewObjRestPat(restPat, objElem))
			}
		}

		// Only generate the destructuring binding at this level
		objectPat := NewObjectPat(objPatElems, pat)
		declarator := &Declarator{
			Pattern: objectPat,
			TypeAnn: nil,
			Init:    targetExpr,
		}
		varDecl := &VarDecl{
			Kind:    ValKind,
			Decls:   []*Declarator{declarator},
			declare: false,
			export:  false,
			span:    nil,
			source:  pat,
		}
		bindingStmt := &DeclStmt{
			Decl:   varDecl,
			span:   nil,
			source: pat,
		}

		// Combine all conditions with &&
		finalCondition := combineConditions(conditions, pat)

		return finalCondition, []Stmt{bindingStmt}

	case *ast.InstancePat:
		// Instance patterns: check instanceof and destructure the object pattern
		var conditions []Expr

		// Create instanceof check: targetExpr instanceof ClassName
		instanceofCheck := NewBinaryExpr(
			targetExpr,
			InstanceOf,
			NewIdentExpr(pat.ClassName, "", pat),
			pat,
		)
		conditions = append(conditions, instanceofCheck)

		// If there's an object pattern, recursively check it
		// TODO: Exclude pattern conditions for fields immediately on the class
		// instance so we know those exist based on the instanceof check.
		var bindingStmts []Stmt
		if pat.Object != nil {
			objCond, objBindings := b.buildPatternCondition(pat.Object, targetExpr)
			conditions = append(conditions, objCond)
			bindingStmts = append(bindingStmts, objBindings...)
		}

		// Combine all conditions with &&
		finalCondition := combineConditions(conditions, pat)

		return finalCondition, bindingStmts

	case *ast.ExtractorPat:
		// Extractor patterns: check instanceof the extractor class and call the custom matcher
		var conditions []Expr

		// Create instanceof check: targetExpr instanceof ExtractorName
		instanceofCheck := NewBinaryExpr(
			targetExpr,
			InstanceOf,
			NewIdentExpr(pat.Name, "", pat),
			pat,
		)
		conditions = append(conditions, instanceofCheck)

		// Create temporary variables for the extracted values
		tempVars := []Expr{}
		tempVarPats := []Pat{}

		for _, arg := range pat.Args {
			tempId := b.NewTempId()
			tempVar := NewIdentExpr(tempId, "", nil)

			var init Expr
			switch arg := arg.(type) {
			case *ast.IdentPat:
				if arg.Default != nil {
					// TODO: Handle defaults if needed
				}
			}
			tempVarPat := NewIdentPat(tempId, init, pat)

			tempVarPats = append(tempVarPats, tempVarPat)
			tempVars = append(tempVars, tempVar)
		}

		// Call the custom matcher: InvokeCustomMatcherOrThrow(extractor, subject, undefined)
		extractor := NewIdentExpr(pat.Name, "", pat)
		receiver := NewIdentExpr("undefined", "", nil)

		call := NewCallExpr(
			NewIdentExpr("InvokeCustomMatcherOrThrow", "", nil),
			[]Expr{extractor, targetExpr, receiver},
			false,
			nil,
		)

		// Create the tuple destructuring for the result
		decls := []*Declarator{
			{
				Pattern: NewTuplePat(tempVarPats, nil),
				TypeAnn: nil,
				Init:    call,
			},
		}

		decl := &VarDecl{
			Kind:    ValKind,
			Decls:   decls,
			declare: false,
			export:  false,
			span:    nil,
			source:  nil,
		}

		callStmt := &DeclStmt{
			Decl:   decl,
			span:   nil,
			source: nil,
		}

		var bindingStmts []Stmt
		bindingStmts = append(bindingStmts, callStmt)

		// Recursively build conditions and bindings for each argument pattern
		for i, argPat := range pat.Args {
			tempVar := tempVars[i]
			argCond, argBindings := b.buildPatternCondition(argPat, tempVar)
			conditions = append(conditions, argCond)
			bindingStmts = append(bindingStmts, argBindings...)
		}

		// Combine all conditions with &&
		finalCondition := combineConditions(conditions, pat)

		return finalCondition, bindingStmts

	default:
		// For now, handle other patterns as always matching
		return NewLitExpr(NewBoolLit(true, pattern), pattern), []Stmt{}
	}
}

// buildDestructuringPattern converts an AST pattern to a codegen pattern for destructuring
func (b *Builder) buildDestructuringPattern(pattern ast.Pat) Pat {
	switch pat := pattern.(type) {
	case *ast.IdentPat:
		return NewIdentPat(pat.Name, nil, pat)
	case *ast.WildcardPat:
		// Wildcards in destructuring are typically represented as identifier patterns
		// with a special name like "_" - but for now we'll skip them
		return NewIdentPat("_", nil, pat)
	case *ast.TuplePat:
		tupleElems := []Pat{}
		for _, elem := range pat.Elems {
			tupleElems = append(tupleElems, b.buildDestructuringPattern(elem))
		}
		return NewTuplePat(tupleElems, pat)
	case *ast.ObjectPat:
		objElems := []ObjPatElem{}
		for _, elem := range pat.Elems {
			switch objElem := elem.(type) {
			case *ast.ObjKeyValuePat:
				valuePat := b.buildDestructuringPattern(objElem.Value)
				objElems = append(objElems, NewObjKeyValuePat(objElem.Key.Name, valuePat, nil, objElem))
			case *ast.ObjShorthandPat:
				objElems = append(objElems, NewObjShorthandPat(objElem.Key.Name, nil, objElem))
			case *ast.ObjRestPat:
				restPat := b.buildDestructuringPattern(objElem.Pattern)
				objElems = append(objElems, NewObjRestPat(restPat, objElem))
			}
		}
		return NewObjectPat(objElems, pat)
	case *ast.RestPat:
		// Handle rest patterns properly for destructuring
		innerPat := b.buildDestructuringPattern(pat.Pattern)
		return NewRestPat(innerPat, pat)
	default:
		// For other patterns, default to an identifier pattern
		return NewIdentPat("_", nil, pat)
	}
}

// buildArrayLengthCheck creates a condition to check if an array has the expected length
func (b *Builder) buildArrayLengthCheck(arrayExpr Expr, expectedLength int, source ast.Node) Expr {
	lengthAccess := NewMemberExpr(
		arrayExpr,
		NewIdentifier("length", source),
		false,
		source,
	)
	expectedLengthExpr := NewLitExpr(NewNumLit(float64(expectedLength), source), source)
	return NewBinaryExpr(lengthAccess, EqualEqual, expectedLengthExpr, source)
}

// isTrueLiteral checks if an expression is a literal true value
func isTrueLiteral(expr Expr) bool {
	if litExpr, ok := expr.(*LitExpr); ok {
		if boolLit, ok := litExpr.Lit.(*BoolLit); ok {
			return boolLit.Value
		}
	}
	return false
}

// combineConditions combines multiple conditions with && operators,
// filtering out literal true values to avoid redundant conditions
func combineConditions(conditions []Expr, source ast.Node) Expr {
	// Filter out true literals
	var validConditions []Expr
	for _, condition := range conditions {
		if !isTrueLiteral(condition) {
			validConditions = append(validConditions, condition)
		}
	}

	// If no valid conditions, return true
	if len(validConditions) == 0 {
		return NewLitExpr(NewBoolLit(true, source), source)
	}

	// If only one condition, return it directly
	if len(validConditions) == 1 {
		return validConditions[0]
	}

	// Combine multiple conditions with &&
	result := validConditions[0]
	for i := 1; i < len(validConditions); i++ {
		result = NewBinaryExpr(result, LogicalAnd, validConditions[i], source)
	}

	return result
}
