package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- Method, getter, and setter members in object type annotations ---

// A method, getter, or setter member resolves to the same MethodElem, GetterElem, or SetterElem
// a class body builds, and the soltype printer renders each back. The signature accepts
// everything a `fn` annotation does, since both resolve through resolveFuncTypeAnn.
func TestInferMemberTypeAnnRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"Method", `type Result = {f(x: number) -> string}`, "{f(x: number) -> string}"},
		{"MethodNoParams", `type Result = {f() -> string}`, "{f() -> string}"},
		{
			"MethodThrows",
			`type Result = {parse(x: string) -> number throws string}`,
			"{parse(x: string) -> number throws string}",
		},
		{
			"MethodRestParam",
			`type Result = {f(...args: [number, string]) -> boolean}`,
			"{f(...args: [number, string]) -> boolean}",
		},
		{"Getter", `type Result = {get a(self) -> number}`, "{get a() -> number}"},
		// A setter returns nothing, so it writes no `-> R`, the way a class body declares one.
		{"Setter", `type Result = {set a(self, v: number)}`, "{set a(value: number)}"},
		{
			"GetterAndSetter",
			`type Result = {get a(self) -> number, set a(self, v: number)}`,
			"{get a() -> number, set a(value: number)}",
		},
		{
			"BesideProperty",
			`type Result = {f(x: number) -> string, origin: number}`,
			"{origin: number, f(x: number) -> string}",
		},
		{
			"UnderSpread",
			`type Result = {...{a: number}, f(x: number) -> string}`,
			"{...{a: number}, f(x: number) -> string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, _, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, messagesWithSpan(errs))
			require.Equal(t, tt.want, soltype.Print(nodes["Result"]))
		})
	}
}

// A named member is keyed by kind as well as by name, so it does not file under the property
// dedup builder and is appended after the properties. That ordering is why BesideProperty above
// renders the method last while the source wrote it first. A generic method keeps its own type
// parameters, which the enclosing annotation does not bind.
func TestInferMethodTypeAnnGeneric(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `type Result = {f<T>(x: T) -> T}`)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t, "{f<T>(x: T) -> T}", soltype.Print(nodes["Result"]))
}

// Two members of one name are the arms of an overload set, matching how a class body collects
// them, so they merge into one MethodElem rather than leaving two elements under one name.
func TestInferMethodTypeAnnOverload(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `type Result = {f(x: number) -> number, f(x: string) -> string}`)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t, "{f(x: number) -> number; f(x: string) -> string}", soltype.Print(nodes["Result"]))

	obj, isObj := nodes["Result"].(*soltype.ObjectType)
	require.True(t, isObj)
	require.Len(t, obj.Elems, 1)
	method, isMethod := obj.Elems[0].(*soltype.MethodElem)
	require.True(t, isMethod)
	require.Len(t, method.Signatures, 2)
}

