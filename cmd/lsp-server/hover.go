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
// `declare` modifier, or "" for a declaration it does not render. A type alias shows its
// whole declaration, a class and an enum show their name and type parameters, and an
// interface shows its members as well.
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

// memberHover returns the hover text for a member read or an object-literal property key,
// and false when the cursor is on neither. A member of a named type carries that name, as
// `(property) Response.status: number`, and a member of an object literal or object type
// carries none, as `(property) volume: number`.
func memberHover(q hoverQuery) (string, bool) {
	if q.info == nil {
		return "", false
	}
	switch n := q.node.(type) {
	case *ast.MemberExpr:
		if !n.Prop.Span().Contains(q.loc) {
			return "", false
		}
		return memberReadText(q.info, n)
	case *ast.PropertyExpr:
		if !n.Name.Span().Contains(q.loc) {
			return "", false
		}
		return objLiteralPropText(q.info, n)
	}
	return "", false
}

// memberReadText returns the hover text for the member read m, and false when its type was
// not recorded. The owning type's members name the kind, so `res.json` reads as
// `(method) Response.json() -> Promise<unknown>` rather than as a plain property.
func memberReadText(info *solver.Info, m *ast.MemberExpr) (string, bool) {
	t := info.DisplayTypeOf(m)
	if t == nil {
		return "", false
	}
	objType := info.ResolvedTypeOf(m.Object)
	owner := baseName(typeName(info.Expand(objType)))
	if obj, ok := info.MemberObject(objType); ok {
		for _, elem := range obj.Elems {
			if soltype.ObjElemName(elem) == m.Prop.Name {
				return memberElemText(owner, elem), true
			}
		}
	}
	// The member resolved to a type with no fixed member set, such as an index
	// signature, so its recorded type stands in and the kind defaults to a property.
	return propertyText(owner, m.Prop.Name, soltype.PrintAsScheme(t)), true
}

// objLiteralPropText returns the hover text for the key of an object-literal property, as
// `(property) volume: number`, and false when prop is shorthand or its value has no
// recorded type.
func objLiteralPropText(info *solver.Info, prop *ast.PropertyExpr) (string, bool) {
	if prop.Value == nil {
		return "", false
	}
	name := objKeyName(prop.Name)
	if name == "" {
		return "", false
	}
	t := info.DisplayTypeOf(prop.Value)
	if t == nil {
		return "", false
	}
	return propertyText("", name, soltype.PrintAsScheme(t)), true
}

// memberElemText renders one member of an object type under owner, which is the name of
// the type the member belongs to or "" for an object with no name.
func memberElemText(owner string, elem soltype.ObjTypeElem) string {
	switch elem := elem.(type) {
	case *soltype.PropertyElem:
		return propertyText(owner, elem.Name, soltype.Print(elem.Type))
	case *soltype.GetterElem:
		return "(getter) " + qualify(owner, elem.Name) + ": " + soltype.Print(elem.Type)
	case *soltype.SetterElem:
		return "(setter) " + qualify(owner, elem.Name) + ": " + soltype.Print(elem.Param.Type)
	case *soltype.MethodElem:
		if len(elem.Signatures) == 0 {
			return propertyText(owner, elem.Name, "")
		}
		text := "(method) " + qualify(owner, elem.Name) + methodSignature(elem.Signatures[0])
		if extra := len(elem.Signatures) - 1; extra > 0 {
			text += " (+" + strconv.Itoa(extra) + " overload" + plural(extra) + ")"
		}
		return text
	}
	return propertyText(owner, soltype.ObjElemName(elem), "")
}

// propertyText renders a property member as `(property) owner.name: type`, dropping the
// owner when it is empty.
func propertyText(owner, name, typ string) string {
	return "(property) " + qualify(owner, name) + ": " + typ
}

// methodSignature renders a method's parameters and return without its receiver, as
// `() -> Promise<unknown>` for a `json` method, so the caller prepends the method name.
func methodSignature(sig *soltype.FuncType) string {
	withoutSelf := *sig
	withoutSelf.SelfParam = nil
	return strings.TrimPrefix(soltype.Print(&withoutSelf), "fn ")
}

