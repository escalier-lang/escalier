package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// method builds a single-signature MethodElem for the tests.
func method(name string, sig *FuncType) *MethodElem {
	return &MethodElem{Name: name, Signatures: []*FuncType{sig}}
}

// TestPrintClassType renders a nominal instance under its display name, with a
// `<...>` type-argument list when it has arguments. The qualified Name carries a
// namespace prefix for registry keying, which the printer strips for display. The
// Final flag does not change the rendering.
func TestPrintClassType(t *testing.T) {
	tests := []struct {
		name string
		in   Type
		want string
	}{
		{"monomorphic instance", &ClassType{Name: "Point"}, "Point"},
		{"generic instance", &ClassType{Name: "Box", Args: []Type{numP()}}, "Box<number>"},
		{
			"two type arguments",
			&ClassType{Name: "Map", Args: []Type{strP(), numP()}},
			"Map<string, number>",
		},
		{
			"qualified name strips the namespace prefix",
			&ClassType{Name: "Geometry.Point"},
			"Point",
		},
		{
			"final class renders the same as a non-final one",
			&ClassType{Name: "Point", Final: true},
			"Point",
		},
		{
			// A ClassType is an atom, so it needs no parens inside a union.
			"instance in a union needs no parens",
			&UnionType{Types: []Type{&ClassType{Name: "Point"}, strP()}},
			"Point | string",
		},
		{
			// A `mut 'static Point` borrow wraps the ClassType in a RefType, which
			// carries the `&`/`mut` prefix. The ClassType arm itself renders no lifetime.
			"borrowed instance renders via the RefType wrapper",
			&RefType{Mut: true, Lt: &StaticLifetime{}, Inner: &ClassType{Name: "Point"}},
			"&'static mut Point",
		},
		{
			"owned-mutable instance renders bare mut",
			&RefType{Mut: true, Inner: &ClassType{Name: "Box", Args: []Type{numP()}}},
			"mut Box<number>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in))
		})
	}
}

// TestPrintObjectMembers renders the method, getter, and setter members an object
// carries once M5 adds them to the element set. A method renders one entry per
// overload arm, arms joined by "; ".
func TestPrintObjectMembers(t *testing.T) {
	tests := []struct {
		name string
		in   Type
		want string
	}{
		{
			"monomorphic method",
			&ObjectType{Elems: []ObjTypeElem{
				method("foo", &FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()}),
			}},
			"{foo(x: number) -> string}",
		},
		{
			"getter",
			&ObjectType{Elems: []ObjTypeElem{&GetterElem{Name: "x", Type: numP()}}},
			"{get x() -> number}",
		},
		{
			"setter",
			&ObjectType{Elems: []ObjTypeElem{&SetterElem{Name: "x", Param: numP()}}},
			"{set x(value: number)}",
		},
		{
			"method, getter, and setter together",
			&ObjectType{Elems: []ObjTypeElem{
				method("greet", &FuncType{Ret: strP()}),
				&GetterElem{Name: "size", Type: numP()},
				&SetterElem{Name: "size", Param: numP()},
			}},
			"{greet() -> string, get size() -> number, set size(value: number)}",
		},
		{
			// An overloaded method renders each arm, joined by "; " so the arm boundary
			// stays distinct from the ", " between sibling members.
			"overloaded method joins arms with a semicolon",
			&ObjectType{Elems: []ObjTypeElem{&MethodElem{Name: "f", Signatures: []*FuncType{
				{Params: []*FuncParam{identP("x", numP())}, Ret: numP()},
				{Params: []*FuncParam{identP("x", strP())}, Ret: strP()},
			}}}},
			"{f(x: number) -> number; f(x: string) -> string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in))
		})
	}
}

// A no-op rewrite over a ClassType keeps its pointer (copy-on-write).
func TestAcceptClassIdentityPreservation(t *testing.T) {
	cls := &ClassType{Name: "Box", Args: []Type{numP()}, Final: true}
	require.Same(t, cls, cls.Accept(identityVisitor{}, Positive), "an unchanged ClassType keeps its pointer")
}

