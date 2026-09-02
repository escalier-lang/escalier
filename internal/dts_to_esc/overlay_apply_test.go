package dts_to_esc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// overlayLib is the synthetic `.d.ts` input the overlay tests route. It
// holds one trio, which converts to `class Array`, one plain interface,
// which stays an interface, and one free function, so the overlay's
// member operations are exercised against each shape and the routing
// produces two packages rather than one.
const overlayLib = `
interface Array<T> { length: number; at(index: number): T | undefined; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; }
declare var Array: ArrayConstructor;
interface ArrayLike<T> { readonly length: number; }
declare function parseInt(string: string, radix?: number): number;
`

// overlayDocLib is overlayLib with a doc comment above the member the
// overlay tests replace. Prose is the bulk of what a TypeScript version
// bump moves, so what happens to it under a `replace` is worth pinning.
const overlayDocLib = `
interface Array<T> { length: number; /** Reads one element. */ at(index: number): T | undefined; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; }
declare var Array: ArrayConstructor;
interface ArrayLike<T> { readonly length: number; }
`

// convertWithOverlay folds an overlay tree into the converted
// overlayLib.
func convertWithOverlay(t *testing.T, files map[string]string) (map[string]*StandaloneModule, error) {
	t.Helper()
	return convertLibWithOverlay(t, overlayLib, files)
}

// convertLibWithOverlay routes one `.d.ts` input, converts every bucket,
// and folds the given overlay files in. It runs the three steps a
// generation runs before it writes anything to disk.
func convertLibWithOverlay(
	t *testing.T,
	lib string,
	files map[string]string,
) (map[string]*StandaloneModule, error) {
	t.Helper()
	overlay, err := LoadOverlay(seedOverlay(t, files))
	if err != nil {
		return nil, err
	}
	res, err := PartitionLibWithOverlay(
		[]LibInput{parseLib(t, "lib.es5.d.ts", lib)}, overlay)
	if err != nil {
		return nil, err
	}
	mods, err := ConvertBuckets(res)
	if err != nil {
		return nil, err
	}
	return mods, ApplyOverlay(mods, overlay)
}

// overlayModules is convertWithOverlay for a case expected to succeed.
func overlayModules(t *testing.T, files map[string]string) map[string]*StandaloneModule {
	t.Helper()
	mods, err := convertWithOverlay(t, files)
	require.NoError(t, err)
	return mods
}

// overlayError is convertWithOverlay for a case expected to fail, and
// returns the message.
func overlayError(t *testing.T, files map[string]string) string {
	t.Helper()
	_, err := convertWithOverlay(t, files)
	require.Error(t, err)
	return err.Error()
}

// renderPackage prints one converted package the way a generation writes
// it, minus the header.
func renderPackage(t *testing.T, mods map[string]*StandaloneModule, uri string) string {
	t.Helper()
	mod, ok := mods[uri]
	require.True(t, ok, "no converted module for %s", uri)
	text, err := RenderStandaloneModule(mod)
	require.NoError(t, err)
	return text
}

