package simplesub

import (
	"fmt"
	"sort"
)

// ---- Level-2 regularity check ("checkRegular") ----
//
// Recursive type aliases are safe to expand (the cycle cache produces a finite
// μ-knot) exactly when they generate finitely many distinct instantiation
// states — i.e. when recursion is *regular*: a parameter is passed around a
// recursion cycle without growing structurally. They are unsafe when some
// parameter grows under a type constructor each lap, so the set of reachable
// instantiations is infinite:
//
//	type List<T>  = {head: T, tail: List<T> | Null}   // T passed through        → regular   (accept)
//	type Grow<T>  = Grow<Array<T>>                     // T wrapped in Array each lap → expanding (reject)
//
// CheckRegular is a sound, conservative, decidable static check: it rejects an
// alias whose body makes a recursive call (into its own recursion cycle) passing
// an argument in which one of the alias's formal parameters appears as a *proper
// subterm* (nested under a constructor) rather than as the whole argument. This
// accepts regular recursion (List, and TS-style Json / Awaited / DeepPartial,
// which recurse on a pass-through or a structurally-smaller component) and
// rejects expanding recursion (Grow). It is incomplete by necessity — an
// expanding alias whose base case always fires terminates but is still rejected,
// since deciding that is the halting problem — so the runtime depth budget
// remains as the backstop for what slips through (e.g. expansion gated on a
// conditional).
//
// Mutual recursion is handled via the alias call graph: "recursive call" means a
// reference to any alias in the same strongly-connected component (SCC).

// RegularityError reports an alias rejected for expanding (non-regular)
// recursion.
type RegularityError struct {
	Alias string
	Param string
}

func (e RegularityError) Error() string {
	return fmt.Sprintf(
		"type alias %q is not regular: parameter %q grows under a type constructor "+
			"in a recursive call, so its expansion is unbounded; introduce a nominal "+
			"type to break the recursion or remove the growing wrapper",
		e.Alias, e.Param)
}

// CheckRegular verifies every alias in the evaluator is regular. It returns the
// errors for all offending aliases (sorted by name for determinism), or nil.
func (e *TypeEvaluator) CheckRegular() []error {
	scc := e.recursionGroups()

	names := make([]string, 0, len(e.aliases))
	for name := range e.aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		alias := e.aliases[name]
		group := scc[name]
		if group == nil {
			continue // not part of any recursion cycle
		}
		paramSet := map[string]bool{}
		for _, p := range alias.Params {
			paramSet[p] = true
		}
		// Walk the body; at each recursive call (a ref into this alias's SCC),
		// flag any argument that contains a parameter as a proper subterm.
		bad := map[string]bool{}
		findExpandingCalls(alias.Body, group, paramSet, bad)
		if len(bad) > 0 {
			offending := keysSorted(bad)
			for _, p := range offending {
				errs = append(errs, RegularityError{Alias: name, Param: p})
			}
		}
	}
	return errs
}

// recursionGroups returns, for each alias that participates in a recursion cycle,
// the set of alias names in its strongly-connected component (including itself).
// Aliases not in any cycle map to nil.
func (e *TypeEvaluator) recursionGroups() map[string]map[string]bool {
	// Direct call edges: alias name -> set of alias names it references.
	edges := map[string]map[string]bool{}
	for name, alias := range e.aliases {
		edges[name] = map[string]bool{}
		collectAliasRefs(alias.Body, e.aliases, edges[name])
	}
	// Transitive closure (small alias sets, so a fixpoint over the relation is
	// fine and avoids a full Tarjan SCC implementation).
	reach := map[string]map[string]bool{}
	for name := range edges {
		reach[name] = map[string]bool{}
		for d := range edges[name] {
			reach[name][d] = true
		}
	}
	changed := true
	for changed {
		changed = false
		for a := range reach {
			for b := range reach[a] {
				for c := range reach[b] {
					if !reach[a][c] {
						reach[a][c] = true
						changed = true
					}
				}
			}
		}
	}
	// a and b are in the same SCC iff each transitively reaches the other.
	groups := map[string]map[string]bool{}
	for a := range reach {
		if !reach[a][a] {
			continue // a does not reach itself ⇒ not recursive
		}
		g := map[string]bool{a: true}
		for b := range reach[a] {
			if reach[b][a] {
				g[b] = true
			}
		}
		groups[a] = g
	}
	return groups
}

