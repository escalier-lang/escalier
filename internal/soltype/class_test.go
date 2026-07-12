package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// method builds a single-signature MethodElem for the tests.
func method(name string, sig *FuncType) *MethodElem {
	return &MethodElem{Name: name, Signatures: []*FuncType{sig}}
}

// selfRecv builds a self receiver whose desugared type is recv.
func selfRecv(recv Type) *FuncParam {
	return &FuncParam{Pattern: &IdentPat{Name: "self"}, Type: recv}
}

// TestPrintMethodSelfReceiver renders a method's self receiver as the Rust-style
// shorthand, read back from the desugared receiver type. Self renders `self`, mut
// Self renders `mut self`, &Self renders `&self`, and &mut Self renders `&mut self`.
// A method with no receiver is a static method and renders with no receiver.
func TestPrintMethodSelfReceiver(t *testing.T) {
	counter := func() Type { return &ClassType{Name: "Counter"} }
	tests := []struct {
		name string
		in   Type
		want string
	}{
		{
			"owned immutable self",
			&ObjectType{Elems: []ObjTypeElem{method("peek", &FuncType{SelfParam: selfRecv(counter()), Ret: numP()})}},
			"{peek(self) -> number}",
		},
		{
			"owned mutable mut self",
			&ObjectType{Elems: []ObjTypeElem{method("inc", &FuncType{SelfParam: selfRecv(&RefType{Mut: true, Inner: &ClassType{Name: "Counter"}}), Ret: &Void{}})}},
			"{inc(mut self) -> void}",
		},
		{
			"immutable borrow &self",
			&ObjectType{Elems: []ObjTypeElem{method("look", &FuncType{SelfParam: selfRecv(&RefType{Lt: Anon, Inner: &ClassType{Name: "Counter"}}), Ret: numP()})}},
			"{look(&self) -> number}",
		},
		{
			"mutable borrow &mut self",
			&ObjectType{Elems: []ObjTypeElem{method("edit", &FuncType{SelfParam: selfRecv(&RefType{Mut: true, Lt: Anon, Inner: &ClassType{Name: "Counter"}}), Ret: &Void{}})}},
			"{edit(&mut self) -> void}",
		},
		{
			"self followed by ordinary params",
			&ObjectType{Elems: []ObjTypeElem{method("add", &FuncType{SelfParam: selfRecv(counter()), Params: []*FuncParam{identP("x", numP())}, Ret: numP()})}},
			"{add(self, x: number) -> number}",
		},
		{
			"static method has no receiver",
			&ObjectType{Elems: []ObjTypeElem{method("make", &FuncType{Ret: &ClassType{Name: "Counter"}})}},
			"{make() -> Counter}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in))
		})
	}
}

// A borrowing method's receiver lifetime flows through Accept, so the scheme printer
// names it and renders the receiver as `&'a self`.
func TestPrintMethodSelfReceiverNamedLifetime(t *testing.T) {
	lv := &LifetimeVar{ID: 0, Level: 1}
	fn := &FuncType{SelfParam: selfRecv(&RefType{Lt: lv, Inner: &ClassType{Name: "Counter"}}), Ret: numP()}
	require.Equal(t, "fn <'a>(&'a self) -> number", PrintAsScheme(fn))
}

// A no-op rewrite over a method keeps its pointer, walking the self receiver yet
// leaving it unchanged.
func TestAcceptFuncSelfParamIdentity(t *testing.T) {
	fn := &FuncType{SelfParam: selfRecv(&ClassType{Name: "Counter"}), Ret: numP()}
	require.Same(t, fn, fn.Accept(identityVisitor{}, Positive), "an unchanged method keeps its pointer")
}

