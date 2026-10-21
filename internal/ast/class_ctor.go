package ast

import (
	"fmt"
	"unicode"

	"github.com/escalier-lang/escalier/internal/set"
)

// selfParamName is the receiver's name in a synthesized constructor. codegen lowers it
// to `this`.
const selfParamName = "self"

// ImplicitConstructor returns the constructor a class that declares none gets: one
// parameter per required instance field, in declaration order, each assigned into
// `self`. `class Point { x: number, y: number }` gets
// `constructor(&mut self, x: number, y: number) { self.x = x; self.y = y }`.
//
// The element is nil for a class that declares a constructor, a `declare class`, and a
// subclass. ImplicitSubclassConstructor builds a subclass's.
//
// The second return names the field that blocked synthesis, which is a non-static field
// whose computed key stableKeyExpr rejects. A caller that reports diagnostics says the
// class needs an explicit constructor, and one that only emits code leaves it out.
//
// internal/solver and JavaScript emission both call this, which is what makes them
// agree on what a class constructs.
func ImplicitConstructor(decl *ClassDecl) (*ConstructorElem, *FieldElem) {
	if decl.Declare() || decl.Extends != nil || declaresConstructor(decl) {
		return nil, nil
	}
	inits, blocker := implicitFieldInits(decl, FreeNamesInComputedKeys(decl), set.NewSet[string](), nil)
	if blocker != nil {
		return nil, blocker
	}
	classSpan := decl.Name.Span()
	params := []*Param{implicitSelfParam(classSpan)}
	stmts := []Stmt{}
	for _, init := range inits {
		params = append(params, init.param)
		rhs := NewIdent(init.param.Pattern.(*IdentPat).Name, init.field.Span())
		stmts = append(stmts, init.assign(rhs))
	}
	return implicitConstructorElem(params, stmts, classSpan), nil
}

// ImplicitSubclassConstructor returns the constructor a subclass that declares none gets,
// when its superclass's constructor was itself synthesized. superParams names that
// constructor's parameters, in order. inherited reports a field the subclass redeclares
// from its superclass, whose value arrives through the superclass's parameter for it.
//
// The constructor takes superParams followed by one parameter per remaining required
// instance field, in declaration order. Its body passes superParams to `super(…)` and then
// assigns each remaining field. For `class Derived extends Base { bar: number }` over
// `class Base { foo: string }` that is
//
//	constructor(&mut self, foo, bar) {
//	    super(foo)
//	    self.bar = bar
//	}
//
// The second return holds the parameters of the remaining fields, in order. A field whose
// key binds no name takes a generated `_field<N>` that avoids superParams.
//
// The element is nil for a class without an `extends` clause, a class that declares a
// constructor, and a `declare class`. The third return names the field that blocked
// synthesis, as it does for ImplicitConstructor.
func ImplicitSubclassConstructor(decl *ClassDecl, superParams []string, inherited func(*FieldElem) bool) (*ConstructorElem, []*Param, *FieldElem) {
	if decl.Declare() || decl.Extends == nil || declaresConstructor(decl) {
		return nil, nil, nil
	}
	inits, blocker := implicitFieldInits(decl, FreeNamesInComputedKeys(decl), set.FromSlice(superParams), inherited)
	if blocker != nil {
		return nil, nil, blocker
	}

	classSpan := decl.Name.Span()
	params := []*Param{implicitSelfParam(classSpan)}
	superArgs := make([]Expr, 0, len(superParams))
	for _, name := range superParams {
		params = append(params, &Param{Pattern: NewIdentPat(name, false /* not mutable */, nil, nil, classSpan)})
		superArgs = append(superArgs, NewIdent(name, classSpan))
	}
	stmts := []Stmt{NewExprStmt(NewSuperCall(superArgs, classSpan), classSpan)}

	fieldParams := make([]*Param, 0, len(inits))
	for _, init := range inits {
		params = append(params, init.param)
		fieldParams = append(fieldParams, init.param)
		rhs := NewIdent(init.param.Pattern.(*IdentPat).Name, init.field.Span())
		stmts = append(stmts, init.assign(rhs))
	}
	return implicitConstructorElem(params, stmts, classSpan), fieldParams, nil
}

