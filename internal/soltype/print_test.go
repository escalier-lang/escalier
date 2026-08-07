package soltype

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

func numP() *PrimType  { return &PrimType{Prim: NumPrim} }
func strP() *PrimType  { return &PrimType{Prim: StrPrim} }
func boolP() *PrimType { return &PrimType{Prim: BoolPrim} }

func identP(name string, t Type) *FuncParam {
	return &FuncParam{Pattern: &IdentPat{Name: name}, Type: t}
}

func optP(name string, t Type) *FuncParam {
	return &FuncParam{Pattern: &IdentPat{Name: name}, Type: t, Optional: true}
}

func restP(name string, t Type) *FuncParam {
	return &FuncParam{Pattern: &IdentPat{Name: name}, Type: t, Rest: true}
}

// TestPrintRoundTrips covers the short, stable round-trips for the M1 coalesced
// type set: primitives, literals, the lattice bounds, tuples, multi-arg
// functions, and multi-element unions/intersections. Per CLAUDE.md these are the
// short stable strings, so they use require.Equal; the richer nested shapes
// (which exercise precedence parenthesization) use MatchInlineSnapshot below.
func TestPrintRoundTrips(t *testing.T) {
	tests := []struct {
		name string
		in   Type
		want string
	}{
		// Primitives.
		{"number", numP(), "number"},
		{"string", strP(), "string"},
		{"boolean", boolP(), "boolean"},

		// Literals.
		{"num literal", &LitType{Lit: &NumLit{Value: 5}}, "5"},
		{"str literal", &LitType{Lit: &StrLit{Value: "hello"}}, `"hello"`},
		{"bool literal", &LitType{Lit: &BoolLit{Value: true}}, "true"},

		// Lattice bounds, the absence atoms, and the error-recovery sentinel.
		{"never", &NeverType{}, "never"},
		{"unknown", &UnknownType{}, "unknown"},
		{"undefined", &UndefinedType{}, "undefined"},
		{"error", &ErrorType{}, "error"}, // PR8 recovery sentinel

		// Tuples.
		{"empty tuple", &TupleType{}, "[]"},
		{"pair tuple", &TupleType{Elems: []Type{numP(), strP()}}, "[number, string]"},
		{
			// An inexact tuple renders a trailing `...`, mirroring inexact objects.
			"inexact tuple renders trailing ...",
			&TupleType{Elems: []Type{numP()}, Inexact: true},
			"[number, ...]",
		},
		{"inexact empty tuple", &TupleType{Inexact: true}, "[...]"},

		// Objects.
		{"empty object", &ObjectType{}, "{}"},
		{
			"two-property object",
			&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: numP()}, &PropertyElem{Name: "b", Type: strP()}}},
			"{a: number, b: string}",
		},
		{
			// A property name that isn't a valid identifier (e.g. from a string-literal
			// key) is quoted so the rendered object stays parseable.
			"non-identifier property name is quoted",
			&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a-b", Type: numP()}}},
			`{"a-b": number}`,
		},
		{
			// An inexact object renders a trailing `...`, mirroring inexact functions.
			"inexact object renders trailing ...",
			&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: numP()}}, Inexact: true},
			"{a: number, ...}",
		},
		{
			"inexact empty object",
			&ObjectType{Inexact: true},
			"{...}",
		},
		{
			// An optional property renders `x?: T`.
			"optional property renders ?",
			&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: numP(), Optional: true}}},
			"{a?: number}",
		},
		{
			// A readonly property renders a leading `readonly ` prefix.
			"readonly property renders readonly",
			&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: numP(), Readonly: true}}},
			"{readonly a: number}",
		},

		// Functions. A bare (exact) function renders with no trailing marker; an
		// inexact one renders a trailing `...`, and an optional param renders `x?: T`.
		{"nullary fn", &FuncType{Ret: numP()}, "fn () -> number"},
		{"unary fn", &FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()}, "fn (x: number) -> string"},
		{
			"multi-arg fn",
			&FuncType{Params: []*FuncParam{identP("a", numP()), identP("b", strP())}, Ret: boolP()},
			"fn (a: number, b: string) -> boolean",
		},
		{"inexact nullary fn", &FuncType{Ret: numP(), Inexact: true}, "fn (...) -> number"},
		{
			"inexact fn with params",
			&FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP(), Inexact: true},
			"fn (x: number, ...) -> string",
		},
		{
			"optional param renders with ?",
			&FuncType{Params: []*FuncParam{identP("a", numP()), optP("b", strP())}, Ret: boolP()},
			"fn (a: number, b?: string) -> boolean",
		},
		{
			"rest param renders with ...",
			&FuncType{Params: []*FuncParam{identP("a", numP()), restP("rest", strP())}, Ret: boolP()},
			"fn (a: number, ...rest: string) -> boolean",
		},

		// A throws clause renders after the return type. A function that raises nothing
		// spells that either as a nil Throws or as `never`, and neither renders a clause.
		{"fn with throws", &FuncType{Ret: numP(), Throws: strP()}, "fn () -> number throws string"},
		{
			"fn with throws and params",
			&FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: numP(), Throws: strP()},
			"fn (x: number) -> number throws string",
		},
		{
			"fn throwing a union",
			&FuncType{Ret: numP(), Throws: &UnionType{Types: []Type{numP(), strP()}}},
			"fn () -> number throws number | string",
		},
		{"fn with never throws renders no clause", &FuncType{Ret: numP(), Throws: &NeverType{}}, "fn () -> number"},
		{
			// `-> R` is greedy, so a function-typed return is parenthesized once a clause
			// follows it. Without the parens this and the next case would render alike and
			// re-read as the next one.
			"fn-typed return with throws parenthesizes the return",
			&FuncType{Ret: &FuncType{Ret: numP()}, Throws: strP()},
			"fn () -> (fn () -> number) throws string",
		},
		{
			"fn-typed return whose own throws is the clause needs no parens",
			&FuncType{Ret: &FuncType{Ret: numP(), Throws: strP()}},
			"fn () -> fn () -> number throws string",
		},
		{
			// `throws` terminates the return type, so a union return needs no parens.
			"union return with throws stays bare",
			&FuncType{Ret: &UnionType{Types: []Type{numP(), strP()}}, Throws: strP()},
			"fn () -> number | string throws string",
		},
		{
			// A throwing function nested in a union is parenthesized by its own precedence,
			// so the clause cannot run on into the sibling member.
			"throwing fn inside a union",
			&UnionType{Types: []Type{&FuncType{Ret: numP(), Throws: strP()}, boolP()}},
			"(fn () -> number throws string) | boolean",
		},

		// Unions and intersections.
		{"union pair", &UnionType{Types: []Type{numP(), strP()}}, "number | string"},
		{"union triple", &UnionType{Types: []Type{numP(), strP(), boolP()}}, "number | string | boolean"},
		// An inexact union renders a trailing `...` entry.
		{"inexact union", &UnionType{Types: []Type{numP(), strP()}, Inexact: true}, "number | string | ..."},
		// NullType renders as `null`. It is a distinct atomic kind from UndefinedType.
		{"null atom", &NullType{}, "null"},
		{"intersection pair", &IntersectionType{Types: []Type{numP(), strP()}}, "number & string"},

		// Promises (M3). A rejecting promise renders its Err as a second argument
		// (M9 PR10c); a nil or `never` Err is the non-rejecting shorthand and renders
		// the one-argument form, the same suppression the throws clause gets.
		{"promise of prim", &PromiseType{Inner: numP()}, "Promise<number>"},
		{"nested promise", &PromiseType{Inner: &PromiseType{Inner: strP()}}, "Promise<Promise<string>>"},
		{"rejecting promise", &PromiseType{Inner: numP(), Err: strP()}, "Promise<number, string>"},
		{"promise with never err renders one argument", &PromiseType{Inner: numP(), Err: &NeverType{}}, "Promise<number>"},

		// Borrows (M4). Ownership and the borrow `&` split on Lt. An owned value has Lt
		// nil and renders bare, as owned-mutable `mut {x}`. A borrow has Lt set and leads
		// with `&`. The inner object or tuple is brace- or bracket-delimited, so it needs
		// no parens. An un-named LifetimeVar is an inferred borrow and prints as a bare
		// `&` with no lifetime. A load-bearing lifetime is named by the scheme printer,
		// which TestPrintScheme exercises. 'static is always shown.
		{
			"owned-mutable object renders bare mut, no borrow &",
			&RefType{Mut: true, Inner: &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}},
			"mut {x: number}",
		},
		{
			"owned-mutable tuple renders bare mut, no borrow &",
			&RefType{Mut: true, Inner: &TupleType{Elems: []Type{numP(), strP()}}},
			"mut [number, string]",
		},
		{
			"immutable borrow with inferred lifetime renders bare &",
			&RefType{Lt: &LifetimeVar{ID: 0}, Inner: &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}},
			"&{x: number}",
		},
		{
			"mutable borrow with inferred lifetime renders &mut",
			&RefType{Mut: true, Lt: &LifetimeVar{ID: 2}, Inner: &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}},
			"&mut {x: number}",
		},
		{
			"borrow with static lifetime always shows 'static",
			&RefType{Mut: true, Lt: &StaticLifetime{}, Inner: &TupleType{Elems: []Type{numP()}}},
			"&'static mut [number]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in))
		})
	}
}

