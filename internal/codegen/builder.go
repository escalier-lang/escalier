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
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

type Builder struct {
	tempId          int
	depGraph        *dep_graph.DepGraph
	hasExtractor    bool
	hasJsx          bool // tracks if _jsx is used
	hasJsxs         bool // tracks if _jsxs is used
	hasFragment     bool // tracks if _Fragment is used
	isModule        bool
	inBlockScope    bool
	overloadDecls   map[string][]*ast.FuncDecl // Function name -> list of overload declarations
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

// BuildTopLevelDecls builds JavaScript code from a dependency graph.
// This version uses BindingKey instead of DeclID and automatically handles
// overloaded functions and interface merging.
func (b *Builder) BuildTopLevelDecls(depGraph *dep_graph.DepGraph) *Module {
	// Set up builder state
	b.depGraph = depGraph // Store dep graph for namespace lookups
	b.isModule = true

	var stmts []Stmt

	// Build namespace hierarchy statements
	nsStmts := b.buildNamespaceStatements(depGraph)
	stmts = slices.Concat(stmts, nsStmts)

	// Track which declarations we've already processed to avoid duplicates.
	// A single VarDecl with pattern destructuring introduces multiple bindings
	// but should only emit code once. For example:
	//   val C(D(msg), E(x, y)) = subject
	// This creates three binding keys in the dep_graph Decls map:
	//   "value:msg" → [VarDecl]
	//   "value:x"   → [VarDecl]
	//   "value:y"   → [VarDecl]
	// All pointing to the same VarDecl instance. When we iterate over binding keys
	// below, we'll encounter this declaration three times. We track processed
	// declarations to ensure we only emit the code once, on the first binding key
	// we encounter.
	processedDecls := set.NewSet[ast.Decl]()

	// Iterate over components in topological order
	for _, component := range depGraph.Components {
		for _, key := range component {
			decls := depGraph.GetDecls(key)
			if len(decls) == 0 {
				continue
			}

			nsName := depGraph.GetNamespace(key)

			// Skip type-only bindings unless they also have a value binding
			if key.Kind() == dep_graph.DepKindType {
				// Check if there's also a value binding with the same name
				valueKey := dep_graph.ValueBindingKey(key.Name())
				if !depGraph.HasBinding(valueKey) {
					continue // Type-only, skip codegen
				}
				// If there is a value binding, that will handle codegen
				continue
			}

			// Handle multiple declarations (overloaded functions)
			if len(decls) > 1 {
				// Check if all declarations are function declarations
				allFuncs := true
				funcDecls := make([]*ast.FuncDecl, 0, len(decls))
				for _, decl := range decls {
					if fd, ok := decl.(*ast.FuncDecl); ok {
						funcDecls = append(funcDecls, fd)
					} else {
						allFuncs = false
						break
					}
				}

				if allFuncs && len(funcDecls) > 0 {
					// Build overloaded function dispatch
					stmts = slices.Concat(stmts, b.buildOverloadedFunc(funcDecls, nsName))
					continue
				}

				// For interface merging, only the first declaration is used for codegen
				// (interfaces don't generate runtime code)
			}

			// Single declaration or first of merged declarations
			decl := decls[0]

			// Build the declaration only once
			// (VarDecls with pattern destructuring appear under multiple binding keys)
			if !processedDecls.Contains(decl) {
				processedDecls.Add(decl)
				stmts = slices.Concat(stmts, b.buildDeclWithNamespace(decl, nsName))
			}

			// Handle namespace assignment for namespaced bindings
			// This needs to happen for EVERY binding, even if the declaration was already processed
			// For example, with `val {x, y} = getPoint()` in namespace "coords", we need both:
			//   coords.x = coords__x
			//   coords.y = coords__y
			//
			// A member the source did not mark `export` is left off the namespace object. The
			// object is the module's export for the namespace, so a property on it is reachable
			// by any consumer. Code elsewhere in the module still reaches the member, because
			// unexportedNsMember rewrites a reference such as `constants.PI` to the mangled
			// name `constants__PI`.
			bindingName := key.Name()
			if strings.Contains(bindingName, ".") && decl.Export() {
				parts := strings.Split(bindingName, ".")
				dunderName := strings.Join(parts, "__")

				// Create assignment: namespace.member = dunderName
				assignExpr := NewBinaryExpr(
					NewIdentExpr(bindingName, "", nil),
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

	// Add JSX runtime imports if needed
	if b.hasJsx || b.hasJsxs || b.hasFragment {
		var specifiers []string
		if b.hasJsx {
			specifiers = append(specifiers, "jsx as _jsx")
		}
		if b.hasJsxs {
			specifiers = append(specifiers, "jsxs as _jsxs")
		}
		if b.hasFragment {
			specifiers = append(specifiers, "Fragment as _Fragment")
		}

		importDecl := NewImportDecl(
			specifiers,
			"react/jsx-runtime",
			nil,
		)
		importStmt := &DeclStmt{
			Decl:   importDecl,
			span:   nil,
			source: nil,
		}
		stmts = slices.Concat([]Stmt{importStmt}, stmts)

		// Reset JSX flags for future builds
		b.hasJsx = false
		b.hasJsxs = false
		b.hasFragment = false
	}

	return &Module{
		Stmts: stmts,
	}
}

// buildNamespaceStatements generates statements to create namespace objects
// for all namespaces in the dependency graph
func (b *Builder) buildNamespaceStatements(depGraph *dep_graph.DepGraph) []Stmt {
	// Track which namespace segments have been defined to avoid redefinition
	definedNamespaces := set.NewSet[string]()
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

func fullyQualifyName(name, nsName string) string {
	if nsName == "" {
		return name
	}
	return strings.ReplaceAll(nsName, ".", "__") + "__" + name
}

// memberChainSegments flattens a dotted reference such as `app.config.multiplier` into the
// segments ["app", "config", "multiplier"]. ok is false when the chain is rooted in something
// other than a plain identifier, or when any link is an optional-chaining `?.`, because
// neither form can name a namespace member.
func memberChainSegments(expr *ast.MemberExpr) ([]string, bool) {
	var segments []string

	var walk func(e ast.Expr) bool
	walk = func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.IdentExpr:
			segments = append(segments, e.Name)
			return true
		case *ast.MemberExpr:
			if e.OptChain || e.Prop == nil {
				return false
			}
			if !walk(e.Object) {
				return false
			}
			segments = append(segments, e.Prop.Name)
			return true
		default:
			return false
		}
	}

	if !walk(expr) {
		return nil, false
	}
	return segments, true
}

// unexportedNsMember matches a dotted reference against the namespace members the module keeps
// to itself, and reports the mangled name to emit in place of the property access.
//
// segments comes from memberChainSegments. The longest prefix of it that names a value binding
// in the dep graph is the member being referenced, and the rest are ordinary property accesses
// on that member's value. In `app.config.multiplier` the binding is `app.config` and
// `multiplier` is a field of the object it holds, so the prefix length returned is 2.
//
// A prefix shorter than two segments is a root-level binding rather than a namespace member,
// and those stay reachable under their own name, so matching starts at two. ok is false when no
// prefix names a binding, and when the binding it names is exported. An exported member is a
// property on the namespace object, so a reference to it needs no rewriting.
func (b *Builder) unexportedNsMember(segments []string) (name string, prefixLen int, ok bool) {
	if b.depGraph == nil {
		return "", 0, false
	}

	for n := len(segments); n >= 2; n-- {
		decls := b.depGraph.GetDecls(dep_graph.ValueBindingKey(strings.Join(segments[:n], ".")))
		if len(decls) == 0 {
			continue
		}
		for _, decl := range decls {
			if decl.Export() {
				return "", 0, false
			}
		}
		return strings.Join(segments[:n], "__"), n, true
	}

	return "", 0, false
}

// nsValueRef returns the emitted name for a dotted reference to a value, such as the
// `MyEnum.Color` an `instanceof` check or a custom matcher call names. A reference to an
// unexported namespace member becomes the mangled name, so `MyEnum.Color` is emitted as
// `MyEnum__Color`. Every other name is returned unchanged.
func (b *Builder) nsValueRef(name string) string {
	segments := strings.Split(name, ".")
	mangled, prefixLen, ok := b.unexportedNsMember(segments)
	if !ok {
		return name
	}
	return strings.Join(append([]string{mangled}, segments[prefixLen:]...), ".")
}

// exportDecl reports whether a top-level declaration gets an `export` keyword. nsName is the
// namespace the declaration is written in, and inBlockScope says it sits inside a function
// body or other block rather than at the top level of the module.
//
// A declaration inside a namespace is not exported under its own name. fullyQualifyName
// mangles that name, so `class Base` in namespace `geo` is emitted as `geo__Base`. No such
// spelling appears in the source, and exporting it would put a name the author never wrote in
// the module's public surface. It could also collide with a root-level declaration spelled the
// same way.
//
// Consumers reach the declaration through the namespace object, which buildNamespaceHierarchy
// exports. `geo.Base` is imported as `import { geo }` and read as `geo.Base`. That is what the
// `.d.ts` declares for the namespace and what a bin script's generated import expects.
func (b *Builder) exportDecl(nsName string, inBlockScope bool) bool {
	return b.isModule && !inBlockScope && nsName == ""
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
			extractor := NewIdentExpr(b.nsValueRef(ast.QualIdentToString(p.Name)), "", p)
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
					arg = ast.NewIdentPat(identPat.Name, identPat.Mutable, identPat.TypeAnn, nil, identPat.Span())
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
		case *ast.ErrorExpr:
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
	case *ast.ForInStmt:
		// Build the iterable expression
		iterableExpr, iterableStmts := b.buildExpr(s.Iterable, nil)

		// Build the loop variable pattern using a temp variable
		id := b.NewTempId()
		_, patStmts := b.buildPattern(s.Pattern, NewIdentExpr(id, "", nil), false, ast.ValKind, "")

		// Build body statements
		bodyStmts := b.buildStmts(s.Body.Stmts)
		bodyStmts = slices.Concat(patStmts, bodyStmts)

		pat := NewIdentPat(id, nil, s.Pattern)

		forOfStmt := NewForOfStmt(pat, iterableExpr, bodyStmts, s.IsAwait, s)
		return append(iterableStmts, forOfStmt)
	case *ast.ErrorStmt:
		return []Stmt{}
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

	// Add JSX runtime imports if needed
	if b.hasJsx || b.hasJsxs || b.hasFragment {
		var specifiers []string
		if b.hasJsx {
			specifiers = append(specifiers, "jsx as _jsx")
		}
		if b.hasJsxs {
			specifiers = append(specifiers, "jsxs as _jsxs")
		}
		if b.hasFragment {
			specifiers = append(specifiers, "Fragment as _Fragment")
		}

		importDecl := NewImportDecl(
			specifiers,
			"react/jsx-runtime",
			nil,
		)
		importStmt := &DeclStmt{
			Decl:   importDecl,
			span:   nil,
			source: nil,
		}
		stmts = slices.Concat([]Stmt{importStmt}, stmts)

		// Reset JSX flags for future builds
		b.hasJsx = false
		b.hasJsxs = false
		b.hasFragment = false
	}

	return &Module{
		Stmts: stmts,
	}
}

// buildNamespaceHierarchy generates statements to create a namespace hierarchy
// For "foo.bar.baz", it generates: export const foo = {}; foo.bar = {}; foo.bar.baz = {};
//
// Only the outermost object carries `export`. It is the module's single export for everything
// in the namespace, and the nested levels are reachable as properties on it. See exportDecl
// for why namespace members are not exported individually.
func (b *Builder) buildNamespaceHierarchy(namespace string, definedNamespaces set.Set[string]) []Stmt {
	if namespace == "" {
		return []Stmt{}
	}

	parts := strings.Split(namespace, ".")
	var stmts []Stmt

	// Build each level of the namespace hierarchy
	for i := 1; i <= len(parts); i++ {
		currentNS := strings.Join(parts[:i], ".")

		// Skip if this namespace level has already been defined
		if definedNamespaces.Contains(currentNS) {
			continue
		}
		definedNamespaces.Add(currentNS)

		if i == 1 {
			// First level: export const foo = {};
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
				export:  b.isModule,
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

		if d.Else != nil {
			return slices.Concat(initStmts, b.buildValElse(d, initExpr))
		}

		// Ignore checks returned by buildPattern
		export := b.exportDecl(nsName, b.inBlockScope)
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
			export:     b.exportDecl(nsName, prevInBlockScope),
			async:      d.Async,
			generator:  d.Gen || containsYield(d.Body.Stmts),
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

		// Every class has at most one in-body ConstructorElem (user-written
		// or synthesized in Phase 2.7). buildClassElems emits the
		// constructor JS from that element directly.
		var superClass Expr
		if d.Extends != nil {
			if name, ok := b.superClassName(d.Extends, nsName); ok {
				superClass = NewIdentExpr(name, "", d.Extends)
			}
		}
		classElems, classStmts := b.buildClassElems(d.Body, superClass != nil)
		allStmts = slices.Concat(allStmts, classStmts)

		classDecl := &ClassDecl{
			Name: &Identifier{
				Name:   fullyQualifyName(d.Name.Name, nsName),
				span:   nil,
				source: d.Name,
			},
			SuperClass: superClass,
			Body:       classElems,
			export:     b.exportDecl(nsName, b.inBlockScope),
			declare:    d.Declare(),
			span:       nil,
			source:     d,
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
			export:  b.exportDecl(nsName, b.inBlockScope),
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
				constructorMethod := NewMethodElem(
					NewIdentExpr("constructor", "", elem),
					params,
					constructorBodyStmts,
					MethodElemOptions{},
					elem,
				)

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

				matcherMethod := NewMethodElem(
					NewComputedKey(symbolCustomMatcher, elem),
					matcherParams,
					matcherBody,
					MethodElemOptions{Static: true},
					elem,
				)

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

	// Order the arms so the chain tests the most specific first, matching the order the
	// checker resolves a call in. See overload_order.go.
	//
	// The WHOLE set is ranked and the declare-only arms are dropped from the result,
	// rather than dropped first and the rest ranked on their own. DispatchOrder ranks an
	// arm by how many OTHER arms outrank it, so an arm removed before the ranking lowers
	// the count of every arm it outranked. That can reorder two arms the checker, which
	// ranks the whole set, kept apart.
	var implementedOverloads []*ast.FuncDecl
	for _, overload := range DispatchOrder(overloads) {
		// A declare-only arm has no body to dispatch to, so it contributes no branch.
		if overload.Body != nil {
			implementedOverloads = append(implementedOverloads, overload)
		}
	}
	if len(implementedOverloads) == 0 {
		return []Stmt{}
	}

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

	// Check if any overload is a generator - if so, the generated function must be a generator
	isGenerator := false
	for _, overload := range implementedOverloads {
		if overload.Gen || (overload.Body != nil && containsYield(overload.Body.Stmts)) {
			isGenerator = true
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
		export:     b.exportDecl(nsName, b.inBlockScope),
		async:      isAsync,
		generator:  isGenerator,
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
		// A reference to a nominal type is tested with `instanceof` against the class
		// name. nominalGuardName resolves the reference through its inferred type, either
		// directly or through a type alias.
		//
		// TODO(#289): handle non-object types
		// TODO(#289): handle structural object types
		if typeName, nominal := nominalGuardName(t); nominal {
			return NewBinaryExpr(
				valueExpr,
				InstanceOf,
				NewIdentExpr(typeName, "", nil),
				nil,
			)
		}
		if isArrayTypeRef(t) {
			return buildArrayIsArrayCheck(valueExpr, nil)
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
		if jsExpr, ok := jsExprFromOwner(expr.Owner); ok {
			return buildDottedJSExpr(jsExpr, expr), []Stmt{}
		}
		var namespaceStr string
		if b.depGraph != nil {
			namespaceStr = b.depGraph.GetNamespaceString(expr.Namespace)
		}
		return NewIdentExpr(expr.Name, namespaceStr, expr), []Stmt{}
	case *ast.SuperCallExpr:
		argsExprs, argsStmts := b.buildExprs(expr.Args)
		return NewCallExpr(NewIdentExpr("super", "", expr), argsExprs, false, expr), argsStmts
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
		if jsExpr, ok := memberJSExpr(expr); ok {
			// `math.sin` collapses to the @js-decorated declaration's
			// argument (e.g. `Math.sin`). The original receiver doesn't
			// participate in the lowered output, so we drop the
			// receiver-binding / temp-var logic below.
			return buildDottedJSExpr(jsExpr, expr), []Stmt{}
		}
		// `constants.PI` reaches an unexported member of namespace `constants`, which is left
		// off the `constants` object, so the reference is emitted as the mangled name
		// `constants__PI` instead. Segments past the member itself stay property accesses, so
		// `app.config.multiplier` becomes `app__config.multiplier`.
		if segments, ok := memberChainSegments(expr); ok {
			if name, prefixLen, ok := b.unexportedNsMember(segments); ok {
				var target Expr = NewIdentExpr(name, "", expr)
				for _, segment := range segments[prefixLen:] {
					target = NewMemberExpr(target, NewIdentifier(segment, nil), false, expr)
				}
				return target, []Stmt{}
			}
		}

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
	case *ast.ArraySpreadExpr:
		argExpr, argStmts := b.buildExpr(expr.Value, expr)
		return NewSpreadExpr(argExpr, expr), argStmts
	case *ast.ObjectExpr:
		stmts := []Stmt{}
		elems := make([]ObjExprElem, len(expr.Elems))
		for i, elem := range expr.Elems {
			switch elem := elem.(type) {
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
			FuncExprOptions{
				Async:     expr.Async,
				Generator: expr.Gen || containsYield(expr.Body.Stmts),
			},
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
	case *ast.YieldExpr:
		var valueExpr Expr
		var valueStmts []Stmt
		if expr.Value != nil {
			valueExpr, valueStmts = b.buildExpr(expr.Value, expr)
		}

		yieldExpr := NewYieldExpr(valueExpr, expr.IsDelegate, expr)
		return yieldExpr, valueStmts
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
	case *ast.IfValExpr:
		// Generate a temporary variable for the if-val result
		tempVar, tempDeclStmt := b.createTempVar(expr)

		stmts := []Stmt{tempDeclStmt}

		// First, generate code for the target expression
		targetExpr, targetStmts := b.buildExpr(expr.Target, expr)
		stmts = slices.Concat(stmts, targetStmts)

		// Generate the condition and binding statements for the pattern
		condition, bindingStmts := b.buildPatternCondition(expr.Pattern, targetExpr)

		// For if-val expressions, check if the target type is nullable and add null/undefined check
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

			// The caught value is what the arms test.
			errorIdent := NewIdentExpr("__error", "", expr)

			// A value no arm takes is re-raised, which is the one thing a catch chain does
			// that a `match` chain does not. An arm that always runs leaves nothing to
			// re-raise, so the chain falls through to nothing and the value cannot escape.
			var rethrow []Stmt
			if !ast.HasUnguardedCatchAll(expr.Catch) {
				rethrow = []Stmt{NewThrowStmt(errorIdent, expr)}
			}

			ctx := armCtx{scrutinee: errorIdent, tempVar: tempVar, source: expr}
			catchBodyStmts := b.buildArmChain(ctx, expr.Catch, rethrow)

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
		return b.buildJSXElement(expr)
	case *ast.JSXFragmentExpr:
		return b.buildJSXFragment(expr)
	case *ast.BorrowExpr:
		// JavaScript has no borrow concept, so a `&p` / `&mut p` expression
		// lowers to the operand directly. Affine soundness is enforced by the
		// new checker, not at runtime.
		return b.buildExpr(expr.Arg, expr)
	case *ast.ErrorExpr:
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
// createTempVarWithInit declares a fresh temp already holding init, for a temp whose
// starting value matters. createTempVar leaves the temp undefined for callers that assign
// it before every read.
func (b *Builder) createTempVarWithInit(init Expr, sourceExpr ast.Expr) (Expr, Stmt) {
	tempVar, declStmt := b.createTempVar(sourceExpr)
	declStmt.(*DeclStmt).Decl.(*VarDecl).Decls[0].Init = init
	return tempVar, declStmt
}

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

		// Extract default from a top-level IdentPat, if present. We emit the
		// default as `const name = typeof temp !== "undefined" ? temp : default`
		// rather than relying on JS parameter defaults, so the rename pass can
		// freely choose the param name.
		var identDefault ast.Expr
		patForBuild := p.Pattern
		if identPat, ok := p.Pattern.(*ast.IdentPat); ok && identPat.Default != nil {
			identDefault = identPat.Default
			patForBuild = ast.NewIdentPat(identPat.Name, identPat.Mutable, identPat.TypeAnn, nil, identPat.Span())
		}

		switch pat := patForBuild.(type) {
		case *ast.RestPat:
			_, paramStmts := b.buildPattern(pat.Pattern, NewIdentExpr(id, "", nil), false, ast.ValKind, "")
			outParamStmts = slices.Concat(outParamStmts, paramStmts)
			paramPat = NewRestPat(paramPat, nil)
		default:
			if identDefault != nil {
				identPat := patForBuild.(*ast.IdentPat)
				defExpr, defStmts := b.buildExpr(identDefault, nil)
				outParamStmts = slices.Concat(outParamStmts, defStmts)

				tempExpr := NewIdentExpr(id, "", nil)
				typeofCheck := NewBinaryExpr(
					NewUnaryExpr(TypeOf, tempExpr, nil),
					StrictNotEqual,
					NewLitExpr(NewStrLit("undefined", nil), nil),
					nil,
				)
				init := NewCondExpr(typeofCheck, NewIdentExpr(id, "", nil), defExpr, nil)

				decl := &VarDecl{
					Kind: ValKind,
					Decls: []*Declarator{{
						Pattern: NewIdentPat(identPat.Name, nil, identPat),
						TypeAnn: nil,
						Init:    init,
					}},
					declare: false,
					export:  false,
					span:    nil,
					source:  nil,
				}
				outParamStmts = append(outParamStmts, &DeclStmt{Decl: decl, span: nil, source: nil})
			} else {
				_, paramStmts := b.buildPattern(pat, NewIdentExpr(id, "", nil), false, ast.ValKind, "")
				outParamStmts = slices.Concat(outParamStmts, paramStmts)
			}
		}

		outParams = append(outParams, &Param{
			Pattern:  paramPat,
			Optional: p.Optional,
			TypeAnn:  nil,
		})
	}

	return outParams, outParamStmts
}

// superClassName returns the emitted JS name of the class an `extends` clause refers to, and
// whether the clause should be emitted at all.
//
// A declaration's own name is emitted with each dot of its namespace-qualified name mangled
// to a double underscore, so `class Color` inside namespace `MyEnum` becomes `MyEnum__Color`,
// and a reference has to be mangled the same way to reach it. The written name is resolved
// the enclosing namespace first and then the root, which is what makes a bare `extends
// Animal` inside a namespace reach the namespace's own Animal where there is one.
//
// Resolution goes through the VALUE binding, because `class B extends A` evaluates A as an
// expression and reaches the constructor the class's value binding holds. A name the module
// declares as a type and not as a value therefore names nothing at runtime, so ok is false
// and the clause is dropped rather than emitted as a reference that fails at module load. A
// name the module declares neither way is left alone, since it is reaching outside the
// module and this graph says nothing about it.
func (b *Builder) superClassName(ref *ast.TypeRefTypeAnn, nsName string) (string, bool) {
	name := ast.QualIdentToString(ref.Name)
	if b.depGraph != nil {
		if nsName != "" && b.depGraph.HasBinding(dep_graph.ValueBindingKey(nsName+"."+name)) {
			name = nsName + "." + name
		} else if !b.depGraph.HasBinding(dep_graph.ValueBindingKey(name)) &&
			b.depGraph.HasBinding(dep_graph.TypeBindingKey(name)) {
			return "", false
		}
	}
	return strings.ReplaceAll(name, ".", "__"), true
}

// hasSuperCall reports whether a constructor body writes a `super(…)` call of its own. A body
// that does carries the call already, at the position it was written, so the synthesized one
// is left out rather than running the superclass constructor a second time.
func hasSuperCall(body *ast.Block) bool {
	if body == nil {
		return false
	}
	finder := &superCallFinder{}
	for _, stmt := range body.Stmts {
		stmt.Accept(finder)
	}
	return finder.found
}

// superCallFinder records whether the walk reached a `super(…)` call.
type superCallFinder struct {
	ast.DefaultVisitor
	found bool
}

func (v *superCallFinder) EnterExpr(e ast.Expr) bool {
	if _, ok := e.(*ast.SuperCallExpr); ok {
		v.found = true
	}
	return !v.found
}

// buildClassElems converts a class body to its JS elements. derived says the class carries an
// `extends` clause, which makes its constructor emit a leading `super()` unless the body
// writes one itself.
func (b *Builder) buildClassElems(inElems []ast.ClassElem, derived bool) ([]ClassElem, []Stmt) {
	var outElems []ClassElem
	var allStmts []Stmt

	for _, elem := range inElems {
		switch e := elem.(type) {
		case *ast.FieldElem:
			// Only handle static fields here; instance fields are
			// initialized inside the constructor body.
			if e.Static {
				name, nameStmts := b.buildObjKey(e.Name)
				allStmts = slices.Concat(allStmts, nameStmts)

				// The checker requires every static field to have an
				// `= expr` initializer (or a type that admits `undefined`),
				// so when there is no Value the field is intentionally
				// emitted as `static x;` (runtime value `undefined`).
				var value Expr
				if e.Value != nil {
					var valueStmts []Stmt
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

			isGenerator := e.Fn.Gen || (e.Fn.Body != nil && containsYield(e.Fn.Body.Stmts))
			methodElem := NewMethodElem(
				name,
				params,
				slices.Concat(paramStmts, bodyStmts),
				MethodElemOptions{
					Static:    e.Static,
					Private:   e.Private,
					Async:     e.Fn.Async,
					Generator: isGenerator,
				},
				e,
			)
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

		case *ast.ConstructorElem:
			if e.Fn == nil {
				continue
			}
			// Strip Fn.Params[0] (the `mut self` receiver) — it is `this`
			// at the JS level, not a callable parameter.
			callableParams := e.Fn.Params
			if len(callableParams) > 0 {
				callableParams = callableParams[1:]
			}
			params, paramStmts := b.buildParams(callableParams)
			var bodyStmts []Stmt
			if e.Fn.Body != nil {
				// Mark that we're inside a method body — same as
				// the method/getter/setter cases above. Suppresses
				// `export` on nested decls (see line 743).
				prevInBlockScope := b.inBlockScope
				b.inBlockScope = true
				bodyStmts = b.buildStmts(e.Fn.Body.Stmts)
				b.inBlockScope = prevInBlockScope
			}
			// JS rejects any use of `this` in a derived constructor before `super()` runs, and
			// every instance field is assigned through `this`, so the call has to lead the
			// body.
			//
			// It passes no arguments, because there is no syntax yet for a subclass to say
			// what its superclass should receive. That leaves the superclass constructor
			// running with every parameter `undefined`, so one that reads a parameter rather
			// than just storing it throws. escalier-lang/escalier#678 is what will let a
			// subclass forward arguments and make this call carry them.
			var prelude []Stmt
			if derived && !hasSuperCall(e.Fn.Body) {
				prelude = []Stmt{&ExprStmt{
					Expr:   NewCallExpr(NewIdentExpr("super", "", e), nil, false, e),
					span:   nil,
					source: e,
				}}
			}
			constructorMethod := NewMethodElem(
				NewIdentExpr("constructor", "", e),
				params,
				slices.Concat(prelude, paramStmts, bodyStmts),
				MethodElemOptions{},
				e,
			)
			outElems = append(outElems, constructorMethod)
		}
	}

	return outElems, allStmts
}

// buildMatchExpr converts a match expression into if-else statements with pattern matching
// armCtx carries what every arm in a chain shares: the value the arms test, the temp the
// chain assigns the winning arm's value to, and the node that generated code is blamed on.
type armCtx struct {
	scrutinee Expr
	tempVar   Expr
	source    ast.Expr
}

// asElse renders a chain's fall-through for an `else` slot. A lone `if` goes in bare so the
// arms print as one `else if` chain. Anything else needs braces, and nothing at all leaves
// the slot empty.
func asElse(fallthroughStmts []Stmt, source ast.Node) Stmt {
	if len(fallthroughStmts) == 0 {
		return nil
	}
	if len(fallthroughStmts) == 1 {
		if ifStmt, isIf := fallthroughStmts[0].(*IfStmt); isIf {
			return ifStmt
		}
	}
	return NewBlockStmt(fallthroughStmts, source)
}

// buildArmChain lowers match or catch arms to the statements that run the first arm taking
// the value. fallthroughStmts is what runs when no arm takes it, which is nothing for a
// `match`, whose arms the checker has already found exhaustive, and the `throw` that
// re-raises a caught value for a `try`.
//
// The arms are built in reverse so each one wraps the chain the later arms have already
// produced, which is the fall-through it hands the value to when it declines.
func (b *Builder) buildArmChain(ctx armCtx, arms []*ast.MatchCase, fallthroughStmts []Stmt) []Stmt {
	for i := len(arms) - 1; i >= 0; i-- {
		arm := arms[i]
		condition, bindingStmts := b.buildPatternCondition(arm.Pattern, ctx.scrutinee)

		if arm.Guard != nil {
			fallthroughStmts = b.buildGuardedArm(ctx, arm, condition, bindingStmts, fallthroughStmts)
			continue
		}

		body := slices.Concat(bindingStmts, b.buildArmBody(arm.Body, ctx.tempVar, ctx.source))
		if isTrueLiteral(condition) {
			// An unguarded catch-all always runs, so it needs no test and every later arm
			// is unreachable. Its body replaces the fall-through.
			fallthroughStmts = body
			continue
		}
		fallthroughStmts = []Stmt{NewIfStmt(
			condition,
			NewBlockStmt(body, ctx.source),
			asElse(fallthroughStmts, ctx.source),
			ctx.source,
		)}
	}
	return fallthroughStmts
}

// buildGuardedArm lowers one arm that carries a guard, returning what the chain becomes once
// this arm wraps it. Such an arm declines the value in two ways, by failing its pattern test
// and by failing its guard, and both have to reach the same fall-through. Which shape does
// that without emitting the fall-through twice depends on what the guard needs, so this
// picks between four.
func (b *Builder) buildGuardedArm(
	ctx armCtx, arm *ast.MatchCase, condition Expr, bindingStmts, fallthroughStmts []Stmt,
) []Stmt {
	source := ctx.source
	guardExpr, guardStmts := b.buildExpr(arm.Guard, source)
	guardBodyStmts := b.buildArmBody(arm.Body, ctx.tempVar, source)

	if isTrueLiteral(condition) {
		// A catch-all pattern admits every value, so the bindings and the guard's `if`
		// need no test around them. A failed guard is then the only way past this arm, so
		// the fall-through goes in the guard's else and has one owner.
		guardIf := NewIfStmt(
			guardExpr, NewBlockStmt(guardBodyStmts, source), asElse(fallthroughStmts, source), source)
		return slices.Concat(bindingStmts, guardStmts, []Stmt{guardIf})
	}

	// One `if` testing the pattern and the guard together gives the two failures a single
	// `else` to share. The guard has to read nothing the pattern declares, since a
	// declaration can only run once the pattern has matched, so guardWithoutBindings
	// rewrites it to read through access paths where it can. The guard must also need no
	// hoisted statements, since those would run before the pattern test rather than after
	// it. The printer parenthesizes a guard that binds looser than the `&&` joining the two.
	if len(guardStmts) == 0 {
		if mergedGuard, canMerge := guardWithoutBindings(
			arm.Pattern, ctx.scrutinee, arm.Guard, guardExpr,
		); canMerge {
			merged := combineConditions([]Expr{condition, mergedGuard}, source)
			// The pattern's declarations move into the body, which is the only place left
			// that reads them.
			body := slices.Concat(bindingStmts, guardBodyStmts)
			return []Stmt{NewIfStmt(
				merged, NewBlockStmt(body, source), asElse(fallthroughStmts, source), source)}
		}
	}

	// A guard that lowers to statements cannot join the condition, since those statements
	// would run before the pattern had matched. Computing it into a flag inside the pattern
	// test keeps that order, and the flag then answers whether this arm takes the value. The
	// arm body and the fall-through share one `if`/`else` from there, so the later arms
	// continue the same chain.
	//
	// The guard's statements are emitted as they were built, so this needs a guard that
	// reads none of the pattern's declarations. Those declarations do not run until the body.
	if len(fallthroughStmts) > 0 && !guardReadsPatternBindings(arm.Pattern, arm.Guard) {
		takenVar, takenDecl := b.createTempVarWithInit(NewLitExpr(NewBoolLit(false, source), source), source)
		computeGuard := append(slices.Clone(guardStmts), &ExprStmt{
			Expr:   NewBinaryExpr(takenVar, Assign, guardExpr, source),
			span:   nil,
			source: source,
		})
		body := slices.Concat(bindingStmts, guardBodyStmts)
		return []Stmt{
			takenDecl,
			NewIfStmt(condition, NewBlockStmt(computeGuard, source), nil, source),
			NewIfStmt(takenVar, NewBlockStmt(body, source), asElse(fallthroughStmts, source), source),
		}
	}

	// Otherwise the two tests stay in separate `if`s and a flag gives the fall-through one
	// owner. Putting it in each `else` would emit it twice, and a chain of N such arms would
	// emit its tail 2^N times. The arm sets the flag on the one path that takes the value,
	// and the fall-through runs when the flag is still unset.
	caseBodyStmts := slices.Concat(bindingStmts, guardStmts)
	if len(fallthroughStmts) == 0 {
		// Nothing to fall through to, so neither failure needs recording.
		guardIf := NewIfStmt(guardExpr, NewBlockStmt(guardBodyStmts, source), nil, source)
		caseBodyStmts = append(caseBodyStmts, guardIf)
		return []Stmt{NewIfStmt(condition, NewBlockStmt(caseBodyStmts, source), nil, source)}
	}

	takenVar, takenDecl := b.createTempVarWithInit(NewLitExpr(NewBoolLit(false, source), source), source)
	guardBodyStmts = append(guardBodyStmts, &ExprStmt{
		Expr:   NewBinaryExpr(takenVar, Assign, NewLitExpr(NewBoolLit(true, source), source), source),
		span:   nil,
		source: source,
	})
	guardIf := NewIfStmt(guardExpr, NewBlockStmt(guardBodyStmts, source), nil, source)
	caseBodyStmts = append(caseBodyStmts, guardIf)
	notTaken := NewUnaryExpr(LogicalNot, takenVar, source)
	return []Stmt{
		takenDecl,
		NewIfStmt(condition, NewBlockStmt(caseBodyStmts, source), nil, source),
		NewIfStmt(notTaken, NewBlockStmt(fallthroughStmts, source), nil, source),
	}
}

// buildArmBody lowers a match or catch arm's body to the statements that run it, ending in
// the assignment that hands its value to the enclosing expression's temp. A block body
// assigns the value of its tail statement, an expression body assigns itself, and an arm
// with neither runs nothing.
func (b *Builder) buildArmBody(body ast.BlockOrExpr, tempVar Expr, source ast.Expr) []Stmt {
	if body.Block != nil {
		return b.buildBlockStmtsWithTempAssignment(body.Block.Stmts, tempVar, source)
	}
	if body.Expr == nil {
		return nil
	}
	bodyExpr, stmts := b.buildExpr(body.Expr, source)
	assignment := &ExprStmt{
		Expr:   NewBinaryExpr(tempVar, Assign, bodyExpr, body.Expr),
		span:   nil,
		source: body.Expr,
	}
	return slices.Concat(stmts, []Stmt{assignment})
}

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

	// The arms test the hoisted target. A `match` needs no fall-through, since the checker
	// has already found the arms exhaustive.
	ctx := armCtx{scrutinee: targetTempVar, tempVar: tempVar, source: expr}
	stmts = slices.Concat(stmts, b.buildArmChain(ctx, expr.Cases, nil))

	return tempVar, stmts
}

// buildPatternCondition builds the condition expression and binding statements for a pattern
// buildValElse lowers `val pat = init else { … }` into a temp-hoisted match check.
// The temp starts as the initializer; on a failed match the else block runs, and its
// tail value is assigned back to the temp so the binding takes it as a fallback. An
// else that diverges with a `return`/`throw` leaves the temp untouched and skips past
// the bindings. The `if`/empty-then/else-runs-the-block shape keeps the condition
// un-negated, which the precedence-naive printer needs. The pattern's bindings read
// the temp after the guard, binding the matched value or the fallback.
func (b *Builder) buildValElse(d *ast.VarDecl, initExpr Expr) []Stmt {
	tempVar, tempDeclStmt := b.createTempVar(d.Init)
	initAssign := &ExprStmt{
		Expr:   NewBinaryExpr(tempVar, Assign, initExpr, d.Init),
		source: d.Init,
	}

	condition, bindingStmts := b.buildPatternCondition(d.Pattern, tempVar)
	if d.TypeAnn != nil {
		// A decl-level type annotation narrows the union to one member. Combine its
		// type guard WITH the pattern's structural condition rather than replacing it,
		// so an annotated destructuring still validates shape before binding.
		// combineConditions drops the trivial `true` a bare identifier contributes.
		typeGuard := b.buildTypeGuard(tempVar, d.TypeAnn)
		condition = combineConditions([]Expr{condition, typeGuard}, d)
	}

	// Assigning the else block's tail value to the temp makes a non-diverging else a
	// fallback; a diverging else emits its `return`/`throw` and assigns nothing.
	elseStmts := b.buildBlockStmtsWithTempAssignment(d.Else.Stmts, tempVar, d.Init)
	guard := NewIfStmt(
		condition,
		NewBlockStmt(nil, d),
		NewBlockStmt(elseStmts, d),
		d,
	)

	stmts := []Stmt{tempDeclStmt, initAssign, guard}
	return slices.Concat(stmts, bindingStmts)
}

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
		if pat.TypeAnn != nil {
			cond = b.buildTypeGuard(targetExpr, pat.TypeAnn)
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

		// Check if the tuple has a rest element and count non-rest elements
		hasRest := false
		nonRestCount := 0
		for _, elem := range pat.Elems {
			if _, ok := elem.(*ast.RestPat); ok {
				hasRest = true
			} else {
				nonRestCount++
			}
		}

		// Use >= for rest patterns (minimum length), == for exact length
		if hasRest {
			lengthCheck := b.buildArrayMinLengthCheck(targetExpr, nonRestCount, pat)
			conditions = append(conditions, lengthCheck)
		} else {
			lengthCheck := b.buildArrayLengthCheck(targetExpr, len(pat.Elems), pat)
			conditions = append(conditions, lengthCheck)
		}

		// For each element, recursively build only the condition (ignore bindings)
		for i, elem := range pat.Elems {
			// Skip rest patterns in condition building
			if _, ok := elem.(*ast.RestPat); ok {
				continue
			}
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

				// Skip destructuring for patterns that introduce no bindings
				// (e.g. {x: 0} or {x: _}) to avoid invalid duplicate "_" bindings
				switch objElem.Value.(type) {
				case *ast.LitPat, *ast.WildcardPat:
					// no binding needed
				default:
					valuePat := b.buildDestructuringPattern(objElem.Value)
					objPatElems = append(objPatElems, NewObjKeyValuePat(objElem.Key.Name, valuePat, nil, objElem))
				}

			case *ast.ObjShorthandPat:
				// Check that the property exists: "propName" in object
				propExistsCheck := buildPropertyInObjectCheck(objElem.Key.Name, targetExpr, objElem)
				conditions = append(conditions, propExistsCheck)

				// Generate runtime type check if type annotation is present
				if objElem.TypeAnn != nil {
					propAccess := NewMemberExpr(targetExpr, NewIdentifier(objElem.Key.Name, objElem), false, objElem)
					typeGuard := b.buildTypeGuard(propAccess, objElem.TypeAnn)
					conditions = append(conditions, typeGuard)
				}

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
		// Only generate destructuring if there are actual bindings
		if len(objPatElems) > 0 {
			allBindingStmts = append(allBindingStmts, bindingStmt)
		}

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
			NewIdentExpr(b.nsValueRef(ast.QualIdentToString(pat.ClassName)), "", pat),
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
			NewIdentExpr(b.nsValueRef(ast.QualIdentToString(pat.Name)), "", pat),
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
		extractor := NewIdentExpr(b.nsValueRef(ast.QualIdentToString(pat.Name)), "", pat)
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

// buildArrayMinLengthCheck creates a condition to check if an array has at least the expected length
func (b *Builder) buildArrayMinLengthCheck(arrayExpr Expr, minLength int, source ast.Node) Expr {
	lengthAccess := NewMemberExpr(
		arrayExpr,
		NewIdentifier("length", source),
		false,
		source,
	)
	minLengthExpr := NewLitExpr(NewNumLit(float64(minLength), source), source)
	return NewBinaryExpr(lengthAccess, GreaterThanEqual, minLengthExpr, source)
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

// guardRefs collects the identifier names a guard expression mentions, so a caller can ask
// whether the guard depends on the names its arm's pattern binds.
type guardRefs struct {
	ast.DefaultVisitor
	names set.Set[string]
}

func (v *guardRefs) EnterExpr(e ast.Expr) bool {
	if ident, isIdent := e.(*ast.IdentExpr); isIdent {
		v.names.Add(ident.Name)
	}
	return true
}

// patternAccessPaths maps each name a pattern binds to the expression that reads its value
// out of target, so `{message: msg}` over `__error` yields msg to `__error.message`. A
// caller can then write the guard against those paths rather than against declarations the
// pattern has to make first.
//
// The second result is false for a pattern whose bindings are not plain reads. An extractor
// or instance pattern runs user code to produce them, a rest element gathers what is left
// rather than one position, and a default supplies a value when the read finds nothing.
func patternAccessPaths(pat ast.Pat, target Expr) (map[string]Expr, bool) {
	paths := map[string]Expr{}
	if !collectAccessPaths(pat, target, paths) {
		return nil, false
	}
	return paths, true
}

func collectAccessPaths(pat ast.Pat, target Expr, into map[string]Expr) bool {
	switch p := pat.(type) {
	case *ast.WildcardPat, *ast.LitPat:
		return true
	case *ast.IdentPat:
		if p.Default != nil {
			return false
		}
		into[p.Name] = target
		return true
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				prop := NewMemberExpr(target, NewIdentifier(e.Key.Name, e.Key), false, e)
				if !collectAccessPaths(e.Value, prop, into) {
					return false
				}
			case *ast.ObjShorthandPat:
				if e.Default != nil {
					return false
				}
				into[e.Key.Name] = NewMemberExpr(target, NewIdentifier(e.Key.Name, e.Key), false, e)
			default:
				return false
			}
		}
		return true
	case *ast.TuplePat:
		for i, elem := range p.Elems {
			if _, isRest := elem.(*ast.RestPat); isRest {
				return false
			}
			index := NewIndexExpr(target, NewLitExpr(NewNumLit(float64(i), p), p), false, p)
			if !collectAccessPaths(elem, index, into) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// substituteAccessPaths rewrites a guard so each reference to a pattern binding reads
// through its access path instead, turning `msg != ""` into `__error.message != ""`. The
// rewritten guard needs no declarations ahead of it, so it can join the pattern test in one
// condition.
//
// The second result is false for an expression kind this does not rewrite. Only the shapes
// an ordinary guard is built from are covered, which keeps a nested function out of the
// walk. Such a function could rebind one of the names, and rewriting through it would
// change which value the guard reads.
func substituteAccessPaths(expr Expr, paths map[string]Expr) (Expr, bool) {
	switch e := expr.(type) {
	case *IdentExpr:
		if path, isBound := paths[e.Name]; isBound {
			return path, true
		}
		return e, true
	case *LitExpr, *TemplateLitExpr:
		return e, true
	case *BinaryExpr:
		left, leftOK := substituteAccessPaths(e.Left, paths)
		right, rightOK := substituteAccessPaths(e.Right, paths)
		if !leftOK || !rightOK {
			return nil, false
		}
		return NewBinaryExpr(left, e.Op, right, e.Source()), true
	case *UnaryExpr:
		arg, argOK := substituteAccessPaths(e.Arg, paths)
		if !argOK {
			return nil, false
		}
		return NewUnaryExpr(e.Op, arg, e.Source()), true
	case *MemberExpr:
		object, objectOK := substituteAccessPaths(e.Object, paths)
		if !objectOK {
			return nil, false
		}
		return NewMemberExpr(object, e.Prop, e.OptChain, e.Source()), true
	case *IndexExpr:
		object, objectOK := substituteAccessPaths(e.Object, paths)
		index, indexOK := substituteAccessPaths(e.Index, paths)
		if !objectOK || !indexOK {
			return nil, false
		}
		return NewIndexExpr(object, index, e.OptChain, e.Source()), true
	case *CallExpr:
		callee, calleeOK := substituteAccessPaths(e.Callee, paths)
		if !calleeOK {
			return nil, false
		}
		args := make([]Expr, len(e.Args))
		for i, arg := range e.Args {
			substituted, argOK := substituteAccessPaths(arg, paths)
			if !argOK {
				return nil, false
			}
			args[i] = substituted
		}
		return NewCallExpr(callee, args, e.OptChain, e.Source()), true
	default:
		return nil, false
	}
}

// guardReadsPatternBindings reports whether a guard mentions any name its arm's pattern
// binds. Such a guard cannot run before the pattern's declarations unless every reference is
// rewritten, and a guard that lowers to statements carries references this package does not
// rewrite, since only the guard's own expression goes through substituteAccessPaths.
func guardReadsPatternBindings(pat ast.Pat, guard ast.Expr) bool {
	bound := set.NewSet[string]()
	ast.CollectPatternBindingNames(pat, bound)
	if bound.Len() == 0 {
		return false
	}
	refs := &guardRefs{names: set.NewSet[string]()}
	guard.Accept(refs)
	return refs.names.Intersection(bound).Len() > 0
}

// guardWithoutBindings returns a guard that reads nothing the arm's pattern declares, so it
// can be tested in the same condition as the pattern. A guard naming none of the bindings is
// already such an expression and is returned unchanged. One that names them is rewritten to
// read through access paths. The second result is false when neither applies.
//
// The caller must have checked that the guard lowers to no statements. Those statements can
// hold references of their own, and rewriting the expression alone would leave them reading
// declarations that no longer precede them.
func guardWithoutBindings(pat ast.Pat, target Expr, guard ast.Expr, guardExpr Expr) (Expr, bool) {
	if !guardReadsPatternBindings(pat, guard) {
		return guardExpr, true
	}
	paths, pathsOK := patternAccessPaths(pat, target)
	if !pathsOK {
		return nil, false
	}
	return substituteAccessPaths(guardExpr, paths)
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

// yieldDetector walks the AST to check for yield expressions.
// It stops at function boundaries so nested function yields don't count.
type yieldDetector struct {
	ast.DefaultVisitor
	found bool
}

func (d *yieldDetector) EnterExpr(e ast.Expr) bool {
	switch e.(type) {
	case *ast.YieldExpr:
		d.found = true
		return false
	case *ast.FuncExpr:
		return false // Don't descend into nested functions
	}
	return true
}

func (d *yieldDetector) EnterDecl(decl ast.Decl) bool {
	switch decl.(type) {
	case *ast.FuncDecl:
		return false // Don't descend into nested function declarations
	case *ast.ClassDecl:
		return false // Don't descend into class declarations
	}
	return true
}

// containsYield checks whether the given statements contain a yield expression,
// stopping at function boundaries (nested functions are separate generators).
func containsYield(stmts []ast.Stmt) bool {
	detector := &yieldDetector{}
	for _, stmt := range stmts {
		stmt.Accept(detector)
		if detector.found {
			return true
		}
	}
	return false
}