// An annotated object holding a method accepts a class instance declaring the same method. The
// receiver is what the two sides describe differently — the class method binds `self` and the
// annotation does not — and subtyping compares through callableView, which drops it.
//
// The annotation carries a trailing `...` because the class declares a `count` field the
// annotation does not name, and an exact object admits no member beyond the ones it lists.
func TestInferMethodTypeAnnAcceptsAClassInstance(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Counter {
			count: number,
			constructor(mut self) { self.count = 0 },
			bump(mut self, by: number) -> number { return self.count }
		}
		declare fn make() -> Counter
		val c: {bump(mut self, by: number) -> number, ...} = make()
	`)
	require.Empty(t, messagesWithSpan(errs))
}

// A method's parameters stay contravariant and its return covariant, the same as a bare function
// type, so a mismatch in either position is reported against the member.
func TestInferMethodTypeAnnChecksSignatures(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ReturnMismatch",
			src: `
				declare fn make() -> {f(x: number) -> number}
				val v: {f(x: number) -> string} = make()
			`,
			want: []string{"3:39-3:45: cannot constrain number <: string"},
		},
		{
			name: "GetterMismatch",
			src: `
				declare fn make() -> {get a(self) -> number}
				val v: {get a(self) -> string} = make()
			`,
			want: []string{"3:38-3:44: cannot constrain number <: string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// An accessor declares what an access raises through the Throws position GetterElem and
// SetterElem carry. The annotation and the signature share one nil-means-`never` shorthand, so an
// absent clause needs no special case: resolveFuncTypeAnn leaves the signature's Throws nil and
// the element stores that nil unchanged.
func TestInferAccessorTypeAnnThrowsRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"Getter",
			`type Result = {get a(self) -> number throws string}`,
			"{get a() -> number throws string}",
		},
		{
			"Setter",
			`type Result = {set a(self, v: number) throws string}`,
			"{set a(value: number) throws string}",
		},
		{
			"SetterWithReturnType",
			`type Result = {set a(self, v: number) -> undefined throws string}`,
			"{set a(value: number) throws string}",
		},
		{
			"Pair",
			`type Result = {get a(self) -> number throws string, set a(self, v: number) throws boolean}`,
			"{get a() -> number throws string, set a(value: number) throws boolean}",
		},
		{"NoClause", `type Result = {get a(self) -> number}`, "{get a() -> number}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, _, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, messagesWithSpan(errs))
			require.Equal(t, tt.want, soltype.Print(nodes["Result"]))
		})
	}
}

// An accessor's throws position is covariant, so what an access raises flows out to whoever
// performs it. A target declaring a wider clause accepts a narrower source, and a target with no
// clause promises `never` and so accepts none. The class cases are what show the annotation
// meeting the class-body wiring: a class getter that raises satisfies an annotation declaring the
// same clause, and fails against one declaring nothing.
func TestInferAccessorTypeAnnThrowsIsCovariant(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "WiderTargetAccepts",
			src: `
				declare fn make() -> {get a(self) -> number throws string}
				val v: {get a(self) -> number throws string | boolean} = make()
			`,
			want: nil,
		},
		{
			name: "NonThrowingTargetRejects",
			src: `
				declare fn make() -> {get a(self) -> number throws string}
				val v: {get a(self) -> number} = make()
			`,
			want: []string{"3:38-3:44: cannot constrain string <: never"},
		},
		{
			name: "ClassGetterFits",
			src: `
				declare fn boom() -> number throws string
				class C {
					get a(self) -> number throws string { return boom() }
				}
				declare fn make() -> C
				val v: {get a(self) -> number throws string, ...} = make()
			`,
			want: nil,
		},
		{
			name: "ClassGetterAgainstNonThrowingTarget",
			src: `
				declare fn boom() -> number throws string
				class C {
					get a(self) -> number throws string { return boom() }
				}
				declare fn make() -> C
				val v: {get a(self) -> number, ...} = make()
			`,
			want: []string{"7:43-7:49: cannot constrain string <: never"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// A method or accessor written in an annotation describes a shape and is compared structurally,
// but it cannot yet be read or called through. valueProp reaches a non-property member only via
// its three class escape hatches — a class instance, a `self` read inside a class body, and a
// static read off a class value — and every other receiver falls to the structural
// `{name: fieldVar}` requirement, which matches a PropertyElem alone.
//
// The gap was unreachable before an annotation could express these members, since an object
// literal rejects method shorthand and no other producer builds a bare ObjectType carrying one.
// A plain property on the same receiver reads normally, which is what isolates the cause to the
// member's kind rather than to the annotation.
func TestInferAnnMemberReadIsNotResolvedYet(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "MethodCall",
			src: `
				declare fn make() -> {f(x: number) -> string, ...}
				val s = make().f(1)
			`,
			want: []string{"3:20-3:21: object is missing property: f"},
		},
		{
			name: "GetterRead",
			src: `
				declare fn make() -> {get a(self) -> number, ...}
				val n = make().a
			`,
			want: []string{"3:20-3:21: object is missing property: a"},
		},
		{
			name: "PropertyReadStillWorks",
			src: `
				declare fn make() -> {a: number, ...}
				val n = make().a
			`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// A setter's one value parameter is the value being assigned, so any other count is reported.
// The element is still built, from the first parameter or from `unknown` when there is none, so
// the object carries a member under the name the source wrote. This is the class rule reaching
// the annotation position, and both report through SetterArityError.
func TestInferSetterTypeAnnArity(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
		obj  string
	}{
		{
			name: "NoValueParam",
			src:  `type Result = {set a(self)}`,
			want: []string{"1:20-1:21: Setter 'a' must declare exactly one value parameter; found 0."},
			obj:  "{set a(value: unknown)}",
		},
		{
			name: "TwoValueParams",
			src:  `type Result = {set a(self, v: number, w: string)}`,
			want: []string{"1:20-1:21: Setter 'a' must declare exactly one value parameter; found 2."},
			obj:  "{set a(value: number)}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, _, errs := inferTypeNodes(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
			require.Equal(t, tt.obj, soltype.Print(nodes["Result"]))
		})
	}
}
