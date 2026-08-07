package ucs

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// This file reads a source pattern into the two things a normalized branch is made of:
// the one tag-level test the branch makes, and the leaves it binds out of the value the
// branch matched. It is the half of normalization that looks at patterns. The half that
// merges branches into a split and threads the default tail through them consumes what
// this produces and lands next; see the PR3 section of
// planning/ucs/implementation_plan.md.

// bindSpec is one leaf a branch's pattern binds. It holds the name, the pattern leaf or
// shorthand element the solver binds through, and the projection the value comes from. A
// spec with no name holds a sub-pattern this stage does not flatten, which its branch
// keeps whole until nested patterns become splits of their own.
type bindSpec struct {
	name   string
	pat    ast.Pat
	elem   *ast.ObjShorthandPat
	source *Scrutinee
	origin Origin
}

// wrapBinds nests a branch's binds around its continuation, so the leaves are in scope
// for everything the branch runs, a guard condition included.
func wrapBinds(binds []bindSpec, cont Norm) Norm {
	for i := len(binds) - 1; i >= 0; i-- {
		bind := binds[i]
		cont = &NormBind{
			Name:   bind.name,
			Pat:    bind.pat,
			Elem:   bind.elem,
			Source: bind.source,
			Cont:   cont,
			Origin: bind.origin,
		}
	}
	return cont
}

// shallowTest reads one tag-level off a pattern: the tag its branch tests, and the
// leaves it binds out of the scrutinee. The test is nil for a catch-all, which binds
// without testing.
//
// It goes exactly one level deep. A sub-pattern that is an identifier becomes a bind on
// its projection, and any other sub-pattern becomes a nameless bind that keeps the
// sub-pattern whole. Turning those into splits over their projections is the stage of
// the rewrite that flattens nesting.
func shallowTest(p ast.Pat, scrutinee *Scrutinee, origin Origin) (Test, []bindSpec) {
	switch p := p.(type) {
	case nil, *ast.WildcardPat:
		// A wildcard binds without testing. A branch carrying no pattern at all is a
		// lowering bug rather than a form the surface can write, and treating it the
		// same way keeps the read total so the bug shows up as a printable IR.
		return nil, nil
	case *ast.IdentPat:
		// An identifier pattern binds the scrutinee itself rather than a projection of
		// it, so the bind's source is the scrutinee node.
		return nil, []bindSpec{{
			name:   p.Name,
			pat:    p,
			source: scrutinee,
			origin: leafOrigin(origin, p),
		}}
	case *ast.LitPat:
		return &LitTest{Lit: p.Lit}, nil
	case *ast.TuplePat:
		return tupleTest(p, scrutinee, origin)
	case *ast.ObjectPat:
		return objectTest(p, scrutinee, origin)
	case *ast.InstancePat:
		// An instance pattern tests a nominal tag and then reaches its fields the same
		// way a structural object does. The fields are not part of the tag, so the
		// object test objectTest derives is dropped and only its binds are kept.
		_, binds := objectTest(p.Object, scrutinee, origin)
		return &ClassTest{Name: p.ClassName}, binds
	case *ast.ExtractorPat:
		binds := make([]bindSpec, 0, len(p.Args))
		for i, arg := range p.Args {
			binds = appendBind(binds, arg, scrutinee, ExtractStep{Index: i}, origin)
		}
		return &ExtractorTest{Name: p.Name, Arity: len(p.Args)}, binds
	default:
		// A bare rest pattern is the only form left, and it is meaningful only inside a
		// tuple or an object. Keeping it whole as a nameless bind hands it to the
		// solver's pattern walk, which reports it, instead of dropping the names under
		// it here.
		return nil, []bindSpec{{pat: p, source: scrutinee, origin: leafOrigin(origin, p)}}
	}
}

// tupleTest derives the tag and binds of a tuple pattern. Len counts the fixed prefix,
// and a trailing rest relaxes the test to that prefix and binds the suffix past it.
//
// A rest anywhere but last names no suffix a SuffixStep can reach, as SuffixStep spells
// out, so the test relaxes to the prefix before it and the elements from the rest on
// bind nothing. Their leaves go unnamed by the IR, so the pass that lowers the surface
// has to reject the pattern before a consumer binds names off the split. It is the pass
// that can: it holds the source span a message points at, and the IR does not.
func tupleTest(p *ast.TuplePat, scrutinee *Scrutinee, origin Origin) (Test, []bindSpec) {
	fixed, rest := splitTrailingRest(p.Elems)
	binds := make([]bindSpec, 0, len(fixed)+1)
	for i, elem := range fixed {
		binds = appendBind(binds, elem, scrutinee, IndexStep{Index: i}, origin)
	}
	kind := NoRest
	if len(fixed) < len(p.Elems) {
		kind = TrailingRest
	}
	if rest != nil {
		binds = appendBind(binds, rest.Pattern, scrutinee, SuffixStep{From: len(fixed)}, origin)
	}
	return &TupleTest{Len: len(fixed), Rest: kind}, binds
}

