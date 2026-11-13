package main

import (
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
)

// hoverQuery is the cursor position a hover request names, with what the last check
// recorded about the file it is in.
type hoverQuery struct {
	loc ast.Location
	// node is the deepest node whose span holds loc, and ancestors are the nodes that
	// enclose it, outermost first.
	node      ast.Node
	ancestors []ast.Node
	info      *solver.Info
	// scope is the scope names in the file resolve in. A name a function body
	// declares is not in it.
	scope *solver.Scope
}

// scopeOfFile returns the scope the names in the file sourceID resolve in, or nil when
// co holds none for it. A bin/ script resolves names in its own scope and a lib/ file in
// its import scope, which encloses the module's declarations.
func scopeOfFile(co *compiler.CheckOutput, sourceID int) *solver.Scope {
	if scope, ok := co.ScriptScopes[sourceID]; ok {
		return scope
	}
	if scope, ok := co.FileScopes[sourceID]; ok {
		return scope
	}
	return co.ModuleScope
}

// typeHover returns the hover text for the cursor in a type position or on the name
// a declaration binds, and false when q is on neither.
func typeHover(q hoverQuery) (string, bool) {
	if tp, ok := typeParamAt(q.node, q.loc); ok {
		return typeParamText(tp), true
	}
	switch n := q.node.(type) {
	case *ast.TypeDecl:
		if n.Name.Span().Contains(q.loc) {
			return declText(n), true
		}
	case *ast.FuncDecl:
		if n.Name != nil && n.Name.Span().Contains(q.loc) && q.info != nil {
			if text := funcText(n.Name.Name, q.info.DisplayTypeOf(n.Name)); text != "" {
				return text, true
			}
		}
	case *ast.ClassDecl:
		if n.Name.Span().Contains(q.loc) {
			return declText(n), true
		}
	case *ast.InterfaceDecl:
		if n.Name.Span().Contains(q.loc) {
			return declText(n), true
		}
	case *ast.EnumDecl:
		if n.Name.Span().Contains(q.loc) {
			return declText(n), true
		}
	case *ast.InferTypeAnn:
		return "(type parameter) " + n.Name, true
	case *ast.TypeRefTypeAnn:
		return typeRefText(q, n)
	}
	return "", false
}

// valueHover returns the hover text for a read of a named function or a class used as a
// constructor, and false when the cursor is on neither. A function read shows its
// signature under its name, as in `fn add(a: number, b: number) -> number`. A class read
// shows its constructor, as in `constructor HttpError(status: number)`.
func valueHover(q hoverQuery) (string, bool) {
	ident, ok := q.node.(*ast.IdentExpr)
	if !ok || q.info == nil {
		return "", false
	}
	decl, ok := q.info.DeclOf(ident)
	if !ok {
		return "", false
	}
	t := q.info.DisplayTypeOf(ident)
	if t == nil {
		return "", false
	}
	switch decl.(type) {
	case *ast.FuncDecl:
		if text := funcText(ident.Name, t); text != "" {
			return text, true
		}
	case *ast.ClassDecl:
		if ctor, ok := constructorElem(q.info, t); ok {
			if text := constructorText(ident.Name, ctor); text != "" {
				return text, true
			}
		}
	}
	return "", false
}

// typeRefText returns the hover text for the type reference ref, and false when the
// cursor is not on its name or the name resolves to nothing the hover can show. A
// reference to a type parameter shows the parameter, and a reference to a declared
// type shows the declaration.
func typeRefText(q hoverQuery, ref *ast.TypeRefTypeAnn) (string, bool) {
	if !ref.Name.Span().Contains(q.loc) {
		return "", false
	}
	if ident, ok := ref.Name.(*ast.Ident); ok {
		if text, ok := typeParamInScope(q.ancestors, ident.Name, q.loc); ok {
			return text, true
		}
	}
	if q.info == nil || q.scope == nil {
		return "", false
	}
	refName := typeName(q.info.TypeOf(ref))
	if refName == "" {
		return "", false
	}
	binding, ok := q.scope.GetType(ast.QualIdentToString(ref.Name))
	// A type a function body declares is not in the file's scope, and a binding of the
	// same name outside the body is a different type, which the solver names
	// differently.
	if !ok || typeName(binding.Type) != refName {
		return "", false
	}
	for _, src := range binding.Sources {
		np, ok := src.(*ast.NodeProvenance)
		if !ok {
			continue
		}
		if decl, ok := np.Node.(ast.Decl); ok {
			if text := declText(decl); text != "" {
				return text, true
			}
		}
	}
	return "", false
}