// Rewriting a variable inside the receiver type rebuilds the FuncType and its self
// receiver while carrying the other fields through.
func TestAcceptFuncSelfParamCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	fn := &FuncType{
		SelfParam: selfRecv(&ClassType{Name: "Box", TypeArgs: []Type{a}}),
		Params:    []*FuncParam{identP("x", numP())},
		Ret:       numP(),
	}

	got := fn.Accept(&replaceVar{target: a, repl: str}, Positive).(*FuncType)

	require.NotSame(t, fn, got, "a changed receiver forces a new FuncType")
	require.Same(t, str, got.SelfParam.Type.(*ClassType).TypeArgs[0], "the receiver argument took the replacement")
	require.Same(t, fn.Params[0], got.Params[0], "an unchanged param keeps its pointer")
}

// A method's self receiver is contravariant, like a parameter, so it is visited in
// the flipped polarity.
func TestAcceptFuncSelfParamContravariant(t *testing.T) {
	recv := &TypeVarType{ID: 7}
	fn := &FuncType{SelfParam: selfRecv(recv), Ret: numP()}

	r := &recorder{seen: map[Type]Polarity{}}
	fn.Accept(r, Positive)

	require.Equal(t, Negative, r.seen[recv], "the receiver is contravariant")
}

// LevelOf includes a method's self receiver, so a receiver-only variable lifts the
// method's level.
func TestLevelOfSelfParam(t *testing.T) {
	fn := &FuncType{SelfParam: selfRecv(&ClassType{Name: "Box", TypeArgs: []Type{&TypeVarType{ID: 1, Level: 6}}}), Ret: numP()}
	require.Equal(t, 6, LevelOf(fn))
}

// freeTypeVars descends a method's self receiver, so a variable appearing only in the
// receiver type is named as a quantified parameter.
func TestFreeTypeVarsSelfParam(t *testing.T) {
	a := &TypeVarType{ID: 1, Level: 1}
	fn := &FuncType{SelfParam: selfRecv(&ClassType{Name: "Box", TypeArgs: []Type{a}}), Ret: a}
	require.Equal(t, "fn <T0>(self) -> T0", PrintAsScheme(fn))
}

// An instance getter or setter renders its self receiver first, through the same
// shorthand as a method. A static getter or setter renders no receiver.
func TestPrintGetterSetterSelfReceiver(t *testing.T) {
	tests := []struct {
		name string
		in   Type
		want string
	}{
		{
			"instance getter",
			&ObjectType{Elems: []ObjTypeElem{&GetterElem{Name: "size", SelfParam: selfRecv(&ClassType{Name: "List"}), Type: numP()}}},
			"{get size(self) -> number}",
		},
		{
			"instance setter with mut self",
			&ObjectType{Elems: []ObjTypeElem{&SetterElem{Name: "size", SelfParam: selfRecv(&RefType{Mut: true, Inner: &ClassType{Name: "List"}}), Param: numP()}}},
			"{set size(mut self, value: number)}",
		},
		{
			"static getter has no receiver",
			&ObjectType{Elems: []ObjTypeElem{&GetterElem{Name: "size", Type: numP()}}},
			"{get size() -> number}",
		},
		{
			"static setter has no receiver",
			&ObjectType{Elems: []ObjTypeElem{&SetterElem{Name: "size", Param: numP()}}},
			"{set size(value: number)}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in))
		})
	}
}

// A getter's and setter's self receiver is contravariant, visited in the flipped
// polarity like a method receiver.
func TestAcceptGetterSetterReceiverContravariant(t *testing.T) {
	getterRecv := &TypeVarType{ID: 1}
	setterRecv := &TypeVarType{ID: 2}
	obj := &ObjectType{Elems: []ObjTypeElem{
		&GetterElem{Name: "g", SelfParam: selfRecv(getterRecv), Type: numP()},
		&SetterElem{Name: "s", SelfParam: selfRecv(setterRecv), Param: numP()},
	}}

	r := &recorder{seen: map[Type]Polarity{}}
	obj.Accept(r, Positive)

	require.Equal(t, Negative, r.seen[getterRecv], "a getter's receiver is contravariant")
	require.Equal(t, Negative, r.seen[setterRecv], "a setter's receiver is contravariant")
}

