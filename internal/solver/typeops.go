package solver

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// maxExpandDepth caps how many times an alias may expand along one reduction path. The
// active-state guard already stops a regular recursive alias, whose instantiation state repeats.
// This budget backstops an expanding recursive alias such as `type Grow<T> = Grow<Array<T>>`,
// whose argument grows every lap so its state never repeats and the active guard never matches.
// No finite analytical bound exists for that fragment, so the budget stops the walk and the
// operator over the unexpanded alias stays symbolic.
const maxExpandDepth = 200

// typeEvaluator reduces a residual type-level operator to its value. It handles `keyof T`, indexed
// access `T[K]`, and the conditional `if C : E { … } else { … }`; later operators join as they
// land. Only constrain invokes it, to check a constraint against a residual. Annotation and display
// keep the residual symbolic, so a stored type prints `keyof {x: number}` or `Point["x"]` the way
// the source wrote it, never the reduced value.
//
// reduce projects the operand's keys: a ground `keyof {x: number}` yields `"x"`, and an alias or
// class operand expands to the referenced type's keys, the transparent-but-named treatment an
// alias itself gets under constrain. A `keyof T` over a type parameter has no ground key set, so
// it stays the symbolic KeyofType.
//
// A recursive alias reached through an operand is made safe by a two-part termination strategy:
//
//   - active holds the alias instantiations currently being expanded, each keyed by the alias
//     name together with its rendered arguments. When one recurs with the identical key, the
//     evaluator leaves that reference as the unexpanded alias node rather than expanding it again.
//     A recursive alias such as `type List<T> = {head: T, tail: List<T> | null}` therefore reduces
//     to a finite type whose recursive position points back to the alias instead of unfolding
//     forever.
//   - depth caps expansions along one path. It backstops an expanding recursion whose argument
//     grows every lap, so its key never repeats and the active guard never fires.
//
// The evaluator mutates no solver state — no bound or variable is touched. It accumulates
// reduction diagnostics on errs, but a fresh evaluator is minted per reduction, so nothing
// leaks across calls. It builds its result unions through newUnion with a nil Context so
// newUnion's subsumption never calls constrain — which reduces residuals through this evaluator
// and would otherwise re-enter it and loop.
type typeEvaluator struct {
	// ctx is the alias environment, used to expand an alias operand and project a class body so a
	// reduction reaches the referenced type's keys. constrain and the test expander both supply a
	// non-nil Context; reduce is never invoked without one.
	ctx    *Context
	active set.Set[string]
	depth  int
	// seen is the enclosing constraint's cycle-detection set, carried in so a conditional's
	// `Check <: Extends` probe shares the caller's alias-cycle guard. A conditional reduces by
	// re-entering constrain to decide its branch, and constrain expands an alias operand and
	// re-reduces the conditional in its body, so a self-referential alias such as
	// `type Bad = if Bad : number { number } else { string }` would recurse without bound if the
	// probe started a fresh set. Reusing the caller's set closes that cycle the same way two
	// structurally-equal instances of a recursive alias close through constrain's seen-set. The
	// value solve seeds it fresh at each constraint site, and the test expander passes an empty set.
	seen set.Set[constraintKey]
	// errs collects the diagnostics a reduction produces. `keyof` reduction is total and adds
	// none; indexed-access reduction records an UnknownObjectKeyError or a
	// TupleIndexOutOfRangeError when a ground access resolves to no member. constrain reads
	// these after reducing an operator it is checking a constraint against, so a malformed
	// `{x: number}["z"]` surfaces at the constraint site. It records diagnostics, not solver
	// state, so no bound or variable is mutated.
	errs []SolverError
}

func newTypeEvaluator(ctx *Context, seen set.Set[constraintKey]) *typeEvaluator {
	return &typeEvaluator{ctx: ctx, active: set.NewSet[string](), depth: maxExpandDepth, seen: seen}
}

// reduce reduces one type-level operator node to its value, returning any other type
// unchanged. A node whose operand is not yet ground reduces to the same operator rebuilt
// around the expanded operand, so it stays symbolic and reduces later once the operand
// grounds.
func (e *typeEvaluator) reduce(t soltype.Type) soltype.Type {
	switch t := t.(type) {
	case *soltype.KeyofType:
		return e.reduceKeyof(t.Operand, t.Exact)
	case *soltype.IndexType:
		return e.reduceIndex(t.Target, t.Index, t.Exact)
	case *soltype.TypeofType:
		// A `typeof x` query reduces to the value's resolved type. constrain unwraps it directly
		// in its pre-switch, so this arm serves a `typeof` reached through another operator.
		return t.Ty
	case *soltype.CondType:
		return e.reduceCond(t)
	case *soltype.TupleType:
		return e.reduceTuple(t)
	case *soltype.ObjectType:
		return e.reduceObject(t)
	default:
		return t
	}
}

// reduceCond reduces `if Check : Extends { Then } else { Else }` by deciding `Check <: Extends`
// and selecting a branch, mirroring the old checker's CondType case
// (internal/checker/expand_type.go), which unifies Check with Extends and returns Then on success
// or Else on failure. The decision runs only when both operands are ground:
//
//   - a ground Check and Extends decide the branch with an assignability probe, and the reduction
//     continues into the selected branch alone, so an error in the unselected branch never surfaces;
//   - a Check or Extends still carrying a type parameter or an unreduced residual keeps the whole
//     conditional symbolic, rebuilt around the reduced Check and Extends, and reduces later once
//     they ground. Then and Else stay unreduced in the symbolic form, since neither is selected yet.
//
// A conditional written over a naked type parameter distributes: a Check that grounds to a union is
// decided one member at a time and the branch results union, so `type Wrap<T> = if T : string { [T] }
// else { boolean }` reduces `Wrap<"a" | 1>` to `["a"] | boolean`. An Extends carrying an `infer U`
// binder routes through reduceCondInfer, which captures the type at each binder's position first.
func (e *typeEvaluator) reduceCond(t *soltype.CondType) soltype.Type {
	check := e.reduce(t.Check)
	extends := e.reduce(t.Extends)
	// An Extends operand holds its `infer` binders by design, so only the Check is tested for one. A
	// binder reaches the Check position only when an enclosing conditional left its Then branch
	// unsubstituted. That position stands for a type no match has chosen yet, so the Check is not
	// ground and the conditional stays symbolic.
	if !condOperandGround(check) || containsInfer(check) || !condOperandGround(extends) {
		return &soltype.CondType{Check: check, Extends: extends, Then: t.Then, Else: t.Else, Distribute: t.Distribute}
	}
	if union, ok := check.(*soltype.UnionType); ok && t.Distribute {
		return e.distributeCond(t, union, extends)
	}
	if containsInfer(extends) {
		return e.reduceCondInfer(t, check, extends)
	}
	if e.ctx.condExtends(check, extends, e.seen) {
		return e.reduce(t.Then)
	}
	return e.reduce(t.Else)
}