// Rewriting a type argument rebuilds the ClassType, carries the Name/Final/Lt
// identity through, and walks the arguments covariantly.
func TestAcceptClassCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	lt := &StaticLifetime{}
	cls := &ClassType{Name: "Box", Args: []Type{a}, Lt: lt, Final: true}

	got := cls.Accept(&replaceVar{target: a, repl: str}, Positive).(*ClassType)

	require.NotSame(t, cls, got, "a changed argument forces a new ClassType")
	require.Equal(t, "Box", got.Name, "the name carries through")
	require.True(t, got.Final, "the Final flag carries through")
	require.Same(t, lt, got.Lt, "the lifetime carries through unchanged")
	require.Same(t, str, got.Args[0], "the changed argument took the replacement")
}

// Type arguments are covariant: a ClassType's argument is visited in the same
// polarity the ClassType is.
func TestAcceptClassArgsCovariant(t *testing.T) {
	a := &TypeVarType{ID: 1}
	cls := &ClassType{Name: "Box", Args: []Type{a}}

	r := &recorder{seen: map[Type]Polarity{}}
	cls.Accept(r, Negative)

	require.Equal(t, Negative, r.seen[cls], "the ClassType keeps the start polarity")
	require.Equal(t, Negative, r.seen[a], "a type argument is covariant")
}

// A no-op rewrite over an object carrying a method, getter, and setter keeps every
// pointer (copy-on-write): a method's signature, the getter's type, and the
// setter's param are all walked, yet nothing changes.
func TestAcceptObjectMembersIdentityPreservation(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{
		method("f", &FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()}),
		&GetterElem{Name: "g", Type: numP()},
		&SetterElem{Name: "s", Param: numP()},
	}}
	require.Same(t, obj, obj.Accept(identityVisitor{}, Positive), "an unchanged object keeps its pointer")
}

// A setter's param is in write position, so it is visited in the FLIPPED polarity,
// while a getter's type and a property's type are read covariantly. A method's
// signature threads through FuncType.Accept, which flips its own parameters.
func TestAcceptObjectMemberVariance(t *testing.T) {
	getterT := &TypeVarType{ID: 1}
	setterT := &TypeVarType{ID: 2}
	methodParamT := &TypeVarType{ID: 3}
	methodRetT := &TypeVarType{ID: 4}
	obj := &ObjectType{Elems: []ObjTypeElem{
		&GetterElem{Name: "g", Type: getterT},
		&SetterElem{Name: "s", Param: setterT},
		method("m", &FuncType{Params: []*FuncParam{identP("x", methodParamT)}, Ret: methodRetT}),
	}}

	r := &recorder{seen: map[Type]Polarity{}}
	obj.Accept(r, Positive)

	require.Equal(t, Positive, r.seen[getterT], "a getter's type is covariant")
	require.Equal(t, Negative, r.seen[setterT], "a setter's param is contravariant")
	require.Equal(t, Negative, r.seen[methodParamT], "a method parameter is contravariant")
	require.Equal(t, Positive, r.seen[methodRetT], "a method return is covariant")
}

// Rewriting a variable inside a setter rebuilds only that element, leaving the
// object's other members untouched (copy-on-write).
func TestAcceptObjectSetterCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	prop := &PropertyElem{Name: "p", Type: numP()}
	setter := &SetterElem{Name: "s", Param: a}
	obj := &ObjectType{Elems: []ObjTypeElem{prop, setter}}

	got := obj.Accept(&replaceVar{target: a, repl: str}, Positive).(*ObjectType)

	require.NotSame(t, obj, got, "a changed member forces a new object")
	require.Same(t, prop, got.Elems[0], "the unchanged property keeps its pointer")
	gotSetter := got.Elems[1].(*SetterElem)
	require.NotSame(t, setter, gotSetter, "the changed setter is a fresh element")
	require.Same(t, str, gotSetter.Param, "the setter param took the replacement")
}

