package ast

import (
	"fmt"
	"unicode"
)

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
// The second return is the field that blocked synthesis, which is non-nil only for a
// non-static field with a computed key. There is no parameter name to bind such a field
// to, so both returns are nil and a caller that reports diagnostics says the class needs
// an explicit constructor. A caller that only emits code leaves the constructor out.
//
// Both internal/checker and JavaScript emission call this, which is what makes them
// agree on what a class constructs. internal/checker installs the element in the class
// body during inference and internal/solver leaves the tree alone, so emission derives
// the element rather than reading one back.
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
	selfPat := NewIdentPat("self", true /* mutable */, nil, nil, classSpan)
	selfParam := &Param{Pattern: selfPat, TypeAnn: nil, Optional: false}
	params := []*Param{selfParam}
	stmts := []Stmt{}

	// tempCounter names a parameter for a field whose key is not a valid JS identifier,
	// such as `"foo-bar"`. The parameter is named `_field<N>` and the assignment reaches
	// the field by index rather than by property.
	tempCounter := 0

	for _, bodyElem := range decl.Body {
		field, ok := bodyElem.(*FieldElem)
		if !ok || field.Static {
			continue
		}
		// An optional field defaults to `undefined` and may be assigned later, so it
		// takes no parameter. Including it would force every caller to pass one.
		if field.Optional {
			continue
		}

		fieldSpan := field.Span()

		// paramName is the parameter the field's value arrives in, and lhs is the
		// assignment target on `self`.
		var paramName string
		var lhs Expr
		selfRef := NewIdent("self", fieldSpan)
		switch k := field.Name.(type) {
		case *IdentExpr:
			paramName = k.Name
			lhs = NewMember(selfRef, NewIdentifier(paramName, fieldSpan), false, fieldSpan)
		case *StrLit:
			if isValidJSIdentifier(k.Value) {
				paramName = k.Value
				lhs = NewMember(selfRef, NewIdentifier(paramName, fieldSpan), false, fieldSpan)
			} else {
				tempCounter++
				paramName = fmt.Sprintf("_field%d", tempCounter)
				keyExpr := NewLitExpr(NewString(k.Value, fieldSpan))
				lhs = NewIndex(selfRef, keyExpr, false, fieldSpan)
			}
		case *ComputedKey:
			// Synthesizing a parameter name for an arbitrary key expression would
			// silently drop the field, so synthesis stops rather than producing a
			// constructor that initializes only some of them.
			return nil, field
		default:
			panic("ImplicitConstructor: unexpected ObjKey variant")
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

// isValidJSIdentifier reports whether s can be used as a parameter name without
// quoting. It accepts any Unicode letter, a digit, `_` and `$`, with the first
// character constrained to a letter, `_`, or `$`.
func isValidJSIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
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
