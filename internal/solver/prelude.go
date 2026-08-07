package solver

import (
	"sync"

	"github.com/escalier-lang/escalier/internal/soltype"
)

var (
	preludeOnce sync.Once
	sharedScope *Scope
)

// sharedPrelude returns a process-wide, lazily-built prelude scope reused across
// InferModule calls instead of rebuilding the operator/stdlib table every time.
// This is safe because the prelude is effectively immutable: every InferModule
// run hangs its module scope off a fresh Child(), writes only to that child, and
// the seeded prelude bindings are concrete types (no shared inference variables
// for constrain to mutate). Tests that need a private prelude still call
// NewPrelude() directly.
func sharedPrelude() *Scope {
	preludeOnce.Do(func() { sharedScope = NewPrelude() })
	return sharedScope
}

// NewPrelude builds the global scope every inference run starts from. It holds
// two hand-seeded sorts:
//
//   - operator/builtin value bindings — the monomorphic-over-primitives schemes
//     the BinaryExpr/UnaryExpr walk resolves (the BinaryExpr walk itself is a
//     later PR; the bindings live here from PR-1). A near-mechanical port of the
//     old checker's addOperatorBindings from type_system constructors to soltype
//     ones.
//   - placeholder stdlib *type* bindings (§3.8) — opaque stubs so a reference to
//     Promise/Iterable/… resolves without an unbound-name error. M2 seeds
//     placeholders only; real ingestion (real structures, arity) is M7.
//
// Nothing here imports internal/checker or internal/type_system — that is the
// M2 gate.
func NewPrelude() *Scope {
	s := NewScope()
	addOperatorBindings(s)
	addStdlibTypePlaceholders(s)
	addIteratorResultAliases(s)
	return s
}

// prim builds a fresh primitive atom; opFunc builds a monomorphic operator
// FuncType with IdentPat-named params "a", "b", …. Fresh values per call keep
// each binding's type independent.
func prim(p soltype.Prim) soltype.Type { return &soltype.PrimType{Prim: p} }

func opFunc(ret soltype.Type, params ...soltype.Type) *soltype.FuncType {
	ps := make([]*soltype.FuncParam, len(params))
	for i, p := range params {
		ps[i] = &soltype.FuncParam{
			Pattern: &soltype.IdentPat{Name: string(rune('a' + i))},
			Type:    p,
		}
	}
	// Operators are concrete function values, hence exact (accept-set [n, n]) — the
	// zero value of Inexact.
	return &soltype.FuncType{Params: ps, Ret: ret}
}

// addOperatorBindings seeds the built-in operators, every one monomorphic over
// primitives (so they need no generics/unions/lib types). Richer forms (bigint
// arithmetic, string <, generic equality) are refinements that land with their
// enabling milestone (overloads M3, unions M6), not M2.
//
// unknown is the top type, so an operand of any type is a subtype of it. The
// equality operators are seeded with `unknown` parameters, so an operand of any
// type fills them. The `_ <: unknown` rule in constrain accepts that directly, so
// `1 == 2` constrains `1 <: unknown` and succeeds. When the operator/call walk
// lands, add a `1 == 2 ⇒ boolean` regression test.
func addOperatorBindings(s *Scope) {
	num := func() soltype.Type { return prim(soltype.NumPrim) }
	str := func() soltype.Type { return prim(soltype.StrPrim) }
	boolean := func() soltype.Type { return prim(soltype.BoolPrim) }
	unknown := func() soltype.Type { return &soltype.UnknownType{} }

	define := func(t soltype.Type, names ...string) {
		for _, name := range names {
			// Prelude operators are monomorphic over primitives — a MonoScheme that
			// instantiates to itself (generic operators are a later milestone).
			s.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(t)}})
		}
	}

	define(opFunc(num(), num(), num()), "+", "-", "*", "/")
	define(opFunc(boolean(), num(), num()), "<", ">", "<=", ">=")
	define(opFunc(boolean(), unknown(), unknown()), "==", "!=")
	define(opFunc(boolean(), boolean(), boolean()), "&&", "||")
	define(opFunc(boolean(), boolean()), "!")
	define(opFunc(str(), str(), str()), "++")
}

// stdlibTypePlaceholders are the names downstream type rules reference (await,
// for-in, yield, iteration built-ins). M2 must make them *resolve* even though
// the rules that consume them — and the real generic definitions — land later
// (real ingestion is M7).
var stdlibTypePlaceholders = []string{
	"Promise",
	"Iterable",
	"AsyncIterable",
	"Generator",
	"AsyncGenerator",
}