// collectAliasRefs records which defined aliases a type expression references.
func collectAliasRefs(expr TyExpr, aliases map[string]*TyAlias, out map[string]bool) {
	switch t := expr.(type) {
	case *TyRef:
		if _, ok := aliases[t.Name]; ok {
			out[t.Name] = true
		}
		for _, a := range t.Args {
			collectAliasRefs(a, aliases, out)
		}
	case *TyUnion:
		for _, m := range t.Members {
			collectAliasRefs(m, aliases, out)
		}
	case *TyArray:
		collectAliasRefs(t.Elem, aliases, out)
	case *TyRecord:
		for _, f := range t.Fields {
			collectAliasRefs(f, aliases, out)
		}
	case *TyCond:
		collectAliasRefs(t.Check, aliases, out)
		collectAliasRefs(t.Extends, aliases, out)
		collectAliasRefs(t.Then, aliases, out)
		collectAliasRefs(t.Else, aliases, out)
	case *TyKeyof:
		collectAliasRefs(t.Target, aliases, out)
	case *TyIndex:
		collectAliasRefs(t.Target, aliases, out)
		collectAliasRefs(t.Index, aliases, out)
	}
}

// findExpandingCalls walks expr; at each reference into the recursion group, it
// checks every argument for a parameter occurring as a proper subterm (nested
// under a constructor). Such a parameter grows each lap, so it is recorded in
// bad. A parameter passed through as the whole argument (depth 0) is regular.
func findExpandingCalls(expr TyExpr, group, params, bad map[string]bool) {
	switch t := expr.(type) {
	case *TyRef:
		if group[t.Name] {
			for _, arg := range t.Args {
				for p := range params {
					if containsParamNested(arg, p, false) {
						bad[p] = true
					}
				}
			}
		}
		for _, arg := range t.Args {
			findExpandingCalls(arg, group, params, bad)
		}
	case *TyUnion:
		for _, m := range t.Members {
			findExpandingCalls(m, group, params, bad)
		}
	case *TyArray:
		findExpandingCalls(t.Elem, group, params, bad)
	case *TyRecord:
		for _, f := range t.Fields {
			findExpandingCalls(f, group, params, bad)
		}
	case *TyCond:
		findExpandingCalls(t.Check, group, params, bad)
		findExpandingCalls(t.Extends, group, params, bad)
		findExpandingCalls(t.Then, group, params, bad)
		findExpandingCalls(t.Else, group, params, bad)
	case *TyKeyof:
		findExpandingCalls(t.Target, group, params, bad)
	case *TyIndex:
		findExpandingCalls(t.Target, group, params, bad)
		findExpandingCalls(t.Index, group, params, bad)
	}
}

// containsParamNested reports whether parameter p appears inside expr strictly
// below at least one type constructor. nested tracks whether we have already
// descended under a constructor: a bare `TyRef{p}` at the top (nested=false) is
// pass-through (regular); the same ref under an Array/Record/etc. (nested=true)
// is growth.
func containsParamNested(expr TyExpr, p string, nested bool) bool {
	switch t := expr.(type) {
	case *TyRef:
		if t.Name == p && len(t.Args) == 0 {
			return nested
		}
		// A constructor application like Array<...> via TyRef args, or another
		// alias/nominal applied to args: descending into the args is nesting.
		for _, a := range t.Args {
			if containsParamNested(a, p, true) {
				return true
			}
		}
		return false
	case *TyArray:
		return containsParamNested(t.Elem, p, true)
	case *TyRecord:
		for _, f := range t.Fields {
			if containsParamNested(f, p, true) {
				return true
			}
		}
		return false
	case *TyUnion:
		// A union is not itself a growth constructor: `T | Null` keeps T at the
		// same depth, so preserve the incoming `nested` flag.
		for _, m := range t.Members {
			if containsParamNested(m, p, nested) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func keysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
