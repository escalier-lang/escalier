package dts_to_esc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// collectFrom converts one `.d.ts` source and renders its collected
// declarations one per line, so a test asserts the whole result rather than
// drilling into each triple.
func collectFrom(t *testing.T, src string) string {
	t.Helper()
	mod, err := ConvertToStandaloneModule(parseLib(t, "lib.test.d.ts", src).Module)
	require.NoError(t, err)

	decls := CollectDeclarations(mod)
	var lines []string
	for _, decl := range decls.Keyed {
		shapes := make([]string, 0, len(decl.Signatures))
		for _, sig := range decl.Signatures {
			shape := fmt.Sprintf("%d", sig.Params)
			if sig.Rest {
				shape += "+rest"
			}
			shapes = append(shapes, shape)
		}
		lines = append(lines, decl.Ref.String()+" ("+strings.Join(shapes, ", ")+")")
	}
	for _, path := range decls.Unkeyed {
		lines = append(lines, "unkeyed: "+path)
	}
	return strings.Join(lines, "\n")
}

// A trio fuses into one class, so its instance members are reached through
// the prototype and its constructor members are statics.
func TestCollectDeclarationsFusedTrio(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface Object {
    toString(): string;
    valueOf(): Object;
}
interface ObjectConstructor {
    new (value?: any): Object;
    keys(o: object): string[];
    defineProperty<T>(o: T, p: PropertyKey, attributes: PropertyDescriptor): T;
}
declare var Object: ObjectConstructor;
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance Object.toString (0)
instance Object.valueOf (0)
static Object.keys (1)
static Object.defineProperty (3)`))
}

// TypeScript spells a constructor the converter cannot fuse as a bare
// interface plus a `declare var` typed by a second interface. The two sides
// still resolve to the same owner.
func TestCollectDeclarationsUnfusedConstructor(t *testing.T) {
	t.Parallel()

	// ArrayConstructor's `new` returns `T[]` rather than `Array<T>`, which is
	// what keeps the trio from fusing.
	got := collectFrom(t, `
interface Array<T> {
    push(...items: T[]): number;
    slice(start?: number, end?: number): T[];
}
interface ArrayConstructor {
    new <T>(arrayLength: number): T[];
    isArray(arg: any): arg is any[];
}
declare var Array: ArrayConstructor;
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance Array.push (1+rest)
instance Array.slice (2)
static Array.isArray (1)`))
}

// A constructor object need not be constructible. `SymbolConstructor` is
// callable only, and `NewableFunction` inherits its `new` through `extends`,
// so both still hold statics of the value bound to them.
func TestCollectDeclarationsCallableConstructor(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface Symbol {
    toString(): string;
}
interface SymbolConstructor {
    (description?: string): symbol;
    for(key: string): symbol;
}
declare var Symbol: SymbolConstructor;
interface FunctionConstructor {
    new (...args: string[]): Function;
}
interface NewableFunction extends FunctionConstructor {
    of(value: string): Function;
}
declare var Newable: NewableFunction;
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance Symbol.toString (0)
static Symbol.for (1)
static Newable.of (1)`))
}

// An interface bound to a `declare var` that is neither callable nor
// constructible describes instances rather than statics, so `Math` does not
// turn into a constructor when its singleton flattening does not fire.
func TestCollectDeclarationsNonCallableBinding(t *testing.T) {
	t.Parallel()

	// A second reference to the interface is what keeps the singleton from
	// flattening, which leaves both the interface and the var emitted.
	got := collectFrom(t, `
interface Shape {
    area(): number;
}
declare var shape: Shape;
declare function render(s: Shape): void;
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance Shape.area (0)
unkeyed: render`))
}

// A member the spec keys no algorithm by is still reported, so the join
// accounts for every member the converter emits.
func TestCollectDeclarationsReportsUnkeyableMembers(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface Weird {
    0(): number;
    [Symbol.iterator](): number;
}
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance Weird.[Symbol.iterator] (0)
unkeyed: Weird.prototype.0`))
}

// A singleton flattens to top-level functions whose runtime path is already
// the spec key, and a known namespace owner makes them functions with no
// receiver.
func TestCollectDeclarationsNamespaceSingleton(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface Math {
    max(...values: number[]): number;
    readonly PI: number;
}
declare var Math: Math;
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`namespace-func Math.max (1+rest)`))
}

// A member of a constructor nested in a namespace is a static of the nested
// constructor, not a function of the namespace.
func TestCollectDeclarationsNestedConstructor(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
declare namespace Intl {
    interface Collator {
        compare(x: string, y: string): number;
    }
    interface CollatorConstructor {
        new (locales?: string): Collator;
        supportedLocalesOf(locales: string): string[];
    }
    var Collator: CollatorConstructor;
}
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance Intl.Collator.compare (2)
static Intl.Collator.supportedLocalesOf (1)`))
}

// A symbol-keyed member joins by kind plus payload, and an accessor carries
// its accessor kind so the join leaves the fixed mutability alone.
func TestCollectDeclarationsSymbolsAndAccessors(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface Map<K, V> {
    get size(): number;
    set size(value: number);
    [Symbol.iterator](): IterableIterator<[K, V]>;
}
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`get instance Map.size (0)
set instance Map.size (1)
instance Map.[Symbol.iterator] (0)`))
}