// addStdlibTypePlaceholders seeds each stdlib type name as an opaque unknown
// stub so a reference resolves without an unbound-name error. No structure, no
// arity — M7 swaps these for real types.
func addStdlibTypePlaceholders(s *Scope) {
	for _, name := range stdlibTypePlaceholders {
		s.defineType(name, TypeBinding{Type: &soltype.UnknownType{}})
	}
}

// The three names an advance of a generator resolves through. They are real aliases rather
// than opaque `unknown` stubs, so `it.next()` reads as `IteratorResult<1, "done">` and a
// caller may annotate against the arms by name.
const (
	iteratorResultName       = "IteratorResult"
	iteratorYieldResultName  = "IteratorYieldResult"
	iteratorReturnResultName = "IteratorReturnResult"
)

// addIteratorResultAliases binds each of the three names to the AliasType handle a reference
// resolves to. The handle carries only the name, so it holds no inference variable and is safe
// in the process-wide prelude scope. registerIteratorResultAliases puts the bodies those names
// expand to on each run's Context, which is where the alias registry lives.
func addIteratorResultAliases(s *Scope) {
	for _, name := range []string{iteratorResultName, iteratorYieldResultName, iteratorReturnResultName} {
		s.defineType(name, TypeBinding{Type: &soltype.AliasType{Name: name}})
	}
}

// registerIteratorResultAliases registers the bodies of the three iterator-result aliases on
// ctx, mirroring the standard library:
//
//	type IteratorYieldResult<TYield>   = {done?: false, value: TYield}
//	type IteratorReturnResult<TReturn> = {done: true, value: TReturn}
//	type IteratorResult<T, TReturn>    = IteratorYieldResult<T> | IteratorReturnResult<TReturn>
//
// TReturn has no default, where TypeScript defaults it to `any`. Escalier has no `any`, and
// every site that mints an iterator result knows the generator's return type, so both
// arguments are required.
//
// The registry is per-Context and the prelude scope is shared across runs, so the bodies are
// rebuilt here rather than once at prelude construction. That also keeps each run's parameter
// vars its own, so nothing a run does to them can reach another.
func registerIteratorResultAliases(ctx *Context) {
	yield := builtinTypeParam("TYield", -1)
	ctx.registerAlias(iteratorYieldResultName, &AliasDef{
		TypeParams: []*soltype.TypeParam{yield},
		Body: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			// `done` is optional on this arm alone, matching the standard library. An
			// iterator written by hand may omit it while yielding, and `{value: 5}` has to
			// satisfy the arm for that to type-check.
			&soltype.PropertyElem{Name: "done", Type: doneTag(false), Optional: true},
			&soltype.PropertyElem{Name: "value", Type: yield.Var},
		}},
	})

	ret := builtinTypeParam("TReturn", -1)
	ctx.registerAlias(iteratorReturnResultName, &AliasDef{
		TypeParams: []*soltype.TypeParam{ret},
		Body: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.PropertyElem{Name: "done", Type: doneTag(true)},
			&soltype.PropertyElem{Name: "value", Type: ret.Var},
		}},
	})

	t, tReturn := builtinTypeParam("T", -1), builtinTypeParam("TReturn", -2)
	ctx.registerAlias(iteratorResultName, &AliasDef{
		TypeParams: []*soltype.TypeParam{t, tReturn},
		Body: newUnion(ctx, []soltype.Type{
			&soltype.AliasType{Name: iteratorYieldResultName, TypeArgs: []soltype.Type{t.Var}},
			&soltype.AliasType{Name: iteratorReturnResultName, TypeArgs: []soltype.Type{tReturn.Var}},
		}, false),
	})
}

// doneTag builds the boolean literal one arm of IteratorResult tags itself with, `false` on the
// yielded-value arm and `true` on the finished one.
func doneTag(done bool) soltype.Type {
	return &soltype.LitType{Lit: &soltype.BoolLit{Value: done}}
}

// builtinTypeParam mints one parameter of a built-in alias. Name is what a diagnostic about the
// alias shows, so it matches the standard library's spelling of that parameter. id is negative
// to keep it outside the run's own var-ID space, and it need only tell this alias's parameters
// apart, because expandAlias substitutes a parameter var by POINTER identity and never by ID.
// Drawing from Context.freshVar instead would shift the ID of every variable a run mints
// afterwards, renaming the `t{ID}` forms diagnostics and tests read, to distinguish variables
// that cannot collide in the first place.
func builtinTypeParam(name string, id int) *soltype.TypeParam {
	return &soltype.TypeParam{Name: name, Var: &soltype.TypeVarType{ID: id}}
}