// A generic function renders its own quantified type parameters as a `<...>` prefix:
// `<U>` bare, `<U: T>` for a constraint carried as the parameter variable's upper
// bound, `<U = D>` for a default, and `<U: T = D>` for both. A use of the parameter in
// the params or return renders under its source name rather than the raw t{ID} form.
func TestPrintGenericFunc(t *testing.T) {
	// tparam builds a type parameter and a fresh variable to stand for it, so each case
	// constructs its own U without sharing state across the table.
	tparam := func(name string, constraint, def Type) (*TypeParam, *TypeVarType) {
		u := &TypeVarType{ID: 10, Level: 1}
		if constraint != nil {
			u.UpperBounds = []Type{constraint}
		}
		return &TypeParam{Name: name, Var: u, Default: def}, u
	}
	tests := []struct {
		name string
		in   func() Type
		want string
	}{
		{
			name: "bare type parameter",
			in: func() Type {
				tp, u := tparam("U", nil, nil)
				return &FuncType{TypeParams: []*TypeParam{tp}, Params: []*FuncParam{identP("x", u)}, Ret: u}
			},
			want: "fn <U>(x: U) -> U",
		},
		{
			name: "constrained type parameter",
			in: func() Type {
				tp, u := tparam("U", numP(), nil)
				return &FuncType{TypeParams: []*TypeParam{tp}, Params: []*FuncParam{identP("x", u)}, Ret: u}
			},
			want: "fn <U: number>(x: U) -> U",
		},
		{
			name: "defaulted type parameter",
			in: func() Type {
				tp, u := tparam("U", nil, strP())
				return &FuncType{TypeParams: []*TypeParam{tp}, Params: []*FuncParam{identP("x", u)}, Ret: u}
			},
			want: "fn <U = string>(x: U) -> U",
		},
		{
			name: "constrained and defaulted type parameter",
			in: func() Type {
				tp, u := tparam("U", numP(), strP())
				return &FuncType{TypeParams: []*TypeParam{tp}, Params: []*FuncParam{identP("x", u)}, Ret: u}
			},
			want: "fn <U: number = string>(x: U) -> U",
		},
		{
			// A generic higher-order signature: the parameter appears inside a nested
			// function and the return, so its name must render at every use. A tuple
			// stands in for Array<U>, which lands with ClassType in A1.
			name: "map-shaped generic",
			in: func() Type {
				u := &TypeVarType{ID: 10, Level: 1}
				return &FuncType{
					TypeParams: []*TypeParam{{Name: "U", Var: u}},
					Params:     []*FuncParam{identP("f", &FuncType{Params: []*FuncParam{identP("n", numP())}, Ret: u})},
					Ret:        &TupleType{Elems: []Type{u}},
				}
			},
			want: "fn <U>(f: fn (n: number) -> U) -> [U]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in()))
		})
	}
}

