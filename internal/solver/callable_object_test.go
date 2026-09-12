package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// An object type annotation may write a call signature among its members, which makes the
// object callable. It is the plain-call twin of the `new (…) -> T` construct signature: a
// constructor answers `new`, a call signature answers a plain call, and an object may carry
// either, both, or neither.
func TestACallSignatureMakesAnObjectCallable(t *testing.T) {
	const decl = `type F = { fn (n: number) -> string, tag: string }`

	t.Run("ItRendersAsWritten", func(t *testing.T) {
		_, types, errs := inferSource(t, decl)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "{fn (n: number) -> string, tag: string}", types["F"])
	})

	t.Run("TheObjectIsCalled", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			declare val f: F
			val r = f(1)
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "string", values["r"])
	})

	// The call signature does not displace the ordinary members beside it.
	t.Run("AMemberBesideItStillReads", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			declare val f: F
			val r = f.tag
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "string", values["r"])
	})

	// The signature says what the object is callable with, so an argument is checked against
	// it the way it would be against a bare function.
	t.Run("AnArgumentIsChecked", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			declare val f: F
			val r = f("x")
		`)
		require.Equal(t, []string{`cannot constrain "x" <: number`}, errorMessagesOf(errs))
	})
}

// An object carrying a call signature is a subtype of the function type it names, so it fills
// a parameter declared as that function.
func TestACallableObjectFillsAFunctionParameter(t *testing.T) {
	values, _, errs := inferSource(t, `
		type F = { fn (n: number) -> string }
		declare val f: F
		declare fn take(g: fn (n: number) -> string) -> number
		val r = take(f)
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["r"])
}

// A plain function is not callable-with-members, so it does not fill an object target naming a
// call signature. The rule runs one way, as it does for a construct signature.
func TestAFunctionDoesNotFillACallSignatureRequirement(t *testing.T) {
	_, _, errs := inferSource(t, `
		type F = { fn (n: number) -> string }
		declare fn take(f: F) -> number
		declare fn g(n: number) -> string
		val r = take(g)
	`)
	require.Equal(t,
		[]string{"cannot constrain function <: object"},
		errorMessagesOf(errs))
}

// Several `fn (…) -> T` members are the arms of one overloaded call signature, the way
// TypeScript writes one, rather than several members the object could not hold.
func TestSeveralCallSignaturesAreOneOverloadedMember(t *testing.T) {
	_, types, errs := inferSource(t, `
		type F = { fn (n: number) -> string, fn (s: string) -> number }
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{fn (n: number) -> string; fn (s: string) -> number}", types["F"])
}

// A constructor and a call signature are separate members. `SymbolConstructor` needs the call
// signature alone, since the specification forbids `new Symbol()`, and `DateConstructor` needs
// both.
func TestAConstructorAndACallSignatureAreSeparateMembers(t *testing.T) {
	t.Run("BothAreKept", func(t *testing.T) {
		_, types, errs := inferSource(t, `type F = { fn (n: number) -> string, new (n: number) -> string }`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "{fn (n: number) -> string, new (n: number) -> string}", types["F"])
	})

	// A constructor does not answer a call-signature requirement.
	t.Run("AConstructorDoesNotFillACallSignature", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type Call = { fn (n: number) -> string }
			type Ctor = { new (n: number) -> string }
			declare fn take(f: Call) -> number
			declare val c: Ctor
			val r = take(c)
		`)
		require.Equal(t,
			[]string{"cannot constrain object <: object"},
			errorMessagesOf(errs))
	})
}

// An overloaded call signature resolves an arm at the call site, and it decides the call even
// when the object carries a constructor beside it. Reading the constructor instead would check
// the call against the wrong member.
func TestAnOverloadedCallSignatureResolvesAnArm(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "TheNullaryArm",
			src: `
				declare val f: { fn () -> string, fn (a: number, b: number) -> boolean }
				val r = f()
			`,
			want: "string",
		},
		{
			name: "TheTwoArgumentArm",
			src: `
				declare val f: { fn () -> string, fn (a: number, b: number) -> boolean }
				val r = f(1, 2)
			`,
			want: "boolean",
		},
		{
			// A constructor sits beside the arms and answers `new`, not this call.
			name: "WithAConstructorBesideIt",
			src: `
				declare val f: {
					fn () -> string,
					fn (a: number, b: number) -> boolean,
					new (a: number, b: number, c: number) -> number,
				}
				val r = f()
			`,
			want: "string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values["r"])
		})
	}
}

// A constructor and a call signature occupy separate merge slots, so an object carrying both
// still fuses. Keying the repeat check on the member name alone read the two as one slot,
// since both answer the empty name.
func TestAnObjectWithBothCallableMembersStillFuses(t *testing.T) {
	values, _, errs := inferSource(t, `
		type A = { fn () -> string, new () -> number, x: number }
		type B = { fn () -> string, new () -> number, y: number }
		declare fn take(v: A & B) -> number
		declare val v: A & B
		val r = take(v)
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["r"])
}

// `keyof` yields an object's own named keys. A call signature is unnamed, so it contributes
// none, the way a constructor contributes none.
func TestKeyofSkipsTheUnnamedCallableMembers(t *testing.T) {
	obj := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
		&soltype.CallableElem{Signatures: []*soltype.FuncType{{Ret: &soltype.PrimType{Prim: soltype.StrPrim}}}},
		&soltype.ConstructorElem{Signatures: []*soltype.FuncType{{Ret: &soltype.PrimType{Prim: soltype.NumPrim}}}},
		&soltype.PropertyElem{Name: "tag", Type: &soltype.PrimType{Prim: soltype.StrPrim}},
	}}
	require.Equal(t, `"tag"`, soltype.Print(keyofObjectNamed(obj)))
}

// `static readonly [Symbol.species]: typeof Arr` is the shape trio fusion emits where the
// source named the constructor interface. The annotation resolves to the class value, so the
// declaration carrying it reports nothing.
func TestAStaticTypeofTheOwnClassResolves(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare class Arr {
			static of(n: number) -> Arr,
			static readonly [Symbol.species]: typeof Arr,
		}
		declare val species: typeof Arr
		val made = species.of(1)
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "Arr", values["made"])
}