// TestApplyOverlay_Operations covers what each operation does to the
// converted package. The `replace` cases also pin substitution in place
// rather than append, which is what keeps a second run byte-identical.
func TestApplyOverlay_Operations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay map[string]string
		want    string
	}{
		{
			name: "add reaches a converted class",
			overlay: map[string]string{
				"std/array.add.esc": "export declare class Array<T> {\n" +
					"    static of<T>(...items: Array<T>) -> Array<T>,\n}\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    length: number,
    at(self, index: number) -> T | undefined,
    constructor(mut self),
    static isArray(arg: any) -> boolean,
    static of<T>(...items: Array<T>) -> Array<T>
}

export declare interface ArrayLike<T> {
    readonly length: number
}
`,
		},
		{
			name: "add reaches a converted interface",
			overlay: map[string]string{
				"std/array.add.esc": "export declare interface ArrayLike<T> {\n" +
					"    readonly first: T,\n}\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    length: number,
    at(self, index: number) -> T | undefined,
    constructor(mut self),
    static isArray(arg: any) -> boolean
}

export declare interface ArrayLike<T> {
    readonly length: number,
    readonly first: T
}
`,
		},
		{
			name: "add contributes a declaration no upstream source has",
			overlay: map[string]string{
				"std/array.add.esc": "@js(\"Symbol.iterator\")\n" +
					"export declare val iteratorKey: unique symbol\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    length: number,
    at(self, index: number) -> T | undefined,
    constructor(mut self),
    static isArray(arg: any) -> boolean
}

export declare interface ArrayLike<T> {
    readonly length: number
}

@js("Symbol.iterator")
export declare val iteratorKey: unique symbol
`,
		},
		{
			name: "replace substitutes a member at its own position",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    at(self, index: number) -> T,\n}\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    length: number,
    at(self, index: number) -> T,
    constructor(mut self),
    static isArray(arg: any) -> boolean
}

export declare interface ArrayLike<T> {
    readonly length: number
}
`,
		},
		{
			name: "replace stands in for a whole declaration of another kind",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare type ArrayLike<T> = { length: number }\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    length: number,
    at(self, index: number) -> T | undefined,
    constructor(mut self),
    static isArray(arg: any) -> boolean
}

export declare type ArrayLike<T> = {
    length: number
}
`,
		},
		{
			name: "drop keeps a member out of the output",
			overlay: map[string]string{
				"std/array.drop.esc": "export declare interface Array {\n" +
					"    at: unknown,\n}\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    length: number,
    constructor(mut self),
    static isArray(arg: any) -> boolean
}

export declare interface ArrayLike<T> {
    readonly length: number
}
`,
		},
		{
			name: "drop keeps a whole declaration out of the output",
			overlay: map[string]string{
				"std/array.drop.esc": "export declare val ArrayLike\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    length: number,
    at(self, index: number) -> T | undefined,
    constructor(mut self),
    static isArray(arg: any) -> boolean
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want,
				renderPackage(t, overlayModules(t, tt.overlay), "std:array"))
		})
	}
}

// TestApplyOverlay_RootDropKeepsASymbolOutOfEveryPackage covers the one
// operation that resolves during routing rather than against a
// converted module.
func TestApplyOverlay_RootDropKeepsASymbolOutOfEveryPackage(t *testing.T) {
	t.Parallel()
	mods := overlayModules(t, map[string]string{
		"drop.esc": "export declare val parseInt\n",
	})
	require.Contains(t, mods, "std:array")
	require.NotContains(t, mods, "std:number",
		"parseInt is the only declaration routing to std:number")
}

// TestApplyOverlay_RejectsAnOverlayTheUpstreamSourceNoLongerBacks is the
// TypeScript-side-removal signal. A generation overwrites the tree
// without reading it, so a stale overlay entry is the only place a
// removal can be caught.
func TestApplyOverlay_RejectsAnOverlayTheUpstreamSourceNoLongerBacks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay map[string]string
		want    string
	}{
		{
			name:    "root drop naming a symbol no lib file declares",
			overlay: map[string]string{"drop.esc": "export declare val eval\n"},
			want:    "overlay: drop.esc drops eval, which no lib.*.d.ts file declares",
		},
		{
			name: "drop naming an absent declaration",
			overlay: map[string]string{
				"std/array.drop.esc": "export declare val ReadonlyArray\n",
			},
			want: "overlay: std/array.drop.esc drops ReadonlyArray, which std:array does not declare",
		},
		{
			name: "drop naming an absent member",
			overlay: map[string]string{
				"std/array.drop.esc": "export declare interface Array {\n    sort: unknown,\n}\n",
			},
			want: "overlay: std/array.drop.esc drops Array.sort, which the converted " +
				"declaration does not have",
		},
		{
			name: "replace naming an absent declaration",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare val ReadonlyArray\n",
			},
			want: "overlay: std/array.replace.esc replaces ReadonlyArray, which std:array does not declare",
		},
		{
			name: "replace naming an absent member",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    sort(mut self) -> Self,\n}\n",
			},
			want: "overlay: std/array.replace.esc replaces Array.sort, which the converted " +
				"declaration does not have",
		},
		{
			name: "add colliding with a converted member",
			overlay: map[string]string{
				"std/array.add.esc": "export declare class Array<T> {\n" +
					"    at(mut self, index: number) -> T,\n}\n",
			},
			want: "overlay: std/array.add.esc adds Array.at, which the converted declaration " +
				"already has; correct it with a replace overlay instead",
		},
		{
			name: "add colliding with a converted declaration",
			overlay: map[string]string{
				"std/array.add.esc": "export declare val ArrayLike\n",
			},
			want: "overlay: std/array.add.esc adds the interface ArrayLike, which std:array " +
				"already declares; correct an existing declaration with a replace overlay",
		},
		{
			name: "replace on a package nothing routes to",
			overlay: map[string]string{
				"std/date.replace.esc": "export declare val Date\n",
			},
			want: "overlay: std/date.replace.esc replaces in std:date, which no upstream " +
				"declaration routes to",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, overlayError(t, tt.overlay))
		})
	}
}

