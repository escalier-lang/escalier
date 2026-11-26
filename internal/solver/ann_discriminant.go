package solver

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Discriminant is the runtime test of a narrowing annotation that names an object type, or
// a union of object types, at the root of a conditional form. The arm `x: &B => …` over a
// `{kind: "a", a: number} | {kind: "b", b: string}` scrutinee tests `value.kind === "b"`.
type Discriminant struct {
	// Always is set when every value of the scrutinee fits the annotation, so the test
	// always passes and Key and Lits are unset.
	Always bool
	// Key is the property the test reads. It and Lits are unset when the only values to
	// reject are `null` and `undefined`, which NullCheck rejects alone.
	Key string
	// Lits holds the literals the property holds in a value of the annotated type. A value
	// passes when its property equals one of them.
	Lits []*soltype.LitType
	// NullCheck is set when the scrutinee also holds `null` or `undefined`. The test then
	// checks `value != null` before it reads Key, since reading a property of either throws.
	NullCheck bool
}

// recordAnnDiscriminant works out the Discriminant of the narrowing annotation ann when the
// root scrutinee has type shape, and records it in Info for ann. It reports an
// AnnotationTestError when ann names an object type and no Discriminant tells it apart
// from the scrutinee's other members. Codegen emits one test per annotation, so a caller
// passes the same shape for every test of one annotation.
//
// It decides each annotation once. A later call for the same ann records and reports
// nothing.
//
// It records nothing for an annotation that names no object type, and nothing for a
// scrutinee that annDiscriminant cannot list the members of.
func (c *checker) recordAnnDiscriminant(scope *Scope, lvl int, shape soltype.Type, ann ast.TypeAnn) {
	if c.info.annTested.Contains(ann) {
		return
	}
	// A probe that discards this walk also discards the diagnostic reported below, so it
	// forgets the decision too and the walk that keeps its results decides again.
	snapshotMapEntry(c, c.info.annTested, ann)
	snapshotMapEntry(c, c.info.annDiscriminants, ann)
	c.info.annTested.Add(ann)
	// The typing walk resolves the annotation again when it binds the arm's name, and reports
	// any fault there, so resolving it here runs under a discarded probe.
	p := c.openProbe()
	annT, d, verdict := c.annDiscriminant(scope, lvl, shape, ann)
	c.closeProbe(p, false)
	switch verdict {
	case discriminantFound:
		c.info.annDiscriminants[ann] = d
	case discriminantMissing:
		c.report(&AnnotationTestError{Ann: ann, Type: annT})
	}
}

// discriminantVerdict is what annDiscriminant concluded about one annotation.
type discriminantVerdict int

const (
	// discriminantSkipped means the question does not apply. The annotation names no object
	// type, or the scrutinee holds values annDiscriminant cannot list.
	discriminantSkipped discriminantVerdict = iota
	// discriminantFound means a Discriminant tells the annotation's values apart.
	discriminantFound
	// discriminantMissing means the annotation names an object type and no Discriminant
	// tells its values apart from the scrutinee's other members.
	discriminantMissing
)

// annDiscriminant returns the type ann resolves to and the Discriminant of ann over a
// scrutinee of type shape. The verdict says whether one was found.
//
// The scrutinee's members are listed after expanding aliases and peeling borrows. A member
// ann admits needs no test. Every other member falls in one of these groups:
//
//   - `null` and `undefined`, which the test rejects with `value != null`.
//   - An object type or a class instance. Its values are rejected when it declares the
//     tested property as required with literal types, none of which ann's literals hold.
//   - A primitive or a primitive literal. A property read off one never yields a literal,
//     except that a string's `length` and its integer indices do.
//
// Any other member, such as `unknown`, a function, `never`, or an unsolved variable, skips
// the question, since no property read is known to reject its values.
//
// The tested property is the first property of the first object type ann names that every
// object type ann names declares as required with literal types, and that rejects every
// member listed above.
func (c *checker) annDiscriminant(
	scope *Scope, lvl int, shape soltype.Type, ann ast.TypeAnn,
) (soltype.Type, Discriminant, discriminantVerdict) {
	annT, resolved := c.resolveTypeAnn(scope, ann, lvl)
	if !resolved || carrierIsVar(shape) {
		return nil, Discriminant{}, discriminantSkipped
	}
	annObjs, isObj := c.objectTypesOf(annT)
	if !isObj {
		return nil, Discriminant{}, discriminantSkipped
	}
	carrier := c.ctx.expandedCarrier(annT)
	var objs []*soltype.ObjectType
	var prims []soltype.Type
	nullCheck := false
	for _, member := range c.unionMembers(c.ctx.expandedCarrier(shape)) {
		member = c.ctx.expandedCarrier(member)
		switch member.(type) {
		case *soltype.NeverType, *soltype.TypeVarType, *soltype.ErrorType:
			// Every annotation admits these, so they are skipped before the admits check
			// below. A scrutinee still being inferred can snapshot as `never` or keep an
			// unsolved variable, though a caller may pass any value.
			return nil, Discriminant{}, discriminantSkipped
		}
		if c.typeAdmits(carrier, member) {
			continue
		}
		switch m := member.(type) {
		case *soltype.NullType, *soltype.UndefinedType:
			nullCheck = true
		case *soltype.ObjectType:
			objs = append(objs, m)
		case *soltype.ClassType:
			body, ok := c.ctx.projectClassBody(m)
			if !ok {
				return nil, Discriminant{}, discriminantSkipped
			}
			objs = append(objs, body)
		case *soltype.PrimType, *soltype.LitType:
			prims = append(prims, m)
		default:
			return nil, Discriminant{}, discriminantSkipped
		}
	}
	if len(objs) == 0 && len(prims) == 0 {
		return annT, Discriminant{Always: !nullCheck, NullCheck: nullCheck}, discriminantFound
	}
	for _, elem := range annObjs[0].Elems {
		prop, isProp := elem.(*soltype.PropertyElem)
		if !isProp {
			continue
		}
		lits, isLits := c.annPropLits(annObjs, prop.Name)
		if !isLits || !c.litsExclude(objs, prop.Name, lits) || !primsReadNoLiteral(prims, prop.Name) {
			continue
		}
		return annT, Discriminant{Key: prop.Name, Lits: lits, NullCheck: nullCheck}, discriminantFound
	}
	return annT, Discriminant{}, discriminantMissing
}