// distributeCond decides a distributive conditional one union member at a time and unions the
// branches each member selects, so `type Wrap<T> = if T : string { [T] } else { boolean }` reduces
// `Wrap<"a" | 1>` to `["a"] | boolean` rather than to the single branch the whole union selects.
//
// A branch that names the distributed type parameter reads it as the member, not the union, which is
// why `Wrap<"a" | "b">` reduces to `["a"] | ["b"]`. Expanding the alias replaced every occurrence of
// that parameter — the Check position and the two branches alike — with one shared type pointer, so
// rewriting the branches' occurrences of the unreduced Check narrows exactly the positions the
// parameter stood at. The Extends operand keeps the union, matching TypeScript, where each member is
// tested against the whole right-hand side.
//
// Each member reduces through a copy of the conditional with Distribute cleared: the member is no
// longer a union, and clearing it states that this pass already applied the rule.
func (e *typeEvaluator) distributeCond(t *soltype.CondType, check *soltype.UnionType, extends soltype.Type) soltype.Type {
	parts := make([]soltype.Type, len(check.Types))
	for i, member := range check.Types {
		parts[i] = e.reduceCond(&soltype.CondType{
			Check:   member,
			Extends: extends,
			Then:    substituteOccurrences(t.Then, t.Check, member),
			Else:    substituteOccurrences(t.Else, t.Check, member),
		})
	}
	return newUnion(nil, parts, false)
}

// substituteOccurrences rewrites every occurrence of the from type inside in to the to type,
// matching on pointer identity. Expanding a generic alias substitutes one shared pointer for each
// occurrence of a type parameter, so identity picks out the positions that parameter stood at
// without touching an equal type the body wrote itself.
func substituteOccurrences(in, from, to soltype.Type) soltype.Type {
	return in.Accept(&occurrenceSubst{from: from, to: to}, soltype.Positive)
}

// occurrenceSubst is the rewriting visitor behind substituteOccurrences.
type occurrenceSubst struct{ from, to soltype.Type }

