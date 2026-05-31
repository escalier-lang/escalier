package simplesub

import (
	"fmt"
	"sort"
	"strings"
)

// ---- M9: lazy alias expansion + coinductive subtyping ----
//
// The M5/M7 evaluator is *eager*: it normalizes an alias to a concrete type and
// then relies on a cycle cache + depth budget (and the optional CheckRegular) to
// stop recursion. This file prototypes the lazy alternative to show what
// laziness does and does not buy.
//
// A LazyType keeps an alias reference UNEXPANDED (LazyRef) and forces it only
// when an operation needs its structure — here, a structural subtype check.
// Subtyping uses a COINDUCTIVE seen-set (Amadio–Cardelli): a (lhs, rhs) pair
// already on the current path counts as success, so a recursive type compared
// against another terminates without ever fully unfolding.
//
// Two cheap structural shortcuts run before any forcing: reflexivity (identical
// types are subtypes by axiom — so even Grow<number> <: Grow<number> is settled
// with no expansion, since the two LazyRefs share a canonical key) and the
// coinductive seen-set.
//
// The payoff and its limit:
//   - REGULAR recursive types (List, Json-shaped) unfold to trees with finitely
//     many distinct subterms, so the seen-set always closes the loop: subtyping
//     is decided COMPLETELY with NO budget and NO CheckRegular.
//   - NON-REGULAR recursion (Grow<T> = Grow<Array<T>>) unfolds to infinitely many
//     distinct subterms. Reflexivity handles the X <: X case for free, but
//     comparing two DIFFERENT non-regular instantiations (Grow<number> vs
//     Grow<string>) the seen-set never closes, so it still needs the depth
//     budget. Laziness relocates the limit; it does not remove it.

// LazyType is a structural type whose alias references are expanded on demand.
type LazyType interface{ isLazyType() }

// LazyPrim is a base type: "number" | "string" | "boolean".
type LazyPrim struct{ Name string }

// LazyNull models a nominal leaf (e.g. Null) — compared by name, never expanded.
type LazyNull struct{}

// LazyObj is a structural record. Width+depth subtyping, fields covariant.
type LazyObj struct{ Fields map[string]LazyType }

// LazyUnion is a union; X <: union iff X <: some member, union <: Y iff every
// member <: Y.
type LazyUnion struct{ Members []LazyType }

// LazyCtor is a named type constructor applied to arguments (e.g. Array<T>),
// compared nominally by name with covariant arguments. It is NOT expanded — it
// is the "stop" that keeps non-alias structure finite.
type LazyCtor struct {
	Name string
	Args []LazyType
}

// LazyRef is an unexpanded alias instantiation. force() lazily produces the
// alias body with arguments substituted; it is called only when an operation
// needs to see through the alias.
type LazyRef struct {
	Name  string
	Args  []LazyType
	force func() LazyType
}

func (*LazyPrim) isLazyType()  {}
func (*LazyNull) isLazyType()  {}
func (*LazyObj) isLazyType()   {}
func (*LazyUnion) isLazyType() {}
func (*LazyCtor) isLazyType()  {}
func (*LazyRef) isLazyType()   {}

// LazyAliases holds lazy alias definitions for forcing LazyRefs.
type LazyAliases struct {
	defs map[string]*lazyAlias
	// forceBudget bounds how many alias forcings a single subtype query may do,
	// the backstop for non-regular recursion the coinductive seen-set can't
	// close. Regular types never reach it.
	forceBudget int
}

type lazyAlias struct {
	params []string
	body   LazyType
}

// lazyForceBudget bounds alias forcings per subtype query — the backstop for
// non-regular recursion the coinductive seen-set can't close. Regular types
// close via the seen-set long before reaching it (a List<T> query forces only a
// couple of levels), so the exact value matters only for how quickly a
// non-regular query gives up.
const lazyForceBudget = 100

func NewLazyAliases() *LazyAliases {
	return &LazyAliases{defs: map[string]*lazyAlias{}, forceBudget: lazyForceBudget}
}