// qualify joins an owner name and a member name with a dot, or returns the member name
// alone when the owner is empty.
func qualify(owner, name string) string {
	if owner == "" {
		return name
	}
	return owner + "." + name
}

// baseName returns a qualified type name's last dotted segment, so the builtin
// `import:std:async.web__fetch.Response` shows on a member as `Response`.
func baseName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// objKeyName returns the name an object key writes, or "" for a computed key.
func objKeyName(key ast.ObjKey) string {
	switch key := key.(type) {
	case *ast.IdentExpr:
		return key.Name
	case *ast.StrLit:
		return key.Value
	}
	return ""
}

// selfHover returns the hover text for a `self` receiver read inside a method, as
// `self: &mut HttpError`, and false when the cursor is not on such a read. The borrow
// comes from the method's receiver and the type name from the enclosing class.
func selfHover(q hoverQuery) (string, bool) {
	ident, ok := q.node.(*ast.IdentExpr)
	if !ok || ident.Name != "self" || q.info == nil {
		return "", false
	}
	// The checker binds `self` without a declaration, so a node with one is an ordinary
	// read, not a receiver.
	if _, declared := q.info.DeclOf(ident); declared {
		return "", false
	}
	class := enclosingClass(q.ancestors)
	if class == nil {
		return "", false
	}
	text := "self: " + classRefName(class)
	if recv := enclosingReceiver(q.ancestors, class); recv != nil && recv.Mode == ast.BorrowReceiver {
		if recv.Mut {
			text = "self: &mut " + classRefName(class)
		} else {
			text = "self: &" + classRefName(class)
		}
	}
	return text, true
}

// enclosingClass returns the nearest class declaration among ancestors, or nil when the
// cursor is in none.
func enclosingClass(ancestors []ast.Node) *ast.ClassDecl {
	for i := len(ancestors) - 1; i >= 0; i-- {
		if class, ok := ancestors[i].(*ast.ClassDecl); ok {
			return class
		}
	}
	return nil
}

// enclosingReceiver returns the `self` receiver of the method whose body holds the cursor,
// found by matching a function-expression ancestor to a member of class. It returns nil
// when no method body encloses the cursor, as for a closure that captured `self`.
func enclosingReceiver(ancestors []ast.Node, class *ast.ClassDecl) *ast.MethodReceiver {
	for i := len(ancestors) - 1; i >= 0; i-- {
		fn, ok := ancestors[i].(*ast.FuncExpr)
		if !ok {
			continue
		}
		if recv, ok := receiverOfFn(class, fn); ok {
			return recv
		}
	}
	return nil
}

// receiverOfFn returns the receiver of the member of class whose function is fn, and false
// when no member has that function.
func receiverOfFn(class *ast.ClassDecl, fn *ast.FuncExpr) (*ast.MethodReceiver, bool) {
	for _, elem := range class.Body {
		switch elem := elem.(type) {
		case *ast.MethodElem:
			if elem.Fn == fn {
				return elem.Receiver, true
			}
		case *ast.GetterElem:
			if elem.Fn == fn {
				return elem.Receiver, true
			}
		case *ast.SetterElem:
			if elem.Fn == fn {
				return elem.Receiver, true
			}
		case *ast.ConstructorElem:
			if elem.Fn == fn {
				return elem.Receiver, true
			}
		}
	}
	return nil, false
}

// classRefName renders a class name with its type parameters as arguments, as `Box<T>`
// for a generic class and `Dog` for a plain one.
func classRefName(class *ast.ClassDecl) string {
	if len(class.TypeParams) == 0 {
		return class.Name.Name
	}
	names := make([]string, len(class.TypeParams))
	for i, tp := range class.TypeParams {
		names[i] = tp.Name
	}
	return class.Name.Name + "<" + strings.Join(names, ", ") + ">"
}
