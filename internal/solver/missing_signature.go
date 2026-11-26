package solver

import (
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// signatureSite is where a function is declared, which decides whether it writes its full
// signature and how MissingSignatureError names it.
type signatureSite int

const (
	// moduleFunc is a `fn` declared at a module's top level.
	moduleFunc signatureSite = iota
	// scriptFunc is a `fn` declared at a script's top level.
	scriptFunc
	// method is a method, getter or setter of a class.
	method
	// moduleClosure is a function expression bound to a module-level `val` or `var` that
	// has no type annotation.
	moduleClosure
	// scriptClosure is a function expression bound to a `val` or `var` at a script's top
	// level that has no type annotation.
	scriptClosure
)

// MissingSignatureError reports a function that leaves out a parameter type or its return
// type where its signature has to be written in full: a function declared at a module's or
// a script's top level, a method, getter or setter, and a function expression bound to a
// top-level variable with no type annotation.
type MissingSignatureError struct {
	// Name is the function's declared name, or the name of the variable it is bound to.
	Name string
	Site signatureSite
	// Params names each parameter written with no type, in order.
	Params []string
	// MissingReturn reports that the return type is left out.
	MissingReturn bool
	// Suggestion is what to write: the return type as `-> T`, or the whole signature when the
	// return type names a lifetime the signature has to declare. It is the empty string when
	// a parameter type is also left out, since the checker has nothing to suggest once it no
	// longer infers parameter types.
	Suggestion string
	// Placeholder reports that the signature writes `_` outside its error type. Params then
	// names the one parameter whose type writes it, or MissingReturn reports that the return
	// type does, and neither is set when another part of the signature does. Suggestion holds
	// the inferred type to write in its place, or is empty when the checker has none.
	Placeholder bool
	// Node is the name the function is declared under, or the misplaced `_` when Placeholder
	// is set. It carries the blame span.
	Node ast.Node
}

func (*MissingSignatureError) isSolverError()        {}
func (e *MissingSignatureError) Span() ast.Span      { return e.Node.Span() }
func (e *MissingSignatureError) Related() []ast.Span { return nil }
func (e *MissingSignatureError) Message() string {
	var where string
	closure := false
	switch e.Site {
	case moduleFunc:
		where = "is declared at module level"
	case scriptFunc:
		where = "is declared at a script's top level"
	case method:
		where = "is a method"
	case moduleClosure:
		where, closure = "is a module-level closure", true
	case scriptClosure:
		where, closure = "is a closure at a script's top level", true
	}
	msg := "'" + e.Name + "' " + where + ", so "
	if e.Placeholder {
		switch {
		case len(e.Params) > 0:
			msg += "parameter '" + e.Params[0] + "' needs a type in place of `_`"
		case e.MissingReturn:
			msg += "its return type may write `_` only for its error type"
		default:
			msg += "its signature may write `_` only for its error type"
		}
		if e.Suggestion == "" {
			return msg
		}
		msg += "; write `" + e.Suggestion + "`"
		if closure {
			msg += " on the closure, or annotate '" + e.Name + "'"
		}
		return msg
	}
	if len(e.Params) > 0 {
		quoted := make([]string, len(e.Params))
		for i, p := range e.Params {
			quoted[i] = "'" + p + "'"
		}
		if len(quoted) == 1 {
			msg += "parameter " + quoted[0] + " needs a type"
		} else {
			msg += "parameters " + strings.Join(quoted[:len(quoted)-1], ", ") + " and " +
				quoted[len(quoted)-1] + " need types"
		}
		if e.MissingReturn {
			msg += " and '" + e.Name + "' needs a return type"
		}
		if closure {
			msg += "; write them on the closure, or annotate '" + e.Name + "'"
		}
		return msg
	}
	msg += "it needs a return type"
	if e.Suggestion == "" {
		return msg
	}
	msg += "; write `" + e.Suggestion + "`"
	if closure {
		msg += " on the closure, or annotate '" + e.Name + "'"
	}
	return msg
}

// checkModuleSignatures reports MissingSignatureError for each function of module whose
// signature has to be written in full and is not. It reads the types the run recorded, so
// it runs once the module is inferred.
func (c *checker) checkModuleSignatures(module *ast.Module) {
	for _, ns := range module.Namespaces.Values() {
		for _, d := range ns.Decls {
			c.checkDeclSignatures(d, moduleFunc, moduleClosure)
		}
	}
}

// checkScriptSignatures reports MissingSignatureError for each function declared at the
// top level of script, and each method of a class declared there, whose signature is not
// written in full. It runs once the script is inferred.
func (c *checker) checkScriptSignatures(script *ast.Script) {
	for _, stmt := range script.Stmts {
		if ds, ok := stmt.(*ast.DeclStmt); ok {
			c.checkDeclSignatures(ds.Decl, scriptFunc, scriptClosure)
		}
	}
}

// checkDeclSignatures reports each function d declares whose signature is not written in
// full. funcSite is where a `fn` d declares sits, and closureSite where a function
// expression d binds sits.
func (c *checker) checkDeclSignatures(d ast.Decl, funcSite, closureSite signatureSite) {
	switch d := d.(type) {
	case *ast.FuncDecl:
		// A `declare fn` has no body to infer from, so the parser already requires its types.
		if d.Body == nil {
			return
		}
		sig, ok := c.info.FuncDeclSignature(d)
		suggest := func() string { return "" }
		if ok {
			suggest = func() string { return writtenSignature("fn "+d.Name.Name, &d.FuncSig, sig) }
		}
		c.checkSignature(&d.FuncSig, true, true, d.Name.Name, funcSite, d.Name, suggest)
		c.checkPlaceholders(&d.FuncSig, d.Name.Name, funcSite, func() (soltype.FuncSignature, bool) {
			return writtenParts(sig, ok)
		})
	case *ast.ClassDecl:
		c.checkMemberSignatures(d)
	case *ast.VarDecl:
		// An annotation on d gives the function its parameter and return types, so it needs
		// none of its own.
		fn, ok := d.Init.(*ast.FuncExpr)
		ident, named := d.Pattern.(*ast.IdentPat)
		if !ok || !named || d.TypeAnn != nil {
			return
		}
		suggest := func() string { return "" }
		ft, typed := c.info.TypeOf(d.Pattern).(*soltype.FuncType)
		if typed {
			suggest = func() string { return writtenSignature("fn ", &fn.FuncSig, DeclSignature{Type: ft}) }
		}
		// checkParamTypes reports a closure parameter left unannotated, since no context
		// types a closure bound with no annotation.
		c.checkSignature(&fn.FuncSig, false, true, ident.Name, closureSite, d.Pattern, suggest)
		c.checkPlaceholders(&fn.FuncSig, ident.Name, closureSite, func() (soltype.FuncSignature, bool) {
			return writtenParts(DeclSignature{Type: ft}, typed)
		})
	}
}

// writtenParts returns sig's parts as PrintWritten renders them. It returns false, and no
// parts, when ok is false or sig has no type.
func writtenParts(sig DeclSignature, ok bool) (soltype.FuncSignature, bool) {
	if !ok || sig.Type == nil {
		return soltype.FuncSignature{}, false
	}
	return sig.PrintWritten(), true
}

// checkMemberSignatures reports each method, getter and setter of class whose signature is
// not written in full. A setter returns nothing, so it needs only its parameter's type.
func (c *checker) checkMemberSignatures(class *ast.ClassDecl) {
	for _, elem := range class.Body {
		var key ast.ObjKey
		var fn *ast.FuncExpr
		suggest := func() string { return "" }
		var rendered func() (soltype.FuncSignature, bool)
		// checkSignature calls suggest only for a member that leaves out just its return
		// type, so the lookups of the member's registered type run for those members alone.
		switch e := elem.(type) {
		case *ast.MethodElem:
			key, fn = e.Name, e.Fn
			rendered = func() (soltype.FuncSignature, bool) {
				return writtenParts(c.info.MethodSignature(class, e))
			}
			suggest = func() string {
				sig, ok := c.info.MethodSignature(class, e)
				if !ok {
					return ""
				}
				return writtenSignature(memberName(e.Name), &e.Fn.FuncSig, sig)
			}
		case *ast.GetterElem:
			key, fn = e.Name, e.Fn
			rendered = func() (soltype.FuncSignature, bool) {
				t, ok := c.accessorType(class, e.Name, e.Static, false)
				return soltype.FuncSignature{Ret: t}, ok
			}
			suggest = func() string {
				t, ok := c.accessorType(class, e.Name, e.Static, false)
				if !ok {
					return ""
				}
				return "-> " + t
			}
		case *ast.SetterElem:
			key, fn = e.Name, e.Fn
			rendered = func() (soltype.FuncSignature, bool) {
				t, ok := c.accessorType(class, e.Name, e.Static, true)
				return soltype.FuncSignature{Params: []string{t}}, ok
			}
		default:
			continue
		}
		if fn.Body == nil {
			continue
		}
		_, setter := elem.(*ast.SetterElem)
		c.checkSignature(&fn.FuncSig, true, !setter, memberName(key), method, key, suggest)
		c.checkPlaceholders(&fn.FuncSig, memberName(key), method, rendered)
	}
}

// memberName returns the name a class member is declared under, as the source writes it.
func memberName(key ast.ObjKey) string {
	if name, ok := objKeyName(key); ok {
		return name
	}
	if computed, ok := key.(*ast.ComputedKey); ok {
		written, _ := printer.Print(computed.Expr, printer.DefaultOptions())
		return "[" + written + "]"
	}
	written, _ := printer.Print(key, printer.DefaultOptions())
	return written
}

// accessorType renders the type an accessor of class named key reads or writes, under the
// names of the class's type and lifetime parameters. static picks the class's static side,
// and setter picks the setter's parameter type over the getter's type. ok is false when the
// checker registered no such accessor.
func (c *checker) accessorType(class *ast.ClassDecl, key ast.ObjKey, static, setter bool) (string, bool) {
	instance, ok := c.info.TypeOf(class.Name).(*soltype.ClassType)
	if !ok {
		return "", false
	}
	def, ok := c.ctx.classDef(instance.Name)
	name, named := objKeyName(key)
	if !ok || !named {
		return "", false
	}
	members := def.Body
	if static {
		members = def.Static
	}
	if members == nil {
		return "", false
	}
	for _, elem := range members.Elems {
		var t soltype.Type
		switch elem := elem.(type) {
		case *soltype.GetterElem:
			if !setter && elem.Name == name {
				t = elem.Type
			}
		case *soltype.SetterElem:
			if setter && elem.Name == name && elem.Param != nil {
				t = elem.Param.Type
			}
		}
		if t != nil {
			return soltype.PrintWithDeclaredParams(t, def.TypeParams, def.LifetimeParams), true
		}
	}
	return "", false
}

// writtenSignature renders what to write for sig, given the signature the checker inferred
// for it. That is the return type as `-> T`, unless the return type names a lifetime the
// signature has to declare. Then it is the whole signature after head, such as `fn pick`,
// with each parameter named as sig names it.
func writtenSignature(head string, sig *ast.FuncSig, inferred DeclSignature) string {
	rendered := inferred.PrintWritten()
	ret := "-> " + rendered.Ret
	if !namesLifetimeBinder(rendered.Ret, rendered.Binders) || len(rendered.Params) != len(sig.Params) {
		return ret
	}
	var params []string
	if rendered.Receiver != "" {
		params = append(params, rendered.Receiver)
	}
	for i, p := range sig.Params {
		name, _ := printer.Print(p.Pattern, printer.DefaultOptions())
		if p.Optional {
			name += "?"
		}
		params = append(params, name+": "+rendered.Params[i])
	}
	return head + "<" + strings.Join(rendered.Binders, ", ") + ">(" + strings.Join(params, ", ") + ") " + ret
}

// namesLifetimeBinder reports whether text names one of the lifetimes binders declares.
func namesLifetimeBinder(text string, binders []string) bool {
	for _, b := range binders {
		name, _, _ := strings.Cut(b, ":")
		if !strings.HasPrefix(name, "'") {
			continue
		}
		for i := strings.Index(text, name); i >= 0; {
			end := i + len(name)
			if end == len(text) || !isIdentByte(text[end]) {
				return true
			}
			next := strings.Index(text[end:], name)
			if next < 0 {
				break
			}
			i = end + next
		}
	}
	return false
}

// isIdentByte reports whether b can continue an identifier or a lifetime name.
func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// checkSignature reports sig, which name declares at site, when it leaves out a parameter
// type and needsParams is set, or its return type when needsReturn is set. node carries the
// blame span. suggest
// renders what to write when only the return type is left out, or the empty string when the
// checker has nothing to suggest.
func (c *checker) checkSignature(
	sig *ast.FuncSig,
	needsParams, needsReturn bool,
	name string,
	site signatureSite,
	node ast.Node,
	suggest func() string,
) {
	var params []string
	for _, p := range sig.Params {
		if needsParams && p.TypeAnn == nil {
			written, _ := printer.Print(p.Pattern, printer.DefaultOptions())
			params = append(params, written)
		}
	}
	missingReturn := needsReturn && sig.Return == nil
	if len(params) == 0 && !missingReturn {
		return
	}
	err := &MissingSignatureError{Name: name, Site: site, Params: params, MissingReturn: missingReturn, Node: node}
	// A suggested return type can name a type the parameters bind, so it is offered only
	// once every parameter is written.
	if !slices.ContainsFunc(sig.Params, func(p *ast.Param) bool { return p.TypeAnn == nil }) {
		err.Suggestion = suggest()
	}
	c.report(err)
}