// typeName returns the qualified name of the declared type t refers to, or "" when t
// refers to none.
func typeName(t soltype.Type) string {
	switch t := t.(type) {
	case *soltype.AliasType:
		return t.Name
	case *soltype.ClassType:
		return t.Name
	}
	return ""
}

// declText returns the declaration header decl shows on hover, without its `export` or
// `declare` modifier, or "" for a declaration it does not render. A function shows its
// signature, a class and an enum show their name and type parameters, and an interface
// shows its members as well.
func declText(decl ast.Decl) string {
	switch decl := decl.(type) {
	case *ast.TypeDecl:
		bare := ast.NewTypeDecl(decl.Name, decl.TypeParams, decl.TypeAnn, false, false, decl.Span())
		return printCompact(bare)
	case *ast.ClassDecl:
		return "class " + decl.Name.Name + typeParamsText(decl.TypeParams)
	case *ast.EnumDecl:
		return "enum " + decl.Name.Name + typeParamsText(decl.TypeParams)
	case *ast.InterfaceDecl:
		bare := *decl
		bare.SetExport(false)
		return printCompact(&bare)
	}
	return ""
}

// funcText renders the function type t under name, as in
// `fn add(a: number, b: number) -> number`. An overload set, which t carries as an
// intersection of functions, renders its first arm and ` (+N overloads)`. It returns ""
// when t is not a function type or an overload set.
func funcText(name string, t soltype.Type) string {
	if t == nil {
		return ""
	}
	if fn, ok := t.(*soltype.FuncType); ok {
		return namedFunc(name, fn)
	}
	inter, ok := t.(*soltype.IntersectionType)
	if !ok {
		return ""
	}
	var arms []*soltype.FuncType
	for _, part := range inter.Types {
		fn, ok := part.(*soltype.FuncType)
		if !ok {
			return ""
		}
		arms = append(arms, fn)
	}
	if len(arms) == 0 {
		return ""
	}
	text := namedFunc(name, arms[0])
	if extra := len(arms) - 1; extra > 0 {
		text += " (+" + strconv.Itoa(extra) + " overload" + plural(extra) + ")"
	}
	return text
}

// namedFunc renders fn as `fn name…`, replacing the leading `fn ` of the type's own
// rendering with `fn name`, so a generic function keeps its parameters after the name as
// in `fn id<T: number>(x: T) -> T`.
func namedFunc(name string, fn *soltype.FuncType) string {
	rendered := soltype.PrintAsScheme(fn)
	return "fn " + name + strings.TrimPrefix(rendered, "fn ")
}

// constructorText renders a class's constructor under name, as in
// `constructor HttpError(status: number)`. A class with more than one constructor renders
// each on its own line.
func constructorText(name string, ctor *soltype.ConstructorElem) string {
	lines := make([]string, 0, len(ctor.Signatures))
	for _, sig := range ctor.Signatures {
		lines = append(lines, "constructor "+name+signatureParams(sig))
	}
	return strings.Join(lines, "\n")
}

// signatureParams renders a function type's parameter list alone, dropping the leading
// `fn ` and the ` -> ret` return, as `(status: number)` for `fn (status: number) -> C`.
// A parameter that is itself a function type holds its own ` -> `, so the return is the
// last one, not the first.
func signatureParams(sig *soltype.FuncType) string {
	rendered := strings.TrimPrefix(soltype.PrintAsScheme(sig), "fn ")
	if i := strings.LastIndex(rendered, " -> "); i >= 0 {
		rendered = rendered[:i]
	}
	return rendered
}