// TestApplyOverlay_AddCreatesAPackageNothingRoutesTo keeps an addition from
// being silently lost when no upstream declaration lands in the package
// it names.
func TestApplyOverlay_AddCreatesAPackageNothingRoutesTo(t *testing.T) {
	t.Parallel()
	mods := overlayModules(t, map[string]string{
		"std/date.add.esc": "@js(\"Date.now\")\nexport declare fn now() -> number\n",
	})
	require.Equal(t, `@js("Date.now")
export declare fn now() -> number
`, renderPackage(t, mods, "std:date"))
}

// TestApplyOverlay_ReplaceKeepsTheConvertedMemberDoc covers the prose a
// member operation would otherwise drop. The overlay states the shape
// and the upstream documentation of that member still reaches the
// output, so a doc edit upstream lands even under a `replace`.
func TestApplyOverlay_ReplaceKeepsTheConvertedMemberDoc(t *testing.T) {
	t.Parallel()
	mods, err := convertLibWithOverlay(t, overlayDocLib, map[string]string{
		"std/array.replace.esc": "export declare class Array<T> {\n" +
			"    at(self, index: number) -> T,\n}\n",
	})
	require.NoError(t, err)
	require.Equal(t, `@js("Array")
export declare class Array<T> {
    length: number,
    /** Reads one element. */
    at(self, index: number) -> T,
    constructor(mut self),
    static isArray(arg: any) -> boolean
}

export declare interface ArrayLike<T> {
    readonly length: number
}
`, renderPackage(t, mods, "std:array"))
}

// overlayKindLib converts to a class holding a getter and a setter under
// one name plus a two-signature overload set. Those are the two shapes a
// member key has to tell apart: one name over several kinds, and one
// name over several signatures of one kind.
const overlayKindLib = `
interface Array<T> { get size(): number; set size(v: number); get first(): T; find(x: number): T; find(x: string): T; }
interface ArrayConstructor { new <T>(): Array<T>; }
declare var Array: ArrayConstructor;
`

// overlayKindModules folds an overlay into the converted overlayKindLib
// for a case expected to succeed.
func overlayKindModules(t *testing.T, files map[string]string) map[string]*StandaloneModule {
	t.Helper()
	mods, err := convertLibWithOverlay(t, overlayKindLib, files)
	require.NoError(t, err)
	return mods
}

// overlayKindError is overlayKindModules for a case expected to fail,
// and returns the message.
func overlayKindError(t *testing.T, files map[string]string) string {
	t.Helper()
	_, err := convertLibWithOverlay(t, overlayKindLib, files)
	require.Error(t, err)
	return err.Error()
}