// One spec algorithm sits behind an overload set, so every overload lands
// under the same triple, in declaration order.
func TestCollectDeclarationsCollapsesOverloads(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface String {
    replace(searchValue: string, replaceValue: string): string;
    replace(searchValue: RegExp, replacer: (substring: string) => string): string;
}
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance String.replace (2, 2)`))
}

// Overloads need not agree on how many parameters they take. They still
// collapse to one address, carrying a signature apiece in declaration order,
// which is what lets a fact's position-keyed parts resolve per overload.
//
// The two members below carry the shapes `lib.es5.d.ts` really declares for
// them: a trailing rest parameter on the longer `splice`, and a `resolve`
// overload that takes nothing at all.
func TestCollectDeclarationsOverloadsWithDifferentArities(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface Array<T> {
    splice(start: number, deleteCount?: number): T[];
    splice(start: number, deleteCount: number, ...items: T[]): T[];
}
interface PromiseConstructor {
    new <T>(executor: (resolve: (value: T) => void) => void): Promise<T>;
    resolve(): Promise<void>;
    resolve<T>(value: T): Promise<T>;
}
declare var Promise: PromiseConstructor;
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance Array.splice (2, 3+rest)
static Promise.resolve (0, 1)`))
}

// The point of keeping a signature per overload: a fact that names a parameter
// position applies only where that position exists. The shorter overload
// declares no parameter at position 1, so the value the algorithm hands back is
// one it cannot name and the claim drops to `unknown`.
//
// The owner is named Fixture because no builtin exercises this yet — every
// committed fact that returns a parameter returns position 0, which each of its
// overloads declares. See the note on joinFixture in internal/ecma262.
func TestOverloadArityResolvesFactsPerSignature(t *testing.T) {
	t.Parallel()

	mod, err := ConvertToStandaloneModule(parseLib(t, "lib.test.d.ts", `
interface Fixture {
    withTail(head: string): string;
    withTail(head: string, tail: string): string;
}
`).Module)
	require.NoError(t, err)

	tail := 1
	facts := &ecma262.Facts{
		SpecTarget: "test",
		Methods: map[string]ecma262.MethodFact{
			"Fixture.prototype.withTail": {
				Classified: ecma262.Coverage{Receiver: true, Returns: true},
				Receiver:   ecma262.RecvBorrow,
				Returns:    ecma262.AliasParam,
				ParamIndex: &tail,
			},
		},
	}
	report := ecma262.NewJoin(facts).Match(CollectDeclarations(mod))

	require.Len(t, report.Matched, 1)
	match := report.Matched[0]
	require.Equal(t, []ecma262.Signature{{Params: 1}, {Params: 2}}, match.Decl.Signatures)

	resolved := make([]string, 0, len(match.PerSignature))
	for _, fact := range match.PerSignature {
		resolved = append(resolved, fact.String())
	}
	require.Equal(t, []string{
		"receiver:borrow returns:unknown",
		"receiver:borrow returns:param(1)",
	}, resolved)

	// The algorithm-level claim is untouched by the per-overload resolution.
	require.Equal(t, "receiver:borrow returns:param(1)", match.Fact.String())
}

// A `this` parameter is a receiver annotation rather than an argument, so
// dropping it keeps the remaining positions aligned with the spec algorithm's
// own numbering.
func TestCollectDeclarationsDropsThisParam(t *testing.T) {
	t.Parallel()

	got := collectFrom(t, `
interface CallableFunction extends Function {
    apply<T, R>(this: (this: T) => R, thisArg: T): R;
}
`)
	snaps.MatchInlineSnapshot(t, got, snaps.Inline(`instance CallableFunction.apply (1)`))
}

// The join is the §5 gate: every std:* method the converter emits either
// resolves to a fact or is reported as unmatched, and nothing lands in both
// lists.
func TestStdDeclarationsJoinAgainstFacts(t *testing.T) {
	t.Parallel()

	inputs := []LibInput{parseLib(t, "lib.es5.d.ts", `
interface Array<T> {
    push(...items: T[]): number;
    [Symbol.iterator](): IterableIterator<T>;
}
interface ArrayConstructor {
    new <T>(arrayLength: number): T[];
    isArray(arg: any): arg is any[];
}
declare var Array: ArrayConstructor;
interface Math {
    max(...values: number[]): number;
}
declare var Math: Math;
declare function parseInt(string: string, radix?: number): number;
`)}
	result, err := PartitionLib(inputs)
	require.NoError(t, err)
	mods, err := ConvertBuckets(result)
	require.NoError(t, err)

	covered := ecma262.Coverage{Receiver: true, Returns: true}
	facts := &ecma262.Facts{
		SpecTarget: "test",
		Methods: map[string]ecma262.MethodFact{
			"Array.prototype.push":           {Classified: covered, Receiver: ecma262.RecvMutBorrow, Returns: ecma262.AliasFresh},
			"Array.prototype [ @@iterator ]": {Classified: covered, Receiver: ecma262.RecvBorrow, Returns: ecma262.AliasFresh},
			"Array.isArray":                  {Classified: covered, Receiver: ecma262.RecvNone, Returns: ecma262.AliasFresh},
			"Math.max":                       {Classified: covered, Receiver: ecma262.RecvNone, Returns: ecma262.AliasFresh},
			"Math.min":                       {Classified: covered, Receiver: ecma262.RecvNone, Returns: ecma262.AliasFresh},
			// A function the global object holds names no owner, so neither
			// side of the join can key it.
			"parseInt": {Classified: covered, Receiver: ecma262.RecvNone, Returns: ecma262.AliasFresh},
		},
	}
	report := ecma262.NewJoin(facts).Match(StdDeclarations(mods))

	var out strings.Builder
	require.NoError(t, ecma262.WriteJoinReport(report, &out))
	snaps.MatchInlineSnapshot(t, out.String(), snaps.Inline(`  join: 4 matched (4 with a receiver claim), 0 declarations without a fact, 1 facts without a declaration, 1 unkeyed declarations, 1 unjoinable facts
    no declaration: Math.min
    unkeyed declaration: parseInt
    unjoinable fact: parseInt
`))
}
