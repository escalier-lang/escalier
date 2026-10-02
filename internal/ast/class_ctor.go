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
// `self`. A class with `class Point { x: number, y: number }` gets
// `constructor(&mut self, x: number, y: number) { self.x = x; self.y = y }`.
//
// The element is nil for a class that already declares a constructor, for a
// `declare class`, and for a subclass, none of which get one. A subclass is excluded
// because the synthesized body would have to forward to `super(…)`, so the checkers
// require it to declare its own.
//
// The second return names the field that blocked synthesis, and is non-nil only for a
// non-static field with a computed key. No parameter name can be derived from an
// arbitrary key expression, so the element is nil and the field is returned instead. A
// caller that reports diagnostics says the class needs an explicit constructor, and a
// caller that only emits code leaves the constructor out.
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

	// `&mut self` is synthesized at the class-name span so any diagnostic produced
	// against the synthesized constructor lands on the header.
	selfPat := NewIdentPat(selfParamName, true /* mutable */, nil, nil, classSpan)
	selfParam := &Param{Pattern: selfPat, TypeAnn: nil, Optional: false}
	params := []*Param{selfParam}
	stmts := []Stmt{}

	// Collect the names the fields bind directly before building any parameter, so a
	// generated name can avoid them. `class C { _field1: number, "a-b": string }` would
	// otherwise bind `_field1` twice, which is a SyntaxError, and assign the first
	// field's value to the second.
	taken := set.NewSet[string]()
	for _, bodyElem := range decl.Body {
		if field, ok := bodyElem.(*FieldElem); ok && takesConstructorParam(field) {
			if name, direct := directParamName(field.Name); direct {
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

		// paramName is the parameter the field's value arrives in, and lhs is the
		// assignment target on `self`.
		var paramName string
		var lhs Expr
		selfRef := NewIdent(selfParamName, fieldSpan)

		if name, direct := directParamName(field.Name); direct {
			paramName = name
			lhs = NewMember(selfRef, NewIdentifier(name, fieldSpan), false, fieldSpan)
		} else {
			// A key that cannot also be a binding name takes a generated parameter and
			// is assigned by index.
			var keyExpr Expr
			switch k := field.Name.(type) {
			case *IdentExpr:
				// Reached by a key spelled like a reserved word or like the receiver.
				// Such a key is a valid property name, so only the parameter it would
				// have bound is renamed.
				keyExpr = NewLitExpr(NewString(k.Name, fieldSpan))
			case *StrLit:
				keyExpr = NewLitExpr(NewString(k.Value, fieldSpan))
			case *NumLit:
				keyExpr = NewLitExpr(NewNumber(k.Value, fieldSpan))
			case *ComputedKey:
				// A parameter name cannot be derived from an arbitrary key expression,
				// so synthesis stops rather than producing a constructor that
				// initializes only some of the fields.
				return nil, field
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

// takesConstructorParam reports whether a field's value arrives through a constructor
// parameter. A static field belongs to the class rather than to the instance, so it
// takes none. An optional field defaults to `undefined` and may be assigned later, and
// giving it a parameter would force every caller to pass one.
func takesConstructorParam(field *FieldElem) bool {
	return !field.Static && !field.Optional
}

// directParamName returns the parameter name a key can bind under, and reports whether
// it can bind one at all. A key binds its own spelling when the constructor can bind
// that spelling, so `x: number` arrives in a parameter named `x`. A key that is a number
// or a computed expression cannot, and neither can one spelled like a word JavaScript
// reserves or like the receiver. Such a field is assigned by index under a generated
// name.
//
// An identifier key and a string key are held to the same test. `class C { class: … }`
// and `class C { "class": … }` name the same field, so accepting one spelling and
// rejecting the other would emit `const class = …` for the first.
func directParamName(key ObjKey) (string, bool) {
	var name string
	switch k := key.(type) {
	case *IdentExpr:
		name = k.Name
	case *StrLit:
		name = k.Value
	default:
		return "", false
	}
	if !canBindParamName(name) {
		return "", false
	}
	return name, true
}

// canBindParamName reports whether the synthesized constructor can bind a parameter
// under this name. It accepts any Unicode letter, a digit, `_` and `$`, with the first
// character constrained to a letter, `_`, or `$`.
//
// A word JavaScript reserves is rejected, since the constructor binds each parameter
// with `const <name> = …`. `class C { "class": number }` would otherwise emit
// `const class = temp1`, which is a SyntaxError.
//
// The receiver's name is rejected for a different reason. A parameter spelled `self`
// would shadow the receiver, and codegen lowers every `self` to `this`. The assignment
// in `class C { self: number }` would then read the instance rather than the argument.
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
// Emitted output is a module and so runs in strict mode, which is why the words
// reserved only there are in the set, `let` and `static` among them. `arguments` and
// `eval` are not reserved words and still cannot be bound in strict mode. `arguments`
// also names the object an emitted overload dispatch reads.
var jsBindingKeywords = set.FromSlice([]string{
	"arguments", "await", "break", "case", "catch", "class", "const", "continue",
	"debugger", "default", "delete", "do", "else", "enum", "eval", "export", "extends",
	"false", "finally", "for", "function", "if", "implements", "import", "in",
	"instanceof", "interface", "let", "new", "null", "package", "private", "protected",
	"public", "return", "static", "super", "switch", "this", "throw", "true", "try",
	"typeof", "var", "void", "while", "with", "yield",
})
