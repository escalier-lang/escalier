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

// A `for await` reads `[Symbol.asyncIterator]`, so the two protocols stay apart and
// neither answers the other's loop. `testdata/stdlib/std/prelude.esc` declares
// `AsyncIterable` beside `Iterable`, each naming its own iterator.
func TestForAwaitReadsTheAsyncMember(t *testing.T) {
	t.Run("AnAsyncMemberAnswers", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare class Stream { [Symbol.asyncIterator](self) -> AsyncIterator<string> }
			declare fn mk() -> Stream
			async fn use() -> Promise<string> { for await x in mk() { return x } return "" }
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn () -> Promise<string>", values["use"])
	})

	t.Run("TheDeclaredAsyncIterableAnswers", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare fn mk() -> AsyncIterable<string>
			async fn use() -> Promise<string> { for await x in mk() { return x } return "" }
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn () -> Promise<string>", values["use"])
	})

	// The sync protocol does not answer a `for await` either, so the two are apart in
	// both directions.
	t.Run("AnAsyncMemberDoesNotAnswerASyncLoop", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			declare fn mk() -> AsyncIterable<string>
			fn use(xs: AsyncIterable<string>) { for x in xs { x } }
		`)
		require.Equal(t, []string{"AsyncIterable<string, undefined, unknown> is not iterable"},
			errorMessagesOf(errs))
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

// Iteration calls the protocol member with no arguments, so the signature it reads is the
// one such a call selects. Reading the first arm regardless would report the element of a
// signature the call never reaches.
func TestForInReadsTheNullaryProtocolSignature(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// The first arm demands an argument, so the second is the one a bare call
			// selects and the one the element comes from.
			name: "AnOverloadSetAnswersFromItsNullaryArm",
			src: `
				declare class Seq {
					[Symbol.iterator](self, hint: string) -> Iterator<number>,
					[Symbol.iterator](self) -> Iterator<boolean>,
				}
				fn use(s: Seq) { for x in s { return x } }
			`,
			want: "fn (s: Seq) -> boolean",
		},
		{
			// An optional parameter binds zero arguments, so it leaves the member
			// callable with none.
			name: "AnOptionalParameterLeavesTheMemberCallable",
			src: `
				declare class Seq { [Symbol.iterator](self, hint?: string) -> Iterator<number> }
				fn use(s: Seq) { for x in s { return x } }
			`,
			want: "fn (s: Seq) -> number",
		},
		{
			// So does a rest parameter, which binds zero or more.
			name: "ARestParameterLeavesTheMemberCallable",
			src: `
				declare class Seq { [Symbol.iterator](self, ...hints: Array<string>) -> Iterator<number> }
				fn use(s: Seq) { for x in s { return x } }
			`,
			want: "fn (s: Seq) -> number",
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

// A member no bare call can reach leaves the operand un-iterable, rather than iterating
// the element of a signature the iteration would never select.
func TestForInRejectsAProtocolMemberDemandingAnArgument(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare class Seq { [Symbol.iterator](self, hint: string) -> Iterator<number> }
		fn use(s: Seq) { for x in s { x } }
	`)
	require.Equal(t, []string{"Seq is not iterable"}, errorMessagesOf(errs))
}

// An alias is followed to what it names, so naming an iterable through one iterates.
func TestForInFollowsAnAliasToTheIterable(t *testing.T) {
	values, _, errs := inferSource(t, `
		type Nums = Array<number>
		fn use(n: Nums) { for x in n { return x } }
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "fn (n: Nums) -> number", values["use"])
}

// An iterator states three slots. `Iterator<T, TReturn, TNext>` names the element, what
// the iteration finishes with, and what it accepts from a sent value, in that order.
// `yield from` forwards all three off the iterator its protocol member hands back, so a
// delegation carries more than the element. A declaration writing fewer arguments states
// fewer slots, and the rest fall back to what a tuple gives.
//
// The body returns the delegation's value, since a generator's own `Ret` is what its body
// returns. Dropping that would leave the slot reading `undefined` whatever the delegate
// finishes with.
func TestYieldFromForwardsEveryProtocolSlot(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AllThreeSlotsAreForwarded",
			src: `
				gen fn g(xs: Iterable<string, number, boolean>) {
					val done = yield from xs
					return done
				}
			`,
			want: "fn (xs: Iterable<string, number, boolean>) -> Generator<string, number, boolean>",
		},
		{
			// `Array` declares `[Symbol.iterator](self) -> Iterator<T>`, one argument, so
			// only the element is stated and the delegation finishes with `undefined`.
			name: "AnUnstatedSlotFallsBack",
			src: `
				gen fn g(xs: Array<number>) {
					val done = yield from xs
					return done
				}
			`,
			want: "fn (xs: Array<number>) -> Generator<number, undefined, unknown>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values["g"])
		})
	}
}

// A diagnostic names a symbol-keyed member the way the source writes it. The reserved
// spelling it is stored under is internal, so a message showing `@@iterator` would name
// something no source form spells.
func TestADiagnosticNamesASymbolKeyedMemberAsWritten(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare fn take(s: { [Symbol.iterator](self) -> number, ... }) -> number
		val n = take({ a: 1 })
	`)
	require.Equal(t,
		[]string{"object is missing property: [Symbol.iterator]"},
		errorMessagesOf(errs))
}