// A generic method renders its own type-parameter prefix through the shared method
// element arm, so a `map<U>` member prints its `<U>` before the receiver and params.
func TestPrintGenericMethod(t *testing.T) {
	u := &TypeVarType{ID: 10, Level: 1}
	obj := &ObjectType{Elems: []ObjTypeElem{
		&MethodElem{
			Name: "map",
			Signatures: []*FuncType{{
				TypeParams: []*TypeParam{{Name: "U", Var: u}},
				SelfParam:  &FuncParam{Pattern: &IdentPat{Name: "self"}, Type: &ClassType{Name: "Box"}},
				Params:     []*FuncParam{identP("x", u)},
				Ret:        u,
			}},
		},
	}}
	require.Equal(t, "{map<U>(self, x: U) -> U}", Print(obj))
}

// A class value renders as an object holding its constructor as `new (params) -> ret`
// alongside its static members, and round-trips through Accept unchanged.
func TestPrintConstructorElem(t *testing.T) {
	obj := &ObjectType{Elems: []ObjTypeElem{
		&ConstructorElem{Fn: &FuncType{
			Params: []*FuncParam{identP("x", numP()), identP("y", numP())},
			Ret:    &ClassType{Name: "Point"},
		}},
		&PropertyElem{Name: "count", Type: numP()},
		&MethodElem{Name: "zero", Signatures: []*FuncType{{Ret: numP()}}, Static: true},
	}}
	require.Equal(t, "{new (x: number, y: number) -> Point, count: number, zero() -> number}", Print(obj))
	// Accept with an identity visitor preserves the node, so a constructor member
	// survives every rewriting pass built on Accept.
	require.Same(t, obj, obj.Accept(identityVisitor{}, Positive))
}

// freeTypeVars descends a constructor's signature, so a variable reachable only through
// a class value's constructor parameter is collected.
func TestFreeTypeVarsConstructorElem(t *testing.T) {
	v := &TypeVarType{ID: 7, Level: 1}
	obj := &ObjectType{Elems: []ObjTypeElem{
		&ConstructorElem{Fn: &FuncType{Params: []*FuncParam{identP("x", v)}, Ret: &ClassType{Name: "Box"}}},
	}}
	require.Equal(t, []*TypeVarType{v}, freeTypeVars(obj))
}

// freeTypeVars excludes a function's own type parameters — they are bound, not free —
// while still collecting an outer free variable, including one that appears only in a
// parameter's constraint.
func TestFreeTypeVarsBoundTypeParam(t *testing.T) {
	t.Run("bound U is omitted, outer var kept", func(t *testing.T) {
		u := &TypeVarType{ID: 10, Level: 1}
		outer := &TypeVarType{ID: 20, Level: 1}
		ft := &FuncType{
			TypeParams: []*TypeParam{{Name: "U", Var: u}},
			Params:     []*FuncParam{identP("x", u), identP("y", outer)},
			Ret:        u,
		}
		require.Equal(t, []*TypeVarType{outer}, freeTypeVars(ft))
	})

	t.Run("an outer var reached only through a constraint is collected", func(t *testing.T) {
		outer := &TypeVarType{ID: 20, Level: 1}
		u := &TypeVarType{ID: 10, Level: 1, UpperBounds: []Type{outer}}
		ft := &FuncType{
			TypeParams: []*TypeParam{{Name: "U", Var: u}},
			Params:     []*FuncParam{identP("x", u)},
			Ret:        u,
		}
		require.Equal(t, []*TypeVarType{outer}, freeTypeVars(ft))
	})
}