// Define registers an alias whose body may reference itself or other aliases via
// Ref (built through this same LazyAliases so forcing resolves correctly).
func (a *LazyAliases) Define(name string, params []string, body LazyType) {
	a.defs[name] = &lazyAlias{params: params, body: body}
}

// Ref builds an unexpanded reference to alias `name` with `args`. Forcing
// substitutes args for the alias's parameters in its body.
func (a *LazyAliases) Ref(name string, args ...LazyType) *LazyRef {
	return &LazyRef{
		Name: name,
		Args: args,
		force: func() LazyType {
			def, ok := a.defs[name]
			// Unknown name or arity mismatch: fall back to an opaque nominal
			// constructor rather than partially substituting (which would leave
			// some params unbound and silently mis-evaluate the body), mirroring
			// evalRef in typeops.go.
			if !ok || len(args) != len(def.params) {
				return &LazyCtor{Name: name, Args: args}
			}
			env := map[string]LazyType{}
			for i, p := range def.params {
				env[p] = args[i]
			}
			return substLazy(def.body, env)
		},
	}
}

// substLazy substitutes parameter references in a lazy type. A bare LazyCtor
// with no args whose name is a parameter is replaced by the bound argument.
func substLazy(t LazyType, env map[string]LazyType) LazyType {
	switch ty := t.(type) {
	case *LazyCtor:
		if len(ty.Args) == 0 {
			if bound, ok := env[ty.Name]; ok {
				return bound
			}
			return ty
		}
		args := make([]LazyType, len(ty.Args))
		for i, a := range ty.Args {
			args[i] = substLazy(a, env)
		}
		return &LazyCtor{Name: ty.Name, Args: args}
	case *LazyObj:
		fields := make(map[string]LazyType, len(ty.Fields))
		for k, v := range ty.Fields {
			fields[k] = substLazy(v, env)
		}
		return &LazyObj{Fields: fields}
	case *LazyUnion:
		members := make([]LazyType, len(ty.Members))
		for i, m := range ty.Members {
			members[i] = substLazy(m, env)
		}
		return &LazyUnion{Members: members}
	case *LazyRef:
		// Re-point the ref's args (substituted), keeping it lazy.
		args := make([]LazyType, len(ty.Args))
		for i, a := range ty.Args {
			args[i] = substLazy(a, env)
		}
		// Rebuild via the original force closure's alias set by capturing name.
		ref := *ty
		ref.Args = args
		orig := ty.force
		ref.force = func() LazyType {
			// Force the original (unsubstituted-arg) closure won't see env; so
			// re-run substitution on its result instead.
			return substLazy(orig(), env)
		}
		return &ref
	default:
		return t // LazyPrim, LazyNull
	}
}

// LazyVar is a convenience for a parameter reference inside an alias body.
// (A bare zero-arg LazyCtor whose name is a parameter; substLazy replaces it.)
func LazyVar(name string) *LazyCtor { return &LazyCtor{Name: name} }

// subPair is a coinductive seen-set key: a (lhs, rhs) pair by canonical form.
type subPair struct{ lhs, rhs string }

// subtyper carries the seen-set and the remaining force budget across a query.
type subtyper struct {
	aliases *LazyAliases
	seen    map[subPair]bool
	budget  int
	// budgetHit records whether the depth budget (not the seen-set) terminated
	// the query — the signal that this was non-regular recursion.
	budgetHit bool
}

// Subtypes reports whether a <: b structurally, expanding aliases lazily and
// using a coinductive seen-set so recursive types terminate. The second return
// value is true iff the depth budget was hit (i.e. non-regular recursion forced
// the cutoff) — for regular types it is always false.
func (a *LazyAliases) Subtypes(lhs, rhs LazyType) (ok bool, budgetHit bool) {
	s := &subtyper{aliases: a, seen: map[subPair]bool{}, budget: a.forceBudget}
	result := s.sub(lhs, rhs)
	return result, s.budgetHit
}