// declaresConstructor reports whether decl writes a `constructor` block.
func declaresConstructor(decl *ClassDecl) bool {
	for _, bodyElem := range decl.Body {
		if _, declared := bodyElem.(*ConstructorElem); declared {
			return true
		}
	}
	return false
}

// implicitSelfParam returns the `mut self` receiver a synthesized constructor binds. It sits
// at the class-name span, so a diagnostic against the synthesized constructor lands on the
// header.
func implicitSelfParam(classSpan Span) *Param {
	selfPat := NewIdentPat(selfParamName, true /* mutable */, nil, nil, classSpan)
	return &Param{Pattern: selfPat, TypeAnn: nil, Optional: false}
}

// implicitConstructorElem wraps a synthesized constructor's parameters and statements in the
// element a class body holds.
func implicitConstructorElem(params []*Param, stmts []Stmt, classSpan Span) *ConstructorElem {
	body := &Block{Stmts: stmts, Span: classSpan}
	fnExpr := NewFuncExpr(nil, nil, params, nil, nil, false, body, classSpan)
	return &ConstructorElem{
		Fn:       fnExpr,
		Receiver: &MethodReceiver{Mut: true, Span_: classSpan},
		Private:  false,
		Span_:    classSpan,
	}
}

// implicitFieldInit is what a synthesized constructor needs for one required field: the
// field, the parameter its value arrives in, and the target `self.<name>` or
// `self[<key>]` the value is assigned to.
type implicitFieldInit struct {
	field *FieldElem
	param *Param
	lhs   Expr
}

// assign returns the statement that stores rhs into the field.
func (init implicitFieldInit) assign(rhs Expr) Stmt {
	span := init.field.Span()
	return NewExprStmt(NewBinary(init.lhs, rhs, Assign, span), span)
}

// implicitFieldInits returns one implicitFieldInit per required instance field of decl, in
// declaration order, leaving out each field skip reports. A nil skip leaves out none.
// freeKeyNames is FreeNamesInComputedKeys(decl). A generated `_field<N>` parameter name
// avoids every name in reserved.
//
// The second return names the field that blocked synthesis, a non-static field whose
// computed key stableKeyExpr rejects. The inits are nil when it is set.
func implicitFieldInits(decl *ClassDecl, freeKeyNames, reserved set.Set[string], skip func(*FieldElem) bool) ([]implicitFieldInit, *FieldElem) {
	// Collected before any parameter is built, so a generated name can avoid them.
	// `class C { _field1: number, "a-b": string }` would otherwise bind `_field1`
	// twice, a SyntaxError, and assign the first field's value to the second.
	taken := reserved.Union(freeKeyNames)
	for _, bodyElem := range decl.Body {
		if field, ok := bodyElem.(*FieldElem); ok && TakesConstructorParam(field) && !skipped(skip, field) {
			if name, direct := directParamName(field.Name, freeKeyNames); direct {
				taken.Add(name)
			}
		}
	}
	generated := 0

	var inits []implicitFieldInit
	for _, bodyElem := range decl.Body {
		field, ok := bodyElem.(*FieldElem)
		if !ok || !TakesConstructorParam(field) || skipped(skip, field) {
			continue
		}

		fieldSpan := field.Span()

		var paramName string
		var lhs Expr
		selfRef := NewIdent(selfParamName, fieldSpan)

		if name, direct := directParamName(field.Name, freeKeyNames); direct {
			paramName = name
			lhs = NewMember(selfRef, NewIdentifier(name, fieldSpan), false, fieldSpan)
		} else {
			// A key that cannot bind a name is assigned by index instead.
			var keyExpr Expr
			switch k := field.Name.(type) {
			case *IdentExpr:
				// A key spelled like a reserved word or like the receiver. It is a
				// valid property name, so only its parameter is renamed.
				keyExpr = NewLitExpr(NewString(k.Name, fieldSpan))
			case *StrLit:
				keyExpr = NewLitExpr(NewString(k.Value, fieldSpan))
			case *NumLit:
				keyExpr = NewLitExpr(NewNumber(k.Value, fieldSpan))
			case *ComputedKey:
				if !stableKeyExpr(k.Expr) {
					// Stop rather than emit a constructor that assigns the wrong
					// property.
					return nil, field
				}
				keyExpr = k.Expr
			default:
				panic("implicitFieldInits: unexpected ObjKey variant")
			}
			for {
				generated++
				paramName = fmt.Sprintf("_field%d", generated)
				if !taken.Contains(paramName) {
					break
				}
			}
			taken.Add(paramName)
			lhs = NewIndex(selfRef, keyExpr, false, fieldSpan)
		}

		paramPat := NewIdentPat(paramName, false /* not mutable */, nil, nil, fieldSpan)
		inits = append(inits, implicitFieldInit{
			field: field,
			param: &Param{Pattern: paramPat, TypeAnn: field.Type, Optional: false},
			lhs:   lhs,
		})
	}
	return inits, nil
}

