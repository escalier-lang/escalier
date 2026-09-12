package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A member keyed off a well-known symbol resolves, and the type it belongs to renders
// the key back as it was written. The reserved name it is stored under is an internal
// spelling no source form shows.
func TestSymbolKeyedMemberResolvesAndRendersBack(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AMethodKeyedOffASymbol",
			src:  `type Seq = { [Symbol.iterator](self) -> number }`,
			want: "{[Symbol.iterator]() -> number}",
		},
		{
			name: "APropertyKeyedOffASymbol",
			src:  `type Tagged = { [Symbol.toStringTag]: string }`,
			want: "{[Symbol.toStringTag]: string}",
		},
		{
			name: "AReadonlyPropertyKeepsItsModifier",
			src:  `type Tagged = { readonly [Symbol.toStringTag]: string }`,
			want: "{readonly [Symbol.toStringTag]: string}",
		},
		{
			// Two symbols are two members, which is what the reserved spelling has to
			// keep apart for the closed set to be sound.
			name: "TwoSymbolsAreTwoMembers",
			src:  `type Both = { [Symbol.iterator](self) -> number, [Symbol.asyncIterator](self) -> string }`,
			want: "{[Symbol.iterator]() -> number, [Symbol.asyncIterator]() -> string}",
		},
		{
			// A symbol-keyed member sits beside an ordinary one under one object.
			name: "BesideAnOrdinaryMember",
			src:  `type Seq = { length: number, [Symbol.iterator](self) -> number }`,
			want: "{length: number, [Symbol.iterator]() -> number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, types, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			for _, got := range types {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

// The rendered form is the one the source wrote, so it parses back to the same type.
// A quoted fallback such as `"@@iterator"` would name a different member.
func TestARenderedSymbolKeyParsesBackToItself(t *testing.T) {
	_, types, errs := inferSource(t, `type Seq = { [Symbol.iterator](self) -> number }`)
	require.Empty(t, errorMessagesOf(errs))

	_, roundTripped, errs := inferSource(t, `type Seq = `+types["Seq"])
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, types["Seq"], roundTripped["Seq"])
}

// A class member keyed off a symbol reaches the instance the same way, so a
// declaration and the class it describes agree on the member.
func TestASymbolKeyedClassMemberSatisfiesTheMatchingObject(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare class Seq {
			[Symbol.iterator](self) -> number,
		}
		declare fn take(s: { [Symbol.iterator](self) -> number, ... }) -> number
		declare fn mkSeq() -> Seq
		val n = take(mkSeq())
	`)
	require.Empty(t, errorMessagesOf(errs))
}

// A key outside the closed set stays unsupported. Reading `Symbol.whatever` as a
// member name would let two different symbols share one, which is the assumption the
// reserved spelling rests on.
func TestAComputedKeyOutsideTheClosedSetIsUnsupported(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "ASymbolPropertyThatIsNotWellKnown",
			src:  `type Seq = { [Symbol.whatever](self) -> number }`,
		},
		{
			// The receiver has to be `Symbol` itself. A member off anything else names
			// no well-known symbol whatever the property is called.
			name: "AnotherReceiverNamedLikeASymbol",
			src:  `type Seq = { [Other.iterator](self) -> number }`,
		},
		{
			name: "ABareIdentifierKey",
			src:  `type Seq = { [k](self) -> number }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, []string{"Unsupported: ComputedKey"}, errorMessagesOf(errs))
		})
	}
}