// An optional protocol member says the value may not carry it, so iterating one would
// type a loop the value cannot run. Both spellings of an optional member decline.
func TestForInRejectsAnOptionalProtocolMember(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "AnOptionalMethod",
			src: `
				declare fn mk() -> { [Symbol.iterator]?(self) -> Iterator<number> }
				fn use() { for x in mk() { x } }
			`,
		},
		{
			name: "AnOptionalProperty",
			src: `
				declare fn mk() -> { [Symbol.iterator]?: fn () -> Iterator<number> }
				fn use() { for x in mk() { x } }
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errorMessagesOf(errs), 1)
			require.Contains(t, errorMessagesOf(errs)[0], "is not iterable")
		})
	}
}

// An alias renaming another nominal reference is followed, so the slots are read in the
// order the iterator declares them rather than the order the alias's arguments were
// written. `Flip<number, string>` is `Iterator<string, number>`, whose element is
// `string`.
func TestForInFollowsAnAliasThatRenamesAnIterator(t *testing.T) {
	values, _, errs := inferSource(t, `
		type Flip<A, B> = Iterator<B, A>
		declare class Seq { [Symbol.iterator](self) -> Flip<number, string> }
		fn use(s: Seq) { for x in s { return x } }
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "fn (s: Seq) -> string", values["use"])
}

// An async body delegates through `[Symbol.asyncIterator]`, so a delegate declaring only
// that member answers. It falls back to the sync member, which is what lets an async
// generator delegate to a plain array.
func TestYieldFromInAnAsyncBodyReadsTheAsyncMember(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AnAsyncOnlyDelegate",
			src: `
				declare class Stream { [Symbol.asyncIterator](self) -> AsyncIterator<string> }
				async gen fn g(s: Stream) { yield from s }
			`,
			want: "fn (s: Stream) -> AsyncGenerator<string, undefined, unknown>",
		},
		{
			name: "ASyncDelegateFallsBack",
			src:  `async gen fn g(xs: Array<number>) { yield from xs }`,
			want: "fn (xs: Array<number>) -> AsyncGenerator<number, undefined, unknown>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values["g"])
		})
	}
}

// A chain of aliases is followed to the nominal reference at its end, and a chain that
// returns to a name it already walked stops rather than looping. The degenerate aliases
// below draw a productivity report of their own; what this pins is that the slot read
// terminates and still answers.
func TestForInFollowsAnAliasChainWithoutLooping(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "AChainReachingTheIterator",
			src: `
				type Mid<T> = Iterator<T>
				declare class Seq { [Symbol.iterator](self) -> Mid<number> }
				fn use(s: Seq) { for x in s { return x } }
			`,
		},
		{
			name: "AnAliasNamingItself",
			src: `
				type Loop<T> = Loop<T>
				declare class Seq { [Symbol.iterator](self) -> Loop<number> }
				fn use(s: Seq) { for x in s { return x } }
			`,
			want: []string{
				"recursive type alias `Loop` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias " +
					"names no type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			name: "TwoAliasesNamingEachOther",
			src: `
				type A<T> = B<T>
				type B<T> = A<T>
				declare class Seq { [Symbol.iterator](self) -> A<number> }
				fn use(s: Seq) { for x in s { return x } }
			`,
			want: []string{
				"recursive type alias `B` reaches itself through `A` without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias " +
					"names no type; wrap the recursive reference in an object, tuple, or function type",
				"recursive type alias `A` reaches itself through `B` without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias " +
					"names no type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, nilIfEmpty(errorMessagesOf(errs)))
			require.Equal(t, "fn (s: Seq) -> number", values["use"])
		})
	}
}

// nilIfEmpty renders an empty message list as nil, so a row expecting no diagnostic
// leaves its want field unset rather than writing an empty slice.
func nilIfEmpty(msgs []string) []string {
	if len(msgs) == 0 {
		return nil
	}
	return msgs
}