func TestIsIdent(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"a", true},
		{"_x", true},
		{"a1", true},
		{"camelCase_9", true},
		{"café", true},  // unicode letter (continue)
		{"naïve", true}, // unicode letter (continue)
		{"数値", true},    // non-Latin letters
		{"Ωmega", true}, // unicode letter (leading)
		{"x٢", true},    // unicode digit (Arabic-Indic) after letter
		{"", false},
		{"1a", false},  // leading digit
		{"٢x", false},  // leading unicode digit
		{"a-b", false}, // hyphen
		{"a b", false}, // space
		{"a.b", false}, // dot
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isIdent(tt.name))
		})
	}
}

// TestPrintNestedPrecedence covers the shapes where operator precedence forces
// parenthesization, mirroring type_system/print_type.go's behavior. These use
// inline snapshots per CLAUDE.md since they're the richer rendered forms.
func TestPrintNestedPrecedence(t *testing.T) {
	// A function inside a union: precFunc < precUnion, so the function is parenthesized.
	t.Run("function in union", func(t *testing.T) {
		ty := &UnionType{Types: []Type{&FuncType{Ret: numP()}, strP()}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`(fn () -> number) | string`))
	})

	// A union inside an intersection: precUnion < precIntersection, so the union
	// is parenthesized.
	t.Run("union in intersection", func(t *testing.T) {
		ty := &IntersectionType{Types: []Type{&UnionType{Types: []Type{numP(), strP()}}, boolP()}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`(number | string) & boolean`))
	})

	// A function inside a tuple is delimited by brackets, so it needs no parens.
	t.Run("function in tuple", func(t *testing.T) {
		ty := &TupleType{Elems: []Type{
			&FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()},
			boolP(),
		}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`[fn (x: number) -> string, boolean]`))
	})

	// An object is brace-delimited (an atom), so an object nested in a union needs
	// no parens, and a function as a property value is delimited by the property's `:`.
	t.Run("object in union", func(t *testing.T) {
		ty := &UnionType{Types: []Type{
			&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "f", Type: &FuncType{Ret: numP()}}}},
			strP(),
		}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`{f: fn () -> number} | string`))
	})
}

// Under the lazy deep-mut form (PR 14) the stored type matches the surface
// annotation, so the printer renders it verbatim with no elision pass: a `mut`
// wrapper over a bare object prints `mut {a: {x}}`, and any explicit nested
// owned-mut cell prints its `mut` rather than being hidden.
func TestPrintLazyDeepMutVerbatim(t *testing.T) {
	mutOwned := func(inner RefInner) Type {
		return &RefType{Mut: true, Inner: inner}
	}
	staticBorrow := func(mut bool, inner RefInner) Type {
		return &RefType{Mut: mut, Lt: &StaticLifetime{}, Inner: inner}
	}
	objX := func() RefInner {
		return &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}
	}

	cases := []struct {
		name string
		ty   Type
		want string
	}{
		{
			name: "mut over a bare nested object prints verbatim",
			ty:   mutOwned(&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: objX()}}}),
			want: "mut {a: {x: number}}",
		},
		{
			name: "&mut over a bare nested object prints verbatim",
			ty:   staticBorrow(true, &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: objX()}}}),
			want: "&'static mut {a: {x: number}}",
		},
		{
			// No elision: a `mut` field under a `mut` wrapper renders its `mut`.
			name: "an explicit nested owned-mut object cell is not hidden",
			ty:   mutOwned(&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: mutOwned(objX())}}}),
			want: "mut {a: mut {x: number}}",
		},
		{
			// The tuple-element path renders the nested `mut` verbatim too.
			name: "an explicit nested owned-mut tuple element is not hidden",
			ty:   mutOwned(&TupleType{Elems: []Type{mutOwned(objX())}}),
			want: "mut [mut {x: number}]",
		},
		{
			name: "borrow field under mut keeps its & marker",
			ty:   mutOwned(&ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: staticBorrow(true, objX())}}}),
			want: "mut {a: &'static mut {x: number}}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Print(tc.ty))
		})
	}
}

// A borrow over a union or intersection prints with the pointee parenthesized. The
// borrow prefix binds at precPrefix, tighter than precUnion and precIntersection, so
// the looser inner is parenthesized to render `&(A | B)` and `&mut (A & B)`. The
// wrapper is outer and shared: one `&`, one lifetime, one mutability for the whole
// pointee, never `&A | &B`.
func TestPrintBorrowOverLatticePointee(t *testing.T) {
	objX := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}
	objY := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "y", Type: strP()}}}
	cases := []struct {
		name string
		ty   Type
		want string
	}{
		{
			name: "immutable borrow over a union",
			ty:   &RefType{Lt: &StaticLifetime{}, Inner: &UnionType{Types: []Type{objX, objY}}},
			want: "&'static ({x: number} | {y: string})",
		},
		{
			name: "mutable borrow over a union",
			ty:   &RefType{Mut: true, Lt: Anon, Inner: &UnionType{Types: []Type{objX, objY}}},
			want: "&mut ({x: number} | {y: string})",
		},
		{
			name: "owned-mutable union prints with mut and parens",
			ty:   &RefType{Mut: true, Inner: &UnionType{Types: []Type{objX, objY}}},
			want: "mut ({x: number} | {y: string})",
		},
		{
			name: "immutable borrow over an intersection",
			ty:   &RefType{Lt: Anon, Inner: &IntersectionType{Types: []Type{objX, objY}}},
			want: "&({x: number} & {y: string})",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Print(tc.ty))
		})
	}
}

