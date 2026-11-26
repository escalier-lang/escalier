package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// placeholder is one `_` a declaration's signature writes.
type placeholder struct {
	ann *ast.WildcardTypeAnn
	// param is the index of the parameter whose type writes ann, or -1 when another part of
	// the signature writes it.
	param int
	// inReturn reports that the return type writes ann.
	inReturn bool
}

// signaturePlaceholders sorts the `_` sig writes into those that stand for its error type
// and the rest.
//
// The error type is the type a `throws` clause writes, or the error parameter of a returned
// `Promise`, `Generator` or `AsyncGenerator`. Every `_` written anywhere in it stands for
// part of it, as in `throws _`, `throws &'a _` or `throws Array<_>`. Every other `_` in sig
// is misplaced.
func signaturePlaceholders(sig *ast.FuncSig) (errorTypes, misplaced []placeholder) {
	gather := func(into *[]placeholder, ta ast.TypeAnn, param int, inReturn bool) {
		if ta == nil {
			return
		}
		col := &placeholderCollector{}
		ta.Accept(col)
		for _, w := range col.found {
			*into = append(*into, placeholder{ann: w, param: param, inReturn: inReturn})
		}
	}
	collect := func(ta ast.TypeAnn, param int, inReturn bool) { gather(&misplaced, ta, param, inReturn) }
	errorSlot := func(ta ast.TypeAnn, inReturn bool) { gather(&errorTypes, ta, -1, inReturn) }
	for _, tp := range sig.TypeParams {
		collect(tp.LowerBound, -1, false)
		collect(tp.UpperBound, -1, false)
		collect(tp.Default, -1, false)
	}
	for i, p := range sig.Params {
		collect(p.TypeAnn, i, false)
	}
	if ref, ok := sig.Return.(*ast.TypeRefTypeAnn); ok {
		errIndex := -1
		switch ast.QualIdentToString(ref.Name) {
		case "Promise":
			errIndex = 1
		case "Generator", "AsyncGenerator":
			errIndex = 3
		}
		for i, arg := range ref.TypeArgs {
			if i == errIndex {
				errorSlot(arg, true)
			} else {
				collect(arg, -1, true)
			}
		}
	} else {
		collect(sig.Return, -1, true)
	}
	if sig.Throws != nil {
		errorSlot(sig.Throws, false)
	}
	return errorTypes, misplaced
}

// placeholderCollector gathers each `_` a type annotation writes, in source order.
type placeholderCollector struct {
	ast.DefaultVisitor
	found []*ast.WildcardTypeAnn
}

func (v *placeholderCollector) EnterTypeAnn(t ast.TypeAnn) bool {
	if w, ok := t.(*ast.WildcardTypeAnn); ok {
		v.found = append(v.found, w)
	}
	return true
}

// checkPlaceholders reports the `_` a declaration's signature writes outside its error type,
// and each error type `_` that inferred a type holding a lifetime. name and site are as
// checkSignature takes them. rendered returns the parts of the signature the checker inferred
// for the declaration, rendered as PrintWritten renders them, and false when it recorded none.
func (c *checker) checkPlaceholders(
	sig *ast.FuncSig,
	name string,
	site signatureSite,
	rendered func() (soltype.FuncSignature, bool),
) {
	errorTypes, misplaced := signaturePlaceholders(sig)
	for _, p := range errorTypes {
		t, ok := c.placeholderTypes[p.ann]
		if !ok {
			continue
		}
		if shown := coalesce(t, soltype.Positive); holdsLifetime(shown) {
			c.report(&ErrorTypePlaceholderLifetimeError{Ann: p.ann, Inferred: soltype.Print(shown)})
		}
	}
	if len(misplaced) == 0 {
		return
	}
	// One error per part of the signature, blamed at the first `_` the part writes.
	parts, known := rendered()
	reported := set.NewSet[int]()
	for _, p := range misplaced {
		part := p.param
		if part < 0 && p.inReturn {
			part = -2
		}
		if reported.Contains(part) {
			continue
		}
		reported.Add(part)
		err := &MissingSignatureError{Name: name, Site: site, Node: p.ann, Placeholder: true}
		switch {
		case p.param >= 0:
			written, _ := printer.Print(sig.Params[p.param].Pattern, printer.DefaultOptions())
			err.Params = []string{written}
			if known && p.param < len(parts.Params) {
				err.Suggestion = written + ": " + parts.Params[p.param]
			}
		case p.inReturn:
			err.MissingReturn = true
			if known {
				err.Suggestion = "-> " + parts.Ret
			}
		}
		c.report(err)
	}
}

// holdsLifetime reports whether t writes a borrow, a lifetime argument, or a function type's
// lifetime.
func holdsLifetime(t soltype.Type) bool {
	finder := &lifetimeHolderFinder{}
	t.Accept(finder, soltype.Positive)
	return finder.found
}

// lifetimeHolderFinder records whether a type writes a borrow, a lifetime argument, or a
// function type's lifetime.
type lifetimeHolderFinder struct {
	found bool
}

func (v *lifetimeHolderFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.RefType:
		v.found = true
	case *soltype.ClassType:
		v.found = v.found || t.Lt != nil || len(t.LifetimeArgs) > 0
	case *soltype.AliasType:
		v.found = v.found || len(t.LifetimeArgs) > 0
	case *soltype.FuncType:
		v.found = v.found || t.Lt != nil
	}
	return soltype.EnterResult{}
}

func (v *lifetimeHolderFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// ErrorTypePlaceholderLifetimeError reports a `_` standing for a declaration's error type
// that inferred a type holding a lifetime. An inferred error type is owned, so a borrowed
// error writes its borrow and lifetime around `_`. Ann is the `_`, which the error blames,
// and Inferred is the type it inferred, rendered.
type ErrorTypePlaceholderLifetimeError struct {
	Ann      ast.Node
	Inferred string
}

func (*ErrorTypePlaceholderLifetimeError) isSolverError()        {}
func (e *ErrorTypePlaceholderLifetimeError) Span() ast.Span      { return e.Ann.Span() }
func (e *ErrorTypePlaceholderLifetimeError) Related() []ast.Span { return nil }
func (e *ErrorTypePlaceholderLifetimeError) Message() string {
	return "`_` in the error type infers `" + e.Inferred + "`, which holds a lifetime; " +
		"write the type, or write its borrow and lifetime around `_`, as in `&'a _`"
}