// plural returns the "s" that pluralizes a noun counted by n, and "" when n is one.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// typeParamsText renders a declaration's type parameters as `<T, U: Bound>`, and "" when
// it declares none.
func typeParamsText(params []*ast.TypeParam) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, tp := range params {
		part := tp.Name
		if tp.UpperBound != nil {
			part += ": " + printCompact(tp.UpperBound)
		}
		if tp.Default != nil {
			part += " = " + printCompact(tp.Default)
		}
		parts[i] = part
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

// printCompact renders node as Escalier source on one line, without doc comments.
func printCompact(node ast.Node) string {
	opts := printer.CompactOptions()
	opts.OmitDocComments = true
	text, err := printer.Print(node, opts)
	if err != nil {
		return ""
	}
	return text
}

// typeParamsOf returns the type parameters node declares.
func typeParamsOf(node ast.Node) []*ast.TypeParam {
	switch n := node.(type) {
	case *ast.TypeDecl:
		return n.TypeParams
	case *ast.InterfaceDecl:
		return n.TypeParams
	case *ast.ClassDecl:
		return n.TypeParams
	case *ast.EnumDecl:
		return n.TypeParams
	case *ast.FuncDecl:
		return n.TypeParams
	case *ast.FuncExpr:
		return n.TypeParams
	case *ast.FuncTypeAnn:
		return n.TypeParams
	}
	return nil
}

// typeParamAt returns the type parameter node declares whose name holds loc.
func typeParamAt(node ast.Node, loc ast.Location) (*ast.TypeParam, bool) {
	for _, tp := range typeParamsOf(node) {
		span := tp.Span()
		nameEnd := ast.Location{Offset: span.Start.Offset + len(tp.Name)}
		if span.Start.Offset <= loc.Offset && loc.Offset <= nameEnd.Offset {
			return tp, true
		}
	}
	return nil, false
}

// typeParamInScope returns the hover text for the type parameter name refers to at loc,
// found among the parameters that ancestors declare, nearest first. It returns false
// when no ancestor declares a parameter of that name.
func typeParamInScope(ancestors []ast.Node, name string, loc ast.Location) (string, bool) {
	for i := len(ancestors) - 1; i >= 0; i-- {
		for _, tp := range typeParamsOf(ancestors[i]) {
			if tp.Name == name {
				return typeParamText(tp), true
			}
		}
		switch n := ancestors[i].(type) {
		case *ast.CondTypeAnn:
			// An `infer` binds its name in the branch taken when the test passes.
			if n.Then.Span().Contains(loc) && infersName(n.Extends, name) {
				return "(type parameter) " + name, true
			}
		case *ast.ObjectTypeAnn:
			for _, elem := range n.Elems {
				mapped, ok := elem.(*ast.MappedTypeAnn)
				if ok && mapped.TypeParam.Name == name && mapped.Span().Contains(loc) {
					return "(type parameter) " + name + " in " + printCompact(mapped.TypeParam.Constraint), true
				}
			}
		}
	}
	return "", false
}

// infersName reports whether the pattern t holds an `infer` of name.
func infersName(t ast.TypeAnn, name string) bool {
	v := &inferFinder{name: name}
	t.Accept(v)
	return v.found
}

// inferFinder records whether a walk reaches an `infer` of name.
type inferFinder struct {
	ast.DefaultVisitor
	name  string
	found bool
}

func (v *inferFinder) EnterTypeAnn(t ast.TypeAnn) bool {
	if infer, ok := t.(*ast.InferTypeAnn); ok && infer.Name == v.name {
		v.found = true
	}
	return !v.found
}

// typeParamText returns the hover text for the type parameter tp, with its bound and
// default when it declares them.
func typeParamText(tp *ast.TypeParam) string {
	text := "(type parameter) " + tp.Name
	if tp.UpperBound != nil {
		text += ": " + printCompact(tp.UpperBound)
	}
	if tp.Default != nil {
		text += " = " + printCompact(tp.Default)
	}
	return text
}