// splitTrailingRest splits a tuple pattern's elements at its first rest. fixed holds
// the elements before it, which bind by position. rest is that element only when it is
// the pattern's last one, the only position whose suffix is expressible. It mirrors how
// the solver's pattern walk splits the same elements.
func splitTrailingRest(elems []ast.Pat) (fixed []ast.Pat, rest *ast.RestPat) {
	for i, elem := range elems {
		r, isRest := elem.(*ast.RestPat)
		if !isRest {
			continue
		}
		if i == len(elems)-1 {
			return elems[:i], r
		}
		return elems[:i], nil
	}
	return elems, nil
}

// objectTest derives the tag and binds of an object pattern. Keys are the fields the
// pattern names at this level in source order, and a field's own sub-pattern becomes a
// bind on the projected field rather than part of the tag.
//
// A rest binds the scrutinee with the keys named here removed. The key set it excludes
// is exactly those keys, so a field a deeper pattern matches is still in a shallower
// rest.
func objectTest(p *ast.ObjectPat, scrutinee *Scrutinee, origin Origin) (Test, []bindSpec) {
	if p == nil {
		return &ObjectTest{}, nil
	}
	objKeys := make([]ObjectKey, 0, len(p.Elems))
	binds := make([]bindSpec, 0, len(p.Elems))
	named := set.NewSet[string]()
	var rest *ast.ObjRestPat

	for i, elem := range p.Elems {
		switch e := elem.(type) {
		case *ast.ObjShorthandPat:
			// A default binds the field even when it is absent, so the test must not
			// demand it. The solver's pattern walk makes the same field optional.
			objKeys = append(objKeys, ObjectKey{Name: e.Key.Name, Optional: e.Default != nil})
			named.Add(e.Key.Name)
			// A shorthand element is not an ast.Pat, so it rides the bind's Elem field.
			// The solver reads the annotation, default, and `mut` marker off it, none of
			// which the name alone carries.
			leaf := leafOrigin(origin, e)
			binds = append(binds, bindSpec{
				name:   e.Key.Name,
				elem:   e,
				source: scrutinee.Project(FieldStep{Name: e.Key.Name}, leaf),
				origin: leaf,
			})
		case *ast.ObjKeyValuePat:
			objKeys = append(objKeys, ObjectKey{
				Name:     e.Key.Name,
				Optional: valueDefaultsField(e.Value),
			})
			named.Add(e.Key.Name)
			binds = appendBind(binds, e.Value, scrutinee, FieldStep{Name: e.Key.Name}, origin)
		case *ast.ObjRestPat:
			// One rest takes every property the pattern did not name, so a second has
			// nothing left, and only a last one holds the whole remainder. Any other
			// placement binds nothing here, the same way a non-trailing tuple rest does
			// and for the same reason, so the lowering pass has to reject it.
			if rest == nil && i == len(p.Elems)-1 {
				rest = e
			}
		}
	}

	kind := NoRest
	if rest != nil {
		kind = TrailingRest
		step := RemainderStep{Exclude: named}
		binds = appendBind(binds, rest.Pattern, scrutinee, step, origin)
	}
	return &ObjectTest{Keys: objKeys, Rest: kind}, binds
}

// valueDefaultsField reports whether an object pattern's value sub-pattern carries a
// default, as `{x: a = 0}` does, which makes the field optional the same way a
// shorthand default does.
func valueDefaultsField(p ast.Pat) bool {
	ident, ok := p.(*ast.IdentPat)
	return ok && ident.Default != nil
}

// appendBind adds what a sub-pattern contributes at the projection step reaches. A
// wildcard binds nothing and needs no test, so it adds nothing at all. An identifier
// binds the projection. Any other sub-pattern is kept whole as a nameless bind for the
// stage that flattens nesting.
func appendBind(binds []bindSpec, p ast.Pat, parent *Scrutinee, step Step, origin Origin) []bindSpec {
	if p == nil {
		return binds
	}
	if _, isWildcard := p.(*ast.WildcardPat); isWildcard {
		return binds
	}
	leaf := leafOrigin(origin, p)
	spec := bindSpec{pat: p, source: parent.Project(step, leaf), origin: leaf}
	if ident, ok := p.(*ast.IdentPat); ok {
		spec.name = ident.Name
	}
	return append(binds, spec)
}

// leafOrigin points a bind and its projection at the pattern leaf they came from, so a
// message about one blames the field the user wrote rather than the whole arm. It keeps
// the branch's kind, since the leaf lowered from the same surface construct, and falls
// back to the branch's origin when the leaf names no node.
func leafOrigin(branch Origin, leaf Spanned) Origin {
	if _, ok := SpanOf(leaf); !ok {
		return branch
	}
	return At(branch.Kind, leaf)
}