// TestApplyOverlay_KeysAMemberOnItsKind pins the half of the member key
// that is not the name. A `readonly x: T` and a `get x()` are two
// members, so an overlay addresses one without disturbing the other and
// cannot turn one into the other by writing the name in a new form.
func TestApplyOverlay_KeysAMemberOnItsKind(t *testing.T) {
	t.Parallel()
	require.Equal(t, `@js("Array")
export declare class Array<T> {
    get size(self) -> number | undefined,
    set size(mut self, v: number) -> undefined,
    get first(self) -> T,
    find(self, x: number) -> T,
    find(self, x: string) -> T,
    constructor(mut self)
}
`, renderPackage(t, overlayKindModules(t, map[string]string{
		"std/array.replace.esc": "export declare class Array<T> {\n" +
			"    get size(self) -> number | undefined,\n}\n",
	}), "std:array"))
}

// TestApplyOverlay_RejectsAKindChange covers the two ways an overlay can
// write a name the converted declaration holds under another kind.
// Neither substitutes across kinds, since that would retype a member
// under cover of replacing it.
func TestApplyOverlay_RejectsAKindChange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay map[string]string
		want    string
	}{
		{
			name: "replace writing a getter as a method",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    size(self) -> number,\n}\n",
			},
			want: "overlay: std/array.replace.esc replaces Array.size as a method, which " +
				"the converted declaration declares as a getter and a setter; drop the " +
				"member and add the new form to change its kind",
		},
		{
			name: "replace writing the half of an accessor the declaration lacks",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    set first(mut self, v: T),\n}\n",
			},
			want: "overlay: std/array.replace.esc replaces Array.first as a setter, " +
				"which the converted declaration declares only as a getter; " +
				"contribute the setter with an add overlay instead",
		},
		{
			name: "add writing a getter as a field",
			overlay: map[string]string{
				"std/array.add.esc": "export declare class Array<T> {\n" +
					"    size: number,\n}\n",
			},
			want: "overlay: std/array.add.esc adds Array.size as a field, which the " +
				"converted declaration declares as a getter and a setter; drop the " +
				"member and add the new form to change its kind",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, overlayKindError(t, tt.overlay))
		})
	}
}

// TestApplyOverlay_ReplaceRestatesTheWholeOverloadSet covers the rule a
// name-shaped key forces. `find` addresses both of the converted
// signatures, so an overlay that restates one of them would drop the
// other without saying so.
func TestApplyOverlay_ReplaceRestatesTheWholeOverloadSet(t *testing.T) {
	t.Parallel()
	t.Run("restating every signature substitutes the set", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, `@js("Array")
export declare class Array<T> {
    get size(self) -> number,
    set size(mut self, v: number) -> undefined,
    get first(self) -> T,
    find(self, x: number) -> T | undefined,
    find(self, x: string) -> T | undefined,
    constructor(mut self)
}
`, renderPackage(t, overlayKindModules(t, map[string]string{
			"std/array.replace.esc": "export declare class Array<T> {\n" +
				"    find(self, x: number) -> T | undefined,\n" +
				"    find(self, x: string) -> T | undefined,\n}\n",
		}), "std:array"))
	})

	t.Run("writing one name as two fields fails", func(t *testing.T) {
		t.Parallel()
		require.Equal(t,
			"overlay: std/array.replace.esc replaces Array.length twice as a field; "+
				"only signatures overload",
			overlayError(t, map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    length: number,\n    length: string,\n}\n",
			}))
	})

	t.Run("restating one signature fails and names the member", func(t *testing.T) {
		t.Parallel()
		require.Equal(t,
			"overlay: std/array.replace.esc replaces 1 of the 2 signatures of "+
				"Array.find; a replace restates the whole overload set, since a name "+
				"is what addresses it",
			overlayKindError(t, map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    find(self, x: number) -> T | undefined,\n}\n",
			}))
	})
}

