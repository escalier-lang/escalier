package dts_to_esc

import (
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// borrowDefaultsTypes declares the types the TestApplyBorrowDefaults cases name.
// `Flat` holds only value types and `Nested` holds an object. `Callback` is a
// function type written as an interface, `Mode` an alias to a union of
// literals, and `Pair` an alias to an object type.
const borrowDefaultsTypes = `
interface Flat { a: number; b?: string; }
interface Nested { inner: Flat; }
interface Callback { (x: number): void; }
type Mode = "a" | "b";
type Pair = { left: Nested; right: Nested };
`

// TestApplyBorrowDefaults covers which parameters and returns applyBorrowDefaults
// rewrites in a declared function. A parameter of an object type becomes `&mut`, and
// every other parameter keeps the type it converts to. A return of an object type
// borrows each borrowed parameter it may share structure with.
func TestApplyBorrowDefaults(t *testing.T) {
	t.Parallel()

	tests := map[string]interopCase{
		"ValueTypeParams": {
			src: `declare function f(a: string, b: (x: number) => void, c: Promise<Flat>, d: Callback, e: Mode): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: string, b: fn (x: number) -> unknown, c: Promise<Flat>, d: Callback, e: Mode) -> undefined",
			},
		},
		"UncertainParamsStayOwned": {
			src: `declare function f<T>(a: T, b: unknown, c: Flat | string, ...rest: Flat[]): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<T>(a: T, b: unknown, c: Flat | string, ...rest: Array<Flat>) -> undefined",
			},
		},
		"ObjectParams": {
			src: `declare function f(a: Flat, b: {x: number}, c: [number, string], d: Pair, e?: Nested): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: &mut Flat, b: &mut {\n    x: number\n}, c: &mut [number, string], d: &mut Pair, e?: &mut Nested) -> undefined",
			},
		},
		"FlatParamDoesNotShareWithReturn": {
			src: `declare function f(a: Flat): Nested;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: &mut Flat) -> Nested",
			},
		},
		"FlatParamOfReturnTypeSharesWithReturn": {
			src: `declare function f(a: Flat): Flat;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut Flat) -> &'a mut Flat",
			},
		},
		"OnlyParamsThatMayShareTakeTheLifetime": {
			src: `declare function f(a: Flat, b: Nested): Pair;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &mut Flat, b: &'a mut Nested) -> &'a mut Pair",
			},
		},
		"NullableReturnBorrowsItsObjectType": {
			src: `declare function f(a: Nested): Nested | null;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut Nested) -> &'a mut Nested | null",
			},
		},
		"NamespaceQualifiedParam": {
			src: `declare namespace NS { interface Opts { a: number; } }
declare function f(o: NS.Opts): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(o: &mut NS.Opts) -> undefined",
			},
		},
		// Each parameter type reaches only value-type slots, through an intersection, an
		// alias, an inherited interface, a getter or a superclass. None declares the `inner`
		// member `Nested` requires, so none may be the return.
		"FlatShapesDoNotShareWithReturn": {
			src: `type FlatPair = Flat & {c: boolean};
type FlatAlias = Flat;
interface FlatChild extends Flat { c: boolean; }
interface FlatGetter { get a(): number; }
declare class FlatClass { a: number; static s: Nested; get b(): string; }
declare class FlatSub extends FlatClass { c: boolean; }
declare function f(b: FlatPair, c: FlatAlias, d: FlatChild, e: FlatGetter, h: FlatSub): Nested;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(b: &mut FlatPair, c: &mut FlatAlias, d: &mut FlatChild, e: &mut FlatGetter, h: &mut FlatSub) -> Nested",
			},
		},
		// A tuple and a mapped type reach only value-type slots, but the converter does not
		// list their members by name. Either may declare `inner`, so either may be the
		// return.
		"FlatShapesWithUnlistedMembersShareWithReturn": {
			src: `type FlatMapped = {[K in "a" | "b"]: number};
declare function f(a: [number, string], g: FlatMapped): Nested;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut [number, string], g: &'a mut FlatMapped) -> &'a mut Nested",
			},
		},
		// Each parameter type reaches an object or a type parameter through one slot.
		"ShapesWithAnObjectSlotShareWithReturn": {
			src: `type NestedPair = Flat & {n: Nested};
interface NestedChild extends Nested { c: boolean; }
interface Generic<T> { a: T; }
declare class Holder { n: Nested; }
declare class HolderSub extends Holder { c: boolean; }
declare class TypedGetter { get n(): Nested; }
declare function f(a: [Nested], b: NestedPair, c: NestedChild, d: Generic<number>, e: HolderSub, g: TypedGetter): Nested;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut [Nested], b: &'a mut NestedPair, c: &'a mut NestedChild, d: &'a mut Generic<number>, e: &'a mut HolderSub, g: &'a mut TypedGetter) -> &'a mut Nested",
			},
		},
		// Each parameter type holds only value types but declares every member `Flat`
		// requires, through an alias, an inherited interface or its own members. The
		// function may return the argument itself.
		"FlatParamThatMayBeTheReturnShares": {
			src: `type FlatAlias = Flat;
interface FlatChild extends Flat { c: boolean; }
declare function f(a: FlatAlias): Flat;
declare function g(a: FlatChild): Flat;
declare function h(a: {a: number; c: boolean}): Flat;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut FlatAlias) -> &'a mut Flat",
				"@js(\"g\")\nexport declare fn g<'a>(a: &'a mut FlatChild) -> &'a mut Flat",
				"@js(\"h\")\nexport declare fn h<'a>(a: &'a mut {\n    a: number,\n    c: boolean\n}) -> &'a mut Flat",
			},
		},
		// Each class declares no members of its own. `FlatImpl` takes the members `Flat`
		// requires from the interface it implements, so it may be the return.
		// `NestedImpl` takes an object slot from `Nested`, so it shares structure with
		// any object-type return.
		"ClassMembersFromImplements": {
			src: `declare class FlatImpl implements Flat {}
declare class NestedImpl implements Nested {}
declare function f(a: FlatImpl): Flat;
declare function g(a: NestedImpl): Flat;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut FlatImpl) -> &'a mut Flat",
				"@js(\"g\")\nexport declare fn g<'a>(a: &'a mut NestedImpl) -> &'a mut Flat",
			},
		},
		// `Flat` lacks the `m` method `WithMethod` requires, so the argument cannot be
		// the return.
		"FlatParamMissingAReturnMemberDoesNotShare": {
			src: `interface WithMethod { a: number; m(): void; }
declare function f(a: Flat): WithMethod;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: &mut Flat) -> WithMethod",
			},
		},
		"ValueTypeReturnStaysOwned": {
			src: `declare function f(a: Nested): Promise<Nested>;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: &mut Nested) -> Promise<Nested>",
			},
		},
	}
	for name, test := range tests {
		test.src = borrowDefaultsTypes + test.src
		tests[name] = test
	}
	runInteropCases(t, tests)
}

// TestApplyBorrowDefaultsClassMembers covers the class members applyBorrowDefaults
// rewrites. An instance method and a static method take the same defaults as a
// function. A constructor and a setter keep their parameters owned.
func TestApplyBorrowDefaultsClassMembers(t *testing.T) {
	t.Parallel()

	lib := parseLib(t, "lib.widget.d.ts", borrowDefaultsTypes+`
interface Widget {
    take(n: Nested): Nested;
    set value(v: Nested);
}
interface WidgetConstructor {
    new (n: Nested): Widget;
    readonly prototype: Widget;
    from(n: Nested): Widget;
}
declare var Widget: WidgetConstructor;
`)
	mod, err := ConvertBucket(mergeDecls(lib.Module.Statements), nil)
	require.NoError(t, err)

	rootNS, ok := mod.Module.Namespaces.Get("")
	require.True(t, ok)
	var class *ast.ClassDecl
	for _, decl := range rootNS.Decls {
		if d, ok := decl.(*ast.ClassDecl); ok && d.Name.Name == "Widget" {
			class = d
		}
	}
	require.NotNil(t, class)
	printed, err := printer.Print(class, printer.DefaultOptions())
	require.NoError(t, err)
	snaps.MatchInlineSnapshot(t, printed, snaps.Inline(`@js("Widget")
export declare class Widget {
    take<'a>(&mut self, n: &'a mut Nested) -> &'a mut Nested,
    set value(&mut self, v: Nested),
    constructor(&mut self, n: Nested),
    static readonly prototype: Widget,
    static from<'a>(n: &'a mut Nested) -> &'a mut Widget
}`))
}

// walkMembersTypes declares the types the TestWalkMembers cases name. `Loop1` and
// `Loop2` extend each other.
const walkMembersTypes = `
interface Base { a: number; b?: string; }
interface Child extends Base { c: boolean; }
declare class Impl implements Base { d: number; static s: Base; }
interface Loop1 extends Loop2 { x: number; }
interface Loop2 extends Loop1 { y: number; }
type Ident<T> = T;
`

// TestWalkMembers covers the types walkMembers lists members for and the ones it gives
// up on, through memberNames and holdsOnlyValueTypes. Each case names a type with
// `type Probe = …`. members renders memberNames as the names it lists, with a `?` on
// an optional one, or "unlisted" when memberNames reports false.
func TestWalkMembers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		probe      string
		members    string
		onlyValues bool
	}{
		"Interface":    {probe: "Base", members: "a b?", onlyValues: true},
		"Extends":      {probe: "Child", members: "a b? c", onlyValues: true},
		"Implements":   {probe: "Impl", members: "a b? d", onlyValues: true},
		"Intersection": {probe: "Base & {e: Base}", members: "a b? e", onlyValues: false},
		"Cycle":        {probe: "Loop1", members: "unlisted", onlyValues: false},
		"Union":        {probe: "Base | Child", members: "unlisted", onlyValues: false},
		"Tuple":        {probe: "[number, string]", members: "unlisted", onlyValues: true},
		"EmptyTuple":   {probe: "[]", members: "unlisted", onlyValues: true},
		"TypeParam":    {probe: "Ident<Base>", members: "unlisted", onlyValues: false},
		"Undeclared":   {probe: "Missing", members: "unlisted", onlyValues: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lib := parseLib(t, "lib.walk.d.ts", walkMembersTypes+"type Probe = "+tc.probe+";\n")
			mod, err := ConvertBucket(mergeDecls(lib.Module.Statements), nil)
			require.NoError(t, err)
			idx := newTypeIndex(mod)
			probe, ok := idx.aliases["Probe"]
			require.True(t, ok)

			members, listed := idx.memberNames(probe.TypeAnn, set.NewSet[string](), set.NewSet[string]())
			got := "unlisted"
			if listed {
				var names []string
				for _, name := range members.all.ToSlice() {
					if !members.required.Contains(name) {
						name += "?"
					}
					names = append(names, name)
				}
				sort.Strings(names)
				got = strings.Join(names, " ")
			}
			require.Equal(t, tc.members, got)
			require.Equal(t, tc.onlyValues, idx.holdsOnlyValueTypes(probe.TypeAnn, set.NewSet[string](), set.NewSet[string]()))
		})
	}
}