// skipped reports whether skip leaves field out. A nil skip leaves out nothing.
func skipped(skip func(*FieldElem) bool, field *FieldElem) bool {
	return skip != nil && skip(field)
}

// stableKeyExpr reports whether reading expr again gives what it gave when the class was
// defined. JavaScript evaluates a computed class-member key once, where the class is
// defined, and the synthesized body reads it per construction, so only an expression
// that cannot change between the two belongs there.
//
// Two shapes qualify. A variable read is pinned by the key's own type. A computed key
// has to name one property, and a binding typed by a single literal or a `unique symbol`
// has no second value to hold. `var k = Symbol()` widens to `symbol`, which is rejected
// as a key before synthesis is reached.
//
// `Symbol.<name>` is the other, which is what `[Symbol.iterator]` writes. A property of
// the global `Symbol` is a data property, so reading it runs no user code.
//
// Every other shape is left to a hand-written constructor, since reading it again can
// answer differently. A call is the plainest case: `declare fn makeKey() -> unique
// symbol` type-checks as a key and answers with a fresh symbol per call, so
// `self[makeKey()] = v` would assign a property no reader can name. A property read off
// anything but `Symbol` is excluded because a getter is a call this package cannot tell
// from a field, which `declare val keys: {get k(&self) -> unique symbol}` makes
// `[keys.k]`.
//
// #1831 removes the distinction by evaluating the key once beside the class, after which
// every computed key gets a constructor and this goes away.
func stableKeyExpr(expr Expr) bool {
	switch e := expr.(type) {
	case *IdentExpr:
		return true
	case *MemberExpr:
		obj, isIdent := e.Object.(*IdentExpr)
		return !e.OptChain && e.Prop != nil && isIdent && obj.Name == symbolGlobalName
	default:
		return false
	}
}

// symbolGlobalName is the global whose properties a computed key may read. See
// stableKeyExpr.
const symbolGlobalName = "Symbol"