// TestApplyOverlay_AddContributesAnOverloadSet covers a name the
// converted declaration has no signature of. The overlay writes each
// signature as its own member, the way the declaration itself does.
func TestApplyOverlay_AddContributesAnOverloadSet(t *testing.T) {
	t.Parallel()
	require.Equal(t, `@js("Array")
export declare class Array<T> {
    length: number,
    at(self, index: number) -> T | undefined,
    constructor(mut self),
    static isArray(arg: any) -> boolean,
    indexOf(self, item: T) -> number,
    indexOf(self, item: T, from: number) -> number
}

export declare interface ArrayLike<T> {
    readonly length: number
}
`, renderPackage(t, overlayModules(t, map[string]string{
		"std/array.add.esc": "export declare class Array<T> {\n" +
			"    indexOf(self, item: T) -> number,\n" +
			"    indexOf(self, item: T, from: number) -> number,\n}\n",
	}), "std:array"))
}

// TestApplyOverlay_AddsTheOtherHalfOfAnAccessor covers the one pair of
// members that share a name. The converted declaration holds the getter
// for `first` alone, so the setter is an addition rather than a kind
// change.
func TestApplyOverlay_AddsTheOtherHalfOfAnAccessor(t *testing.T) {
	t.Parallel()
	require.Equal(t, `@js("Array")
export declare class Array<T> {
    get size(self) -> number,
    set size(mut self, v: number) -> undefined,
    get first(self) -> T,
    find(self, x: number) -> T,
    find(self, x: string) -> T,
    constructor(mut self),
    set first(mut self, v: T)
}
`, renderPackage(t, overlayKindModules(t, map[string]string{
		"std/array.add.esc": "export declare class Array<T> {\n" +
			"    set first(mut self, v: T),\n}\n",
	}), "std:array"))
}

// TestApplyOverlay_RejectsAddingOneNameAsTwoMembers covers the clash an
// overlay file can only have with itself. Neither member is in the
// converted declaration, so the checks against it pass and the file's own
// members are what carry the clash.
func TestApplyOverlay_RejectsAddingOneNameAsTwoMembers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay string
		want    string
	}{
		{
			name: "a field and a getter",
			overlay: "export declare class Array<T> {\n" +
				"    first: T,\n    get first(self) -> T,\n}\n",
			want: "overlay: std/array.add.esc adds Array.first as a getter beside a " +
				"field it adds under the same name; one name holds one member, or a " +
				"getter and a setter",
		},
		{
			name: "one name as two fields",
			overlay: "export declare class Array<T> {\n" +
				"    first: T,\n    first: number,\n}\n",
			want: "overlay: std/array.add.esc adds Array.first twice as a field; " +
				"only signatures overload",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want,
				overlayError(t, map[string]string{"std/array.add.esc": tt.overlay}))
		})
	}
}

// overlaySideLib converts to a class holding `of` on both sides, one
// instance member and one static. Which side a member lives on is the
// part of its key that neither its name nor its kind carries.
const overlaySideLib = `
interface Array<T> { of(x: number): T; at(x: number): T; }
interface ArrayConstructor { new <T>(): Array<T>; of(x: string): Array<any>; }
declare var Array: ArrayConstructor;
`

// overlaySideModules folds an overlay into the converted overlaySideLib
// for a case expected to succeed.
func overlaySideModules(t *testing.T, files map[string]string) map[string]*StandaloneModule {
	t.Helper()
	mods, err := convertLibWithOverlay(t, overlaySideLib, files)
	require.NoError(t, err)
	return mods
}