// AnonLifetime renders as bare `&` / `&mut`, keeping a borrow distinguishable
// from an owned value when its lifetime is elided.
func TestPrintAnonLifetime(t *testing.T) {
	body := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}
	t.Run("mutable anon", func(t *testing.T) {
		ty := &RefType{Mut: true, Lt: Anon, Inner: body}
		require.Equal(t, "&mut {x: number}", Print(ty))
	})
	t.Run("immutable anon", func(t *testing.T) {
		ty := &RefType{Mut: false, Lt: Anon, Inner: body}
		require.Equal(t, "&{x: number}", Print(ty))
	})
}

// TestPrintRawTypeVar verifies that Print tolerates a raw, un-coalesced
// TypeVarType (rendering it as `t{ID}`) instead of panicking — the M2 walk
// records var-carrying types in Info and only coalesces at binding boundaries,
// so a consumer can hand Print a live variable, standalone or nested in a
// function. See print.go's printType TypeVarType arm.
func TestPrintRawTypeVar(t *testing.T) {
	t.Run("bare variable", func(t *testing.T) {
		require.Equal(t, "t7", Print(&TypeVarType{ID: 7}))
	})

	t.Run("variable nested in a function", func(t *testing.T) {
		fn := &FuncType{
			Params: []*FuncParam{identP("x", &TypeVarType{ID: 0})},
			Ret:    &TypeVarType{ID: 0},
		}
		require.Equal(t, "fn (x: t0) -> t0", Print(fn))
	})
}

// TestPrintUnnamedParamFallback verifies that a parameter with no IdentPat
// pattern falls back to a positional name (arg0, arg1, ...), numbered by param
// index. This path isn't reachable in M1 (params are always IdentPat), but the
// fallback exists for nil/unknown patterns, so it's covered directly here.
func TestPrintUnnamedParamFallback(t *testing.T) {
	fn := &FuncType{
		Params: []*FuncParam{
			{Pattern: nil, Type: numP()},
			{Pattern: nil, Type: strP()},
		},
		Ret: boolP(),
	}
	require.Equal(t, "fn (arg0: number, arg1: string) -> boolean", Print(fn))
}

// A destructuring parameter renders its pattern (M4 E1). Each Pat concrete in the
// sealed set has a printPat arm, including the M5 constructor patterns
// ExtractorPat and InstancePat, which are forward-declared members of the set.
func TestPrintDestructuringParamPatterns(t *testing.T) {
	tests := []struct {
		name string
		pat  Pat
		want string
	}{
		{
			name: "object shorthand and rename",
			pat: &ObjectPat{Fields: []*ObjectPatField{
				{Name: "x", Value: &IdentPat{Name: "x"}},
				{Name: "y", Value: &IdentPat{Name: "b"}},
			}},
			want: "{x, y: b}",
		},
		{
			name: "tuple with wildcard",
			pat:  &TuplePat{Elems: []Pat{&IdentPat{Name: "a"}, &WildcardPat{}}},
			want: "[a, _]",
		},
		{
			name: "literal",
			pat:  &LitPat{Lit: &NumLit{Value: 5}},
			want: "5",
		},
		{
			name: "null",
			pat:  &NullPat{},
			want: "null",
		},
		{
			name: "undefined",
			pat:  &UndefinedPat{},
			want: "undefined",
		},
		{
			name: "nested object in tuple",
			pat: &TuplePat{Elems: []Pat{
				&ObjectPat{Fields: []*ObjectPatField{{Name: "x", Value: &IdentPat{Name: "x"}}}},
			}},
			want: "[{x}]",
		},
		{
			name: "extractor",
			pat:  &ExtractorPat{Name: "Some", Args: []Pat{&IdentPat{Name: "v"}}},
			want: "Some(v)",
		},
		{
			name: "instance",
			pat: &InstancePat{ClassName: "Point", Object: &ObjectPat{Fields: []*ObjectPatField{
				{Name: "x", Value: &IdentPat{Name: "x"}},
				{Name: "y", Value: &IdentPat{Name: "y"}},
			}}},
			want: "Point {x, y}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := &FuncType{Params: []*FuncParam{{Pattern: tt.pat, Type: numP()}}, Ret: boolP()}
			require.Equal(t, "fn ("+tt.want+": number) -> boolean", Print(fn))
		})
	}
}