func (s *occurrenceSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if t == s.from {
		return soltype.EnterResult{Type: s.to, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *occurrenceSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// reduceCondInfer decides a conditional whose Extends operand declares `infer` captures, such as
// `if T : [infer U] { U } else { boolean }`. It runs in three steps:
//
//  1. matchInfer walks Check against Extends and records the type at each binder's position.
//  2. Those captures are substituted into Extends, turning the pattern into an ordinary type, and
//     the resulting `Check <: Extends` probe decides the branch the same way a capture-free
//     conditional does. Substituting first is what lets a pattern mix captures with written types:
//     `[infer A, number]` over `[number, string]` binds A but the probe still rejects on element 1.
//  3. On success the captures are substituted into the Then branch, so a reference to a captured
//     name reduces to the type that was matched.
//
// A structural mismatch, or a pattern position the matcher has no arm for, leaves a binder without a
// capture and selects Else. The post-substitution check for a surviving binder is what makes that
// total: no branch is ever selected with an unsubstituted capture in it.
func (e *typeEvaluator) reduceCondInfer(t *soltype.CondType, check, extends soltype.Type) soltype.Type {
	captures := map[string]soltype.Type{}
	if !e.matchInfer(check, extends, captures) {
		return e.reduce(t.Else)
	}
	pattern := substituteBinders(extends, captures)
	if containsInfer(pattern) {
		return e.reduce(t.Else)
	}
	if !e.ctx.condExtends(check, pattern, e.seen) {
		return e.reduce(t.Else)
	}
	return e.reduce(substituteCaptures(t.Then, captures))
}

// matchInfer walks the ground type check alongside the Extends pattern and records into captures
// the type sitting at each `infer` binder's position, reporting whether the two aligned. A pattern
// subtree declaring no binder needs no alignment — the `Check <: Extends` probe decides it — so the
// walk stops there and reports success.
//
// The arms cover the positions a capture is written in: a tuple element, an object property, a
// function parameter or return, a promise payload, and a type argument of an alias or class
// reference. A pattern position with no arm, such as a union or a borrow, reports a mismatch, which
// selects the Else branch. Reporting success there instead would leave the binder uncaptured, and
// the caller would reject the branch anyway.
func (e *typeEvaluator) matchInfer(check, pattern soltype.Type, captures map[string]soltype.Type) bool {
	if binder, ok := pattern.(*soltype.InferType); ok {
		capture(captures, binder.Name, check)
		return true
	}
	if !containsInfer(pattern) {
		return true
	}
	switch pat := pattern.(type) {
	case *soltype.TupleType:
		tup, ok := e.alignCheck(check).(*soltype.TupleType)
		if !ok || len(tup.Elems) != len(pat.Elems) || hasRestSpread(tup.Elems) {
			return false
		}
		for i, el := range pat.Elems {
			if !e.matchInfer(tup.Elems[i], el, captures) {
				return false
			}
		}
		return true
	case *soltype.ObjectType:
		obj, ok := e.groundToObject(check)
		if !ok {
			return false
		}
		for _, el := range pat.Elems {
			prop, ok := el.(*soltype.PropertyElem)
			if !ok {
				// Only a named property pattern has a member to read the capture off. Any other
				// member is skipped, and a binder it carries stays uncaptured, which the caller
				// rejects when it finds one surviving substitution.
				continue
			}
			read, hasValue, _ := memberReadContribution(obj, prop.Name)
			if !hasValue {
				return false
			}
			if !e.matchInfer(read, prop.Type, captures) {
				return false
			}
		}
		return true
	case *soltype.FuncType:
		fn, ok := e.alignCheck(check).(*soltype.FuncType)
		if !ok || len(fn.Params) != len(pat.Params) {
			return false
		}
		for i, p := range pat.Params {
			if !e.matchInfer(fn.Params[i].Type, p.Type, captures) {
				return false
			}
		}
		return e.matchInfer(fn.Ret, pat.Ret, captures)
	case *soltype.PromiseType:
		promise, ok := e.alignCheck(check).(*soltype.PromiseType)
		if !ok {
			return false
		}
		return e.matchInfer(promise.Inner, pat.Inner, captures)
	case *soltype.ClassType:
		cls, ok := e.alignCheck(check).(*soltype.ClassType)
		if !ok || cls.Name != pat.Name {
			return false
		}
		return e.matchInferArgs(cls.TypeArgs, pat.TypeArgs, captures)
	case *soltype.AliasType:
		return e.matchInferAlias(check, pat, captures)
	default:
		return false
	}
}

// matchInferAlias matches a named-alias pattern such as `Box<infer U>`. A check naming the same
// alias matches argument by argument, which is the exact case `Awaited`-shaped patterns rely on.
// Otherwise the pattern's alias expands to its body and the match retries against that, so
// `Box<infer U>` over `type Box<T> = {v: T}` still captures U from a plain `{v: number}`. Expansion
// runs under the shared termination guard, so a recursive alias pattern stops rather than unfolding
// forever, and a guard that blocks expansion reports a mismatch.
//
// Comparing names first is what makes a recursive alias such as `type List<T> = {head: T, tail:
// List<T> | null}` match: expanding both sides would reach the `List<T> | null` field, a union the
// matcher has no arm for. The match against the expanded body runs inside the guard's callback,
// where the alias is still on the active path, so a body that re-references the alias stops.
func (e *typeEvaluator) matchInferAlias(check soltype.Type, pat *soltype.AliasType, captures map[string]soltype.Type) bool {
	if alias, ok := e.reduce(check).(*soltype.AliasType); ok && alias.Name == pat.Name {
		return e.matchInferArgs(alias.TypeArgs, pat.TypeArgs, captures)
	}
	matched := false
	e.expandAliasGuarded(pat, nil, func(body soltype.Type) soltype.Type {
		matched = e.matchInfer(check, body, captures)
		return nil
	})
	return matched
}

// matchInferArgs matches two positional type-argument lists, the shared body of the class and alias
// arms. Differing arity is a mismatch, since no position lines up.
func (e *typeEvaluator) matchInferArgs(checkArgs, patArgs []soltype.Type, captures map[string]soltype.Type) bool {
	if len(checkArgs) != len(patArgs) {
		return false
	}
	for i, arg := range patArgs {
		if !e.matchInfer(checkArgs[i], arg, captures) {
			return false
		}
	}
	return true
}

// capture records the type matched at one binder's position. A name written at two positions, as in
// `[infer U, infer U]`, keeps both matched types by unioning them, so `[number, string]` captures
// `number | string`.
func capture(captures map[string]soltype.Type, name string, matched soltype.Type) {
	if prev, ok := captures[name]; ok {
		captures[name] = newUnion(nil, []soltype.Type{prev, matched}, false)
		return
	}
	captures[name] = matched
}

// alignCheck reduces a check operand toward the structural shape a pattern matches against. A
// `typeof` query resolves to the value's type and a named alias expands to its body, both under the
// termination guard, so `if Pair : [infer A, infer B]` over `type Pair = [number, string]` aligns
// with the tuple pattern. A guard that blocks expansion leaves the alias in place, which the caller
// reads as a mismatch. The object arm uses groundToObject instead, which also projects a class body.
func (e *typeEvaluator) alignCheck(check soltype.Type) soltype.Type {
	switch c := check.(type) {
	case *soltype.TypeofType:
		return e.alignCheck(c.Ty)
	case *soltype.AliasType:
		return e.expandAliasGuarded(c, c, func(body soltype.Type) soltype.Type {
			return e.alignCheck(body)
		})
	default:
		return e.reduce(check)
	}
}

// substituteBinders rewrites each `infer U` binder in a pattern to the type captured for U, turning
// the Extends operand into an ordinary type the `Check <: Extends` probe can decide. A binder whose
// name has no capture is left in place, which is how the caller detects a match that bound only part
// of the pattern.
func substituteBinders(t soltype.Type, captures map[string]soltype.Type) soltype.Type {
	return substituteInfer(t, captures, true)
}

// substituteCaptures rewrites each reference to a captured name in a selected branch to the type
// captured for it, so the branch reduces to what the match found. A nested conditional that binds
// the same name again keeps its own binder and its own Then branch, so the inner capture shadows the
// outer one rather than being overwritten by it.
func substituteCaptures(t soltype.Type, captures map[string]soltype.Type) soltype.Type {
	return substituteInfer(t, captures, false)
}

// substituteInfer is the shared body of substituteBinders and substituteCaptures. Its two callers
// need opposite halves of the `infer` nodes, which binders selects: a pattern replaces the binders
// that declare the names, and a branch replaces the references that read them.
func substituteInfer(t soltype.Type, captures map[string]soltype.Type, binders bool) soltype.Type {
	if len(captures) == 0 {
		return t
	}
	return t.Accept(&inferSubst{captures: captures, binders: binders}, soltype.Positive)
}

// inferSubst is the rewriting visitor behind substituteInfer. It replaces a captured `infer` node
// with the matched type and skips that node's children, since the replacement is already reduced.
type inferSubst struct {
	captures map[string]soltype.Type
	binders  bool
}

func (s *inferSubst) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.InferType:
		if matched, found := s.captures[t.Name]; found && t.Binder == s.binders {
			return soltype.EnterResult{Type: matched, SkipChildren: true}
		}
	case *soltype.CondType:
		if shadowed := shadowedCaptures(t, s.captures); len(shadowed) > 0 {
			return s.rewriteShadowingCond(t, pol, shadowed)
		}
	}
	return soltype.EnterResult{}
}

func (s *inferSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// rewriteShadowingCond rewrites a nested conditional that declares a name the enclosing conditional
// also captured. Its Then branch reads the inner capture, so that branch is rewritten with the
// shadowed names dropped and the inner binder survives to be filled when the inner conditional
// reduces. The other three operands sit outside the inner name's scope, so they still read the outer
// captures and are rewritten with the full set.
func (s *inferSubst) rewriteShadowingCond(t *soltype.CondType, pol soltype.Polarity, shadowed []string) soltype.EnterResult {
	inner := &inferSubst{captures: withoutNames(s.captures, shadowed), binders: s.binders}
	return soltype.EnterResult{
		Type: &soltype.CondType{
			Check:      t.Check.Accept(s, pol),
			Extends:    t.Extends.Accept(s, pol),
			Then:       t.Then.Accept(inner, pol),
			Else:       t.Else.Accept(s, pol),
			Distribute: t.Distribute,
		},
		SkipChildren: true,
	}
}

// shadowedCaptures returns the names the nested conditional t declares that captures also holds a
// type for. Those are the names whose meaning differs inside t's Then branch.
func shadowedCaptures(t *soltype.CondType, captures map[string]soltype.Type) []string {
	var shadowed []string
	f := &inferBinderFinder{}
	t.Extends.Accept(f, soltype.Positive)
	for _, name := range f.names {
		if _, found := captures[name]; found {
			shadowed = append(shadowed, name)
		}
	}
	return shadowed
}

// inferBinderFinder collects the names the `infer U` binders of one Extends operand declare.
type inferBinderFinder struct{ names []string }

func (f *inferBinderFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if iv, ok := t.(*soltype.InferType); ok && iv.Binder {
		f.names = append(f.names, iv.Name)
	}
	return soltype.EnterResult{}
}

func (f *inferBinderFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type { return t }

// withoutNames copies captures with the given names removed, the capture set that is in scope inside
// a nested conditional's Then branch.
func withoutNames(captures map[string]soltype.Type, names []string) map[string]soltype.Type {
	out := make(map[string]soltype.Type, len(captures))
	for name, matched := range captures {
		out[name] = matched
	}
	for _, name := range names {
		delete(out, name)
	}
	return out
}

// containsInfer reports whether t holds an `infer` node with no capture substituted into it yet. A
// conditional consults it on its Extends operand to route to the matcher, and on a substituted
// pattern to reject a match that left a binder uncaptured.
func containsInfer(t soltype.Type) bool {
	f := &inferFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// inferFinder is the walking visitor behind containsInfer. It flags the first `infer` node it
// reaches and skips that node's children, since one occurrence is enough.
type inferFinder struct{ found bool }

func (f *inferFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if _, ok := t.(*soltype.InferType); ok {
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *inferFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type { return t }

// condExtends decides a conditional's `Check <: Extends` test with an assignability probe. The
// trial runs under a discard-only probe, so a speculative match records no bound and leaves no
// solver state behind, preserving the evaluator's no-mutation invariant. It runs constrain over a
// clone of the caller's cycle-detection set, so a recursive alias reached through the probe closes
// through the same seen-set the caller built up, while the clone keeps the probe's own keys out of
// the caller's set. An empty result means the subtype check held, selecting the Then branch.
func (c *Context) condExtends(check, extends soltype.Type, seen set.Set[constraintKey]) bool {
	return !hasHardError(c.trialUnderProbeSeen(check, extends, seen.Clone()))
}

// condOperandGround reports whether a conditional's Check or Extends operand is ground enough to
// decide the branch. An operand carrying a type variable, a skolem, or an unreduced residual
// operator is abstract, so the `Check <: Extends` probe cannot decide a branch over it and the
// conditional stays symbolic. containsFreeVar catches the variable and skolem cases;
// containsResidualOp catches a residual such as a `keyof T` the reduction left symbolic.
func condOperandGround(t soltype.Type) bool {
	return !containsFreeVar(t) && !containsResidualOp(t)
}

// reduceTuple splices each `...P` spread element whose operand grounds to a concrete tuple into
// position, reducing `[...P, x]` to a plain tuple. A tuple with no spread element returns unchanged
// so a plain tuple keeps its pointer. The splice mirrors the M4 literal case in inferTuple:
//
//   - a non-spread element carries through, reduced in case it holds a nested operator;
//   - a spread whose operand grounds to an exact tuple contributes that tuple's elements;
//   - an inexact operand splices only as the last element, where its known prefix extends the
//     result and its open tail makes the result inexact too;
//   - a spread whose operand stays abstract — a type parameter, an alias the guard leaves
//     unexpanded, or an inexact operand in a non-last position — is kept as a `...P` element around
//     the reduced operand, so the tuple stays inert and reduces later once the operand grounds.
func (e *typeEvaluator) reduceTuple(t *soltype.TupleType) soltype.Type {
	if !hasRestSpread(t.Elems) {
		return t
	}
	elems := make([]soltype.Type, 0, len(t.Elems))
	inexact := t.Inexact
	for i, el := range t.Elems {
		rest, ok := el.(*soltype.RestSpreadType)
		if !ok {
			elems = append(elems, e.reduce(el))
			continue
		}
		operand := e.groundSpreadOperand(rest.Operand)
		tup, ok := operand.(*soltype.TupleType)
		last := i == len(t.Elems)-1
		if !ok || hasRestSpread(tup.Elems) || (tup.Inexact && !last) {
			// The operand is not a fully-ground tuple, or is inexact in a non-last position: keep
			// the spread residual around the reduced operand rather than splicing.
			elems = append(elems, &soltype.RestSpreadType{Operand: operand})
			continue
		}
		elems = append(elems, tup.Elems...)
		if tup.Inexact {
			inexact = true
		}
	}
	return &soltype.TupleType{Elems: elems, Inexact: inexact}
}

// groundTuple reduces a tuple's `...P` spreads and reports the concrete tuple when none remain. A
// tuple with no spread returns unchanged with ok=true. A tuple whose spread never grounds returns
// ok=false, so `keyof`/indexed access over it stays symbolic rather than projecting the spread
// element as if it were a single position.
func (e *typeEvaluator) groundTuple(t *soltype.TupleType) (*soltype.TupleType, bool) {
	if !hasRestSpread(t.Elems) {
		return t, true
	}
	reduced, ok := e.reduceTuple(t).(*soltype.TupleType)
	if !ok || hasRestSpread(reduced.Elems) {
		return nil, false
	}
	return reduced, true
}

// groundSpreadOperand reduces a tuple-spread operand toward a concrete tuple. It reduces any
// nested operator, then expands a named alias to its body under the shared termination guard, so
// `[...Pair, x]` over `type Pair = [number, string]` grounds to the referenced tuple. A type
// parameter, a recurring alias state, an exhausted budget, or an unresolved alias body each leaves
// the operand unexpanded, which keeps the spread symbolic.
func (e *typeEvaluator) groundSpreadOperand(operand soltype.Type) soltype.Type {
	reduced := e.reduce(operand)
	alias, ok := reduced.(*soltype.AliasType)
	if !ok {
		return reduced
	}
	return e.expandAliasGuarded(alias, reduced, func(body soltype.Type) soltype.Type {
		return e.groundSpreadOperand(body)
	})
}

// reduceObject merges a spread-carrying object's elements left to right into a plain ObjectType,
// following Flow's spread semantics. An object with no `...A` spread element returns unchanged, so a
// plain object keeps its pointer. Each element contributes its fields: a property as its own
// one-field group, a `...A` spread as the fields of the object its operand grounds to. A later
// field wins over an earlier key, except that a later optional field unions with the earlier value
// rather than overriding it, see mergeSpreadElem.
//
// Per element it mirrors reduceTuple: a non-spread member has its value types reduced in place, and
// a spread's operand is reduced and expanded toward a concrete object through groundObjectOperand,
// under the termination guard reduceKeyofAlias uses.
//
// Unlike reduceTuple, the merge is all-or-nothing. If any spread operand stays abstract, such as a
// type parameter or a truncated expanding alias, the whole object stays symbolic and carries every
// element in its reduced form, so it merges once the operand grounds. reduceTuple can finalize each
// ground spread independently, because tuple splice is positional concatenation and a later element
// never disturbs an earlier position. An object spread instead overrides earlier keys, so a field is
// safe to finalize only when no un-ground spread follows it. reduceObject keeps the whole object
// symbolic rather than finalizing just that safe suffix: it is simpler and renders the way the
// source wrote it, at the cost of re-reducing from scratch each pass.
func (e *typeEvaluator) reduceObject(t *soltype.ObjectType) soltype.Type {
	if !soltype.HasObjectSpread(t.Elems) {
		return t
	}
	reducedElems := make([]soltype.ObjTypeElem, len(t.Elems))
	operandElems := make([][]soltype.ObjTypeElem, 0, len(t.Elems))
	inexact := t.Inexact
	ground := true
	for i, el := range t.Elems {
		spread, ok := el.(*soltype.SpreadElem)
		if !ok {
			// A non-spread member has its value types reduced, then contributes as a one-field group.
			re := e.reduceElem(el)
			reducedElems[i] = re
			operandElems = append(operandElems, []soltype.ObjTypeElem{re})
			continue
		}
		operand := e.groundObjectOperand(spread.Type)
		reducedElems[i] = &soltype.SpreadElem{Type: operand}
		obj, ok := operand.(*soltype.ObjectType)
		if !ok || soltype.HasObjectSpread(obj.Elems) {
			ground = false
			continue
		}
		operandElems = append(operandElems, obj.Elems)
		inexact = inexact || obj.Inexact
	}
	if !ground {
		return &soltype.ObjectType{Elems: reducedElems, Inexact: inexact}
	}
	return &soltype.ObjectType{Elems: mergeSpreadOperands(operandElems), Inexact: inexact}
}

// reduceElem reduces the value types inside one non-spread object member, the object analogue of the
// `e.reduce(el)` reduceTuple runs on each non-spread tuple element. A property, getter, or setter
// carries a value type that may itself be an operator such as `keyof X`, so its type is reduced. A
// method or constructor carries only function types, which reduce leaves untouched, so it passes
// through unchanged.
func (e *typeEvaluator) reduceElem(el soltype.ObjTypeElem) soltype.ObjTypeElem {
	switch el := el.(type) {
	case *soltype.PropertyElem:
		return &soltype.PropertyElem{Name: el.Name, Type: e.reduce(el.Type), Optional: el.Optional, Readonly: el.Readonly}
	case *soltype.GetterElem:
		return &soltype.GetterElem{Name: el.Name, SelfParam: el.SelfParam, Type: e.reduce(el.Type)}
	case *soltype.SetterElem:
		return &soltype.SetterElem{Name: el.Name, SelfParam: el.SelfParam, Param: e.reduce(el.Param)}
	default:
		return el
	}
}

// groundObjectOperand reduces a `...A` spread operand toward the concrete object whose fields it
// contributes, the object analogue of the tuple's groundSpreadOperand. It resolves a `typeof` query
// to the value's type, projects a class instance body, and expands an alias to its body under the
// active-state and depth guard reduceKeyofAlias uses, so a recursive alias reached through a spread
// terminates. Any other kind — an object, a type variable, a nested operator — is reduced. It
// returns the reduced operand rather than an ok flag, so a caller keeps that reduced form when the
// operand does not ground, matching how reduceTuple keeps the operand it could not splice.
func (e *typeEvaluator) groundObjectOperand(operand soltype.Type) soltype.Type {
	switch op := operand.(type) {
	case *soltype.TypeofType:
		return e.groundObjectOperand(op.Ty)
	case *soltype.ClassType:
		if obj, ok := e.ctx.projectClassBody(op); ok {
			return obj
		}
		return op
	case *soltype.AliasType:
		key := soltype.PrintQualified(op)
		if e.active.Contains(key) || e.depth <= 0 {
			return op
		}
		body := e.ctx.expandAlias(op)
		if _, unresolved := body.(*soltype.ErrorType); unresolved {
			return op
		}
		e.active.Add(key)
		e.depth--
		red := e.groundObjectOperand(body)
		e.active.Remove(key)
		e.depth++
		return red
	default:
		return e.reduce(operand)
	}
}

// groundToObject reports the concrete ObjectType a spread operand contributes, or ok=false when the
// operand has no ground object shape — a type variable, a truncated expanding alias, or an object
// that still carries a spread. It is the ok-returning wrapper over groundObjectOperand that keyof
// and indexed access use to decide whether a spread target has grounded.
func (e *typeEvaluator) groundToObject(operand soltype.Type) (*soltype.ObjectType, bool) {
	obj, ok := e.groundObjectOperand(operand).(*soltype.ObjectType)
	if !ok || soltype.HasObjectSpread(obj.Elems) {
		return nil, false
	}
	return obj, true
}

// mergeSpreadOperands folds the field lists of an object spread's operands into one element list,
// preserving first-appearance order. A field whose name is new is appended; one that overlaps an
// earlier key is merged through mergeSpreadElem, so the operands compose left to right. It is
// shared by the type-level reduction and the literal-level inferObject, so the spread merge rule
// lives in one place.
func mergeSpreadOperands(operandElems [][]soltype.ObjTypeElem) []soltype.ObjTypeElem {
	total := 0
	for _, elems := range operandElems {
		total += len(elems)
	}
	out := make([]soltype.ObjTypeElem, 0, total)
	pos := make(map[string]int, total)
	for _, elems := range operandElems {
		for _, elem := range elems {
			name := soltype.ObjElemName(elem)
			if i, seen := pos[name]; seen {
				out[i] = mergeSpreadElem(out[i], elem)
				continue
			}
			pos[name] = len(out)
			out = append(out, elem)
		}
	}
	return out
}

// mergeSpreadElem combines an earlier object member with a later one of the same name under Flow's
// spread rule. A later required field overrides the earlier one, the rightmost-wins default. A
// later optional field instead shows the earlier value through: the merged value unions
// `earlier | later`, and the field stays required unless both are optional. `{...A, ...B}` with
// `A = {k: number}` and `B = {k?: string}` therefore yields `k: number | string`, required. A
// non-property member on either side has no optional flag to key the union off, so the later one
// overrides.
//
// The later operand supplies the merged Readonly flag in both branches, the same rightmost-wins
// source the override branch uses when it returns the later member whole.
func mergeSpreadElem(earlier, later soltype.ObjTypeElem) soltype.ObjTypeElem {
	ep, eok := earlier.(*soltype.PropertyElem)
	lp, lok := later.(*soltype.PropertyElem)
	if !eok || !lok || !lp.Optional {
		return later
	}
	return &soltype.PropertyElem{
		Name:     ep.Name,
		Type:     newUnion(nil, []soltype.Type{ep.Type, lp.Type}, false),
		Optional: ep.Optional && lp.Optional,
		Readonly: lp.Readonly,
	}
}

// reduceKeyof reduces `keyof operand` to the union of the operand's keys, mirroring the old
// checker's KeyOfType case (internal/checker/expand_type.go):
//
//   - an object projects its property, getter, and setter names as string-literal types;
//   - a tuple yields only its own numeric indices, omitting the inherited "length"; see keyofTuple;
//   - `keyof` distributes over a union or intersection, unioning each member's keys;
//   - `keyof` of a primitive, literal, `never`, or `unknown` is `never`, since none has
//     enumerable keys;
//   - an alias expands to its body and a class projects its instance body, and `keyof` reduces
//     over that under the termination guard;
//   - a `typeof` query resolves to the value's type, and `keyof` reduces over that.
//
// A type variable, a skolem, or a named reference the evaluator does not expand keeps the
// operator symbolic, rebuilt around the operand.
func (e *typeEvaluator) reduceKeyof(operand soltype.Type, exact bool) soltype.Type {
	switch op := operand.(type) {
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType:
		// The operand is itself an operator — a `keyof`, an indexed access, or a conditional. Reduce
		// it first, then take keyof its value, so a ground conditional operand selects its branch and
		// `keyof` projects that branch's keys. If the inner operator stays symbolic because its own
		// operands are not ground, wrap it as `keyof <inner>` rather than re-reducing the same shape
		// forever.
		inner := e.reduce(op)
		if isResidualOp(inner) {
			return &soltype.KeyofType{Operand: inner, Exact: exact}
		}
		return e.reduceKeyof(inner, exact)
	case *soltype.AliasType:
		return e.reduceKeyofAlias(op, exact)
	case *soltype.TypeofType:
		// `keyof typeof x` resolves the query to the value's type, then projects that type's keys.
		return e.reduceKeyof(op.Ty, exact)
	case *soltype.ObjectType:
		// An object carrying an unreduced `...A` spread has no ground key set, so `keyof` over it
		// stays symbolic until the spread grounds, mirroring the TupleType arm below.
		if obj, ok := e.groundToObject(op); ok {
			return e.keyofObject(obj)
		}
		return &soltype.KeyofType{Operand: operand, Exact: exact}
	case *soltype.ClassType:
		obj, ok := e.ctx.projectClassBody(op)
		if !ok {
			return &soltype.KeyofType{Operand: operand, Exact: exact}
		}
		return e.keyofObject(obj)
	case *soltype.TupleType:
		// A tuple carrying an unreduced `...P` spread has no ground index set, so `keyof` over it
		// stays symbolic until the spread grounds to a concrete tuple.
		if tup, ok := e.groundTuple(op); ok {
			return e.keyofTuple(tup)
		}
		return &soltype.KeyofType{Operand: operand, Exact: exact}
	case *soltype.UnionType:
		return e.keyofDistribute(op.Types, exact)
	case *soltype.IntersectionType:
		return e.keyofDistribute(op.Types, exact)
	case *soltype.PrimType, *soltype.LitType, *soltype.NeverType, *soltype.UnknownType:
		return &soltype.NeverType{}
	default:
		return &soltype.KeyofType{Operand: operand, Exact: exact}
	}
}

// reduceKeyofAlias reduces `keyof Alias` by expanding the alias and reducing `keyof` over its
// body under the termination guard, leaving the alias symbolic when the guard blocks expansion.
func (e *typeEvaluator) reduceKeyofAlias(op *soltype.AliasType, exact bool) soltype.Type {
	symbolic := &soltype.KeyofType{Operand: op, Exact: exact}
	return e.expandAliasGuarded(op, symbolic, func(body soltype.Type) soltype.Type {
		return e.reduceKeyof(body, exact)
	})
}

// expandAliasGuarded expands a named alias to its body and applies cont to the result, under the
// two-part termination guard that makes reduction safe over a recursive alias. The alias stays on
// the active path for the whole reduction of its body, so a member that re-references it, directly
// or through a chain, sees it active and stops. A recurring instantiation state, an exhausted
// budget, or an unresolved body each returns fallback with the alias left unexpanded, so the
// operator over it stays symbolic.
func (e *typeEvaluator) expandAliasGuarded(op *soltype.AliasType, fallback soltype.Type, cont func(body soltype.Type) soltype.Type) soltype.Type {
	key := soltype.PrintQualified(op)
	if e.active.Contains(key) || e.depth <= 0 {
		return fallback
	}
	body := e.ctx.expandAlias(op)
	if _, unresolved := body.(*soltype.ErrorType); unresolved {
		// expandAlias yields ErrorType for an unregistered alias, or one whose body a dep-graph
		// sibling has not filled yet. Keep the operator symbolic rather than reducing over `error`.
		return fallback
	}
	e.active.Add(key)
	e.depth--
	result := cont(body)
	e.active.Remove(key)
	e.depth++
	return result
}

// keyofObject projects an object's property, getter, and setter names as string-literal types
// and unions them. An empty projection collapses to `never`, the union identity newUnion returns
// for no members.
//
// An inexact object carries an unknown-keyed tail, so its key set is open: `keyof {a: number, ...}`
// is `"a" | ...`, an inexact union whose members are the known keys and whose tail stands for the
// unlisted ones. keyofObject seeds the union's exactness from the object's, so an exact object
// yields an exact key union and an inexact object an inexact one.
//
// It omits methods, which is correct for a class instance whose methods live on the prototype
// and so are absent from Object.keys, but wrong for a bare object whose methods are own
// enumerable keys. keyofObject cannot tell the two apart from the ObjectType alone, so it
// under-approximates the bare-object case. Issue #916 tracks deciding how keyof should account
// for own vs inherited members.
func (e *typeEvaluator) keyofObject(obj *soltype.ObjectType) soltype.Type {
	keys := make([]soltype.Type, 0, len(obj.Elems))
	for _, elem := range obj.Elems {
		switch elem := elem.(type) {
		case *soltype.PropertyElem:
			keys = append(keys, strLitType(elem.Name))
		case *soltype.GetterElem:
			keys = append(keys, strLitType(elem.Name))
		case *soltype.SetterElem:
			keys = append(keys, strLitType(elem.Name))
		}
	}
	return newUnion(nil, keys, obj.Inexact)
}

// keyofTuple yields a tuple's own keys: one number-literal type per positional element, the
// indices Object.keys returns. `keyof [number, string]` reduces to `0 | 1`. This deliberately
// deviates from TypeScript, whose keyof of a tuple also includes "length" and the other
// Array.prototype members. Those are inherited rather than own keys, so Escalier omits them.
// TODO: decide how keyof should account for inherited prototype members once interop is designed.
func (e *typeEvaluator) keyofTuple(tup *soltype.TupleType) soltype.Type {
	keys := make([]soltype.Type, 0, len(tup.Elems))
	for i := range tup.Elems {
		keys = append(keys, &soltype.LitType{Lit: &soltype.NumLit{Value: float64(i)}})
	}
	return newUnion(nil, keys, false)
}

// keyofDistribute unions the keys of each member of a union or intersection operand, the
// shared body of both distribution arms: `keyof (A | B)` and `keyof (A & B)` both reduce to
// `keyof A | keyof B`, since an intersection carries the keys of all its members.
func (e *typeEvaluator) keyofDistribute(members []soltype.Type, exact bool) soltype.Type {
	parts := make([]soltype.Type, len(members))
	for i, m := range members {
		parts[i] = e.reduceKeyof(m, exact)
	}
	return newUnion(nil, parts, false)
}

// reduceIndex reduces `target[index]` to the type stored at that key, mirroring the old
// checker's IndexType case (internal/checker/expand_type.go):
//
//   - an object indexed by a string-literal key yields that member's read type, and an unknown
//     key records an UnknownObjectKeyError;
//   - a tuple indexed by a numeric-literal key yields that element, and an out-of-range or
//     non-integer index records a TupleIndexOutOfRangeError;
//   - a union index distributes, so `T["a" | "b"]` reduces to `T["a"] | T["b"]`; `T[keyof T]`
//     rides this once `keyof T` reduces to its key union;
//   - a union target distributes the other way, so `(A | B)[K]` reduces to `A[K] | B[K]`, where
//     every member must carry K;
//   - an intersection target reduces to the meet of the value types the members that carry K
//     contribute, so `(A & B)[K]` picks K from whichever members have it;
//   - an alias expands to its body and a class projects its instance body, and the access
//     reduces over that under the termination guard;
//   - a `typeof` query resolves to the value's type, and the access reduces over that.
//
// A type-variable target or index, or any operand the evaluator does not ground, keeps the
// access symbolic, rebuilt around the reduced operands.
func (e *typeEvaluator) reduceIndex(target, index soltype.Type, exact bool) soltype.Type {
	idx := e.reduce(index)
	// A union index distributes member-wise. `T[keyof T]` rides this once `keyof T` reduces to
	// its `"a" | "b"` key union, so the access yields the union of the members' value types.
	if u, ok := idx.(*soltype.UnionType); ok {
		parts := make([]soltype.Type, len(u.Types))
		for i, m := range u.Types {
			parts[i] = e.reduceIndex(target, m, exact)
		}
		return newUnion(nil, parts, false)
	}
	switch tgt := target.(type) {
	case *soltype.AliasType:
		return e.reduceIndexAlias(tgt, idx, exact)
	case *soltype.TypeofType:
		// `(typeof x)[K]` resolves the query to the value's type, then indexes that type.
		return e.reduceIndex(tgt.Ty, idx, exact)
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType:
		// The target is itself an operator — a `keyof`, an indexed access, or a conditional. Reduce
		// it first, then index its value, so a ground conditional target selects its branch and the
		// access reduces over that. When the target stays symbolic because its own operands are not
		// ground, keep the access wrapped around the reduced target rather than re-reducing the same
		// shape forever.
		inner := e.reduce(target)
		if isResidualOp(inner) {
			return &soltype.IndexType{Target: inner, Index: idx, Exact: exact}
		}
		return e.reduceIndex(inner, idx, exact)
	case *soltype.ObjectType:
		// An object carrying an unreduced `...A` spread has no ground fields, so indexing it stays
		// symbolic until the spread grounds, mirroring the TupleType arm below.
		if obj, ok := e.groundToObject(tgt); ok {
			return e.indexObject(obj, idx, exact)
		}
		return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
	case *soltype.ClassType:
		obj, ok := e.ctx.projectClassBody(tgt)
		if !ok {
			return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
		}
		return e.indexObject(obj, idx, exact)
	case *soltype.TupleType:
		// A tuple carrying an unreduced `...P` spread has no ground positions, so indexing it stays
		// symbolic until the spread grounds to a concrete tuple.
		if tup, ok := e.groundTuple(tgt); ok {
			return e.indexTuple(tup, idx, exact)
		}
		return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
	case *soltype.UnionType:
		// A union target distributes member-wise: `(A | B)[K]` ⇒ `A[K] | B[K]`, the other-axis
		// twin of the union-index distribution above, matching how keyof distributes over a union
		// operand. A union value is one of its members, so every member must carry K — a member
		// lacking it records its own absence diagnostic through reduceIndex. Each member indexes
		// with the same reduced key.
		parts := make([]soltype.Type, len(tgt.Types))
		for i, m := range tgt.Types {
			parts[i] = e.reduceIndex(m, idx, exact)
		}
		return newUnion(nil, parts, false)
	case *soltype.IntersectionType:
		return e.reduceIndexIntersection(tgt, idx, exact)
	default:
		return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
	}
}

// reduceIndexIntersection reduces `(A & B & …)[K]`. An intersection value satisfies every member,
// so it carries key K when ANY member does, and the access yields the meet of the value types the
// members that carry K contribute. This is the opposite of the union-target rule, where every
// member must carry K. A member lacking K contributes nothing rather than erroring, so its own
// absence diagnostic is rolled back and kept aside. The access stays symbolic when a member is not
// ground enough to decide whether it carries K, and reports absence only when no member carries it.
func (e *typeEvaluator) reduceIndexIntersection(tgt *soltype.IntersectionType, idx soltype.Type, exact bool) soltype.Type {
	var resolved []soltype.Type
	var absentErrs []SolverError
	anySymbolic := false
	for _, m := range tgt.Types {
		before := len(e.errs)
		r := e.reduceIndex(m, idx, exact)
		if produced := e.errs[before:]; len(produced) > 0 {
			// A ground member lacking K recorded its own absence diagnostic. A sibling may still
			// carry K, so roll the diagnostic back and keep it aside in case none does.
			absentErrs = append(absentErrs, produced...)
			e.errs = e.errs[:before]
			continue
		}
		if isResidualOp(r) {
			anySymbolic = true
			continue
		}
		resolved = append(resolved, r)
	}
	// A member the evaluator could not ground might carry K with an unknown type, so the meet is
	// undecided. Stay symbolic rather than committing to the members that did resolve.
	if anySymbolic {
		return &soltype.IndexType{Target: tgt, Index: idx, Exact: exact}
	}
	if len(resolved) > 0 {
		return newIntersection(nil, resolved)
	}
	// Every member is ground and none carries K, so K is genuinely absent. Surface one member's
	// absence diagnostic and reduce to the error sentinel.
	if len(absentErrs) > 0 {
		e.errs = append(e.errs, absentErrs[0])
	}
	return &soltype.ErrorType{}
}

// reduceIndexAlias reduces `Alias[K]` by expanding the alias and indexing its body under the
// termination guard, the indexed-access twin of reduceKeyofAlias. The alias stays on the active
// path for the whole reduction of its body, so a member that re-references it stops. A recurring
// instantiation state, an exhausted budget, or an unresolved body each leaves the access
// unexpanded and symbolic.
func (e *typeEvaluator) reduceIndexAlias(op *soltype.AliasType, index soltype.Type, exact bool) soltype.Type {
	symbolic := &soltype.IndexType{Target: op, Index: index, Exact: exact}
	key := soltype.PrintQualified(op)
	if e.active.Contains(key) || e.depth <= 0 {
		return symbolic
	}
	body := e.ctx.expandAlias(op)
	if _, unresolved := body.(*soltype.ErrorType); unresolved {
		return symbolic
	}
	e.active.Add(key)
	e.depth--
	result := e.reduceIndex(body, index, exact)
	e.active.Remove(key)
	e.depth++
	return result
}

// indexObject reduces `obj[key]` for a ground object. A string-literal key selects the named
// member's read type — a property's or getter's declared type, a method's callable value — and a
// key the object carries no member for records an UnknownObjectKeyError and reduces to the error
// sentinel. A non-string-literal index, such as a bare `string` primitive, selects no single
// member yet. An index signature reads it once mapped types land (M9 PR4), so the access stays
// symbolic until then.
func (e *typeEvaluator) indexObject(obj *soltype.ObjectType, index soltype.Type, exact bool) soltype.Type {
	name, ok := strLitName(index)
	if !ok {
		return &soltype.IndexType{Target: obj, Index: index, Exact: exact}
	}
	if _, found := obj.Member(name); !found {
		e.errs = append(e.errs, &UnknownObjectKeyError{Object: obj, Key: name})
		return &soltype.ErrorType{}
	}
	read, hasValue, _ := memberReadContribution(obj, name)
	if !hasValue {
		// The member is a write-only setter, which exposes no readable value. Leave the access
		// symbolic rather than resolving a write slot to a read type.
		return &soltype.IndexType{Target: obj, Index: index, Exact: exact}
	}
	return read
}

// indexTuple reduces `tup[n]` for a ground tuple. A numeric-literal key selects the element at
// that position. An index outside `[0, len)`, or a non-integer or negative literal, records a
// TupleIndexOutOfRangeError and reduces to the error sentinel. A non-numeric-literal index has no
// positional slot to select, so the access stays symbolic.
func (e *typeEvaluator) indexTuple(tup *soltype.TupleType, index soltype.Type, exact bool) soltype.Type {
	lit, ok := index.(*soltype.LitType)
	if !ok {
		return &soltype.IndexType{Target: tup, Index: index, Exact: exact}
	}
	num, ok := lit.Lit.(*soltype.NumLit)
	if !ok {
		return &soltype.IndexType{Target: tup, Index: index, Exact: exact}
	}
	i := int(num.Value)
	if float64(i) != num.Value || i < 0 || i >= len(tup.Elems) {
		e.errs = append(e.errs, &TupleIndexOutOfRangeError{Tuple: tup, Index: num.Value})
		return &soltype.ErrorType{}
	}
	return tup.Elems[i]
}

// strLitName returns the property name a string-literal index selects, and false for any other
// type. Object keys are strings, so only a StrLit names a member.
func strLitName(t soltype.Type) (string, bool) {
	if lit, ok := t.(*soltype.LitType); ok {
		if s, ok := lit.Lit.(*soltype.StrLit); ok {
			return s.Value, true
		}
	}
	return "", false
}

// isResidualOp reports whether t is an unreduced type-level operator node — a `keyof`, an indexed
// access, a conditional, or a `...P` tuple-spread element — at its top level. The evaluator consults
// it to stop re-reducing an operand whose reduction stayed symbolic.
func isResidualOp(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType, *soltype.RestSpreadType:
		return true
	}
	return false
}

// tupleHasSpread reports whether t is a tuple carrying at least one unreduced `...P` spread
// element. Such a tuple is inert — constrain passes it through untouched until the evaluator
// splices the spread — whereas a plain tuple is a structural type constrain decomposes. A non-tuple
// is never a spread tuple.
func tupleHasSpread(t soltype.Type) bool {
	tup, ok := t.(*soltype.TupleType)
	return ok && hasRestSpread(tup.Elems)
}

// objectHasSpread reports whether t is an object carrying an unreduced `...A` spread element, the
// object twin of tupleHasSpread.
func objectHasSpread(t soltype.Type) bool {
	obj, ok := t.(*soltype.ObjectType)
	return ok && soltype.HasObjectSpread(obj.Elems)
}

// hasRestSpread reports whether any element of elems is a `...P` spread.
func hasRestSpread(elems []soltype.Type) bool {
	for _, el := range elems {
		if _, ok := el.(*soltype.RestSpreadType); ok {
			return true
		}
	}
	return false
}

// strLitType builds the string-literal type for one key name, the form a projected object or
// tuple key takes in a `keyof` union.
func strLitType(name string) soltype.Type {
	return &soltype.LitType{Lit: &soltype.StrLit{Value: name}}
}

// containsResidualOp reports whether t holds any unreduced type-level operator node — a `keyof`,
// an indexed access, or a tuple spread. constrain consults it to decide whether a reduced operator
// fully grounded: a result with no residual is safe to recurse on, while one that still carries a
// `keyof`, a `T[K]`, or a `[...T, x]` — an unexpanded type parameter or a budget-truncated
// expanding alias — must not, since re-reducing it would loop.
func containsResidualOp(t soltype.Type) bool {
	f := &residualOpFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// residualOpFinder is the walking visitor behind containsResidualOp. It flags the first residual
// operator it reaches and skips that node's children, since one occurrence is enough.
type residualOpFinder struct{ found bool }

func (f *residualOpFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if isResidualOp(t) {
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *residualOpFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t
}

// containsFreeVar reports whether t holds any type variable or skolem — an abstract leaf that makes
// t non-ground. A conditional consults it to decide whether its Check and Extends are concrete
// enough to probe `Check <: Extends`. A conditional whose Check is a bare type parameter stays
// symbolic, since that parameter is a free variable.
func containsFreeVar(t soltype.Type) bool {
	f := &freeVarFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// freeVarFinder is the walking visitor behind containsFreeVar. It flags the first type variable or
// skolem it reaches and skips that node's children, since one occurrence is enough.
type freeVarFinder struct{ found bool }

func (f *freeVarFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t.(type) {
	case *soltype.TypeVarType, *soltype.SkolemType:
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *freeVarFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t
}