func (s *subtyper) sub(a, b LazyType) bool {
	ka, kb := lazyKey(a), lazyKey(b)

	// Reflexivity: identical types are subtypes by axiom, with no expansion. This
	// settles X <: X for any X — including a non-regular recursive instantiation
	// like Grow<number> <: Grow<number> — without forcing either side, since the
	// two LazyRefs share a canonical key. (Without this, one-sided forcing would
	// grow the left unboundedly against a fixed right and only the budget would
	// stop it.)
	if ka == kb {
		return true
	}

	// Coinductive guard: if this exact pair is already being proven on the
	// current path, assume it holds (greatest fixed point). This is what makes
	// recursive-vs-recursive subtyping terminate.
	key := subPair{ka, kb}
	if s.seen[key] {
		return true
	}
	s.seen[key] = true
	defer delete(s.seen, key)

	// Force alias references on demand (the lazy step), guarded by the budget.
	if ar, ok := a.(*LazyRef); ok {
		if s.budget <= 0 {
			s.budgetHit = true
			return true // give up conservatively; non-regular recursion
		}
		s.budget--
		return s.sub(ar.force(), b)
	}
	if br, ok := b.(*LazyRef); ok {
		if s.budget <= 0 {
			s.budgetHit = true
			return true
		}
		s.budget--
		return s.sub(a, br.force())
	}

	// Union on the left: every member must be <: b.
	if au, ok := a.(*LazyUnion); ok {
		for _, m := range au.Members {
			if !s.sub(m, b) {
				return false
			}
		}
		return true
	}
	// Union on the right (a is not a union): a <: some member. Checked before the
	// concrete-a cases so e.g. Null <: (List | Null) succeeds via the Null member.
	if bu, ok := b.(*LazyUnion); ok {
		for _, m := range bu.Members {
			if s.sub(a, m) {
				return true
			}
		}
		return false
	}

	switch at := a.(type) {
	case *LazyPrim:
		bt, ok := b.(*LazyPrim)
		return ok && at.Name == bt.Name
	case *LazyNull:
		_, ok := b.(*LazyNull)
		return ok
	case *LazyCtor:
		bt, ok := b.(*LazyCtor)
		if !ok || at.Name != bt.Name || len(at.Args) != len(bt.Args) {
			return false
		}
		for i := range at.Args {
			if !s.sub(at.Args[i], bt.Args[i]) { // covariant args (spike-grade)
				return false
			}
		}
		return true
	case *LazyObj:
		bt, ok := b.(*LazyObj)
		if !ok {
			return false
		}
		for name, bf := range bt.Fields { // width: a must have every field b needs
			af, ok := at.Fields[name]
			if !ok || !s.sub(af, bf) { // depth: covariant
				return false
			}
		}
		return true
	}
	return false
}

// lazyKey renders a lazy type to a canonical string for the coinductive seen-set.
// A LazyRef is keyed by its (name, args) instantiation WITHOUT forcing — that is
// precisely what lets a regular recursive type close its loop: the same
// instantiation recurs with the same key.
func lazyKey(t LazyType) string {
	switch ty := t.(type) {
	case *LazyPrim:
		return ty.Name
	case *LazyNull:
		return "Null"
	case *LazyObj:
		names := make([]string, 0, len(ty.Fields))
		for k := range ty.Fields {
			names = append(names, k)
		}
		sort.Strings(names)
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = n + ":" + lazyKey(ty.Fields[n])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case *LazyUnion:
		parts := make([]string, len(ty.Members))
		for i, m := range ty.Members {
			parts[i] = lazyKey(m)
		}
		sort.Strings(parts)
		return "(" + strings.Join(parts, "|") + ")"
	case *LazyCtor:
		return ctorKey(ty.Name, ty.Args)
	case *LazyRef:
		return ctorKey(ty.Name, ty.Args)
	}
	return "?"
}

func ctorKey(name string, args []LazyType) string {
	if len(args) == 0 {
		return name
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = lazyKey(a)
	}
	return fmt.Sprintf("%s<%s>", name, strings.Join(parts, ","))
}