// FreeNamesInComputedKeys returns the names this class's computed keys read from the
// surrounding scope, the free names of those key expressions. No parameter the emitted
// constructor binds may use one, since the constructor reads each key after binding its
// parameters, and a parameter of that name would shadow what the key meant where the
// class was defined.
//
// ImplicitConstructor keeps the names it chooses off this list. JavaScript emission has
// its own, the `temp<N>` a lowered parameter takes, so it reads this to keep those off it
// too.
//
// Only a free name counts, so `[Symbol.iterator]` contributes `Symbol` and not
// `iterator`. A property is a name on the object rather than a binding, so a field named
// `iterator` shadows nothing and keeps its own parameter.
func FreeNamesInComputedKeys(decl *ClassDecl) set.Set[string] {
	names := set.NewSet[string]()
	for _, bodyElem := range decl.Body {
		field, ok := bodyElem.(*FieldElem)
		if !ok || !TakesConstructorParam(field) {
			continue
		}
		key, computed := field.Name.(*ComputedKey)
		if !computed || !stableKeyExpr(key.Expr) {
			continue
		}
		switch keyExpr := key.Expr.(type) {
		case *IdentExpr:
			names.Add(keyExpr.Name)
		case *MemberExpr:
			// stableKeyExpr admits no deeper chain, so the object is the free name.
			if obj, isIdent := keyExpr.Object.(*IdentExpr); isIdent {
				names.Add(obj.Name)
			}
		}
	}
	return names
}

// TakesConstructorParam reports whether a field's value arrives through a constructor
// parameter. A static field belongs to the class rather than the instance. An optional
// field defaults to `undefined`, and a parameter would force every caller to pass one.
func TakesConstructorParam(field *FieldElem) bool {
	return !field.Static && !field.Optional
}

// directParamName returns the parameter name a key binds under, and reports whether it
// binds one at all. A key binds its own spelling, so `x: number` arrives in a parameter
// named `x`. Four kinds bind none: a number, a computed expression, a name
// canBindParamName rejects, and a name in freeKeyNames. Such a field is assigned by
// index under a generated name.
//
// freeKeyNames comes from FreeNamesInComputedKeys. Binding one of those names would
// shadow it for the rest of the constructor, so `class C { k: number, [k]: string }`
// would store the second field under the first field's value.
//
// An identifier key and a string key are held to the same test, since
// `class C { class: … }` and `class C { "class": … }` name the same field.
func directParamName(key ObjKey, freeKeyNames set.Set[string]) (string, bool) {
	var name string
	switch k := key.(type) {
	case *IdentExpr:
		name = k.Name
	case *StrLit:
		name = k.Value
	default:
		return "", false
	}
	if !canBindParamName(name) || freeKeyNames.Contains(name) {
		return "", false
	}
	return name, true
}

// canBindParamName reports whether the synthesized constructor can bind a parameter
// under this name. It accepts a Unicode letter, a digit, `_` and `$`, with the first
// character a letter, `_`, or `$`.
//
// A reserved word is rejected because each parameter is bound with `const <name> = …`,
// so `class C { "class": number }` would emit the SyntaxError `const class = temp1`.
//
// The receiver's name is rejected because codegen lowers every `self` to `this`, so the
// assignment in `class C { self: number }` would read the instance, not the argument.
func canBindParamName(name string) bool {
	if name == "" || name == selfParamName || jsBindingKeywords.Contains(name) {
		return false
	}
	for i, r := range name {
		isLetter := unicode.IsLetter(r) || r == '_' || r == '$'
		if i == 0 {
			if !isLetter {
				return false
			}
			continue
		}
		if !isLetter && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// jsBindingKeywords are the words JavaScript does not accept as a binding name.
//
// Emitted output is a module and so runs in strict mode, which is why `let` and
// `static` are here. `arguments` and `eval` are not reserved and still cannot be bound
// in strict mode; `arguments` also names the object an emitted overload dispatch reads.
var jsBindingKeywords = set.FromSlice([]string{
	"arguments", "await", "break", "case", "catch", "class", "const", "continue",
	"debugger", "default", "delete", "do", "else", "enum", "eval", "export", "extends",
	"false", "finally", "for", "function", "if", "implements", "import", "in",
	"instanceof", "interface", "let", "new", "null", "package", "private", "protected",
	"public", "return", "static", "super", "switch", "this", "throw", "true", "try",
	"typeof", "var", "void", "while", "with", "yield",
})