// TestApplyOverlay_KeysAMemberOnItsSideOfTheClass covers the one name
// that reaches two members without either being an accessor. `Array.of`
// is an instance member and a static one, so an overlay addresses each
// on its own and neither operation disturbs the other side.
func TestApplyOverlay_KeysAMemberOnItsSideOfTheClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay map[string]string
		want    string
	}{
		{
			name: "replace reaches the instance member alone",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    of(mut self, x: number) -> T | undefined,\n}\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    of(mut self, x: number) -> T | undefined,
    at(self, x: number) -> T,
    constructor(mut self),
    static of(x: string) -> Array<any>
}
`,
		},
		{
			name: "replace reaches the static member alone",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare class Array<T> {\n" +
					"    static of(x: string) -> Array<T>,\n}\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    of(mut self, x: number) -> T,
    at(self, x: number) -> T,
    constructor(mut self),
    static of(x: string) -> Array<T>
}
`,
		},
		{
			name: "add contributes the static half of a name the instance side holds",
			overlay: map[string]string{
				"std/array.add.esc": "export declare class Array<T> {\n" +
					"    static at(x: number) -> T,\n}\n",
			},
			want: `@js("Array")
export declare class Array<T> {
    of(mut self, x: number) -> T,
    at(self, x: number) -> T,
    constructor(mut self),
    static of(x: string) -> Array<any>,
    static at(x: number) -> T
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want,
				renderPackage(t, overlaySideModules(t, tt.overlay), "std:array"))
		})
	}
}

// TestApplyOverlay_ReportsWhichSideAMemberIsMissingFrom keeps a report
// from naming a member that visibly exists. `at` is on the class, so a
// report of a missing `Array.at` would send a contributor looking at the
// member they can already see rather than at the side they wrote.
func TestApplyOverlay_ReportsWhichSideAMemberIsMissingFrom(t *testing.T) {
	t.Parallel()
	_, err := convertLibWithOverlay(t, overlaySideLib, map[string]string{
		"std/array.replace.esc": "export declare class Array<T> {\n" +
			"    static at(x: number) -> T,\n}\n",
	})
	require.EqualError(t, err,
		"overlay: std/array.replace.esc replaces static Array.at, which the "+
			"converted declaration does not have")
}

// TestApplyOverlay_HoldsAMemberOperationToTheConvertedTypeParameters
// covers the header a member `add` or `replace` does not get to change.
// The converted declaration keeps its own, so an overlay binding other
// names would leave its members referring to nothing.
func TestApplyOverlay_HoldsAMemberOperationToTheConvertedTypeParameters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay map[string]string
		want    string
	}{
		{
			name: "replace binding another name",
			overlay: map[string]string{
				"std/array.replace.esc": "export declare class Array<U> {\n" +
					"    at(self, index: number) -> U,\n}\n",
			},
			want: "overlay: std/array.replace.esc writes Array<U>, which the converted " +
				"declaration binds as Array<T>; a member operation keeps the converted " +
				"declaration's type parameters, so the overlay restates them as they are",
		},
		{
			name: "add binding none at all",
			overlay: map[string]string{
				"std/array.add.esc": "export declare interface ArrayLike {\n" +
					"    readonly first: unknown,\n}\n",
			},
			want: "overlay: std/array.add.esc writes ArrayLike, which the converted " +
				"declaration binds as ArrayLike<T>; a member operation keeps the " +
				"converted declaration's type parameters, so the overlay restates " +
				"them as they are",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, overlayError(t, tt.overlay))
		})
	}
}

// TestApplyOverlay_RejectsAHeaderAMemberOperationWouldDrop covers what
// an overlay writes around its members. The converted declaration keeps
// its own header, so a clause the merge would not read is a report
// rather than a silent omission.
func TestApplyOverlay_RejectsAHeaderAMemberOperationWouldDrop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		overlay map[string]string
		want    string
	}{
		{
			name: "an extends clause on an interface",
			overlay: map[string]string{
				"std/array.add.esc": "export declare interface ArrayLike<T> " +
					"extends Iterable<T> {\n    readonly first: T,\n}\n",
			},
			want: "overlay: std/array.add.esc writes an extends clause on ArrayLike, " +
				"which a member operation does not read; the converted declaration " +
				"keeps its own header, so drop it from the overlay",
		},
		{
			name: "a decorator on a class",
			overlay: map[string]string{
				"std/array.replace.esc": "@js(\"Array\")\n" +
					"export declare class Array<T> {\n" +
					"    at(self, index: number) -> T,\n}\n",
			},
			want: "overlay: std/array.replace.esc writes a decorator on Array, which " +
				"a member operation does not read; the converted declaration keeps " +
				"its own header, so drop it from the overlay",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, overlayError(t, tt.overlay))
		})
	}
}