// TestPrintScheme covers the M3 quantifier-prefix rendering: a generalized type's
// free variables are collected into a <T0, T1, …> prefix (named by first
// appearance in print order) and rendered under those names, while a variable-free
// type renders exactly as Print would.
func TestPrintScheme(t *testing.T) {
	t.Run("no free vars renders as Print", func(t *testing.T) {
		ty := &FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()}
		require.Equal(t, "fn (x: number) -> string", PrintAsScheme(ty))
	})

	t.Run("identity gets one type parameter", func(t *testing.T) {
		a := &TypeVarType{ID: 7, Level: 1}
		ty := &FuncType{Params: []*FuncParam{identP("x", a)}, Ret: a}
		require.Equal(t, "fn <T0>(x: T0) -> T0", PrintAsScheme(ty))
	})

	t.Run("captured free var and own type param merge into one prefix", func(t *testing.T) {
		u := &TypeVarType{ID: 10, Level: 2}    // the function's own type parameter
		free := &TypeVarType{ID: 20, Level: 1} // a captured scheme variable
		// fn <U>(x: U, y: free) -> [U, free]: the scheme names free as T0, the function
		// contributes U, and the two merge into a single ordered `<T0, U>` prefix rather
		// than the malformed `<T0><U>` two adjacent groups would produce.
		ty := &FuncType{
			TypeParams: []*TypeParam{{Name: "U", Var: u}},
			Params:     []*FuncParam{identP("x", u), identP("y", free)},
			Ret:        &TupleType{Elems: []Type{u, free}},
		}
		require.Equal(t, "fn <T0, U>(x: U, y: T0) -> [U, T0]", PrintAsScheme(ty))
	})

	t.Run("a generated name skips a source name from the same alphabet", func(t *testing.T) {
		t0 := &TypeVarType{ID: 10, Level: 2}   // a parameter the source wrote as T0
		free := &TypeVarType{ID: 20, Level: 1} // a captured scheme variable
		// fn <T0>(x: T0, y: free) -> [T0, free]: naming free T0 as well would put two binders
		// under one name, so the generated alphabet skips to T1.
		ty := &FuncType{
			TypeParams: []*TypeParam{{Name: "T0", Var: t0}},
			Params:     []*FuncParam{identP("x", t0), identP("y", free)},
			Ret:        &TupleType{Elems: []Type{t0, free}},
		}
		require.Equal(t, "fn <T1, T0>(x: T0, y: T1) -> [T0, T1]", PrintAsScheme(ty))
	})

	t.Run("distinct vars are named by first appearance", func(t *testing.T) {
		a := &TypeVarType{ID: 1, Level: 1}
		b := &TypeVarType{ID: 2, Level: 1}
		// fn (x: a, y: b) -> [b, a]: a appears first (param x), then b (param y).
		ty := &FuncType{
			Params: []*FuncParam{identP("x", a), identP("y", b)},
			Ret:    &TupleType{Elems: []Type{b, a}},
		}
		require.Equal(t, "fn <T0, T1>(x: T0, y: T1) -> [T1, T0]", PrintAsScheme(ty))
	})

	t.Run("a free var keeps one name across positions", func(t *testing.T) {
		a := &TypeVarType{ID: 3, Level: 1}
		ty := &TupleType{Elems: []Type{a, a}}
		require.Equal(t, "<T0> [T0, T0]", PrintAsScheme(ty))
	})

	t.Run("object property vars are named in property order", func(t *testing.T) {
		a := &TypeVarType{ID: 1, Level: 1}
		b := &TypeVarType{ID: 2, Level: 1}
		// fn () -> {a: a, b: b}: freeTypeVars walks the return object's properties in
		// order, so a names T0 (property a) and b names T1 (property b).
		ty := &FuncType{Ret: &ObjectType{Elems: []ObjTypeElem{
			&PropertyElem{Name: "a", Type: a},
			&PropertyElem{Name: "b", Type: b},
		}}}
		require.Equal(t, "fn <T0, T1>() -> {a: T0, b: T1}", PrintAsScheme(ty))
	})

	t.Run("a load-bearing borrow lifetime is named in & notation", func(t *testing.T) {
		lv := &LifetimeVar{ID: 0, Level: 1}
		// One borrow lifetime is shared by the param and the return, so the scheme names
		// it once and renders both in the mutable-borrow `&'a mut {x: number}` form.
		ref := &RefType{Mut: true, Lt: lv, Inner: &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}}
		ty := &FuncType{Params: []*FuncParam{identP("p", ref)}, Ret: ref}
		require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}", PrintAsScheme(ty))
	})

	t.Run("an immutable borrow lifetime is named after the &", func(t *testing.T) {
		lv := &LifetimeVar{ID: 1, Level: 1}
		ref := &RefType{Lt: lv, Inner: &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: numP()}}}}
		ty := &FuncType{Params: []*FuncParam{identP("p", ref)}, Ret: ref}
		require.Equal(t, "fn <'a>(p: &'a {x: number}) -> &'a {x: number}", PrintAsScheme(ty))
	})

	t.Run("a borrowed generic survives generalization", func(t *testing.T) {
		a := &TypeVarType{ID: 1, Level: 1}
		// fn (p: mut {x: a}) -> a: freeTypeVars must descend through the RefType
		// wrapper into its inner object to find a, so the borrowed param and the
		// return share the one type parameter T0. This is the realistic C3 shape — a
		// field-write makes the receiver a `mut` object — surviving M3 generalization.
		ty := &FuncType{
			Params: []*FuncParam{identP("p", &RefType{Mut: true,
				Inner: &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: a}}}})},
			Ret: a,
		}
		require.Equal(t, "fn <T0>(p: mut {x: T0}) -> T0", PrintAsScheme(ty))
	})
}

// PrintAsSchemeWith names ONLY the variables the predicate accepts as quantified
// type parameters; a variable it rejects (one coalescing should have inlined)
// renders as the raw t{ID} debug anchor instead of being masked as a <Tn>
// parameter. This preserves the leak signal that plain PrintAsScheme (which names
// every free var) would hide.
func TestPrintSchemeParamsLeakAnchor(t *testing.T) {
	param := &TypeVarType{ID: 1, Level: 2}   // a genuine type parameter (Level > 1)
	leaked := &TypeVarType{ID: 99, Level: 0} // a var that should have been inlined
	ty := &FuncType{
		Params: []*FuncParam{identP("x", param)},
		Ret:    &TupleType{Elems: []Type{param, leaked}},
	}
	got := PrintAsSchemeWith(ty, func(v *TypeVarType) bool { return v.Level > 1 }, nil, nil)
	require.Equal(t, "fn <T0>(x: T0) -> [T0, t99]", got)
}

