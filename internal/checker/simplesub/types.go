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
// fewer.
type Record struct{ fields map[string]SimpleType }

func (*Variable) isSimpleType()  {}
func (*Primitive) isSimpleType() {}
func (*Literal) isSimpleType()   {}
func (*Function) isSimpleType()  {}
func (*Tuple) isSimpleType()     {}
func (*Record) isSimpleType()    {}

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
	default:
		return 0
	}
}
