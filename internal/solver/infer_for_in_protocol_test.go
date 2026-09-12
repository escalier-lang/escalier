package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Iterating reads `[Symbol.iterator]` off the operand and takes the first type argument
// of what that member returns, so anything declaring the member is iterable and the
// element is whatever its own declaration says.
//
// The rows cover the three shapes a member view comes from. `Array` is a class, whose
// body projects with the arguments the instance is reached at. `Iterable` is an
// interface, which expands to the object it stands for. The annotated object is that
// object already. `testdata/stdlib/std/prelude.esc` holds the declarations.
func TestForInReadsTheIteratorProtocol(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// The element comes through the class's own member rather than through a
			// rule that knows what an array is.
			name: "AnArrayIteratesThroughItsOwnMember",
			src: `
				declare fn mk() -> Array<number>
				fn use() { for x in mk() { return x } }
			`,
			want: "fn () -> number",
		},
		{
			name: "AnInterfaceDeclaringTheMember",
			src: `
				declare fn mk() -> Iterable<string>
				fn use() { for x in mk() { return x } }
			`,
			want: "fn () -> string",
		},
		{
			// A class the program declares itself, which no rule knows anything about.
			name: "AUserClassDeclaringTheMember",
			src: `
				declare class Seq { [Symbol.iterator](self) -> Iterator<boolean> }
				declare fn mk() -> Seq
				fn use() { for x in mk() { return x } }
			`,
			want: "fn () -> boolean",
		},
		{
			// The member may be a property holding a function rather than a method,
			// which is how an object writes one.
			name: "APropertyHoldingTheFunction",
			src: `
				declare fn mk() -> { [Symbol.iterator]: fn () -> Iterator<string> }
				fn use() { for x in mk() { return x } }
			`,
			want: "fn () -> string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values["use"])
		})
	}
}

// The arms that do not go through the protocol keep answering. A tuple is iterable by a
// rule the lookup does not cover, and a generator is a concrete the solver mints rather
// than a declaration, so both resolve ahead of it.
func TestForInKeepsItsNonProtocolArms(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "ATuple",
			src:  `fn use() { for x in [1, 2, 3] { return x } }`,
			want: "fn () -> 1 | 2 | 3",
		},
		{
			name: "AGenerator",
			src: `
				gen fn g() { yield 1 }
				fn use() { for x in g() { return x } }
			`,
			want: "fn () -> 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values["use"])
		})
	}
}

// An operand declaring no protocol member is not iterable, and the report names it. The
// lookup declining is what leaves the existing diagnostic in place rather than iterating
// `unknown`.
func TestForInRejectsWhatDeclaresNoProtocolMember(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "APrimitive",
			src:  `fn use(n: number) { for x in n { x } }`,
			want: "number is not iterable",
		},
		{
			name: "AClassWithoutTheMember",
			src: `
				declare class Bag { size: number }
				fn use(b: Bag) { for x in b { x } }
			`,
			want: "Bag is not iterable",
		},
		{
			// The member is there but returns something carrying no type argument, so
			// there is no element to read.
			name: "AMemberWithNoElementToRead",
			src: `
				declare class Seq { [Symbol.iterator](self) -> number }
				fn use(s: Seq) { for x in s { x } }
			`,
			want: "Seq is not iterable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, []string{tt.want}, errorMessagesOf(errs))
		})
	}
}

// A sync member does not answer a `for await`, and the two symbols stay apart. Nothing
// in the test stdlib declares `[Symbol.asyncIterator]`, so the class below is what says
// the async lookup reads its own member.
func TestForAwaitReadsTheAsyncMember(t *testing.T) {
	t.Run("AnAsyncMemberAnswers", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare class Stream { [Symbol.asyncIterator](self) -> Iterator<string> }
			declare fn mk() -> Stream
			async fn use() -> Promise<string> { for await x in mk() { return x } return "" }
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn () -> Promise<string>", values["use"])
	})

	t.Run("ASyncMemberDoesNotAnswerAForAwait", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			async fn use(xs: Array<number>) -> Promise<undefined> { for await x in xs { x } }
		`)
		require.Equal(t, []string{"Array<number> is not an async iterable"}, errorMessagesOf(errs))
	})
}

// `yield from` reads its operand through the same walk `for`-`in` does, so delegating to
// something that declares the protocol member yields that member's element.
func TestYieldFromReadsTheIteratorProtocol(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare fn mk() -> Array<number>
		gen fn g() { yield from mk() }
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "fn () -> Generator<number, undefined, unknown>", values["g"])
}