// A class, alias, or enum keeps its type parameters outside the type it binds, so
// PrintAsSchemeWith renders them under their source names only when the caller passes them as
// declared. The cases below build the shape a class VALUE binding takes: an object holding
// the constructor, whose free variables are the class's own parameters.
//
// quantified is the predicate renderScheme uses for a generalized binding: a variable minted
// deeper than the binding's own level is one that generalization quantified.
func TestPrintSchemeDeclaredNames(t *testing.T) {
	quantified := func(v *TypeVarType) bool { return v.Level > 1 }

	t.Run("a class parameter renders under its source name", func(t *testing.T) {
		// class Node<T> { value: T, tail: Node<T> }
		tv := &TypeVarType{ID: 0, Level: 2}
		node := &ClassType{Name: "Node", TypeArgs: []Type{tv}}
		obj := ctorObj(&FuncType{
			Params: []*FuncParam{identP("value", tv), identP("tail", node)},
			Ret:    node,
		})
		got := PrintAsSchemeWith(obj, quantified, nil, []*TypeParam{{Name: "T", Var: tv}})
		require.Equal(t, "<T> {new (value: T, tail: Node<T>) -> Node<T>}", got)
	})

	t.Run("binders follow declaration order, not first appearance", func(t *testing.T) {
		// class Pair<K, V> { …, constructor(mut self, v: V, k: K) { … } }. The constructor
		// takes v first, so first appearance would order the binders V, K.
		k := &TypeVarType{ID: 0, Level: 2}
		v := &TypeVarType{ID: 1, Level: 2}
		pair := &ClassType{Name: "Pair", TypeArgs: []Type{k, v}}
		obj := ctorObj(&FuncType{
			Params: []*FuncParam{identP("v", v), identP("k", k)},
			Ret:    pair,
		})
		got := PrintAsSchemeWith(obj, quantified, nil,
			[]*TypeParam{{Name: "K", Var: k}, {Name: "V", Var: v}})
		require.Equal(t, "<K, V> {new (v: V, k: K) -> Pair<K, V>}", got)
	})

	t.Run("a generated name skips one the source already wrote", func(t *testing.T) {
		// class C<T0> declares the name the generated alphabet starts at, so the undeclared
		// variable beside it takes T1.
		declared := &TypeVarType{ID: 0, Level: 2}
		extra := &TypeVarType{ID: 1, Level: 2}
		obj := ctorObj(&FuncType{
			Params: []*FuncParam{identP("a", declared), identP("b", extra)},
			Ret:    &ClassType{Name: "C", TypeArgs: []Type{declared}},
		})
		got := PrintAsSchemeWith(obj, quantified, nil, []*TypeParam{{Name: "T0", Var: declared}})
		require.Equal(t, "<T0, T1> {new (a: T0, b: T1) -> C<T0>}", got)
	})

	t.Run("a variable the predicate rejects keeps the leak anchor", func(t *testing.T) {
		// A source name does not mask a variable that coalescing failed to inline. leaked is
		// declared, but the predicate rejects it, so it renders as t99 rather than as E.
		tv := &TypeVarType{ID: 0, Level: 2}
		leaked := &TypeVarType{ID: 99, Level: 0}
		obj := ctorObj(&FuncType{
			Params: []*FuncParam{identP("a", tv), identP("b", leaked)},
			Ret:    &ClassType{Name: "C", TypeArgs: []Type{tv}},
		})
		got := PrintAsSchemeWith(obj, quantified, nil,
			[]*TypeParam{{Name: "T", Var: tv}, {Name: "E", Var: leaked}})
		require.Equal(t, "<T> {new (a: T, b: t99) -> C<T>}", got)
	})

	t.Run("a method parameter shadowing the class's stays distinct", func(t *testing.T) {
		// class Shadow<T> { v: T, m<T>(x: T) -> T }. The class's T holds the source name, so
		// the method's own parameter renders under a suffixed one and the two never collide.
		outer := &TypeVarType{ID: 0, Level: 2}
		inner := &TypeVarType{ID: 1, Level: 3}
		obj := &ObjectType{Elems: []ObjTypeElem{
			&PropertyElem{Name: "v", Type: outer},
			&MethodElem{Name: "m", Signatures: []*FuncType{{
				TypeParams: []*TypeParam{{Name: "T", Var: inner}},
				Params:     []*FuncParam{identP("x", inner)},
				Ret:        outer,
			}}},
		}}
		got := PrintAsSchemeWith(obj, quantified, nil, []*TypeParam{{Name: "T", Var: outer}})
		require.Equal(t, "<T> {v: T, m<T_2>(x: T_2) -> T}", got)
	})
}