// Rewriting a variable inside an instance setter's receiver rebuilds the setter and
// carries the receiver through.
func TestAcceptSetterSelfParamCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	setter := &SetterElem{Name: "s", SelfParam: selfRecv(&ClassType{Name: "Box", TypeArgs: []Type{a}}), Param: numP()}
	obj := &ObjectType{Elems: []ObjTypeElem{setter}}

	got := obj.Accept(&replaceVar{target: a, repl: str}, Positive).(*ObjectType)

	gotSetter := got.Elems[0].(*SetterElem)
	require.NotSame(t, setter, gotSetter, "a changed receiver forces a new setter")
	require.Same(t, str, gotSetter.SelfParam.Type.(*ClassType).TypeArgs[0], "the receiver argument took the replacement")
}

// LevelOf descends an instance getter's receiver.
func TestLevelOfGetterReceiver(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{
		&GetterElem{Name: "g", SelfParam: selfRecv(&ClassType{Name: "Box", TypeArgs: []Type{&TypeVarType{ID: 1, Level: 8}}}), Type: numP()},
	}}
	require.Equal(t, 8, LevelOf(obj))
}

// freeTypeVars descends an instance getter's receiver, so a variable appearing only
// there is named as a quantified parameter even though the receiver prints as `self`.
func TestFreeTypeVarsGetterReceiver(t *testing.T) {
	a := &TypeVarType{ID: 1, Level: 1}
	obj := &ObjectType{Elems: []ObjTypeElem{
		&GetterElem{Name: "g", SelfParam: selfRecv(&ClassType{Name: "Box", TypeArgs: []Type{a}}), Type: numP()},
	}}
	require.Equal(t, "<T0> {get g(self) -> number}", PrintAsScheme(obj))
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
		{"generic instance", &ClassType{Name: "Box", TypeArgs: []Type{numP()}}, "Box<number>"},
		{
			"two type arguments",
			&ClassType{Name: "Map", TypeArgs: []Type{strP(), numP()}},
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
			&RefType{Mut: true, Inner: &ClassType{Name: "Box", TypeArgs: []Type{numP()}}},
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

// A no-op rewrite over a ClassType allocates nothing and keeps its pointer.
func TestAcceptClassIdentityPreservation(t *testing.T) {
	cls := &ClassType{Name: "Box", TypeArgs: []Type{numP()}, Final: true}
	require.Same(t, cls, cls.Accept(identityVisitor{}, Positive), "an unchanged ClassType keeps its pointer")
}

// Rewriting a type argument rebuilds the ClassType, carries the Name/Final/Lt
// identity through, and walks the arguments covariantly.
func TestAcceptClassCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	lt := &StaticLifetime{}
	cls := &ClassType{Name: "Box", TypeArgs: []Type{a}, Lt: lt, Final: true}

	got := cls.Accept(&replaceVar{target: a, repl: str}, Positive).(*ClassType)

	require.NotSame(t, cls, got, "a changed argument forces a new ClassType")
	require.Equal(t, "Box", got.Name, "the name carries through")
	require.True(t, got.Final, "the Final flag carries through")
	require.Same(t, lt, got.Lt, "the lifetime carries through unchanged")
	require.Same(t, str, got.TypeArgs[0], "the changed argument took the replacement")
}

// Type arguments are covariant: a ClassType's argument is visited in the same
// polarity the ClassType is.
func TestAcceptClassArgsCovariant(t *testing.T) {
	a := &TypeVarType{ID: 1}
	cls := &ClassType{Name: "Box", TypeArgs: []Type{a}}

	r := &recorder{seen: map[Type]Polarity{}}
	cls.Accept(r, Negative)

	require.Equal(t, Negative, r.seen[cls], "the ClassType keeps the start polarity")
	require.Equal(t, Negative, r.seen[a], "a type argument is covariant")
}

// A no-op rewrite over an object carrying a method, getter, and setter walks the
// method's signature, the getter's type, and the setter's param, yet nothing
// changes, so it keeps every pointer.
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

// Rewriting a variable inside a setter rebuilds only that element and reuses the
// pointers of the object's other members.
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

// A constructor's parameter is in write position, so Accept visits it in the FLIPPED
// polarity, mirroring an ordinary parameter, while its return is covariant. A changed
// variable rebuilds the ConstructorElem and the enclosing object copy-on-write.
func TestAcceptConstructorElem(t *testing.T) {
	paramT := &TypeVarType{ID: 1}
	retT := &TypeVarType{ID: 2}
	obj := &ObjectType{Elems: []ObjTypeElem{
		&ConstructorElem{Fn: &FuncType{Params: []*FuncParam{identP("x", paramT)}, Ret: retT}},
	}}

	r := &recorder{seen: map[Type]Polarity{}}
	obj.Accept(r, Positive)
	require.Equal(t, Negative, r.seen[paramT], "a constructor parameter is contravariant")
	require.Equal(t, Positive, r.seen[retT], "a constructor return is covariant")

	str := &PrimType{Prim: StrPrim}
	got := obj.Accept(&replaceVar{target: paramT, repl: str}, Positive).(*ObjectType)
	require.NotSame(t, obj, got, "a changed constructor forces a new object")
	gotCtor := got.Elems[0].(*ConstructorElem)
	require.Same(t, str, gotCtor.Fn.Params[0].Type, "the constructor param took the replacement")
}

// LevelOf on an object descends into a constructor's signature.
func TestLevelOfConstructorElem(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{
		&ConstructorElem{Fn: &FuncType{Params: []*FuncParam{identP("x", &TypeVarType{ID: 1, Level: 6})}, Ret: numP()}},
	}}
	require.Equal(t, 6, LevelOf(obj), "the level is the max over the constructor signature")
}

