package codegen

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/type_system"
)

type Builder struct {
	tempId        int
	depGraphV2    *dep_graph.DepGraphV2
	hasExtractor  bool
	isModule      bool
	inBlockScope  bool
	overloadDecls map[string][]*ast.FuncDecl // Function name -> list of overload declarations
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

					elems = append(elems, NewObjKeyValuePat(
						fullyQualifyName(e.Key.Name, nsName),
						buildPatternRec(e.Value, newTarget),
						nil,
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
			b.hasExtractor = true

			tempVars := []Expr{}
			tempVarPats := []Pat{}

			for _, arg := range p.Args {
				tempId := b.NewTempId()
				tempVar := NewIdentExpr(tempId, "", nil)

				var init Expr
				var tempVarPat Pat
				switch arg := arg.(type) {
				case *ast.IdentPat:
					if arg.Default != nil {
						var defStmts []Stmt
						defExpr, defStmts := b.buildExpr(arg.Default, nil)
						stmts = slices.Concat(stmts, defStmts)
						init = defExpr
					}
					tempVarPat = NewIdentPat(tempId, init, p)
				case *ast.RestPat:
					// For rest patterns, wrap the IdentPat in a RestPat
					tempVarPat = NewRestPat(NewIdentPat(tempId, nil, p), arg)
				case *ast.ExtractorPat:
					tempVarPat = NewIdentPat(tempId, init, p)
				default:
					panic("TODO - buildPattern - ExtractorPat - default case")
				}

				tempVarPats = append(tempVarPats, tempVarPat)
				tempVars = append(tempVars, tempVar)
			}
			extractor := NewIdentExpr(ast.QualIdentToString(p.Name), "", p)
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
				Kind:    VariableKind(kind),
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
				// If the arg is a RestPat, unwrap it since the rest has already been
				// destructured into the temp variable
				if restPat, ok := arg.(*ast.RestPat); ok {
					arg = restPat.Pattern
				}
				// If the arg is an IdentPat with a default, remove the default since it's
				// already been applied in the destructuring pattern
				if identPat, ok := arg.(*ast.IdentPat); ok && identPat.Default != nil {
					// Create a new IdentPat without the default
					arg = ast.NewIdentPat(identPat.Name, identPat.TypeAnn, nil, identPat.Span())
				}
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

			// If the expr is an EmptyExpr (used for terminal expressions),
			// don't create an ExprStmt for it
			if _, ok := expr.(*EmptyExpr); ok {
				return exprStmts
			}

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

			// If the expression is a throw (EmptyExpr), don't create
			// the return statement since throw never returns
			if _, ok := expr.(*EmptyExpr); ok {
				return stmts
			}
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
	b.isModule = false
	var stmts []Stmt
	for _, s := range mod.Stmts {
		stmts = slices.Concat(stmts, b.buildStmt(s))
	}

	if b.hasExtractor {
		// Add an import statement at the start of `stmts`
		importDecl := NewImportDecl(
			[]string{"InvokeCustomMatcherOrThrow"},
			"@escalier/runtime",
			nil,
		)
		importStmt := &DeclStmt{
			Decl:   importDecl,
			span:   nil,
			source: nil,
		}
		stmts = slices.Concat([]Stmt{importStmt}, stmts)

		// Reset hasExtractor for future builds
		b.hasExtractor = false
	}

	return &Module{
		Stmts: stmts,
	}
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
	case *ast.TypeDecl:
		return []Stmt{}
	case *ast.InterfaceDecl:
		return []Stmt{}
	case *ast.VarDecl:
		if d.Init == nil {
			panic("TODO - TransformDecl - VarDecl - Init is nil")
		}
		initExpr, initStmts := b.buildExpr(d.Init, nil)

		// If the init expression is a throw (EmptyExpr), don't create the variable
		// declaration, just return the throw statement
		if _, ok := initExpr.(*EmptyExpr); ok {
			return initStmts
		}

		// Ignore checks returned by buildPattern
		// Only export if we're in module mode AND not inside a block scope
		export := b.isModule && !b.inBlockScope
		_, patStmts := b.buildPattern(d.Pattern, initExpr, export, d.Kind, nsName)
		return slices.Concat(initStmts, patStmts)
	case *ast.FuncDecl:
		// Check if this is an overloaded function
		funcName := d.Name.Name
		if nsName != "" {
			funcName = nsName + "." + funcName
		}

		overloads, isOverloaded := b.overloadDecls[funcName]
		if isOverloaded && len(overloads) > 1 {
			// Only generate the dispatch function for the first overload we encounter
			// Check if this is the first one by comparing pointers
			if overloads[0] == d {
				return b.buildOverloadedFunc(overloads, nsName)
			}
			// Skip other overload declarations - they're handled by the first one
			return []Stmt{}
		}

		// Single function (non-overloaded) - use existing logic
		params, allParamStmts := b.buildParams(d.Params)
		if d.Body == nil {
			return []Stmt{}
		}

		// Mark that we're inside a function body
		prevInBlockScope := b.inBlockScope
		b.inBlockScope = true
		bodyStmts := slices.Concat(allParamStmts, b.buildStmts(d.Body.Stmts))
		b.inBlockScope = prevInBlockScope

		fnDecl := &FuncDecl{
			Name: &Identifier{
				Name:   fullyQualifyName(d.Name.Name, nsName),
				span:   nil,
				source: d.Name,
			},
			TypeParams: nil,
			Params:     params,
			Body:       bodyStmts,
			TypeAnn:    nil,
			declare:    decl.Declare(),
			export:     b.isModule && !prevInBlockScope,
			async:      d.Async,
			span:       nil,
			source:     decl,
		}
		stmt := &DeclStmt{
			Decl:   fnDecl,
			span:   nil,
			source: decl,
		}
		return []Stmt{stmt}
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
			export:  b.isModule,
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
	case *ast.EnumDecl:
		allStmts := []Stmt{}

		// Create a namespace object for the enum
		// e.g., const Color = {}
		enumNamespace := fullyQualifyName(d.Name.Name, nsName)
		namespacePattern := NewIdentPat(enumNamespace, nil, nil)
		namespaceInit := NewObjectExpr([]ObjExprElem{}, nil)

		namespaceDecl := &VarDecl{
			Kind: VariableKind(ast.ValKind),
			Decls: []*Declarator{
				{
					Pattern: namespacePattern,
					TypeAnn: nil,
					Init:    namespaceInit,
				},
			},
			declare: false,
			export:  b.isModule,
			span:    nil,
			source:  d,
		}

		allStmts = append(allStmts, &DeclStmt{
			Decl:   namespaceDecl,
			span:   nil,
			source: d,
		})

		// Process each enum element
		for _, elem := range d.Elems {
			switch elem := elem.(type) {
			case *ast.EnumVariant:
				// For each variant, create a class with:
				// 1. Constructor that accepts the variant's parameters
				// 2. Symbol.customMatcher method for pattern matching

				// Build constructor parameters
				params, paramStmts := b.buildParams(elem.Params)

				// Constructor body: assign parameters to instance properties
				var constructorBodyStmts []Stmt
				constructorBodyStmts = slices.Concat(constructorBodyStmts, paramStmts)

				// Assign each parameter to this.paramName
				for _, param := range elem.Params {
					if identPat, ok := param.Pattern.(*ast.IdentPat); ok {
						lhs := NewMemberExpr(
							NewIdentExpr("this", "", nil),
							NewIdentifier(identPat.Name, param.Pattern),
							false,
							nil,
						)
						rhs := NewIdentExpr(identPat.Name, "", param.Pattern)
						assignment := &ExprStmt{
							Expr:   NewBinaryExpr(lhs, Assign, rhs, param.Pattern),
							span:   nil,
							source: elem,
						}
						constructorBodyStmts = append(constructorBodyStmts, assignment)
					}
				}

				// Create constructor method
				constructorMethod := &MethodElem{
					Name:    NewIdentExpr("constructor", "", elem),
					Params:  params,
					Body:    constructorBodyStmts,
					MutSelf: nil,
					Static:  false,
					Private: false,
					Async:   false,
					span:    nil,
					source:  elem,
				}

				// Create Symbol.customMatcher method
				// This method destructures the instance and returns the parameters as a tuple
				// Method signature: [Symbol.customMatcher](subject) { return [subject.param1, subject.param2, ...]; }

				matcherParams := []*Param{
					{
						Pattern: NewIdentPat("subject", nil, elem),
						TypeAnn: nil,
					},
				}

				// Build return array with subject.paramName for each parameter
				var returnElements []Expr
				for _, param := range elem.Params {
					if identPat, ok := param.Pattern.(*ast.IdentPat); ok {
						returnElements = append(returnElements, NewMemberExpr(
							NewIdentExpr("subject", "", nil),
							NewIdentifier(identPat.Name, param.Pattern),
							false,
							nil,
						))
					}
				}

				matcherBody := []Stmt{
					&ReturnStmt{
						Expr:   NewArrayExpr(returnElements, nil),
						span:   nil,
						source: elem,
					},
				}

				// Use Symbol.customMatcher as the method key
				// We need to access Symbol.customMatcher which is a computed property
				symbolCustomMatcher := NewMemberExpr(
					NewIdentExpr("Symbol", "", nil),
					NewIdentifier("customMatcher", nil),
					false,
					nil,
				)

				matcherMethod := &MethodElem{
					Name:    NewComputedKey(symbolCustomMatcher, elem),
					Params:  matcherParams,
					Body:    matcherBody,
					MutSelf: nil,
					Static:  true,
					Private: false,
					Async:   false,
					span:    nil,
					source:  elem,
				}

				// Create the class for this variant
				classElems := []ClassElem{constructorMethod, matcherMethod}

				variantClassName := fullyQualifyName(d.Name.Name+"__"+elem.Name.Name, nsName)
				variantClass := &ClassDecl{
					Name: &Identifier{
						Name:   variantClassName,
						span:   nil,
						source: elem.Name,
					},
					Body:    classElems,
					export:  false,
					declare: false,
					span:    nil,
					source:  elem,
				}

				allStmts = append(allStmts, &DeclStmt{
					Decl:   variantClass,
					span:   nil,
					source: elem,
				})

				// Assign the variant class to the enum namespace
				// e.g., Color.RGB = Color__RGB
				assignExpr := NewBinaryExpr(
					NewMemberExpr(
						NewIdentExpr(enumNamespace, "", nil),
						NewIdentifier(elem.Name.Name, elem.Name),
						false,
						nil,
					),
					Assign,
					NewIdentExpr(variantClassName, "", elem),
					nil,
				)

				allStmts = append(allStmts, &ExprStmt{
					Expr:   assignExpr,
					span:   nil,
					source: elem,
				})

			case *ast.EnumSpread:
				// TODO: Handle enum spreads
				// For now, we'll panic as this is a more advanced feature
				panic("TODO: EnumSpread codegen not yet implemented")
			}
		}

		return allStmts
	default:
		str, _ := printer.Print(d, printer.DefaultOptions())
		fmt.Fprintf(os.Stderr, "d = %s\n", str)
		panic("TODO - TransformDecl - default case")
	}
}

// buildOverloadedFunc generates a single function with dispatch logic for overloaded functions
func (b *Builder) buildOverloadedFunc(overloads []*ast.FuncDecl, nsName string) []Stmt {
	if len(overloads) == 0 {
		return []Stmt{}
	}

	// All overloads should have the same name
	funcName := overloads[0].Name.Name

	// Filter out declare-only functions (they have no body)
	var implementedOverloads []*ast.FuncDecl
	for _, overload := range overloads {
		if overload.Body != nil {
			implementedOverloads = append(implementedOverloads, overload)
		}
	}

	// If all overloads are declare-only, skip codegen
	if len(implementedOverloads) == 0 {
		return []Stmt{}
	}

	// Helper function to count the specificity of a parameter type
	// For object types, count the number of required properties
	countTypeSpecificity := func(param *ast.Param) int {
		if param.TypeAnn == nil {
			return 0
		}
		switch typeAnn := param.TypeAnn.(type) {
		case *ast.ObjectTypeAnn:
			// Count required properties (non-optional)
			count := 0
			for _, elem := range typeAnn.Elems {
				if propType, ok := elem.(*ast.PropertyTypeAnn); ok {
					if !propType.Optional {
						count++
					}
				}
			}
			return count
		default:
			return 1 // Default specificity for other types
		}
	}

	// Sort overloads by specificity (descending) so that more specific overloads
	// are checked first. This is necessary because without arity checks, function
	// subtyping allows less specific functions to match more specific calls.
	// We sort by: 1) parameter count (more first), 2) type specificity (more first)
	slices.SortFunc(implementedOverloads, func(a, b *ast.FuncDecl) int {
		// First, compare by parameter count
		if len(a.Params) != len(b.Params) {
			return len(b.Params) - len(a.Params) // Descending order
		}

		// If same parameter count, compare by type specificity
		// Calculate total specificity for each overload
		aSpecificity := 0
		for _, param := range a.Params {
			aSpecificity += countTypeSpecificity(param)
		}

		bSpecificity := 0
		for _, param := range b.Params {
			bSpecificity += countTypeSpecificity(param)
		}

		return bSpecificity - aSpecificity // Descending order
	})

	// Collect all unique parameter names across overloads
	// We'll use the maximum parameter count and give them generic names
	maxParams := 0
	for _, overload := range implementedOverloads {
		if len(overload.Params) > maxParams {
			maxParams = len(overload.Params)
		}
	}

	// Generate parameter names: param0, param1, param2, ...
	params := make([]*Param, 0, maxParams)
	for i := 0; i < maxParams; i++ {
		paramName := fmt.Sprintf("param%d", i)
		params = append(params, &Param{
			Pattern:  NewIdentPat(paramName, nil, nil),
			Optional: false,
			TypeAnn:  nil,
		})
	}

	// Build the dispatch logic as nested if-else statements
	var buildDispatchChain func(int) Stmt
	buildDispatchChain = func(overloadIdx int) Stmt {
		if overloadIdx >= len(implementedOverloads) {
			// No more overloads - throw error
			errorMsg := fmt.Sprintf("No overload matches the provided arguments for function '%s'", funcName)
			return NewThrowStmt(
				NewNewExpr(
					NewIdentExpr("TypeError", "", nil),
					[]Expr{NewLitExpr(NewStrLit(errorMsg, nil), nil)},
					nil,
				),
				nil,
			)
		}

		overload := implementedOverloads[overloadIdx]

		if len(overload.Params) == 0 {
			// No parameters - this overload always matches
			prevInBlockScope := b.inBlockScope
			b.inBlockScope = true
			bodyStmts := b.buildStmts(overload.Body.Stmts)
			b.inBlockScope = prevInBlockScope
			return NewBlockStmt(bodyStmts, overload)
		}

		// Generate type guards for all parameters that need checking
		// We need to check enough parameters to distinguish this overload from remaining ones
		var guards []Expr
		for i, param := range overload.Params {
			if param.TypeAnn != nil {
				paramGuard := b.buildTypeGuard(NewIdentExpr(fmt.Sprintf("param%d", i), "", nil), param.TypeAnn)
				guards = append(guards, paramGuard)
			}
		}

		// Combine all guards with && operators
		var guard Expr
		if len(guards) == 0 {
			// No type annotations - accept anything
			guard = NewLitExpr(NewBoolLit(true, nil), nil)
		} else if len(guards) == 1 {
			guard = guards[0]
		} else {
			// Combine multiple guards with &&
			guard = guards[0]
			for _, g := range guards[1:] {
				guard = NewBinaryExpr(guard, LogicalAnd, g, nil)
			}
		}

		// Build the body for this overload
		prevInBlockScope := b.inBlockScope
		b.inBlockScope = true

		// Map params to expected names using buildPattern to handle all pattern types
		var bodyStmts []Stmt
		for j, param := range overload.Params {
			// Create the source expression: param{j}
			paramExpr := NewIdentExpr(fmt.Sprintf("param%d", j), "", nil)

			// Use buildPattern to handle all pattern types (IdentPat, destructuring, rest, etc.)
			// Pass export=false since these are local parameter bindings
			_, patternStmts := b.buildPattern(param.Pattern, paramExpr, false, ast.ValKind, "")
			bodyStmts = slices.Concat(bodyStmts, patternStmts)
		}

		bodyStmts = slices.Concat(bodyStmts, b.buildStmts(overload.Body.Stmts))
		b.inBlockScope = prevInBlockScope

		// Create if-else: if (guard) { body } else { next overload }
		return NewIfStmt(
			guard,
			NewBlockStmt(bodyStmts, overload),
			buildDispatchChain(overloadIdx+1),
			overload,
		)
	}

	dispatchStmt := buildDispatchChain(0)

	// Check if any overload is async - if so, the generated function must be async
	isAsync := false
	for _, overload := range implementedOverloads {
		if overload.Async {
			isAsync = true
			break
		}
	}

	// Create the function declaration
	fnDecl := &FuncDecl{
		Name: &Identifier{
			Name:   fullyQualifyName(funcName, nsName),
			span:   nil,
			source: overloads[0].Name,
		},
		TypeParams: nil,
		Params:     params,
		Body:       []Stmt{dispatchStmt},
		TypeAnn:    nil,
		declare:    false,
		export:     b.isModule,
		async:      isAsync,
		span:       nil,
		source:     overloads[0],
	}

	return []Stmt{&DeclStmt{
		Decl:   fnDecl,
		span:   nil,
		source: overloads[0],
	}}
}

// buildTypeOfCheck constructs a binary expression for typeof checks like `typeof x === "string"`
func (b *Builder) buildTypeOfCheck(valueExpr Expr, typeString string, operator BinaryOp, source ast.Node) Expr {
	typeofExpr := NewUnaryExpr(TypeOf, valueExpr, source)
	typeStringLit := NewLitExpr(NewStrLit(typeString, source), source)
	return NewBinaryExpr(typeofExpr, operator, typeStringLit, source)
}

// buildLit converts an ast.Lit to a codegen Lit
func buildLit(lit ast.Lit) Lit {
	switch l := lit.(type) {
	case *ast.BoolLit:
		return NewBoolLit(l.Value, l)
	case *ast.NumLit:
		return NewNumLit(l.Value, l)
	case *ast.StrLit:
		return NewStrLit(l.Value, l)
	case *ast.RegexLit:
		return NewRegexLit(l.Value, l)
	case *ast.BigIntLit:
		panic("TODO: big int literal")
	case *ast.NullLit:
		return NewNullLit(l)
	case *ast.UndefinedLit:
		return NewUndefinedLit(l)
	default:
		panic(fmt.Sprintf("buildLit: unsupported literal type: %T", lit))
	}
}

// buildArrayIsArrayCheck generates a call to Array.isArray(valueExpr)
func buildArrayIsArrayCheck(valueExpr Expr, source ast.Node) Expr {
	return NewCallExpr(
		NewMemberExpr(
			NewIdentExpr("Array", "", source),
			NewIdentifier("isArray", source),
			false,
			source,
		),
		[]Expr{valueExpr},
		false,
		source,
	)
}

// buildPropertyInObjectCheck generates a check for "propName" in objectExpr
func buildPropertyInObjectCheck(propName string, objectExpr Expr, source ast.Node) Expr {
	return NewBinaryExpr(
		NewLitExpr(NewStrLit(propName, source), source),
		In,
		objectExpr,
		source,
	)
}

// buildTypeGuard generates a runtime type check expression for a given type annotation
func (b *Builder) buildTypeGuard(valueExpr Expr, typeAnn ast.TypeAnn) Expr {
	switch t := typeAnn.(type) {
	case *ast.NumberTypeAnn:
		return b.buildTypeOfCheck(valueExpr, "number", StrictEqual, nil)
	case *ast.StringTypeAnn:
		return b.buildTypeOfCheck(valueExpr, "string", StrictEqual, nil)
	case *ast.BooleanTypeAnn:
		return b.buildTypeOfCheck(valueExpr, "boolean", StrictEqual, nil)
	case *ast.LitTypeAnn:
		// For literal types, check exact value
		litExpr := NewLitExpr(buildLit(t.Lit), nil)
		return NewBinaryExpr(valueExpr, StrictEqual, litExpr, nil)
	case *ast.ObjectTypeAnn:
		// For structural object types, check properties similar to buildPatternCondition
		var conditions []Expr

		// Check that it's not null
		notNull := NewBinaryExpr(
			valueExpr,
			StrictNotEqual,
			NewLitExpr(NewNullLit(nil), nil),
			nil,
		)
		conditions = append(conditions, notNull)

		// Check that typeof is "object"
		isObject := b.buildTypeOfCheck(valueExpr, "object", StrictEqual, nil)
		conditions = append(conditions, isObject)

		// For each required property, check that it exists
		for _, elem := range t.Elems {
			switch objElem := elem.(type) {
			case *ast.PropertyTypeAnn:
				// Check that the property exists: "propName" in object
				var propName string
				switch key := objElem.Name.(type) {
				case *ast.IdentExpr:
					propName = key.Name
				case *ast.StrLit:
					propName = key.Value
				default:
					continue // Skip computed properties
				}

				propExistsCheck := buildPropertyInObjectCheck(propName, valueExpr, nil)
				conditions = append(conditions, propExistsCheck)
				propAccess := NewMemberExpr(valueExpr, NewIdentifier(propName, nil), false, nil)
				propTypeGuard := b.buildTypeGuard(propAccess, objElem.Value)
				conditions = append(conditions, propTypeGuard)
			}
		}

		// Combine all conditions with &&
		return combineConditions(conditions, t)
	case *ast.TupleTypeAnn:
		// Check if it's an array
		return buildArrayIsArrayCheck(valueExpr, nil)
	case *ast.TypeRefTypeAnn:
		// For type references, expand the type and check if it's a nominal type
		inferredType := t.InferredType()
		if inferredType != nil {
			// Prune type variables to get the actual type
			prunedType := type_system.Prune(inferredType)

			// Check if it's a TypeRefType with a TypeAlias
			if typeRef, ok := prunedType.(*type_system.TypeRefType); ok {
				if typeRef.TypeAlias != nil {
					// Get the aliased type
					aliasedType := type_system.Prune(typeRef.TypeAlias.Type)

					// Check if the aliased type is a nominal object type
					if objType, ok := aliasedType.(*type_system.ObjectType); ok && objType.Nominal {
						// Generate instanceof check for nominal types
						typeName := ast.QualIdentToString(t.Name)
						return NewBinaryExpr(
							valueExpr,
							InstanceOf,
							NewIdentExpr(typeName, "", nil),
							nil,
						)
					}
				}
			}

			// Check if it's directly an object type (not aliased)
			if objType, ok := prunedType.(*type_system.ObjectType); ok && objType.Nominal {
				// Generate instanceof check for nominal types
				typeName := ast.QualIdentToString(t.Name)
				return NewBinaryExpr(
					valueExpr,
					InstanceOf,
					NewIdentExpr(typeName, "", nil),
					nil,
				)
			}

			// TODO(#289): handle non-object types
			// TODO(#289): handle structural object types
		}

		// For type references like Array<T>, try to infer the check
		if t.Name != nil {
			// Get the simple name from QualIdent
			name := ast.QualIdentToString(t.Name)
			switch name {
			case "Array":
				return buildArrayIsArrayCheck(valueExpr, nil)
			}
		}
		// Default: accept anything
		return NewLitExpr(NewBoolLit(true, nil), nil)
	default:
		// For complex types we can't easily check at runtime, accept anything
		return NewLitExpr(NewBoolLit(true, nil), nil)
	}
}

func (b *Builder) buildExpr(expr ast.Expr, parent ast.Expr) (Expr, []Stmt) {
	if expr == nil {
		return nil, []Stmt{}
	}

	switch expr := expr.(type) {
	case *ast.LiteralExpr:
		return NewLitExpr(buildLit(expr.Lit), expr), []Stmt{}
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
		if b.depGraphV2 != nil {
			namespaceStr = b.depGraphV2.GetNamespaceString(expr.Namespace)
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
				// If the object is not already an IdentExpr, extract it to a temp variable
				// to avoid duplicating complex expressions and running side-effects multiple times
				var bindTargetExpr Expr
				if _, isIdentExpr := objExpr.(*IdentExpr); isIdentExpr {
					bindTargetExpr = objExpr
				} else {
					tempVar, tempDeclStmt := b.createTempVar(expr.Object)

					// Initialize the temp variable with the object expression
					tempDecl := tempDeclStmt.(*DeclStmt).Decl.(*VarDecl)
					tempDecl.Decls[0].Init = objExpr
					tempDecl.Kind = ValKind

					objStmts = append(objStmts, tempDeclStmt)

					// Update the member expression to use the temp variable
					member = NewMemberExpr(tempVar, propExpr, expr.OptChain, expr)
					bindTargetExpr = tempVar
				}

				bindIdent := NewIdentifier("bind", nil)
				callee := NewMemberExpr(member, bindIdent, false, expr)
				call := NewCallExpr(callee, []Expr{bindTargetExpr}, false, nil)
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

				elems[i] = NewMethodExpr(
					key,
					params,
					slices.Concat(allParamStmts, b.buildStmts(elem.Fn.Body.Stmts)),
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
				elems[i] = NewSetterExpr(
					key,
					params,
					slices.Concat(allParamStmts, b.buildStmts(elem.Fn.Body.Stmts)),
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

		// Mark that we're inside a function body
		prevInBlockScope := b.inBlockScope
		b.inBlockScope = true
		bodyStmts := slices.Concat(allParamStmts, b.buildStmts(expr.Body.Stmts))
		b.inBlockScope = prevInBlockScope

		return NewFuncExpr(
			params,
			bodyStmts,
			expr.Async,
			expr,
		), []Stmt{}
	case *ast.DoExpr:
		return b.buildBlockWithTempVar(expr.Body.Stmts, expr)
	case *ast.IfElseExpr:
		// Check if all branches are terminal (return or throw in all paths)
		consIsTerminal := isASTBlockTerminal(expr.Cons.Stmts)
		altIsTerminal := expr.Alt != nil && ((expr.Alt.Block != nil && isASTBlockTerminal(expr.Alt.Block.Stmts)) ||
			(expr.Alt.Expr != nil && isASTExprTerminal(expr.Alt.Expr)))
		allBranchesTerminal := consIsTerminal && altIsTerminal

		// Build the condition
		condExpr, condStmts := b.buildExpr(expr.Cond, expr)

		if allBranchesTerminal {
			// All branches terminate - no need for temp variable
			// Just build the if-else as statements
			stmts := condStmts

			// Build the consequent (then branch) without temp assignment
			consStmts := b.buildStmts(expr.Cons.Stmts)

			var altStmt Stmt
			if expr.Alt != nil {
				var altStmts []Stmt

				if expr.Alt.Block != nil {
					// Alternative is a block
					altStmts = b.buildStmts(expr.Alt.Block.Stmts)
				} else if expr.Alt.Expr != nil {
					// Alternative is an expression
					altExpr, altExprStmts := b.buildExpr(expr.Alt.Expr, expr)
					altStmts = slices.Concat(altStmts, altExprStmts)

					// If it's a terminal expression, it should be a statement
					altStmts = append(altStmts, &ExprStmt{
						Expr:   altExpr,
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

			// Return an EmptyExpr as a placeholder since all branches are terminal
			// The caller should handle terminal expressions properly
			return NewEmptyExpr(expr), stmts
		}

		// Non-terminal branches - use temp variable
		// Generate a temporary variable for the if-else result
		tempVar, tempDeclStmt := b.createTempVar(expr)

		stmts := []Stmt{tempDeclStmt}
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

		// Since throw never returns, we don't need a temporary variable
		// Return an EmptyExpr as a placeholder since this is a terminal expression
		allStmts := argStmts
		allStmts = append(allStmts, throwStmt)

		return NewEmptyExpr(expr), allStmts
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
	case *ast.TemplateLitExpr:
		// Build the quasi strings
		quasis := make([]string, len(expr.Quasis))
		for i, quasi := range expr.Quasis {
			quasis[i] = quasi.Value
		}

		// Build the interpolated expressions
		exprs, stmts := b.buildExprs(expr.Exprs)

		return NewTemplateLitExpr(quasis, exprs, expr), stmts
	case *ast.TaggedTemplateLitExpr:
		// Build the tag expression
		tag, tagStmts := b.buildExpr(expr.Tag, expr)

		// Build the quasi strings
		quasis := make([]string, len(expr.Quasis))
		for i, quasi := range expr.Quasis {
			quasis[i] = quasi.Value
		}

		// Build the interpolated expressions
		exprs, exprStmts := b.buildExprs(expr.Exprs)
		stmts := slices.Concat(tagStmts, exprStmts)

		return NewTaggedTemplateLitExpr(tag, quasis, exprs, expr), stmts
	case *ast.IfLetExpr:
		// Generate a temporary variable for the if-let result
		tempVar, tempDeclStmt := b.createTempVar(expr)

		stmts := []Stmt{tempDeclStmt}

		// First, generate code for the target expression
		targetExpr, targetStmts := b.buildExpr(expr.Target, expr)
		stmts = slices.Concat(stmts, targetStmts)

		// Generate the condition and binding statements for the pattern
		condition, bindingStmts := b.buildPatternCondition(expr.Pattern, targetExpr)

		// For if-let expressions, check if the target type is nullable and add null/undefined check
		if expr.Target.InferredType() != nil {
			targetType := type_system.Prune(expr.Target.InferredType())
			if unionType, ok := targetType.(*type_system.UnionType); ok {
				// Check if the union contains null or undefined
				hasNull := false
				hasUndefined := false
				for _, t := range unionType.Types {
					if litType, ok := type_system.Prune(t).(*type_system.LitType); ok {
						if _, isNull := litType.Lit.(*type_system.NullLit); isNull {
							hasNull = true
						}
						if _, isUndefined := litType.Lit.(*type_system.UndefinedLit); isUndefined {
							hasUndefined = true
						}
					}
				}

				// Add null check if needed
				if hasNull {
					nullCheck := NewBinaryExpr(
						targetExpr,
						NotEqual,
						NewLitExpr(NewNullLit(expr), expr),
						expr,
					)
					condition = NewBinaryExpr(nullCheck, LogicalAnd, condition, expr)
				}

				// Add undefined check if needed
				if hasUndefined {
					undefinedCheck := NewBinaryExpr(
						targetExpr,
						NotEqual,
						NewLitExpr(NewUndefinedLit(&ast.UndefinedLit{}), expr),
						expr,
					)
					condition = NewBinaryExpr(undefinedCheck, LogicalAnd, condition, expr)
				}
			}
		}

		// Build the consequent (then branch) with assignments to temp variable
		consStmts := b.buildBlockStmtsWithTempAssignment(expr.Cons.Stmts, tempVar, expr)

		// Prepend the binding statements to the consequent
		consStmts = slices.Concat(bindingStmts, consStmts)

		// Create the consequent block
		consBlock := NewBlockStmt(consStmts, expr)

		// Build the alternative expression or block
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

				// Assign expression result to temp variable
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

		// Create the if statement
		ifStmt := NewIfStmt(condition, consBlock, altStmt, expr)
		stmts = append(stmts, ifStmt)

		// Return the temp variable and all generated statements
		return tempVar, stmts
	case *ast.TryCatchExpr:
		// Create a temporary variable for the result
		tempVar, tempDeclStmt := b.createTempVar(expr)
		stmts := []Stmt{tempDeclStmt}

		// Build the try block with temp assignment
		tryStmts := b.buildBlockStmtsWithTempAssignment(expr.Try.Stmts, tempVar, expr)
		tryBlock := NewBlockStmt(tryStmts, expr)

		// Create catch clause if there are catch cases
		var catchClause *CatchClause
		if len(expr.Catch) > 0 {
			// Create a temp variable for the caught error
			errorVar := NewIdentPat("__error", nil, expr)

			// Build if-else chain for catch cases (similar to match expression)
			errorIdent := NewIdentExpr("__error", "", expr)
			var catchBodyStmts []Stmt

			// Build the catch cases in reverse order to create if-else chain
			var currentStmt Stmt
			for i := len(expr.Catch) - 1; i >= 0; i-- {
				matchCase := expr.Catch[i]

				// Build pattern matching condition and bindings
				condition, bindingStmts := b.buildPatternCondition(matchCase.Pattern, errorIdent)

				// Build the case body statements
				var caseBodyStmts []Stmt

				// Handle guards - always use nested if structure to ensure bindings are available
				if matchCase.Guard != nil {
					// Add bindings before guard evaluation
					caseBodyStmts = slices.Concat(caseBodyStmts, bindingStmts)

					// Build guard as a nested if
					guardExpr, guardStmts := b.buildExpr(matchCase.Guard, expr)
					caseBodyStmts = slices.Concat(caseBodyStmts, guardStmts)

					// Create nested guard body
					var guardBodyStmts []Stmt

					// Add body statements or expression
					if matchCase.Body.Block != nil {
						body := b.buildBlockStmtsWithTempAssignment(matchCase.Body.Block.Stmts, tempVar, expr)
						guardBodyStmts = slices.Concat(guardBodyStmts, body)
					} else if matchCase.Body.Expr != nil {
						bodyExpr, bodyStmts := b.buildExpr(matchCase.Body.Expr, expr)
						guardBodyStmts = slices.Concat(guardBodyStmts, bodyStmts)
						assignment := NewBinaryExpr(tempVar, Assign, bodyExpr, matchCase.Body.Expr)
						guardBodyStmts = append(guardBodyStmts, &ExprStmt{
							Expr:   assignment,
							span:   nil,
							source: matchCase.Body.Expr,
						})
					}

					guardBlock := NewBlockStmt(guardBodyStmts, expr)
					guardIf := NewIfStmt(guardExpr, guardBlock, currentStmt, expr)
					caseBodyStmts = append(caseBodyStmts, guardIf)

					// Wrap in outer pattern condition block
					caseBlock := NewBlockStmt(caseBodyStmts, expr)
					currentStmt = NewIfStmt(condition, caseBlock, nil, expr)
					continue
				}

				// No guard - add pattern bindings to case body
				caseBodyStmts = slices.Concat(caseBodyStmts, bindingStmts)

				// Add body statements or expression
				if matchCase.Body.Block != nil {
					// Body is a block
					body := b.buildBlockStmtsWithTempAssignment(matchCase.Body.Block.Stmts, tempVar, expr)
					caseBodyStmts = slices.Concat(caseBodyStmts, body)
				} else if matchCase.Body.Expr != nil {
					// Body is an expression
					bodyExpr, bodyStmts := b.buildExpr(matchCase.Body.Expr, expr)
					caseBodyStmts = slices.Concat(caseBodyStmts, bodyStmts)

					// Assign expression result to temp variable
					assignment := NewBinaryExpr(tempVar, Assign, bodyExpr, matchCase.Body.Expr)
					caseBodyStmts = append(caseBodyStmts, &ExprStmt{
						Expr:   assignment,
						span:   nil,
						source: matchCase.Body.Expr,
					})
				}

				// Create block statement for the case
				caseBlock := NewBlockStmt(caseBodyStmts, expr)

				// Create if statement
				if currentStmt == nil {
					// Last case, no else clause
					currentStmt = NewIfStmt(condition, caseBlock, nil, expr)
				} else {
					// Previous cases become the else clause
					currentStmt = NewIfStmt(condition, caseBlock, currentStmt, expr)
				}
			}

			// If we have match cases but the chain might not be exhaustive,
			// add a final else that re-throws the error
			if currentStmt != nil {
				// Check if the last pattern is a wildcard (catches all)
				lastCase := expr.Catch[len(expr.Catch)-1]
				_, isWildcard := lastCase.Pattern.(*ast.WildcardPat)

				if !isWildcard {
					// Not exhaustive, add a re-throw
					rethrowStmt := NewThrowStmt(errorIdent, expr)
					rethrowBlock := NewBlockStmt([]Stmt{rethrowStmt}, expr)
					currentStmt = simplifyTrueLiterals(currentStmt)

					// Find the last if statement and add the re-throw as its else
					lastIf := currentStmt
					for {
						if ifStmt, ok := lastIf.(*IfStmt); ok {
							if ifStmt.Alt == nil {
								ifStmt.Alt = rethrowBlock
								break
							}
							lastIf = ifStmt.Alt
						} else {
							break
						}
					}
				} else {
					currentStmt = simplifyTrueLiterals(currentStmt)
				}

				catchBodyStmts = append(catchBodyStmts, currentStmt)
			}

			catchBlock := NewBlockStmt(catchBodyStmts, expr)
			catchClause = &CatchClause{
				Param: errorVar,
				Body:  catchBlock,
			}
		}

		// Create the try-catch statement
		tryStmt := NewTryStmt(tryBlock, catchClause, nil, expr)
		stmts = append(stmts, tryStmt)

		return tempVar, stmts
	case *ast.JSXElementExpr:
		panic("TODO - buildExpr - JSXElementExpr")
	case *ast.JSXFragmentExpr:
		panic("TODO - buildExpr - JSXFragmentExpr")
	case *ast.EmptyExpr:
		undefined := NewLitExpr(NewUndefinedLit(&ast.UndefinedLit{}), expr)
		return undefined, []Stmt{}
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

// isASTBlockTerminal checks if an AST block is guaranteed to terminate execution
func isASTBlockTerminal(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	lastStmt := stmts[len(stmts)-1]
	switch s := lastStmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		return isASTExprTerminal(s.Expr)
	default:
		return false
	}
}

// isASTExprTerminal checks if an AST expression is guaranteed to terminate execution
func isASTExprTerminal(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.ThrowExpr:
		return true
	case *ast.IfElseExpr:
		// If-else is terminal if both branches are terminal
		consIsTerminal := isASTBlockTerminal(e.Cons.Stmts)
		altIsTerminal := e.Alt != nil && ((e.Alt.Block != nil && isASTBlockTerminal(e.Alt.Block.Stmts)) ||
			(e.Alt.Expr != nil && isASTExprTerminal(e.Alt.Expr)))
		return consIsTerminal && altIsTerminal
	default:
		return false
	}
}

// isTerminalStmt checks if a statement is guaranteed to terminate execution
// (return, throw, or if-else with returns in all branches)
func isTerminalStmt(stmt Stmt) bool {
	switch s := stmt.(type) {
	case *ReturnStmt:
		return true
	case *ThrowStmt:
		return true
	case *IfStmt:
		// If statement is terminal if the consequent is terminal AND
		// there is an alternative that is also terminal
		if s.Alt == nil {
			return false // if without else can skip the body
		}
		return isTerminalStmt(s.Cons) && isTerminalStmt(s.Alt)
	case *BlockStmt:
		// Block is terminal if its last statement is terminal
		if len(s.Stmts) == 0 {
			return false
		}
		return isTerminalStmt(s.Stmts[len(s.Stmts)-1])
	default:
		return false
	}
}

// buildBlockStmtsWithTempAssignment builds statements for a block, treating the last
// statement specially by assigning its result to the given temp variable.
func (b *Builder) buildBlockStmtsWithTempAssignment(stmts []ast.Stmt, tempVar Expr, sourceExpr ast.Expr) []Stmt {
	blockStmts := []Stmt{}

	// Mark that we're inside a block scope
	prevInBlockScope := b.inBlockScope
	b.inBlockScope = true
	defer func() {
		b.inBlockScope = prevInBlockScope
	}()

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

			// Don't create assignment for throw expressions since they never return
			_, isThrow := exprStmt.Expr.(*ast.ThrowExpr)
			if !isThrow {
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
			}
		} else {
			// Last statement is not an expression, add it as-is
			builtLastStmts := b.buildStmt(lastStmt)
			blockStmts = slices.Concat(blockStmts, builtLastStmts)

			// Only assign undefined if the last statement is not a terminal statement
			// (return statements, throw expressions, and if-else statements with
			// returns in all branches end execution, so no assignment is needed)
			// Note: We check isTerminalStmt on the *built* statement rather than the AST
			// because buildStmt can transform statements in ways that affect terminality.
			// For example, buildStmt might return multiple statements or transform an
			// expression statement containing a throw into a terminal ThrowStmt.
			if len(builtLastStmts) > 0 && !isTerminalStmt(builtLastStmts[len(builtLastStmts)-1]) {
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
				// Mark that we're inside a method body
				prevInBlockScope := b.inBlockScope
				b.inBlockScope = true
				bodyStmts = b.buildStmts(e.Fn.Body.Stmts)
				b.inBlockScope = prevInBlockScope
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
				// Mark that we're inside a getter body
				prevInBlockScope := b.inBlockScope
				b.inBlockScope = true
				bodyStmts = b.buildStmts(e.Fn.Body.Stmts)
				b.inBlockScope = prevInBlockScope
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
				// Mark that we're inside a setter body
				prevInBlockScope := b.inBlockScope
				b.inBlockScope = true
				bodyStmts = b.buildStmts(e.Fn.Body.Stmts)
				b.inBlockScope = prevInBlockScope
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

		// Build the case body statements
		var caseStmts []Stmt

		// Handle guards - always use nested if structure to ensure bindings are available
		if matchCase.Guard != nil {
			// Add bindings before guard evaluation
			caseStmts = slices.Concat(caseStmts, patternBindings)

			// Build guard as a nested if
			guardExpr, guardStmts := b.buildExpr(matchCase.Guard, expr)
			caseStmts = slices.Concat(caseStmts, guardStmts)

			// Create nested guard body
			var guardBodyStmts []Stmt

			// Add body statements or expression
			if matchCase.Body.Block != nil {
				blockStmts := b.buildBlockStmtsWithTempAssignment(matchCase.Body.Block.Stmts, tempVar, expr)
				guardBodyStmts = slices.Concat(guardBodyStmts, blockStmts)
			} else if matchCase.Body.Expr != nil {
				bodyExpr, bodyStmts := b.buildExpr(matchCase.Body.Expr, expr)
				guardBodyStmts = slices.Concat(guardBodyStmts, bodyStmts)
				assignment := NewBinaryExpr(tempVar, Assign, bodyExpr, matchCase.Body.Expr)
				guardBodyStmts = append(guardBodyStmts, &ExprStmt{
					Expr:   assignment,
					span:   nil,
					source: matchCase.Body.Expr,
				})
			}

			guardBlock := NewBlockStmt(guardBodyStmts, expr)
			guardIf := NewIfStmt(guardExpr, guardBlock, currentStmt, expr)
			caseStmts = append(caseStmts, guardIf)

			// Wrap in outer pattern condition block
			caseBlock := NewBlockStmt(caseStmts, expr)
			currentStmt = NewIfStmt(patternCond, caseBlock, nil, expr)
			continue
		}

		// No guard - add pattern bindings to case body
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
			currentStmt = NewIfStmt(patternCond, caseBlock, nil, expr)
		} else {
			// Previous cases become the else clause
			currentStmt = NewIfStmt(patternCond, caseBlock, currentStmt, expr)
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
			inferred := type_system.Prune(pat.TypeAnn.InferredType())
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
		var defaultStmts []Stmt

		for _, elem := range pat.Elems {
			switch objElem := elem.(type) {
			case *ast.ObjKeyValuePat:
				// Check that the property exists: "propName" in object
				propExistsCheck := buildPropertyInObjectCheck(objElem.Key.Name, targetExpr, objElem)
				conditions = append(conditions, propExistsCheck)

				// Recursively check the value pattern (condition only)
				propTarget := NewMemberExpr(targetExpr, NewIdentifier(objElem.Key.Name, objElem.Key), false, objElem)
				cond, _ := b.buildPatternCondition(objElem.Value, propTarget)
				conditions = append(conditions, cond)

				valuePat := b.buildDestructuringPattern(objElem.Value)
				objPatElems = append(objPatElems, NewObjKeyValuePat(objElem.Key.Name, valuePat, nil, objElem))

			case *ast.ObjShorthandPat:
				// Check that the property exists: "propName" in object
				propExistsCheck := buildPropertyInObjectCheck(objElem.Key.Name, targetExpr, objElem)
				conditions = append(conditions, propExistsCheck)

				// Handle defaults for shorthand patterns
				var defExpr Expr
				if objElem.Default != nil {
					var defStmts []Stmt
					defExpr, defStmts = b.buildExpr(objElem.Default, nil)
					defaultStmts = append(defaultStmts, defStmts...)
				}

				objPatElems = append(objPatElems, NewObjShorthandPat(objElem.Key.Name, defExpr, objElem))

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

		// Combine all binding statements (defaults first, then destructuring)
		var allBindingStmts []Stmt
		allBindingStmts = append(allBindingStmts, defaultStmts...)
		allBindingStmts = append(allBindingStmts, bindingStmt)

		// Combine all conditions with &&
		finalCondition := combineConditions(conditions, pat)

		return finalCondition, allBindingStmts

	case *ast.InstancePat:
		// Instance patterns: check instanceof and destructure the object pattern
		var conditions []Expr

		// Create instanceof check: targetExpr instanceof ClassName
		instanceofCheck := NewBinaryExpr(
			targetExpr,
			InstanceOf,
			NewIdentExpr(ast.QualIdentToString(pat.ClassName), "", pat),
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
		b.hasExtractor = true

		// Extractor patterns: check instanceof the extractor class and call the custom matcher
		var conditions []Expr

		// Create instanceof check: targetExpr instanceof ExtractorName
		instanceofCheck := NewBinaryExpr(
			targetExpr,
			InstanceOf,
			NewIdentExpr(ast.QualIdentToString(pat.Name), "", pat),
			pat,
		)
		conditions = append(conditions, instanceofCheck)

		// Create temporary variables for the extracted values
		tempVars := []Expr{}
		tempVarPats := []Pat{}
		var defaultStmts []Stmt

		for _, arg := range pat.Args {
			tempId := b.NewTempId()
			tempVar := NewIdentExpr(tempId, "", nil)

			var init Expr
			switch arg := arg.(type) {
			case *ast.IdentPat:
				if arg.Default != nil {
					defExpr, defStmts := b.buildExpr(arg.Default, nil)
					defaultStmts = append(defaultStmts, defStmts...)
					init = defExpr
				}
			}
			tempVarPat := NewIdentPat(tempId, init, pat)

			tempVarPats = append(tempVarPats, tempVarPat)
			tempVars = append(tempVars, tempVar)
		}

		// Call the custom matcher: InvokeCustomMatcherOrThrow(extractor, subject, undefined)
		extractor := NewIdentExpr(ast.QualIdentToString(pat.Name), "", pat)
		receiver := NewIdentExpr("undefined", "", nil)

		call := NewCallExpr(
			NewIdentExpr("InvokeCustomMatcherOrThrow", "", nil),
			[]Expr{extractor, targetExpr, receiver},
			false,
			nil,
		) // Create the tuple destructuring for the result
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
		// Add any statements from building default expressions first
		bindingStmts = append(bindingStmts, defaultStmts...)
		// Then add the call to the custom matcher
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
		var defExpr Expr
		if pat.Default != nil {
			var defStmts []Stmt
			defExpr, defStmts = b.buildExpr(pat.Default, nil)
			// Note: defStmts are ignored here as they should have been handled
			// by the calling code in buildPatternCondition
			_ = defStmts
		}
		return NewIdentPat(pat.Name, defExpr, pat)
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
