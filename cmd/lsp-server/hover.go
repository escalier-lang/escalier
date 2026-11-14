package main

import (
	"slices"
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
	// infoFor returns the records of the file a SourceID names. For a declaration in
	// another file, those are not info.
	infoFor func(sourceID int) *solver.Info
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
			return declHoverText(q.info, n), true
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
		if variant, ok := variantNamedAt(n, q.loc); ok {
			return variantDeclText(n, variant), true
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
	decl, ok := refDecl(q, ref)
	if !ok {
		return "", false
	}
	if text := refDeclText(q, decl); text != "" {
		return text, true
	}
	return "", false
}

// refDeclText returns declHoverText for decl, the declaration a type reference in q
// names, read against the records of the file that declares it.
func refDeclText(q hoverQuery, decl ast.Decl) string {
	// A bin/ script reading a lib/ alias has a table of its own, which holds nothing
	// about the alias's annotation.
	return declHoverText(q.infoFor(decl.Span().SourceID), decl)
}

// refDecl returns the declaration of the type the type reference ref names, and false
// when the name resolves to no declared type in the file's scope.
func refDecl(q hoverQuery, ref *ast.TypeRefTypeAnn) (ast.Decl, bool) {
	if q.info == nil || q.scope == nil {
		return nil, false
	}
	refName := typeName(q.info.TypeOf(ref))
	if refName == "" {
		return nil, false
	}
	binding, ok := q.scope.GetType(ast.QualIdentToString(ref.Name))
	// A type a function body declares is not in the file's scope, and a binding of the
	// same name outside the body is a different type, which the solver names
	// differently.
	if !ok || typeName(binding.Type) != refName {
		return nil, false
	}
	for _, src := range binding.Sources {
		np, ok := src.(*ast.NodeProvenance)
		if !ok {
			continue
		}
		if decl, ok := np.Node.(ast.Decl); ok {
			return decl, true
		}
	}
	return nil, false
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

// declHoverText returns declText for decl, followed for a type alias by a line holding
// the alias's evaluated type when that prints differently from the type as written.
// `type Y = Point["y"]` for `type Point = {x: number, y?: number}` shows
// `type Y = Point["y"]` and then `= number`.
func declHoverText(info *solver.Info, decl ast.Decl) string {
	text := declText(decl)
	alias, ok := decl.(*ast.TypeDecl)
	if !ok || text == "" || info == nil {
		return text
	}
	written := info.TypeOf(alias.TypeAnn)
	if written == nil {
		return text
	}
	evaluated := soltype.PrintAsScheme(info.Evaluate(written))
	if evaluated == soltype.PrintAsScheme(written) {
		return text
	}
	return text + "\n= " + evaluated
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
// `fn `, any `<T>` type-parameter binder, and the ` -> ret` return, as `(status: number)`
// for `fn <T>(status: number) -> C`. A parameter that is itself a function type holds its
// own ` -> `, so the return is the last one, not the first.
func signatureParams(sig *soltype.FuncType) string {
	// A constructor's or a variant's signature is generalized over its class's or enum's
	// type parameters, so the printer writes them as a leading binder:
	// `fn <T, E>(value: T) -> Result<T, E>` for `Result.Ok`. Kept, the hover would read
	// `(variant) Result.Ok<T, E>(value: T)`, as if the variant took type arguments of its
	// own. The printed text can't tell the owner's parameters from the signature's, so a
	// signature with type parameters of its own loses them too. A printer that renders the
	// parameter list on its own would remove the guess, which #2716 tracks.
	rendered := dropTypeBinder(strings.TrimPrefix(soltype.PrintAsScheme(sig), "fn "))
	if i := strings.LastIndex(rendered, " -> "); i >= 0 {
		rendered = rendered[:i]
	}
	return rendered
}

// dropTypeBinder returns s without a leading `<…>` type-parameter binder, matching the
// closing `>` by angle-bracket depth so a nested generic in a bound stays inside the
// binder. The `>` of a function type's `->` is not a bracket, so a function-typed bound
// such as `<T: fn (x: number) -> number>` stays inside the binder too. A string with no
// leading binder is returned unchanged.
func dropTypeBinder(s string) string {
	if !strings.HasPrefix(s, "<") {
		return s
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			if i > 0 && s[i-1] == '-' {
				continue
			}
			depth--
			if depth == 0 {
				return strings.TrimPrefix(s[i+1:], " ")
			}
		}
	}
	return s
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
			// A capture, such as an `infer` or a named regex group, binds its name in the
			// test and in the branch taken when the test passes.
			inScope := n.Extends.Span().Contains(loc) || n.Then.Span().Contains(loc)
			if inScope && slices.Contains(ast.CaptureNames(n.Extends), name) {
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
	if ctor, ok := variantConstructor(t); ok {
		return "(variant) " + qualify(baseName(typeName(ctor.Ret)), m.Prop.Name) + signatureParams(ctor), true
	}
	objType := info.ResolvedTypeOf(m.Object)
	owner := memberOwner(info, m.Object, objType)
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

// memberOwner returns the name of the type a member read through object belongs to, where
// objType is object's resolved type, or "" for an object with no name. A read of a class
// name, as in `Thermostat.minimum`, reads the class's static side, whose type is an
// unnamed object, so the class's name is taken from its declaration.
func memberOwner(info *solver.Info, object ast.Expr, objType soltype.Type) string {
	if ident, ok := object.(*ast.IdentExpr); ok {
		if decl, ok := info.DeclOf(ident); ok {
			if class, ok := decl.(*ast.ClassDecl); ok {
				return class.Name.Name
			}
		}
	}
	return baseName(typeName(info.Expand(objType)))
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
		return modifiedPropertyText(owner, elem.Name, soltype.Print(elem.Type), elem.Readonly, elem.Optional)
	case *soltype.GetterElem:
		return "(getter) " + qualify(owner, elem.Name) + ": " + soltype.Print(elem.Type)
	case *soltype.SetterElem:
		return "(setter) " + qualify(owner, elem.Name) + ": " + soltype.Print(elem.Param.Type)
	case *soltype.MethodElem:
		if len(elem.Signatures) == 0 {
			return propertyText(owner, elem.Name, "")
		}
		name := qualify(owner, elem.Name)
		if elem.Optional {
			name += "?"
		}
		text := "(method) " + name + methodSignature(elem.Signatures[0])
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

// modifiedPropertyText renders a property member as propertyText does, adding the
// `readonly` and `?` modifiers the property declares, as
// `(property) readonly Task.id: number` and `(property) Point.y?: number`.
func modifiedPropertyText(owner, name, typ string, readonly, optional bool) string {
	text := "(property) "
	if readonly {
		text += "readonly "
	}
	text += qualify(owner, name)
	if optional {
		text += "?"
	}
	return text + ": " + typ
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

// enclosingReceiver returns the `self` receiver of the nearest method whose body holds the
// cursor, found by matching a function-expression ancestor to a member of class. A closure
// inside a method is not a member, so `self` read in it resolves to that method's
// receiver. It returns nil when no method body encloses the cursor.
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

// variantNamedAt returns the variant of decl whose name holds loc.
func variantNamedAt(decl *ast.EnumDecl, loc ast.Location) (*ast.EnumVariant, bool) {
	for _, elem := range decl.Elems {
		if variant, ok := elem.(*ast.EnumVariant); ok && variant.Name.Span().Contains(loc) {
			return variant, true
		}
	}
	return nil, false
}

// variantDeclText renders the variant of decl as it is declared, as
// `(variant) Expr.Num(value: number)`, the form a read of the variant shows.
func variantDeclText(decl *ast.EnumDecl, variant *ast.EnumVariant) string {
	params := make([]string, len(variant.Params))
	for i, param := range variant.Params {
		text := printCompact(param.Pattern)
		if param.Optional {
			text += "?"
		}
		if param.TypeAnn != nil {
			text += ": " + printCompact(param.TypeAnn)
		}
		params[i] = text
	}
	return "(variant) " + decl.Name.Name + "." + variant.Name.Name + "(" + strings.Join(params, ", ") + ")"
}

// variantConstructor returns an enum variant's constructor signature when t is the type a
// variant name reads as, and false otherwise. A variant carries both a constructor and a
// `Symbol.customMatcher` method, which a plain class constructor object does not, so the
// two are told apart by the matcher.
func variantConstructor(t soltype.Type) (*soltype.FuncType, bool) {
	obj, ok := t.(*soltype.ObjectType)
	if !ok {
		return nil, false
	}
	var ctor *soltype.FuncType
	hasMatcher := false
	for _, elem := range obj.Elems {
		switch elem := elem.(type) {
		case *soltype.ConstructorElem:
			if len(elem.Signatures) > 0 {
				ctor = elem.Signatures[0]
			}
		case *soltype.MethodElem:
			if sym, ok := soltype.SymbolOfMemberName(elem.Name); ok && sym == "customMatcher" {
				hasMatcher = true
			}
		}
	}
	if ctor == nil || !hasMatcher {
		return nil, false
	}
	return ctor, true
}

// namespaceHover returns `namespace Math` for a read of a namespace name, and false when
// the cursor is on none. A name that also reads a value shows the value, so a namespace
// hover fills in only where no value type was recorded.
func namespaceHover(q hoverQuery) (string, bool) {
	ident, ok := q.node.(*ast.IdentExpr)
	if !ok || q.info == nil || q.scope == nil {
		return "", false
	}
	if q.info.DisplayTypeOf(ident) != nil {
		return "", false
	}
	if _, ok := q.scope.GetNamespace(ident.Name); ok {
		return "namespace " + ident.Name, true
	}
	return "", false
}

// declMemberHover returns the hover text for a member written in a declaration: a class
// field, method, getter or setter name, or a property of an object type. It returns false
// when the cursor is on none. A class member carries the class name, as
// `(property) Dog.name: string`; an object-type property carries none.
func declMemberHover(q hoverQuery) (string, bool) {
	switch n := q.node.(type) {
	case *ast.IdentExpr:
		// A static field's initializer and a computed key also sit directly under the
		// class, so the identifier has to be one of the members' own name nodes.
		class, ok := parentClassDecl(q.ancestors)
		if !ok {
			return "", false
		}
		if member, ok := memberNamedBy(class, n); ok {
			return classMemberDeclText(q.info, q.scope, class, member)
		}
	case *ast.FuncExpr:
		// A method's function expression spans its name, so the cursor on the name lands
		// here rather than on an identifier.
		class, ok := parentClassDecl(q.ancestors)
		if !ok {
			return "", false
		}
		if member, ok := memberOfFn(class, n); ok {
			return classMemberDeclText(q.info, q.scope, class, member)
		}
	case *ast.ObjectTypeAnn:
		if member, ok := objTypeMemberNamedAt(n, q.loc); ok {
			return objTypeMemberText(q, n, member)
		}
	case *ast.FuncTypeAnn:
		// A method's function type spans its name, so the cursor on a method, getter or
		// setter name in an object type lands here rather than on the object type.
		obj, ok := parentObjectTypeAnn(q.ancestors)
		if !ok {
			return "", false
		}
		if member, ok := objTypeMemberOfFn(obj, n); ok && member.Key().Span().Contains(q.loc) {
			// The function type's ancestors end at obj, so obj's own ancestors are the ones
			// before it.
			outer := q
			outer.ancestors = q.ancestors[:len(q.ancestors)-1]
			return objTypeMemberText(outer, obj, member)
		}
	}
	return "", false
}

// memberNamedBy returns the field, method, getter or setter of class whose name node is
// ident itself, and false when ident is no member's name node.
func memberNamedBy(class *ast.ClassDecl, ident *ast.IdentExpr) (ast.ClassElem, bool) {
	key := ast.ObjKey(ident)
	for _, elem := range class.Body {
		if name, ok := classElemName(elem); ok && name == key {
			return elem, true
		}
	}
	return nil, false
}

// memberOfFn returns the method, getter or setter of class whose function is fn, and false
// when no such member has it.
func memberOfFn(class *ast.ClassDecl, fn *ast.FuncExpr) (ast.ClassElem, bool) {
	for _, elem := range class.Body {
		switch elem := elem.(type) {
		case *ast.MethodElem:
			if elem.Fn == fn {
				return elem, true
			}
		case *ast.GetterElem:
			if elem.Fn == fn {
				return elem, true
			}
		case *ast.SetterElem:
			if elem.Fn == fn {
				return elem, true
			}
		}
	}
	return nil, false
}

// classElemName returns the name key of a field, method, getter or setter, and false for
// any other class element.
func classElemName(elem ast.ClassElem) (ast.ObjKey, bool) {
	switch elem := elem.(type) {
	case *ast.FieldElem:
		return elem.Name, true
	case *ast.MethodElem:
		return elem.Name, true
	case *ast.GetterElem:
		return elem.Name, true
	case *ast.SetterElem:
		return elem.Name, true
	}
	return nil, false
}

// sameMemberKind reports whether the class element member and the object-type element
// elem are the same kind of member: a field and a property, or two methods, getters or
// setters.
func sameMemberKind(member ast.ClassElem, elem soltype.ObjTypeElem) bool {
	switch member.(type) {
	case *ast.FieldElem:
		_, ok := elem.(*soltype.PropertyElem)
		return ok
	case *ast.MethodElem:
		_, ok := elem.(*soltype.MethodElem)
		return ok
	case *ast.GetterElem:
		_, ok := elem.(*soltype.GetterElem)
		return ok
	case *ast.SetterElem:
		_, ok := elem.(*soltype.SetterElem)
		return ok
	}
	return false
}

// parentObjectTypeAnn returns the object type annotation when it is the cursor node's
// immediate parent.
func parentObjectTypeAnn(ancestors []ast.Node) (*ast.ObjectTypeAnn, bool) {
	if len(ancestors) == 0 {
		return nil, false
	}
	obj, ok := ancestors[len(ancestors)-1].(*ast.ObjectTypeAnn)
	return obj, ok
}

// parentClassDecl returns the class declaration when it is the cursor node's immediate
// parent, which holds for a member name written directly in the class body.
func parentClassDecl(ancestors []ast.Node) (*ast.ClassDecl, bool) {
	if len(ancestors) == 0 {
		return nil, false
	}
	class, ok := ancestors[len(ancestors)-1].(*ast.ClassDecl)
	return class, ok
}

// classMemberDeclText renders member, a field, method, getter or setter of class, in the
// form a read of it shows. It finds the matching element of the class's members by name
// and kind, so a getter and a setter of one name each render as themselves. It returns
// false when info or scope is nil or the class exposes no such member.
func classMemberDeclText(info *solver.Info, scope *solver.Scope, class *ast.ClassDecl, member ast.ClassElem) (string, bool) {
	if info == nil || scope == nil {
		return "", false
	}
	key, ok := classElemName(member)
	if !ok {
		return "", false
	}
	name := objKeyName(key)
	obj, ok := classMembers(info, scope, class, isStaticMember(member))
	if !ok {
		return "", false
	}
	for _, elem := range obj.Elems {
		if soltype.ObjElemName(elem) == name && sameMemberKind(member, elem) {
			return memberElemText(class.Name.Name, elem), true
		}
	}
	return "", false
}

// classMembers returns the members of class: its instance members, or its static members
// when static is true. The static members belong to the class's value, the object a read
// of the class name yields, so they are looked up through the class's value binding.
func classMembers(info *solver.Info, scope *solver.Scope, class *ast.ClassDecl, static bool) (*soltype.ObjectType, bool) {
	if static {
		binding, ok := scope.GetValue(class.Name.Name)
		if !ok {
			return nil, false
		}
		return info.MemberObject(binding.DisplayType())
	}
	binding, ok := scope.GetType(class.Name.Name)
	if !ok {
		return nil, false
	}
	return info.MemberObject(binding.Type)
}

// isStaticMember reports whether member is declared `static`.
func isStaticMember(member ast.ClassElem) bool {
	switch member := member.(type) {
	case *ast.FieldElem:
		return member.Static
	case *ast.MethodElem:
		return member.Static
	case *ast.GetterElem:
		return member.Static
	case *ast.SetterElem:
		return member.Static
	}
	return false
}

// objTypeMemberText renders the member of the object type annotation obj that member
// names, where member is one of obj's elements. A property renders from its annotation, as
// `(property) readonly id: number`. A method, getter or setter renders from obj's type, in
// the form a read of it shows, so its receiver is left out. It returns false when member
// is no such element or obj's type is unknown.
func objTypeMemberText(q hoverQuery, obj *ast.ObjectTypeAnn, member ast.ObjTypeAnnElem) (string, bool) {
	if prop, ok := member.(*ast.PropertyTypeAnn); ok {
		return modifiedPropertyText("", objKeyName(prop.Name), printCompact(prop.Value), prop.Readonly, prop.Optional), true
	}
	recorded, ok := objTypeAnnMembers(q, obj)
	if !ok {
		return "", false
	}
	name := objKeyName(member.Key())
	for _, elem := range recorded.Elems {
		if soltype.ObjElemName(elem) == name && sameObjTypeMemberKind(member, elem) {
			return memberElemText("", elem), true
		}
	}
	return "", false
}

// objTypeAnnMembers returns the members of the type the object type annotation obj
// denotes. The checker records a type for an object type written as a type, such as the
// body of an alias. It records none for an interface's body, so that body's members come
// from the interface's own type, found by the interface's name. It returns false when
// neither is known.
func objTypeAnnMembers(q hoverQuery, obj *ast.ObjectTypeAnn) (*soltype.ObjectType, bool) {
	if q.info == nil {
		return nil, false
	}
	if recorded, ok := q.info.TypeOf(obj).(*soltype.ObjectType); ok {
		return recorded, true
	}
	if len(q.ancestors) == 0 || q.scope == nil {
		return nil, false
	}
	iface, ok := q.ancestors[len(q.ancestors)-1].(*ast.InterfaceDecl)
	if !ok || iface.TypeAnn != obj {
		return nil, false
	}
	binding, ok := q.scope.GetType(iface.Name.Name)
	if !ok {
		return nil, false
	}
	return q.info.MemberObject(binding.Type)
}

// objTypeMemberNamedAt returns the property, method, getter or setter of obj whose name
// holds loc.
func objTypeMemberNamedAt(obj *ast.ObjectTypeAnn, loc ast.Location) (ast.ObjTypeAnnElem, bool) {
	for _, elem := range obj.Elems {
		switch elem.(type) {
		case *ast.PropertyTypeAnn, *ast.MethodTypeAnn, *ast.GetterTypeAnn, *ast.SetterTypeAnn:
			if key := elem.Key(); key != nil && key.Span().Contains(loc) {
				return elem, true
			}
		}
	}
	return nil, false
}

// objTypeMemberOfFn returns the method, getter or setter of obj whose function type is fn.
func objTypeMemberOfFn(obj *ast.ObjectTypeAnn, fn *ast.FuncTypeAnn) (ast.ObjTypeAnnElem, bool) {
	for _, elem := range obj.Elems {
		switch elem := elem.(type) {
		case *ast.MethodTypeAnn:
			if elem.Fn == fn {
				return elem, true
			}
		case *ast.GetterTypeAnn:
			if elem.Fn == fn {
				return elem, true
			}
		case *ast.SetterTypeAnn:
			if elem.Fn == fn {
				return elem, true
			}
		}
	}
	return nil, false
}

// sameObjTypeMemberKind reports whether the object-type annotation member and the
// object-type element elem are the same kind of member: two methods, getters or setters.
func sameObjTypeMemberKind(member ast.ObjTypeAnnElem, elem soltype.ObjTypeElem) bool {
	switch member.(type) {
	case *ast.MethodTypeAnn:
		_, ok := elem.(*soltype.MethodElem)
		return ok
	case *ast.GetterTypeAnn:
		_, ok := elem.(*soltype.GetterElem)
		return ok
	case *ast.SetterTypeAnn:
		_, ok := elem.(*soltype.SetterElem)
		return ok
	}
	return false
}
