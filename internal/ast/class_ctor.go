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
// subclass, whose body would have to forward to `super(…)`.
//
// The second return names the field that blocked synthesis, which is a non-static field
// whose computed key stableKeyExpr rejects. A caller that reports diagnostics says the
// class needs an explicit constructor, and one that only emits code leaves it out.
//
// internal/checker and JavaScript emission both call this, which is what makes them
// agree on what a class constructs.
func ImplicitConstructor(decl *ClassDecl) (*ConstructorElem, *FieldElem) {
	if decl.Declare() || decl.Extends != nil {
		return nil, nil
	}
	for _, bodyElem := range decl.Body {
		if _, declared := bodyElem.(*ConstructorElem); declared {
			return nil, nil
		}
	}

	classSpan := decl.Name.Span()

	// At the class-name span, so a diagnostic against the synthesized constructor
	// lands on the header.
	selfPat := NewIdentPat(selfParamName, true /* mutable */, nil, nil, classSpan)
	selfParam := &Param{Pattern: selfPat, TypeAnn: nil, Optional: false}
	params := []*Param{selfParam}
	stmts := []Stmt{}

	// A name a computed key reads cannot also be a parameter, so it is settled first
	// and the direct-name decision below consults it.
	keyReads := keyReadNames(decl)

	// Collected before any parameter is built, so a generated name can avoid them.
	// `class C { _field1: number, "a-b": string }` would otherwise bind `_field1`
	// twice, a SyntaxError, and assign the first field's value to the second.
	taken := set.NewSet[string]()
	for name := range keyReads {
		taken.Add(name)
	}
	for _, bodyElem := range decl.Body {
		if field, ok := bodyElem.(*FieldElem); ok && takesConstructorParam(field) {
			if name, direct := directParamName(field.Name, keyReads); direct {
				taken.Add(name)
			}
		}
	}
	generated := 0

	for _, bodyElem := range decl.Body {
		field, ok := bodyElem.(*FieldElem)
		if !ok || !takesConstructorParam(field) {
			continue
		}

		fieldSpan := field.Span()

		var paramName string
		var lhs Expr
		selfRef := NewIdent(selfParamName, fieldSpan)

		if name, direct := directParamName(field.Name, keyReads); direct {
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
				panic("ImplicitConstructor: unexpected ObjKey variant")
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

		rhs := NewIdent(paramName, fieldSpan)
		paramPat := NewIdentPat(paramName, false /* not mutable */, nil, nil, fieldSpan)
		params = append(params, &Param{
			Pattern:  paramPat,
			TypeAnn:  field.Type,
			Optional: false,
		})

		assign := NewBinary(lhs, rhs, Assign, fieldSpan)
		stmts = append(stmts, NewExprStmt(assign, fieldSpan))
	}

	body := &Block{Stmts: stmts, Span: classSpan}
	fnExpr := NewFuncExpr(nil, nil, params, nil, nil, false, body, classSpan)
	return &ConstructorElem{
		Fn:       fnExpr,
		Receiver: &MethodReceiver{Mut: true, Span_: classSpan},
		Private:  false,
		Span_:    classSpan,
	}, nil
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
// anything else is excluded because a getter is a call this package cannot tell from a
// field, which `declare val keys: {get k(&self) -> unique symbol}` makes `[keys.k]`.
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

// keyReadNames returns the names a stable key expression reads. A parameter cannot bind
// one of them, since the constructor reads the key after binding its parameters and a
// parameter of that name would shadow what the key meant where the class was defined.
//
// The shapes are stableKeyExpr's: a variable read names itself, and `Symbol.<name>`
// names `Symbol`.
func keyReadNames(decl *ClassDecl) set.Set[string] {
	names := set.NewSet[string]()
	for _, bodyElem := range decl.Body {
		field, ok := bodyElem.(*FieldElem)
		if !ok || !takesConstructorParam(field) {
			continue
		}
		key, computed := field.Name.(*ComputedKey)
		if !computed || !stableKeyExpr(key.Expr) {
			continue
		}
		if ident, isIdent := key.Expr.(*IdentExpr); isIdent {
			names.Add(ident.Name)
			continue
		}
		// stableKeyExpr admits only `Symbol.<name>` besides a variable read.
		names.Add(symbolGlobalName)
	}
	return names
}

// takesConstructorParam reports whether a field's value arrives through a constructor
// parameter. A static field belongs to the class rather than the instance. An optional
// field defaults to `undefined`, and a parameter would force every caller to pass one.
func takesConstructorParam(field *FieldElem) bool {
	return !field.Static && !field.Optional
}

// directParamName returns the parameter name a key binds under, and reports whether it
// binds one at all. A key binds its own spelling, so `x: number` arrives in a parameter
// named `x`. Four kinds bind none: a number, a computed expression, a name
// canBindParamName rejects, and a name in keyReads. Such a field is assigned by index
// under a generated name.
//
// keyReads holds the names this class's computed keys read. Binding one would shadow it
// for the rest of the constructor, so `class C { k: number, [k]: string }` would store
// the second field under the first field's value.
//
// An identifier key and a string key are held to the same test, since
// `class C { class: … }` and `class C { "class": … }` name the same field.
func directParamName(key ObjKey, keyReads set.Set[string]) (string, bool) {
	var name string
	switch k := key.(type) {
	case *IdentExpr:
		name = k.Name
	case *StrLit:
		name = k.Value
	default:
		return "", false
	}
	if !canBindParamName(name) || keyReads.Contains(name) {
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
