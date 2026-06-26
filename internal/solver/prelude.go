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
//     the BinaryExpr/UnaryExpr walk resolves. A port of the old checker's
//     addOperatorBindings from type_system constructors to soltype ones.
//   - placeholder stdlib *type* bindings — opaque stubs so a reference to
//     Promise/Iterable/… resolves without an unbound-name error. These are
//     placeholders only, with no real structure or arity.
//
// Nothing here imports internal/checker or internal/type_system, so the package
// stays acyclic.
func NewPrelude() *Scope {
	s := NewScope()
	addOperatorBindings(s)
	addStdlibTypePlaceholders(s)
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
// primitives so they need no generics, unions, or lib types. Richer forms such
// as bigint arithmetic, string `<`, and generic equality are not yet modeled.
//
// The equality operators are seeded with `unknown` (⊤) parameters. constrain has
// no `T <: UnknownType` rule, so an UnknownType super falls through to
// CannotConstrainError. Applying ==/!= to a non-unknown operand therefore needs
// either an `_ <: UnknownType => ok` arm treating UnknownType as ⊤, or ==/!=
// seeded with fresh per-call vars instead.
func addOperatorBindings(s *Scope) {
	num := func() soltype.Type { return prim(soltype.NumPrim) }
	str := func() soltype.Type { return prim(soltype.StrPrim) }
	boolean := func() soltype.Type { return prim(soltype.BoolPrim) }
	unknown := func() soltype.Type { return &soltype.UnknownType{} }

	define := func(t soltype.Type, names ...string) {
		for _, name := range names {
			// Prelude operators are monomorphic over primitives — a MonoScheme that
			// instantiates to itself.
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

// stdlibTypePlaceholders are the names downstream type rules reference for
// await, for-in, yield, and the iteration built-ins. They exist so those names
// *resolve*, even though the real generic definitions are not yet ingested.
var stdlibTypePlaceholders = []string{
	"Promise",
	"Iterable",
	"AsyncIterable",
	"Generator",
	"AsyncGenerator",
	"IteratorResult",
}

// addStdlibTypePlaceholders seeds each stdlib type name as an opaque unknown
// stub so a reference resolves without an unbound-name error. The stubs carry no
// structure and no arity.
func addStdlibTypePlaceholders(s *Scope) {
	for _, name := range stdlibTypePlaceholders {
		s.defineType(name, TypeBinding{Type: &soltype.UnknownType{}})
	}
}