// A signature's own type parameters are visible only inside it, so two sibling signatures
// each written `<T>` both render T. Without that scoping the second would be renamed to avoid
// the first, which shares no scope with it.
func TestPrintSiblingSignaturesReuseOneName(t *testing.T) {
	a := &TypeVarType{ID: 1, Level: 2}
	b := &TypeVarType{ID: 2, Level: 2}
	obj := &ObjectType{Elems: []ObjTypeElem{&MethodElem{Name: "m", Signatures: []*FuncType{
		{TypeParams: []*TypeParam{{Name: "T", Var: a}}, Params: []*FuncParam{identP("x", a)}, Ret: a},
		{TypeParams: []*TypeParam{{Name: "T", Var: b}}, Params: []*FuncParam{identP("y", b)}, Ret: b},
	}}}}
	require.Equal(t, "{m<T>(x: T) -> T; m<T>(y: T) -> T}", Print(obj))
}

// PrintWithParams is the plain-Print path for a TYPE binding, which carries no quantifier
// prefix to hang names on. `class Node<T>` binds the type Node<t0>; handing over the class's
// parameters renders it Node<T>.
func TestPrintWithParams(t *testing.T) {
	tv := &TypeVarType{ID: 0, Level: 2}
	declared := []*TypeParam{{Name: "T", Var: tv}}

	t.Run("a class instance names its argument", func(t *testing.T) {
		got := PrintWithParams(&ClassType{Name: "Node", TypeArgs: []Type{tv}}, declared)
		require.Equal(t, "Node<T>", got)
	})

	t.Run("an alias body names the variable it holds", func(t *testing.T) {
		// The rendered form of `type Alias<T> = {v: T}`, whose binding shows the body rather
		// than the opaque alias name.
		body := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "v", Type: tv}}}
		require.Equal(t, "{v: T}", PrintWithParams(body, declared))
	})

	t.Run("an enum names its variants' arguments", func(t *testing.T) {
		// The rendered form of `enum Opt<T> { Some(v: T), None }`, whose binding is the union
		// of the variant handles.
		body := &UnionType{Types: []Type{
			&ClassType{Name: "Opt.Some", TypeArgs: []Type{tv}, Variant: true},
			&ClassType{Name: "Opt.None", TypeArgs: []Type{tv}, Variant: true},
		}}
		require.Equal(t, "Opt.Some<T> | Opt.None<T>", PrintWithParams(body, declared))
	})

	t.Run("an undeclared variable keeps the raw debug form", func(t *testing.T) {
		other := &TypeVarType{ID: 42, Level: 2}
		got := PrintWithParams(&ClassType{Name: "Node", TypeArgs: []Type{other}}, declared)
		require.Equal(t, "Node<t42>", got)
	})
}

// ctorObj wraps a constructor signature in the object a class value binds to, the
// `{new (…) -> C}` shape classValue builds in internal/solver.
func ctorObj(fn *FuncType) *ObjectType {
	return &ObjectType{Elems: []ObjTypeElem{&ConstructorElem{Fn: fn}}}
}

// PrintElided renders a type like Print but stops at maxDepth, standing in ElisionMark for every
// subtree below it. A type reduction can leave a residual whose argument is far larger than
// anything the source wrote, and a diagnostic naming it needs a bound; see describeMaxDepth in
// internal/solver.
//
// The cases walk one nesting chain at each depth so the boundary is visible, then cover the two
// rules that are not "cut everything below the line": a leaf at the boundary renders itself rather
// than an ellipsis, since the ellipsis would tell the reader less than `number` does, and a
// maxDepth of zero elides nothing so the zero value matches Print.
func TestPrintElided(t *testing.T) {
	// nest builds `{a: {a: … {a: number}}}` with depth levels of object wrapping.
	nest := func(depth int) Type {
		var ty Type = numP()
		for range depth {
			ty = &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "a", Type: ty}}}
		}
		return ty
	}
	alias := func(arg Type) Type { return &AliasType{Name: "Grow", TypeArgs: []Type{arg}} }

	tests := []struct {
		name     string
		in       Type
		maxDepth int
		want     string
	}{
		{"AliasArgCutAtOne", alias(nest(3)), 1, "Grow<…>"},
		{"AliasArgCutAtTwo", alias(nest(3)), 2, "Grow<{a: …}>"},
		{"AliasArgCutAtFour", alias(nest(4)), 4, "Grow<{a: {a: {a: …}}}>"},
		// The argument is shallower than the cut, so nothing elides.
		{"ShallowerThanCut", alias(nest(1)), 4, "Grow<{a: number}>"},
		// `number` sits exactly at the boundary. A leaf renders itself there.
		{"LeafAtBoundary", alias(nest(2)), 3, "Grow<{a: {a: number}}>"},
		// Every branch of a wide type is cut at the same depth.
		{
			name: "BranchesCutAlike",
			in: alias(&ObjectType{Elems: []ObjTypeElem{
				&PropertyElem{Name: "a", Type: nest(2)},
				&PropertyElem{Name: "b", Type: nest(2)},
			}}),
			maxDepth: 3,
			want:     "Grow<{a: {a: …}, b: {a: …}}>",
		},
		{"ZeroElidesNothing", alias(nest(3)), 0, "Grow<{a: {a: {a: number}}}>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, PrintElided(test.in, test.maxDepth))
		})
	}
}

// PrintElided with no limit renders exactly what Print does, so a caller that opts out of eliding
// is not quietly getting a second rendering of the same type.
func TestPrintElidedZeroMatchesPrint(t *testing.T) {
	ty := &UnionType{Types: []Type{
		&FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()},
		&TupleType{Elems: []Type{strP(), boolP()}},
	}}
	require.Equal(t, Print(ty), PrintElided(ty, 0))
}
