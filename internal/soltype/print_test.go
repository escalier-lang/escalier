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

		// Lattice bounds, void, and the error-recovery sentinel.
		{"never", &NeverType{}, "never"},
		{"unknown", &UnknownType{}, "unknown"},
		{"void", &Void{}, "void"},
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

		// Unions and intersections.
		{"union pair", &UnionType{Types: []Type{numP(), strP()}}, "number | string"},
		{"union triple", &UnionType{Types: []Type{numP(), strP(), boolP()}}, "number | string | boolean"},
		// An inexact union renders a trailing `...` entry.
		{"inexact union", &UnionType{Types: []Type{numP(), strP()}, Inexact: true}, "number | string | ..."},
		// NullType renders as `null`. It is a distinct atomic kind from Void.
		{"null atom", &NullType{}, "null"},
		{"intersection pair", &IntersectionType{Types: []Type{numP(), strP()}}, "number & string"},

		// Promises (M3).
		{"promise of prim", &PromiseType{Inner: numP()}, "Promise<number>"},
		{"nested promise", &PromiseType{Inner: &PromiseType{Inner: strP()}}, "Promise<Promise<string>>"},

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
	got := PrintAsSchemeWith(ty, func(v *TypeVarType) bool { return v.Level > 1 }, nil)
	require.Equal(t, "fn <T0>(x: T0) -> [T0, t99]", got)
}