// LevelOf on a ClassType is the max level over its type arguments; the Name and
// Final identity carry no variables.
func TestLevelOfClassType(t *testing.T) {
	require.Equal(t, 0, LevelOf(&ClassType{Name: "Point"}), "no arguments ⇒ level 0")
	cls := &ClassType{Name: "Box", Args: []Type{
		&TypeVarType{ID: 1, Level: 2},
		&TypeVarType{ID: 2, Level: 5},
	}}
	require.Equal(t, 5, LevelOf(cls), "the level is the max over the type arguments")
}

// LevelOf on an object descends into every member kind: a method's signatures, a
// getter's type, and a setter's param.
func TestLevelOfObjectMembers(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{
		method("m", &FuncType{Params: []*FuncParam{identP("x", &TypeVarType{ID: 1, Level: 3})}, Ret: numP()}),
		&GetterElem{Name: "g", Type: &TypeVarType{ID: 2, Level: 7}},
		&SetterElem{Name: "s", Param: &TypeVarType{ID: 3, Level: 4}},
	}}
	require.Equal(t, 7, LevelOf(obj), "the level is the max over all member types")
}

// freeTypeVars descends a ClassType's arguments, so a generic instance's arguments
// are named as quantified parameters by the scheme printer.
func TestFreeTypeVarsClassType(t *testing.T) {
	a := &TypeVarType{ID: 1, Level: 1}
	b := &TypeVarType{ID: 2, Level: 1}
	cls := &ClassType{Name: "Map", Args: []Type{a, b}}
	require.Equal(t, "<T0, T1> Map<T0, T1>", PrintAsScheme(cls))
}

// freeTypeVars descends every object member kind, so a method parameter, a getter
// type, and a setter param are each named as quantified parameters.
func TestFreeTypeVarsObjectMembers(t *testing.T) {
	a := &TypeVarType{ID: 1, Level: 1}
	b := &TypeVarType{ID: 2, Level: 1}
	c := &TypeVarType{ID: 3, Level: 1}
	obj := &ObjectType{Elems: []ObjTypeElem{
		method("m", &FuncType{Params: []*FuncParam{identP("x", a)}, Ret: numP()}),
		&GetterElem{Name: "g", Type: b},
		&SetterElem{Name: "s", Param: c},
	}}
	require.Equal(t, "<T0, T1, T2> {m(x: T0) -> number, get g() -> T1, set s(value: T2)}", PrintAsScheme(obj))
}

// Member generalizes Prop across all element kinds, returning the element of any
// kind by name and reporting absence.
func TestObjectMember(t *testing.T) {
	m := method("m", &FuncType{Ret: numP()})
	g := &GetterElem{Name: "g", Type: numP()}
	obj := &ObjectType{Elems: []ObjTypeElem{
		&PropertyElem{Name: "p", Type: numP()},
		m,
		g,
	}}

	gotProp, ok := obj.Member("p")
	require.True(t, ok)
	_, isProp := gotProp.(*PropertyElem)
	require.True(t, isProp, "Member returns the property element")

	gotMethod, ok := obj.Member("m")
	require.True(t, ok)
	require.Same(t, m, gotMethod, "Member returns the method element")

	gotGetter, ok := obj.Member("g")
	require.True(t, ok)
	require.Same(t, g, gotGetter, "Member returns the getter element")

	_, ok = obj.Member("missing")
	require.False(t, ok, "an absent name reports not present")
}

// ObjElemName reads the name off any element kind.
func TestObjElemName(t *testing.T) {
	require.Equal(t, "p", ObjElemName(&PropertyElem{Name: "p"}))
	require.Equal(t, "m", ObjElemName(method("m", &FuncType{Ret: numP()})))
	require.Equal(t, "g", ObjElemName(&GetterElem{Name: "g"}))
	require.Equal(t, "s", ObjElemName(&SetterElem{Name: "s"}))
}