// objectTypesOf returns the object types t names, with each alias and borrow peeled. It
// reports false unless t is an object type or a union whose every member is one.
func (c *checker) objectTypesOf(t soltype.Type) ([]*soltype.ObjectType, bool) {
	var objs []*soltype.ObjectType
	for _, member := range c.unionMembers(c.ctx.expandedCarrier(t)) {
		obj, isObj := c.ctx.expandedCarrier(member).(*soltype.ObjectType)
		if !isObj {
			return nil, false
		}
		objs = append(objs, obj)
	}
	return objs, len(objs) > 0
}

// annPropLits returns the literals the property name holds across objs, without repeats.
// It reports false when some object leaves the property out, marks it optional, or gives it
// a type other than a literal or a union of literals.
func (c *checker) annPropLits(objs []*soltype.ObjectType, name string) ([]*soltype.LitType, bool) {
	var lits []*soltype.LitType
	for _, obj := range objs {
		propLits, ok := c.propLits(obj, name)
		if !ok {
			return nil, false
		}
		for _, lit := range propLits {
			if !containsLit(lits, lit) {
				lits = append(lits, lit)
			}
		}
	}
	return lits, true
}

// litsExclude reports whether every object in objs declares the property name with literal
// types that hold none of lits.
func (c *checker) litsExclude(objs []*soltype.ObjectType, name string, lits []*soltype.LitType) bool {
	for _, obj := range objs {
		propLits, ok := c.propLits(obj, name)
		if !ok {
			return false
		}
		for _, lit := range propLits {
			if containsLit(lits, lit) {
				return false
			}
		}
	}
	return true
}

// propLits returns the literals the required property name of obj can hold. It reports
// false when obj lacks the property, marks it optional, or gives it a type other than a
// literal or a union of literals. A soltype literal is a string, number, or boolean.
func (c *checker) propLits(obj *soltype.ObjectType, name string) ([]*soltype.LitType, bool) {
	prop, found := obj.Prop(name)
	if !found || prop.Optional {
		return nil, false
	}
	var lits []*soltype.LitType
	for _, member := range c.unionMembers(prop.Type) {
		lit, isLit := member.(*soltype.LitType)
		if !isLit {
			return nil, false
		}
		lits = append(lits, lit)
	}
	return lits, len(lits) > 0
}

// primsReadNoLiteral reports whether reading the property name off a value of each type in
// prims yields no string, number, or boolean literal. Every type in prims is a PrimType or
// a LitType. The methods a primitive's prototype holds are functions, so only these reads
// can yield a literal:
//
//   - a string's `length` and integer indices, as `"ab".length` is `2` and `"ab"[0]` is `"a"`,
//   - a symbol's `description`, as `Symbol("x").description` is `"x"`.
func primsReadNoLiteral(prims []soltype.Type, name string) bool {
	_, indexErr := strconv.ParseUint(name, 10, 64)
	stringRead := name == "length" || indexErr == nil
	for _, t := range prims {
		switch primKind(t) {
		case soltype.StrPrim:
			if stringRead {
				return false
			}
		case soltype.SymPrim:
			if name == "description" {
				return false
			}
		}
	}
	return true
}

// primKind returns the primitive t is or is a literal of. t is a PrimType or a LitType, and
// a soltype literal is a string, number, or boolean.
func primKind(t soltype.Type) soltype.Prim {
	if p, isPrim := t.(*soltype.PrimType); isPrim {
		return p.Prim
	}
	switch t.(*soltype.LitType).Lit.(type) {
	case *soltype.StrLit:
		return soltype.StrPrim
	case *soltype.BoolLit:
		return soltype.BoolPrim
	}
	return soltype.NumPrim
}

// containsLit reports whether lits holds a literal equal to lit.
func containsLit(lits []*soltype.LitType, lit *soltype.LitType) bool {
	for _, l := range lits {
		if l.Equal(lit) {
			return true
		}
	}
	return false
}