// LevelOf on a ClassType is the max level over its type arguments; the Name and
// Final identity carry no variables.
func TestLevelOfClassType(t *testing.T) {
	require.Equal(t, 0, LevelOf(&ClassType{Name: "Point"}), "no arguments ⇒ level 0")
	cls := &ClassType{Name: "Box", TypeArgs: []Type{
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
// are named as quantified parameters by the scheme printer. The instance shows those
// parameters inline in its `<...>` argument list, so the scheme printer adds no
// separate quantifier prefix: Map<K, V> renders as Map<T0, T1>, not
// <T0, T1> Map<T0, T1>.
func TestFreeTypeVarsClassType(t *testing.T) {
	a := &TypeVarType{ID: 1, Level: 1}
	b := &TypeVarType{ID: 2, Level: 1}
	cls := &ClassType{Name: "Map", TypeArgs: []Type{a, b}}
	require.Equal(t, "Map<T0, T1>", PrintAsScheme(cls))
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

// A getter and a setter may legitimately share a name. Member returns the first in
// declaration order and cannot reach the second, so it is insufficient on its own to
// resolve read-versus-write member access, where obj.x reads the getter and obj.x = v
// writes the setter. A caller that needs both sides scans Elems by name AND kind
// rather than relying on Member. This pins the first-declared-wins behavior.
func TestObjectMemberGetterSetterSameName(t *testing.T) {
	getter := &GetterElem{Name: "x", Type: numP()}
	setter := &SetterElem{Name: "x", Param: strP()}

	got, ok := (&ObjectType{Elems: []ObjTypeElem{getter, setter}}).Member("x")
	require.True(t, ok)
	require.Same(t, getter, got, "the getter is declared first, so Member returns it")

	got, ok = (&ObjectType{Elems: []ObjTypeElem{setter, getter}}).Member("x")
	require.True(t, ok)
	require.Same(t, setter, got, "declaration order decides which of the two Member returns")
}

// ObjElemName reads the name off any element kind.
func TestObjElemName(t *testing.T) {
	require.Equal(t, "p", ObjElemName(&PropertyElem{Name: "p"}))
	require.Equal(t, "m", ObjElemName(method("m", &FuncType{Ret: numP()})))
	require.Equal(t, "g", ObjElemName(&GetterElem{Name: "g"}))
	require.Equal(t, "s", ObjElemName(&SetterElem{Name: "s"}))
}
