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
			"{f(x: number) -> string, origin: number}",
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
			require.Empty(t, messagesWithSpan(t, errs))
			require.Equal(t, tt.want, soltype.Print(nodes["Result"]))
		})
	}
}

// Every member of an annotation goes through one builder, so the object renders in the order the
// source wrote it whatever kinds it mixes. A generic method keeps its own type parameters, which
// the enclosing annotation does not bind.
func TestInferMethodTypeAnnGeneric(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `type Result = {f<T>(x: T) -> T}`)
	require.Empty(t, messagesWithSpan(t, errs))
	require.Equal(t, "{f<T>(x: T) -> T}", soltype.Print(nodes["Result"]))
}

// Two members of one name are the arms of an overload set, matching how a class body collects
// them, so they merge into one MethodElem rather than leaving two elements under one name.
func TestInferMethodTypeAnnOverload(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `type Result = {f(x: number) -> number, f(x: string) -> string}`)
	require.Empty(t, messagesWithSpan(t, errs))
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
	require.Empty(t, messagesWithSpan(t, errs))
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
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
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
			require.Empty(t, messagesWithSpan(t, errs))
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
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// A method, getter, or setter written in an annotation can be read and called through, not only
// compared. valueProp intercepts these member kinds on a plain object receiver, since the
// structural `{name: fieldVar}` requirement it otherwise builds matches a PropertyElem alone.
//
// An object type annotation is the only source of such an object, an object literal having no
// syntax for these members, so this path exists for the members this file adds.
func TestInferAnnMemberRead(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "MethodCall",
			src: `
				declare fn make() -> {f(x: number) -> string, ...}
				val s: string = make().f(1)
			`,
			want: nil,
		},
		{
			// The receiver is a var holding the object rather than the object itself, which is
			// the shape objectCarrier reads out of a var's lower bounds.
			name: "MethodCallThroughABinding",
			src: `
				declare fn make() -> {f(x: number) -> string, ...}
				val o = make()
				val s: string = o.f(1)
			`,
			want: nil,
		},
		{
			name: "MethodArgumentIsChecked",
			src: `
				declare fn make() -> {f(x: number) -> string, ...}
				val s = make().f("nope")
			`,
			want: []string{`3:22-3:28: cannot constrain "nope" <: number`},
		},
		{
			name: "MethodReturnIsChecked",
			src: `
				declare fn make() -> {f(x: number) -> string, ...}
				val s: number = make().f(1)
			`,
			want: []string{"3:21-3:32: cannot constrain string <: number"},
		},
		{
			name: "GetterRead",
			src: `
				declare fn make() -> {get a(self) -> number, ...}
				val n: number = make().a
			`,
			want: nil,
		},
		{
			name: "GetterReadIsChecked",
			src: `
				declare fn make() -> {get a(self) -> number, ...}
				val n: string = make().a
			`,
			want: []string{"3:21-3:29: cannot constrain number <: string"},
		},
		{
			// Each call site picks the arm matching its argument type out of the overload set.
			name: "OverloadResolvesPerCall",
			src: `
				declare fn make() -> {f(x: number) -> number, f(x: string) -> string, ...}
				val a: number = make().f(1)
				val b: string = make().f("s")
			`,
			want: nil,
		},
		{
			name: "SetterOnlyNameIsWriteOnly",
			src: `
				declare fn make() -> {set a(self, v: number), ...}
				val n = make().a
			`,
			want: []string{"3:13-3:21: Property 'a' is write-only; it has a setter but no getter or field to read."},
		},
		{
			// A name the object does not carry keeps the structural path's diagnostic, since
			// the interception declines a miss rather than reporting one of its own.
			name: "MissingNameIsStillReported",
			src: `
				declare fn make() -> {f(x: number) -> string}
				val n = make().nope
			`,
			want: []string{"3:20-3:24: object is missing property: nope"},
		},
		{
			// A property keeps the structural path, which is what carries the read-after-write
			// record, the borrow edges, and the union join.
			name: "PropertyReadIsUnaffected",
			src: `
				declare fn make() -> {a: number, ...}
				val n: number = make().a
			`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// Reading through a getter runs its body, so the read is an exceptional exit of the enclosing
// body the way a call is. Reading a method is not: `o.f` only names the function, and what it
// raises stays in the signature until it is called. Both rules already held for a class
// instance, and reach an annotated object once its members resolve.
func TestInferAnnAccessorReadRaises(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "GetterReadRaisesUndeclared",
			src: `
				declare fn make() -> {get a(self) -> number throws string, ...}
				fn g() -> number { return make().a }
			`,
			want: []string{"3:31-3:39: cannot constrain string <: never"},
		},
		{
			name: "GetterReadRaisesDeclared",
			src: `
				declare fn make() -> {get a(self) -> number throws string, ...}
				fn g() -> number throws string { return make().a }
			`,
			want: nil,
		},
		{
			name: "MethodReadDoesNotRaise",
			src: `
				declare fn make() -> {f() -> number throws string, ...}
				fn g() -> fn () -> number throws string { return make().f }
			`,
			want: nil,
		},
		{
			name: "SetterWriteRaisesUndeclared",
			src: `
				declare fn make() -> mut {set a(self, v: number) throws string, ...}
				fn g() { var o = make() o.a = 5 }
			`,
			want: []string{"3:29-3:36: cannot constrain string <: never"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// A field write resolves the setter half of an annotated object through writeMember, which reads
// a plain object type. The receiver must be mutable, so these bind with `var` through a `mut`
// return.
func TestInferAnnSetterWrite(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "Write",
			src: `
				declare fn make() -> mut {set a(self, v: number), ...}
				fn g() { var o = make() o.a = 5 }
			`,
			want: nil,
		},
		{
			name: "WrittenValueIsChecked",
			src: `
				declare fn make() -> mut {set a(self, v: number), ...}
				fn g() { var o = make() o.a = "nope" }
			`,
			want: []string{`3:35-3:41: cannot constrain "nope" <: number`},
		},
		{
			name: "GetterOnlyNameIsReadOnly",
			src: `
				declare fn make() -> mut {get a(self) -> number, ...}
				fn g() { var o = make() o.a = 5 }
			`,
			want: []string{"3:29-3:36: Property 'a' is read-only; it has a getter but no setter or field to write."},
		},
		{
			name: "PairWritesThenReads",
			src: `
				declare fn make() -> mut {get a(self) -> number, set a(self, v: number), ...}
				fn g() -> number { var o = make() o.a = 5 return o.a }
			`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// Declaring one name twice in an object type annotation is a redeclaration, so the later member
// is reported. Two members collide when they answer the same access: a property answers a read
// and a write, a method or getter answers a read, and a setter answers a write. The later one
// still wins at the first one's position, which keeps the object at one member per access as the
// recovery.
//
// A getter and a setter answer different accesses, so a pair is not a redeclaration. Two methods
// are the arms of an overload set, which Escalier supports, so they merge rather than collide.
// Neither reports.
//
// A spread is the other way an earlier member is superseded, and it stays silent — overriding is
// the point of writing one. The ordered path handles it and reduceObject merges once it grounds.
func TestInferMemberTypeAnnDeduplicates(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
		obj  string
	}{
		{
			name: "TwoProperties",
			src:  `type Result = {a: number, a: string}`,
			want: []string{"1:27-1:28: An object type may declare 'a' only once."},
			obj:  "{a: string}",
		},
		{
			name: "TwoGetters",
			src:  `type Result = {get a(self) -> number, get a(self) -> string}`,
			want: []string{"1:43-1:44: An object type may declare 'a' only once."},
			obj:  "{get a() -> string}",
		},
		{
			name: "TwoSetters",
			src:  `type Result = {set a(self, v: number), set a(self, v: string)}`,
			want: []string{"1:44-1:45: An object type may declare 'a' only once."},
			obj:  "{set a(value: string)}",
		},
		{
			name: "GetterThenProperty",
			src:  `type Result = {get a(self) -> number, a: string}`,
			want: []string{"1:39-1:40: An object type may declare 'a' only once."},
			obj:  "{a: string}",
		},
		{
			name: "PropertyThenMethod",
			src:  `type Result = {a: number, a(x: number) -> string}`,
			want: []string{"1:27-1:28: An object type may declare 'a' only once."},
			obj:  "{a(x: number) -> string}",
		},
		{
			// The property answers both accesses, so it displaces the getter and the setter
			// together and lands at the getter's position. One member is written twice, so one
			// error is reported however many earlier members it supersedes.
			name: "PropertyDisplacesBothHalves",
			src:  `type Result = {get a(self) -> number, set a(self, v: number), b: boolean, a: string}`,
			want: []string{"1:75-1:76: An object type may declare 'a' only once."},
			obj:  "{a: string, b: boolean}",
		},
		{
			// The getter takes over the property's read and is itself a redeclaration. The
			// property's write falls free rather than staying pointed at the getter, so the
			// setter lands beside it and adds no second error.
			name: "GetterThenSetterOverAProperty",
			src:  `type Result = {a: number, get a(self) -> string, set a(self, v: string)}`,
			want: []string{"1:31-1:32: An object type may declare 'a' only once."},
			obj:  "{get a() -> string, set a(value: string)}",
		},
		{
			name: "AccessorPairCoexists",
			src:  `type Result = {get a(self) -> number, set a(self, v: number)}`,
			want: nil,
			obj:  "{get a() -> number, set a(value: number)}",
		},
		{
			name: "MethodsAreOverloadArms",
			src:  `type Result = {f(x: number) -> number, f(x: string) -> string}`,
			want: nil,
			obj:  "{f(x: number) -> number; f(x: string) -> string}",
		},
		{
			// A spread supersedes the earlier member without a diagnostic, which is what
			// separates an override from a redeclaration.
			name: "SpreadOverrideIsSilent",
			src:  `type Result = {a: number, ...{a: string}}`,
			want: nil,
			obj:  "{a: number, ...{a: string}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, _, errs := inferTypeNodes(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
			require.Equal(t, tt.obj, soltype.Print(nodes["Result"]))
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
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
			require.Equal(t, tt.obj, soltype.Print(nodes["Result"]))
		})
	}
}
