package simplesub

// ---- SimpleType: the internal inference representation ----

type SimpleType interface{ isSimpleType() }

// Variable is an inference variable carrying Simple-sub lower/upper bounds and
// the level at which it was created (used for let-generalization).
type Variable struct {
	id          int
	level       int
	lowerBounds []SimpleType
	upperBounds []SimpleType
}

// boundsAt returns the bounds relevant to the given polarity: lower bounds in
// Positive position (the variable becomes their union), upper bounds in Negative
// position (the variable becomes their intersection).
func (v *Variable) boundsAt(pol Polarity) []SimpleType {
	if pol == Positive {
		return v.lowerBounds
	}
	return v.upperBounds
}

// Primitive is a base type: "number" | "string" | "boolean".
type Primitive struct{ name string }

// Literal is a literal type, e.g. "hello" or 5.
type Literal struct {
	kind string // "str" | "num" | "bool"
	str  string
	num  float64
	b    bool
}

// Function is a (possibly multi-argument) function type.
type Function struct {
	params     []SimpleType
	paramNames []string
	ret        SimpleType
}

// Tuple is a fixed-length tuple type.
type Tuple struct{ elems []SimpleType }

// Record is a structural record type with named fields. Fields are covariant,
// and width subtyping lets a record with MORE fields be a subtype of one with
// fewer. lt is the record's lifetime (the borrow it carries), or nil for a
// freshly-allocated record that borrows nothing.
type Record struct {
	fields map[string]SimpleType
	lt     Lifetime
}

// Mut is a mutable reference (cell) holding a value of type inner. It is
// INVARIANT in inner. Algebraic subtyping has no native invariance — it is all
// co/contravariance — so Mut is modeled by the standard read/write
// decomposition: the cell's content occurs both as a covariant "read" view and
// a contravariant "write" view, i.e. Mut{T} behaves like a structure
// {read: T (+), write: T (-)}. Subtyping two such structures forces T in both
// directions, which is exactly invariance. See constrain for where the two
// directions are emitted.
type Mut struct{ inner SimpleType }

// Void is the result type of a statement block with no value (e.g. a function
// body that only performs assignments).
type Void struct{}

// Alias is a reference to a named type alias (e.g. `Point` for
// `type Point = {x: number}`). Like Record it can carry a lifetime — a `mut`
// borrow of an alias-typed value gets one, rendering as `mut 'a Point`. The
// body is the alias's underlying type, used for structural subtyping; name is
// what prints.
type Alias struct {
	name string
	body SimpleType
	lt   Lifetime
}

func (*Variable) isSimpleType()  {}
func (*Primitive) isSimpleType() {}
func (*Literal) isSimpleType()   {}
func (*Function) isSimpleType()  {}
func (*Tuple) isSimpleType()     {}
func (*Record) isSimpleType()    {}
func (*Mut) isSimpleType()       {}
func (*Void) isSimpleType()      {}
func (*Alias) isSimpleType()     {}

func (l *Literal) eq(o *Literal) bool {
	if l.kind != o.kind {
		return false
	}
	switch l.kind {
	case "str":
		return l.str == o.str
	case "num":
		return l.num == o.num
	case "bool":
		return l.b == o.b
	}
	return false
}

// levelOf is the maximum level of any variable inside ty; concrete leaves are
// level 0. Used to decide generalization and extrusion.
func levelOf(ty SimpleType) int {
	switch t := ty.(type) {
	case *Variable:
		return t.level
	case *Function:
		m := 0
		for _, p := range t.params {
			m = max(m, levelOf(p))
		}
		return max(m, levelOf(t.ret))
	case *Tuple:
		m := 0
		for _, e := range t.elems {
			m = max(m, levelOf(e))
		}
		return m
	case *Record:
		m := 0
		for _, f := range t.fields {
			m = max(m, levelOf(f))
		}
		return m
	case *Mut:
		return levelOf(t.inner)
	case *Alias:
		return levelOf(t.body)
	case *ResidualOp:
		return levelOf(t.operand)
	default:
		// Primitive, Literal, Void: no nested variables.
		return 0
	}
}
