package checker

import (
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// lifetimeParamName returns the inferred lifetime parameter name for the
// i-th allocated lifetime in a signature: 'a, 'b, ..., 'z, 'aa, 'ab, etc.
// Using letters (rather than the underlying param name) makes printed
// signatures easier to read since lifetimes are visually distinct from
// the params they constrain.
func lifetimeParamName(i int) string {
	const base = 26
	name := ""
	for {
		name = string(rune('a'+(i%base))) + name
		i = i/base - 1
		if i < 0 {
			break
		}
	}
	return name
}

// InferLifetimes analyzes a function body to determine which parameters
// are aliased by the return value (or yielded value, for generators), or
// escape into module-level state, and attaches the appropriate lifetime
// to each such parameter and to the return / yield type, recording
// lifetime parameters on the FuncType.
//
// This is the entry point for the body-side of Phase 8.3 + 8.4 + 8.7.
// The algorithm handles:
//   - Returns of a parameter (or property/index access of a parameter):
//     produces a container-level lifetime on the parameter and return type.
//   - Multiple returns aliasing different parameters: emits a
//     LifetimeUnion on the return type.
//   - Parameters stored into module-level (non-local) state: assigned
//     'static directly. See detectEscapingLeafIndices.
//   - Yields and returns aliasing parameters (generators): yields drive
//     the yield T slot inside Generator<T, TReturn, TNext>; explicit
//     `return expr` paths drive the TReturn slot. Each is inferred
//     independently, so a generator may carry distinct lifetimes on
//     T and TReturn. Lifetimes attach to the inner slots rather than
//     the Generator container, since the container is freshly assembled
//     per call. See "Generator-specific behavior" below.
//   - Per-leaf lifetimes for tuple-, object-, and rest-destructured
//     parameters: walkPatternForLeaves walks each parameter's pattern in
//     lockstep with its inferred type, producing one (VarID, leafType)
//     entry per leaf binding so that each destructured leaf can receive
//     its own lifetime variable.
//
// For SCC re-runs in mutual recursion (Phase 8.7), see ReinferLifetimes,
// which forwards to inferLifetimesCore with reinfer=true so that
// previously-inferred LifetimeParams can be extended once peers' signatures
// become resolvable on the second pass.
//
// Generator-specific behavior (Phase 8.3):
//   - Yields (regular and delegate) drive the yield T position;
//     `return expr` paths drive the TReturn position; the two are
//     inferred independently via `attachLifetimeToResult`.
//   - For `yield from iter`, the iterator expression is the alias
//     source — each yielded element borrows from iter, so iter's
//     lifetime propagates to the relay generator's yield T.
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Element-level lifetimes (Array<'a T>) for fresh containers whose
//     elements alias a parameter.
//   - Propagating lifetimes through nested calls into other functions.
//   - TODO(#507): Shared-instance overspecification for unioned yield
//     types and `yield from`: when the result position shares its
//     underlying Type instance with a parameter, attaching a lifetime
//     to the result also writes through to the parameter, producing
//     tighter output than necessary. Fix would require shallow-cloning
//     the yield T / TReturn before lifetime attachment.
//
// astParams is the list of parameter ASTs (used to map their VarIDs to
// parameter indices). funcType is mutated in place. isAsync reports
// whether the function was declared `async` — together with the
// presence of `yield` in the body it determines whether the return
// type should be unwrapped to its inner T (Promise<T,_>'s value or
// Generator<T,_,_>'s yield) or treated as the direct result.
// lifetimeInferPass distinguishes the first-pass entry point from the
// SCC re-run. The first pass refuses to touch a signature that already
// has user-declared LifetimeParams; the reinfer pass deliberately
// extends a previously-inferred set so peers in a mutually recursive
// SCC can grow each other's lifetime params to a fixed point.
type lifetimeInferPass int

const (
	initialPass lifetimeInferPass = iota
	reinferPass
)

func (p lifetimeInferPass) isReinfer() bool { return p == reinferPass }

func (c *Checker) InferLifetimes(
	astParams []*ast.Param,
	body *ast.Block,
	funcType *type_system.FuncType,
	async asyncMode,
) {
	c.inferLifetimesCore(astParams, body, funcType, async, initialPass)
}

// ReinferLifetimes re-runs lifetime inference on a function whose first
// pass has already completed. Intended for the SCC re-run in
// InferComponent: peers in the same SCC may have gained inferred
// lifetimes after this function's first pass, in which case
// `determineCheckerAliasSource` can now reach through their call
// expressions and reveal additional aliased leaves. Unlike
// InferLifetimes, this entry point bypasses the user-explicit guard,
// so callers must ensure they don't invoke it on a function whose
// LifetimeParams were declared explicitly by the user.
func (c *Checker) ReinferLifetimes(
	astParams []*ast.Param,
	body *ast.Block,
	funcType *type_system.FuncType,
	async asyncMode,
) {
	c.inferLifetimesCore(astParams, body, funcType, async, reinferPass)
}

// inferLifetimesCore is the shared body of InferLifetimes and
// ReinferLifetimes. The pass argument controls whether the function may
// extend an already-inferred set of lifetime params: initialPass bails
// out as soon as it sees existing LifetimeParams (the historical
// behavior protecting user-explicit lifetimes); reinferPass walks the
// leaves and *appends* new lifetime params for any leaves that became
// visible via newly-resolved peer signatures.
func (c *Checker) inferLifetimesCore(
	astParams []*ast.Param,
	body *ast.Block,
	funcType *type_system.FuncType,
	async asyncMode,
	pass lifetimeInferPass,
) {
	isAsync := async.isAsync()
	if body == nil || funcType == nil {
		return
	}
	// If the user already declared explicit lifetime parameters on the
	// signature, don't second-guess them. (Resolution of those annotated
	// lifetimes during type-checking is a separate concern handled by
	// the type-annotation inference path.) The reinfer path skips this
	// guard so the SCC re-run can extend a previously-inferred result.
	if !pass.isReinfer() && len(funcType.LifetimeParams) > 0 {
		return
	}

	// Build an ordered list of param "leaves": for IdentPat params, the
	// leaf is the param itself; for tuple-destructured params (TuplePat),
	// each leaf IdentPat becomes its own leaf with a Type position
	// pointing at the corresponding sub-position of the param's type;
	// for RestPat (`...args: T[]`), the inner pattern's leaf points at
	// the *element* type Array<T>'s TypeArgs[0], not the array container
	// — the container is freshly assembled per call and has no
	// caller-provided lifetime.
	//
	// Object-destructured params are not yet walked (Phase 8.6 deferred).
	leaves := collectParamLeaves(astParams, funcType.Params)
	if len(leaves) == 0 {
		return
	}
	leafIndex := make(map[liveness.VarID]int)
	for i, l := range leaves {
		leafIndex[l.varID] = i
	}

	// Phase 8.4: detect parameters that escape into module-level state and
	// assign them 'static directly. This must run before the return-alias
	// pass so that an escaping param gets 'static rather than a fresh 'a
	// even when it is also returned by some path. Already-set 'static
	// (from a prior pass) is a no-op.
	escapingLeaves := detectEscapingLeafIndices(body, leafIndex, nil)
	for idx := range escapingLeaves {
		leaf := leaves[idx]
		if !typeCarriesLifetime(leaf.leafType) {
			continue
		}
		if existing, ok := type_system.PruneLifetime(type_system.GetLifetime(leaf.leafType)).(*type_system.LifetimeValue); ok && existing.IsStatic {
			continue
		}
		setLifetimeOnType(leaf.leafType, &type_system.LifetimeValue{
			Name:     "static",
			IsStatic: true,
		})
	}

	// Walk the body to collect alias-source expressions: every return
	// statement's expression for plain functions, plus every yield
	// expression's value for generators (the yielded value is what
	// callers see via Iterator.next()).
	v := &returnExprVisitor{}
	for _, stmt := range body.Stmts {
		stmt.Accept(v)
	}
	yields := collectYieldExprs(body)
	if len(v.exprs) == 0 && len(yields) == 0 {
		return
	}

	// A function is a generator when its body actually contains `yield`,
	// not merely when its return type happens to be Generator<...>. A
	// plain function that forwards a Generator<T> parameter (e.g.
	// `return g`) is NOT a generator and the return type carries the
	// lifetime as a whole, not just its inner yield T.
	isGenerator := len(yields) > 0

	// Generators have two lifetime-bearing result positions: the yield
	// type T (driven by `yield expr` paths) and TReturn (driven by
	// `return expr` paths). They are inferred independently — a generator
	// might yield from one parameter and return another, in which case
	// each receives its own lifetime variable. For non-generators there
	// is a single result position: the return type for plain functions,
	// or the inner T inside Promise<T, E> for async functions (the
	// Promise container itself is freshly assembled per call, with no
	// caller-provided lifetime).
	if isGenerator {
		c.attachLifetimeToResult(funcType, leaves, leafIndex, escapingLeaves, yields, generatorYieldType(funcType.Return))
		c.attachLifetimeToResult(funcType, leaves, leafIndex, escapingLeaves, v.exprs, generatorReturnType(funcType.Return))
		return
	}
	resultType := funcType.Return
	if isAsync {
		if t := promiseValueType(funcType.Return); t != nil {
			resultType = t
		}
	}
	c.attachLifetimeToResult(funcType, leaves, leafIndex, escapingLeaves, v.exprs, resultType)
}

// attachLifetimeToResult is the per-result-position core of lifetime
// inference: given a list of alias-source expressions (e.g. `return e`
// or `yield e`) and a single result-type position, walk the expressions
// to collect aliased leaves, allocate / reuse a LifetimeVar per leaf,
// attach those lifetimes to the leaves AND to the result, and append
// any fresh LifetimeVars to funcType.LifetimeParams.
//
// resultType may be nil (e.g. a Generator<_, void, _>'s TReturn slot is
// `void` and carries no lifetime); in that case the function bails
// without allocating any lifetime params even if returns happen to
// alias parameters, since there is no result-side position to attach.
//
// This is invoked once per result position. Generators call it twice
// (yields → yield T, returns → TReturn); plain and async functions
// call it once. When called twice, fresh LifetimeVars allocated by the
// first call are appended to funcType.LifetimeParams before the second
// call begins, so the second call's `nameIdx` (which reads
// len(funcType.LifetimeParams)) correctly continues the 'a, 'b, ...
// sequence.
//
// escapingLeaves names leaves whose lifetime was already overwritten to
// 'static by inferLifetimesCore prior to this call. Reading those leaves
// here is read-only — the helper records returnHasStatic but does not
// re-mutate the leaf's lifetime — so it is safe to invoke twice.
func (c *Checker) attachLifetimeToResult(
	funcType *type_system.FuncType,
	leaves []paramLeaf,
	leafIndex map[liveness.VarID]int,
	escapingLeaves set.Set[int],
	sourceExprs []ast.Expr,
	resultType type_system.Type,
) {
	if resultType == nil || !typeCarriesLifetime(resultType) {
		return
	}
	if len(sourceExprs) == 0 {
		return
	}

	// Per-paramLeaf lifetime allocation: each aliased leaf gets at most
	// one fresh LifetimeVar across all sources. Reused if the leaf
	// already carries one from an earlier pass / the first generator
	// call.
	leafLV := map[int]*type_system.LifetimeVar{}
	var newLifetimeParams []*type_system.LifetimeVar

	// Per-slot accumulation in the result type. Each (Path-driven)
	// destination slot collects the lifetimes contributed to it across
	// all sourceExprs. Slot identity is the post-Prune Type pointer
	// returned by descendIntoSlot, so two paths landing on the same
	// element type of an Array<T> dedupe naturally.
	slots := newSlotAccumulator()

	for _, re := range sourceExprs {
		// Use the checker-aware variant so call expressions whose callee
		// has inferred lifetime parameters propagate the relevant
		// argument's alias source through to the result. This is what
		// makes Phase 8.7's mutual-recursion fixed-point pass actually
		// converge: on the second pass, peers' lifetimes are visible
		// here.
		src := determineCheckerAliasSource(re)
		if len(src.Leaves) == 0 {
			continue
		}
		for _, leaf := range src.Leaves {
			idx, ok := leafIndex[leaf.RootVarID]
			if !ok {
				continue
			}
			paramLeafEntry := leaves[idx]
			if !typeCarriesLifetime(paramLeafEntry.leafType) {
				continue
			}

			slot := pickResultSlot(resultType, src.Origin, leaf.Path)

			// If the destination slot can't carry a lifetime (e.g. the
			// path landed on a type parameter, void, or a primitive),
			// skip the leaf entirely. Allocating a fresh LifetimeVar
			// for the param without anywhere to attach it on the
			// result side would surface as a phantom 'a in the
			// signature with no observable effect.
			if !typeCarriesLifetime(slot) {
				continue
			}

			if escapingLeaves.Contains(idx) {
				slots.markStatic(slot)
				continue
			}

			lv, allocated := leafLV[idx]
			if !allocated {
				// Reuse any LifetimeVar already on the leaf so the
				// signature stays stable across re-runs / generator
				// TReturn reuse from a prior yield call.
				prunedLT := type_system.PruneLifetime(type_system.GetLifetime(paramLeafEntry.leafType))
				if existing, ok := prunedLT.(*type_system.LifetimeVar); ok && existing != nil {
					lv = existing
				} else {
					nameIdx := len(funcType.LifetimeParams) + len(newLifetimeParams)
					lv = c.FreshLifetimeVar(lifetimeParamName(nameIdx))
					newLifetimeParams = append(newLifetimeParams, lv)
					setLifetimeOnType(paramLeafEntry.leafType, lv)
				}
				leafLV[idx] = lv
			}
			slots.add(slot, lv)
		}
	}

	slots.finalize()

	if len(newLifetimeParams) > 0 {
		funcType.LifetimeParams = append(funcType.LifetimeParams, newLifetimeParams...)
	}
}

// pickResultSlot chooses the destination slot in a result-side type for a
// leaf. Fresh-rooted sources (`[a, b]`, `{k: a}`) carry leaf paths that
// describe a descent INTO the new container — walk the result type along
// the path. Alias-rooted sources (`obj`, `obj.field`) carry paths that
// describe a projection into the source root, not a result-side slot —
// attach at the top.
func pickResultSlot(resultType type_system.Type, origin liveness.AliasOrigin, path []liveness.ProjectionStep) type_system.Type {
	switch origin {
	case liveness.AliasOriginFresh:
		return descendIntoSlot(resultType, path)
	default:
		return resultType
	}
}

// slotAccumulator collects per-slot lifetime contributions and applies them
// in a single finalize pass. Slot identity is by Type pointer (typically
// the post-Prune type returned by descendIntoSlot), so two paths landing
// on the same element type of an Array<T> dedupe naturally. Used by both
// attachLifetimeToResult (function results) and attachFieldSlotLifetimes
// (constructor field assignments) to share the union-vs-single bookkeeping.
type slotAccumulator struct {
	byPtr map[type_system.Type]*slotEntry
	order []type_system.Type
}

type slotEntry struct {
	members   []type_system.Lifetime
	hasStatic bool
}

func newSlotAccumulator() *slotAccumulator {
	return &slotAccumulator{byPtr: map[type_system.Type]*slotEntry{}}
}

func (s *slotAccumulator) entry(slot type_system.Type) *slotEntry {
	e, ok := s.byPtr[slot]
	if !ok {
		e = &slotEntry{}
		s.byPtr[slot] = e
		s.order = append(s.order, slot)
	}
	return e
}

func (s *slotAccumulator) add(slot type_system.Type, lt type_system.Lifetime) {
	e := s.entry(slot)
	e.members = append(e.members, lt)
}

func (s *slotAccumulator) markStatic(slot type_system.Type) {
	s.entry(slot).hasStatic = true
}

// finalize attaches a (possibly-unioned) lifetime to each accumulated slot.
// Members within a slot are deduped by pointer identity — repeating the
// same LifetimeVar across multiple sources is common (e.g. `return p` in
// two branches both contribute leaf p).
func (s *slotAccumulator) finalize() {
	for _, slot := range s.order {
		entry := s.byPtr[slot]
		members := dedupeLifetimes(entry.members)
		if entry.hasStatic {
			members = append(members, &type_system.LifetimeValue{
				Name:     "static",
				IsStatic: true,
			})
		}
		var lt type_system.Lifetime
		switch len(members) {
		case 0:
			continue
		case 1:
			lt = members[0]
		default:
			lt = &type_system.LifetimeUnion{Lifetimes: members}
		}
		// TODO(#507): if slot is pointer-shared with a param leaf
		// (e.g. unioned yield types, `yield from`, or unannotated
		// constructor params whose leaf type IS the field-slot type),
		// this write bleeds through to the param. Shallow-clone the
		// shared type here and substitute the clone back.
		setLifetimeOnType(slot, lt)
	}
}

// dedupeLifetimes returns a copy of members with duplicates (by pointer
// identity) removed, preserving order of first occurrence. Used to
// avoid emitting `('a | 'a)` when the same param leaf is aliased by
// multiple branches of the same source position.
func dedupeLifetimes(members []type_system.Lifetime) []type_system.Lifetime {
	if len(members) <= 1 {
		return members
	}
	seen := map[type_system.Lifetime]bool{}
	out := make([]type_system.Lifetime, 0, len(members))
	for _, m := range members {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// descendIntoSlot walks a return type along a projection path and
// returns the slot type to attach a lifetime to. When a step cannot be
// applied to the current type's shape (e.g. a PropertyOf step on an
// Array<T>), descent stops gracefully and the deepest type reached so
// far is returned. This realizes the spec's "the descent fails
// gracefully (no attachment) if the return type's shape doesn't match
// the path" clause: a partial descent still yields the best slot we
// could find for a lifetime.
func descendIntoSlot(t type_system.Type, path []liveness.ProjectionStep) type_system.Type {
	current := t
	for _, step := range path {
		next := stepIntoSlot(current, step)
		if next == nil {
			return current
		}
		current = next
	}
	return current
}

// stepIntoSlot applies a single projection step to a type, returning
// the inner slot type or nil if the step doesn't fit the type's shape.
// The caller is responsible for unwrapping MutType — we Prune and peel
// here so we can match against TupleType / ObjectType / Array<T> /
// Promise<T> regardless of mutability wrappers.
func stepIntoSlot(t type_system.Type, step liveness.ProjectionStep) type_system.Type {
	pruned := type_system.Prune(t)
	if mut, ok := pruned.(*type_system.MutType); ok {
		pruned = type_system.Prune(mut.Type)
	}
	switch s := step.(type) {
	case liveness.IndexOf:
		if tup, ok := pruned.(*type_system.TupleType); ok {
			if s.Index >= 0 && s.Index < len(tup.Elems) {
				return tup.Elems[s.Index]
			}
			return nil
		}
		// IndexOf on Array<T>: collapse to ElementOf — the source
		// expression was a tuple-like literal `[a, b]` whose container
		// type happens to be Array<T> (all elements share T).
		if elem := arrayElement(pruned); elem != nil {
			return elem
		}
	case liveness.ElementOf:
		if elem := arrayElement(pruned); elem != nil {
			return elem
		}
	case liveness.PropertyOf:
		if obj, ok := pruned.(*type_system.ObjectType); ok {
			// Numeric keys: the producer (propertyKeyToString) stringifies
			// NumLit indexes via strconv.FormatFloat, so to compare against
			// a NumObjTypeKeyKind property we parse s.Key back to float64.
			// Going through float comparison (rather than string round-trip)
			// avoids coupling to the producer's exact format choice.
			var keyAsFloat float64
			var keyIsNumeric bool
			if f, err := strconv.ParseFloat(s.Key, 64); err == nil {
				keyAsFloat = f
				keyIsNumeric = true
			}
			// TODO(#543): SymObjTypeKeyKind properties are not handled —
			// the producer side can't emit symbol-keyed PropertyOf steps
			// today, so this is currently unreachable.
			for _, elem := range obj.Elems {
				prop, ok := elem.(*type_system.PropertyElem)
				if !ok {
					continue
				}
				switch prop.Name.Kind {
				case type_system.StrObjTypeKeyKind:
					if prop.Name.Str == s.Key {
						return prop.Value
					}
				case type_system.NumObjTypeKeyKind:
					if keyIsNumeric && prop.Name.Num == keyAsFloat {
						return prop.Value
					}
				}
			}
		}
	case liveness.AwaitOf:
		if ref, ok := pruned.(*type_system.TypeRefType); ok && type_system.QualIdentToString(ref.Name) == "Promise" && len(ref.TypeArgs) >= 1 {
			return ref.TypeArgs[0]
		}
	case liveness.CastOf:
		return pruned
	}
	return nil
}

// arrayElement returns the element type of an Array<T> reference, or
// nil if the type isn't an Array<T>. Centralized so future container
// recognizers (ReadonlyArray<T>, etc.) have one place to extend.
func arrayElement(t type_system.Type) type_system.Type {
	ref, ok := t.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	if type_system.QualIdentToString(ref.Name) != "Array" {
		return nil
	}
	if len(ref.TypeArgs) != 1 {
		return nil
	}
	return ref.TypeArgs[0]
}

// paramLeaf represents a leaf binding within a function-parameter pattern,
// paired with the Type position to which a lifetime should be attached.
// For a simple identifier param `p: T`, leafType is the param's full type;
// for a tuple-destructured param `[a, b]: [T, U]`, each leaf points at the
// corresponding tuple element type; for a rest param `...args: T[]`, the
// leaf points at the *element* type T (the array container is freshly
// assembled per call and has no caller-provided lifetime).
type paramLeaf struct {
	varID    liveness.VarID
	leafType type_system.Type
}

// collectParamLeaves walks each function parameter's pattern in lockstep
// with the parameter's inferred type, producing an ordered list of
// (VarID, leafType) pairs for every leaf binding that has a positive
// VarID set by the rename pass. Tuple-, object-, and rest-destructuring
// patterns are supported; for object patterns, leaf positions are
// resolved by string-key lookup against the corresponding ObjectType's
// PropertyElems.
func collectParamLeaves(
	astParams []*ast.Param,
	funcParams []*type_system.FuncParam,
) []paramLeaf {
	var leaves []paramLeaf
	for i, p := range astParams {
		if i >= len(funcParams) {
			continue
		}
		walkPatternForLeaves(p.Pattern, funcParams[i].Type, &leaves)
	}
	return leaves
}

// walkPatternForLeaves recurses through a destructuring pattern in
// lockstep with its inferred type, appending a paramLeaf for every
// leaf binding (IdentPat or ObjShorthandPat) with a positive VarID.
//
// We don't use the ast.Visitor interface here because the visitor only
// carries pattern context — it has no notion of a parallel type tree.
// Each container pattern (TuplePat, ObjectPat, RestPat) needs to pick
// the matching sub-type (tuple element, property value, array element)
// before descending, which would force a visitor implementation to
// maintain a side stack of types pushed/popped on EnterPat/ExitPat and
// to repeat the same type-shape switching done here. With one caller
// and one purpose, a self-contained recursive walk is simpler.
func walkPatternForLeaves(pat ast.Pat, t type_system.Type, into *[]paramLeaf) {
	if pat == nil || t == nil {
		return
	}
	pt := stripMutabilityWrapper(type_system.Prune(t))
	switch p := pat.(type) {
	case *ast.IdentPat:
		if p.VarID > 0 {
			*into = append(*into, paramLeaf{
				varID:    liveness.VarID(p.VarID),
				leafType: t,
			})
		}
	case *ast.TuplePat:
		tt, ok := pt.(*type_system.TupleType)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if i >= len(tt.Elems) {
				break
			}
			elemType := tt.Elems[i]
			// Tuple rest spreads (`[T, ...Array<U>]`) carry a
			// RestSpreadType in the tuple's Elems slot. The pattern's
			// matching RestPat expects to receive the *array* type so
			// its inner pattern can be walked against the element type.
			// Unwrap the spread here so RestPat sees Array<U> directly.
			if rest, ok := elemType.(*type_system.RestSpreadType); ok {
				elemType = rest.Type
			}
			walkPatternForLeaves(elem, elemType, into)
		}
	case *ast.ObjectPat:
		ot, ok := pt.(*type_system.ObjectType)
		if !ok {
			return
		}
		// Build a key→Type map for the object type's properties so each
		// pattern element can resolve its corresponding sub-position.
		propTypes := make(map[string]type_system.Type)
		for _, elem := range ot.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok &&
				prop.Name.Kind == type_system.StrObjTypeKeyKind {
				propTypes[prop.Name.Str] = prop.Value
			}
		}
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				// `{ key: value-pat }` — recurse into value-pat with
				// the property's type. The property name is the lookup
				// key.
				if e.Key == nil {
					continue
				}
				if propType, exists := propTypes[e.Key.Name]; exists {
					walkPatternForLeaves(e.Value, propType, into)
				}
			case *ast.ObjShorthandPat:
				// `{ key }` (or `{ key: TypeAnn }`, `{ key = default }`)
				// — the leaf binding has the same name as the property.
				if e.Key == nil || e.VarID <= 0 {
					continue
				}
				propType, exists := propTypes[e.Key.Name]
				if !exists {
					continue
				}
				*into = append(*into, paramLeaf{
					varID:    liveness.VarID(e.VarID),
					leafType: propType,
				})
			case *ast.ObjRestPat:
				// `{ ...rest }` — the rest pattern collects remaining
				// properties into a fresh object. Like a function rest
				// param's container, this is freshly assembled per
				// call, so attaching a container-level lifetime would
				// be wrong. Per-property element lifetimes for object
				// rest are deferred (would require synthesizing a
				// per-call object type).
			}
		}
	case *ast.RestPat:
		// `...args: T[]` — the lifetime-bearing position is the
		// *element* type, not the array container. Descend into the
		// inner pattern with the element type.
		elem := arrayElemType(t)
		if elem == nil {
			return
		}
		walkPatternForLeaves(p.Pattern, elem, into)
	}
}

// stripMutabilityWrapper strips a MutType wrapper so callers can
// match the underlying structural type. Returns the input unchanged if
// not wrapped.
func stripMutabilityWrapper(t type_system.Type) type_system.Type {
	if mt, ok := t.(*type_system.MutType); ok {
		return type_system.Prune(mt.Type)
	}
	return t
}

// arrayElemType returns the element type T of an Array<T> reference,
// walking past mutability wrappers. Returns nil if t is not an Array.
func arrayElemType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	if type_system.QualIdentToString(tref.Name) != "Array" || len(tref.TypeArgs) != 1 {
		return nil
	}
	return tref.TypeArgs[0]
}

// detectEscapingLeafIndices walks a function body looking for
// assignments that store one of the function's parameter leaves into a
// non-local location (module-level variable, prelude binding, or any
// other binding looked up through the function's enclosing scope
// chain). leafIndex maps a leaf binding's VarID to its position in the
// leaves list; the returned set contains the indices of leaves whose
// value escapes.
//
// Detection is by VarID: the rename pass assigns positive VarIDs to
// locals and negative VarIDs to outer-scope references. An assignment
// whose lvalue root is a non-local identifier (VarID <= 0) is treated
// as a store into storage whose lifetime outlives the callee's stack
// frame. Stores into locals don't escape because the local's lifetime
// is bounded by the function body.
//
// extraEscapeRoots, if non-nil, names additional positive-VarID roots
// whose lvalue assignments should ALSO be treated as escaping. Used for
// constructor bodies, where `self` is a local parameter (positive VarID)
// but `self.<field> = expr` should escape because the receiver outlives
// the constructor's stack frame. Pass nil for ordinary function bodies.
//
// Limitations:
//   - Closures over a *nested* function's local: inner functions whose
//     body assigns to an outer function's local will mark that param as
//     'static, which is more conservative than necessary. The
//     borrow-checker tolerates this (it's sound, just imprecise).
//   - Stores via property assignment whose root is a local but is
//     itself stored elsewhere: not tracked here.
func detectEscapingLeafIndices(
	body *ast.Block,
	leafIndex map[liveness.VarID]int,
	extraEscapeRoots set.Set[liveness.VarID],
) set.Set[int] {
	escaped := set.NewSet[int]()
	if body == nil || len(leafIndex) == 0 {
		return escaped
	}
	v := &escapingRefsVisitor{
		leafIndex:        leafIndex,
		extraEscapeRoots: extraEscapeRoots,
		escaped:          escaped,
	}
	body.Accept(v)
	return escaped
}

type escapingRefsVisitor struct {
	ast.DefaultVisitor
	leafIndex        map[liveness.VarID]int
	extraEscapeRoots set.Set[liveness.VarID] // may be nil
	escaped          set.Set[int]
}

func (v *escapingRefsVisitor) EnterExpr(e ast.Expr) bool {
	if be, ok := e.(*ast.BinaryExpr); ok && be.Op == ast.Assign {
		if v.isEscapingLValue(be.Left) {
			// Use the checker-aware variant so a parameter that flows
			// through a call whose callee returns its argument
			// (`cache = wrap(p)`) is still detected as escaping.
			//
			// Iterate Leaves directly (rather than via Kind/VarIDs) so
			// fresh-rooted sources like `self.items = [a, b]` are
			// recognized: each leaf names a param that escapes via the
			// new container's slot, regardless of whether the root of
			// the RHS is alias-of-existing or freshly-constructed.
			src := determineCheckerAliasSource(be.Right)
			for _, leaf := range src.Leaves {
				if idx, ok := v.leafIndex[leaf.RootVarID]; ok {
					v.escaped.Add(idx)
				}
			}
		}
	}
	// Don't descend into nested function bodies — their assignments
	// concern their own params, not ours.
	if _, ok := e.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

func (v *escapingRefsVisitor) EnterDecl(d ast.Decl) bool {
	switch d.(type) {
	case *ast.FuncDecl, *ast.ClassDecl:
		return false
	}
	return true
}

// isEscapingLValue returns true if the given assignment-target expression's
// root identifier (walking through MemberExpr/IndexExpr chains) is an
// escape root: either a non-local binding (rootObjectVarID returns 0,
// meaning module-level / prelude / outer-scope) or a positive VarID that
// the caller declared as a forced escape root (e.g. `self` in a
// constructor body, where `self.<field> = expr` outlives the local frame
// even though `self` itself is a local parameter).
func (v *escapingRefsVisitor) isEscapingLValue(expr ast.Expr) bool {
	root := rootObjectVarID(expr)
	if root == 0 {
		return true
	}
	if v.extraEscapeRoots != nil && v.extraEscapeRoots.Contains(root) {
		return true
	}
	return false
}

// setLifetimeOnType attaches a lifetime to a type's Lifetime field, walking
// past wrapper types (mutability) to reach the underlying reference type.
// Has no effect on types that don't carry a lifetime field (primitives,
// void, never, etc.).
func setLifetimeOnType(t type_system.Type, lt type_system.Lifetime) {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		ty.Lifetime = lt
	case *type_system.ObjectType:
		ty.Lifetime = lt
	case *type_system.TupleType:
		ty.Lifetime = lt
	case *type_system.MutType:
		setLifetimeOnType(ty.Type, lt)
	}
}

// propagateCalleeStaticLifetimes is the caller-side counterpart of Phase
// 8.4's escape detection. After a call is type-checked, walk the
// callee's parameters: any param whose lifetime resolves to `'static`
// represents a value that the callee stored into storage whose lifetime
// outlives the callee's stack frame. For each such param, mark the
// corresponding argument variable's alias sets via AliasTracker.MarkStatic
// with the param's mutability — this records that a permanent reference
// to the argument has escaped, so the transition checker can flag later
// mut↔immut transitions on the argument as unsafe.
//
// Argument resolution goes through determineCheckerAliasSource, so the
// escape propagates through identity-like nested calls whose return
// type aliases an argument (e.g. `cacheItem(id(p))` marks `p`). Has no
// effect when no underlying VarID can be resolved — e.g. property
// projections like `f(p.x)`, which would need projection-path tracking,
// deferred to a later phase.
func (c *Checker) propagateCalleeStaticLifetimes(
	ctx Context,
	fnType *type_system.FuncType,
	args []ast.Expr,
) {
	if ctx.Aliases == nil || fnType == nil {
		return
	}
	for i, p := range fnType.Params {
		// For a rest parameter (`...items: Array<T>`), the lifetime-
		// bearing position is the *element* type T, not the Array
		// container. Every variadic argument from this index onward is
		// passed as an element, so we mark each one — not just args[i].
		if rp, isRest := p.Pattern.(*type_system.RestPat); isRest {
			elem := arrayElemType(p.Type)
			if elem == nil {
				continue
			}
			// Walk the rest's inner pattern × element type to collect
			// every leaf-position mutability whose lifetime resolved to
			// `'static`. This catches both `...args: T[]` (a single
			// IdentPat leaf) and the destructured form `...[a, b]: ...[]`
			// where individual elements may escape independently.
			muts := collectStaticEscapeMutabilities(rp.Pattern, elem)
			if len(muts) == 0 {
				continue
			}
			for j := i; j < len(args); j++ {
				src := determineCheckerAliasSource(args[j])
				switch src.RootKind() {
				case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
					for _, mut := range muts {
						for _, vid := range src.UniqueVarIDs() {
							ctx.Aliases.MarkStatic(vid, mut)
						}
					}
				}
			}
			return
		}

		if i >= len(args) {
			break
		}
		// Walk the param's pattern × type in lockstep (same shape as
		// collectParamLeaves) so that destructured leaves carrying
		// `'static` are detected even when the *outer* param type has no
		// `'static` annotation. Phase 8.4's setLifetimeOnType attaches
		// `'static` at the leaf type position only — a tuple- or object-
		// destructured param with one escaping leaf has no top-level
		// static lifetime, so a single `GetLifetime(p.Type)` check would
		// silently miss it.
		muts := collectStaticEscapeMutabilities(p.Pattern, p.Type)
		if len(muts) == 0 {
			continue
		}
		// Use the checker-aware alias-source helper so a nested call whose
		// return aliases its argument (e.g. an identity-like wrapper)
		// propagates the escape to the underlying variable. The plain
		// liveness helper treats every CallExpr as fresh, hiding such
		// transitive aliases from the static-escape check.
		src := determineCheckerAliasSource(args[i])
		switch src.RootKind() {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, mut := range muts {
				for _, vid := range src.UniqueVarIDs() {
					ctx.Aliases.MarkStatic(vid, mut)
				}
			}
		}
	}
}

// collectStaticEscapeMutabilities walks a callee's parameter pattern in
// lockstep with its inferred type and returns the alias mutability of
// every leaf whose pruned lifetime contains `'static`. Pattern shapes
// match collectParamLeaves / walkPatternForLeaves: IdentPat is a leaf
// at the param's full type, TuplePat / ObjectPat / RestPat descend into
// the matching sub-type, ObjShorthandPat is a leaf at the property
// type, and ObjRestPat is skipped (its container is freshly assembled
// per call and can't carry a caller-provided lifetime). The mutability
// is determined per-leaf via isMutableType so a destructured param can
// emit a mix of mut/immut escapes.
func collectStaticEscapeMutabilities(pat type_system.Pat, t type_system.Type) []liveness.AliasMutability {
	var out []liveness.AliasMutability
	walkPatternForStaticLeaves(pat, t, &out)
	return out
}

func walkPatternForStaticLeaves(pat type_system.Pat, t type_system.Type, into *[]liveness.AliasMutability) {
	if pat == nil || t == nil {
		return
	}
	pt := stripMutabilityWrapper(type_system.Prune(t))
	switch p := pat.(type) {
	case *type_system.IdentPat:
		lt := type_system.PruneLifetime(type_system.GetLifetime(t))
		if !lifetimeContainsStatic(lt) {
			return
		}
		mut := liveness.AliasImmutable
		if isMutableType(t) {
			mut = liveness.AliasMutable
		}
		*into = append(*into, mut)
	case *type_system.TuplePat:
		tt, ok := pt.(*type_system.TupleType)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if i >= len(tt.Elems) {
				break
			}
			elemType := tt.Elems[i]
			if rest, ok := elemType.(*type_system.RestSpreadType); ok {
				elemType = rest.Type
			}
			walkPatternForStaticLeaves(elem, elemType, into)
		}
	case *type_system.ObjectPat:
		ot, ok := pt.(*type_system.ObjectType)
		if !ok {
			return
		}
		propTypes := make(map[string]type_system.Type)
		for _, elem := range ot.Elems {
			if prop, ok := elem.(*type_system.PropertyElem); ok &&
				prop.Name.Kind == type_system.StrObjTypeKeyKind {
				propTypes[prop.Name.Str] = prop.Value
			}
		}
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *type_system.ObjKeyValuePat:
				if propType, exists := propTypes[e.Key]; exists {
					walkPatternForStaticLeaves(e.Value, propType, into)
				}
			case *type_system.ObjShorthandPat:
				propType, exists := propTypes[e.Key]
				if !exists {
					continue
				}
				lt := type_system.PruneLifetime(type_system.GetLifetime(propType))
				if !lifetimeContainsStatic(lt) {
					continue
				}
				mut := liveness.AliasImmutable
				if isMutableType(propType) {
					mut = liveness.AliasMutable
				}
				*into = append(*into, mut)
			}
		}
	case *type_system.RestPat:
		elem := arrayElemType(t)
		if elem == nil {
			return
		}
		walkPatternForStaticLeaves(p.Pattern, elem, into)
	}
}

// lifetimeContainsStatic reports whether the given (already pruned)
// lifetime is `'static` or a LifetimeUnion that contains `'static`. A
// LifetimeUnion containing `'static` means at least one branch escaped,
// which is enough to treat the argument as permanently aliased.
func lifetimeContainsStatic(lt type_system.Lifetime) bool {
	switch v := lt.(type) {
	case *type_system.LifetimeValue:
		return v != nil && v.IsStatic
	case *type_system.LifetimeUnion:
		for _, m := range v.Lifetimes {
			if lifetimeContainsStatic(type_system.PruneLifetime(m)) {
				return true
			}
		}
	}
	return false
}

// findSelfVarID returns the VarID of the first `self` IdentExpr it
// finds in the given body. All references to `self` within a single
// ctor body share the same VarID (assigned as an extra param by
// `runLivenessPrePass`), so the first match is sufficient. Returns 0
// when the body has no `self` reference.
//
// The visitor descends into nested function expressions, which is
// safe today because `self` inside a closure within a ctor body
// captures the outer ctor's `self` and shares its VarID. Nested
// class declarations are not yet supported by the checker (the body
// phase panics on them), so a nested class introducing its own
// `self` is unreachable. If/when nested classes are implemented,
// this visitor must be hardened to stop at ClassDecl boundaries.
func findSelfVarID(body *ast.Block) liveness.VarID {
	if body == nil {
		return 0
	}
	v := &selfVarIDVisitor{}
	body.Accept(v)
	return v.found
}

type selfVarIDVisitor struct {
	ast.DefaultVisitor
	found liveness.VarID
}

func (v *selfVarIDVisitor) EnterExpr(e ast.Expr) bool {
	if v.found != 0 {
		return false
	}
	if ident, ok := e.(*ast.IdentExpr); ok && ident.Name == "self" && ident.VarID > 0 {
		v.found = liveness.VarID(ident.VarID)
		return false
	}
	return true
}

// InferConstructorLifetimes analyzes a class declaration to determine
// which constructor parameters are stored into `self` (as fields or
// through transitive aliases), attaches a fresh LifetimeVar to each such
// parameter and to the constructor's return type, and records the
// lifetime parameters on the class TypeAlias.
//
// Runs in the body phase, after `inferFuncBodyWithFuncSigType` has
// type-checked the constructor body. By that point the body phase's
// `runLivenessPrePass` has already populated VarIDs on the callable
// param patterns and every IdentExpr in the body, and callee signatures
// from peers in the same SCC are resolved — so
// `determineCheckerAliasSource` can see lifetime info on call returns
// and trace escapes like `self.f = wrap(p)`. The only piece the body
// phase doesn't write is `self`'s IdentPat VarID (it's an extra param,
// not in the rename's astParams), which `findSelfVarID` recovers by
// looking at any `self` IdentExpr in the body.
//
// Why not call `inferLifetimesCore` directly? Three reasons it isn't a
// drop-in fit, even after sharing the escape-detection primitives:
//
//  1. Escape root differs. `inferLifetimesCore` calls
//     `detectEscapingLeafIndices` with a nil extra-roots set, which
//     only flags lvalues whose root has `VarID <= 0` (non-local). In a
//     constructor, `self` is a local positive-VarID parameter, so
//     `self.x = p` wouldn't be detected. The constructor path passes
//     `self`'s VarID as an extra escape root to the same function.
//
//  2. No return-alias pass needed. `inferLifetimesCore` does most of
//     its work in `attachLifetimeToResult` — collecting return/yield
//     exprs and unioning lifetimes onto the result type. Constructor
//     bodies have no `return expr` (the return is the implicit
//     `self`), and the return type already gets `LifetimeArgs`
//     attached separately below. Running the result-side machinery
//     would be a no-op at best.
//
//  3. Output shape differs. `inferLifetimesCore` writes lifetimes
//     onto `funcType.LifetimeParams` only. Constructors also write
//     to `typeAlias.LifetimeParams` and call `setLifetimeArgsOnType`
//     on the return so callers see `C<'a>`. That's class-specific
//     bookkeeping `inferLifetimesCore` doesn't do.
//
// Deferred (see implementation_plan.md Phase 8 status):
//   - Inference of method-side return-type lifetimes when a method
//     captures a constructor param (the constructor gets the lifetime,
//     but the method's return type does not yet inherit it).
func (c *Checker) InferConstructorLifetimes(
	classDecl *ast.ClassDecl,
	typeAlias *type_system.TypeAlias,
	ctorFn *type_system.FuncType,
) {
	if classDecl == nil || ctorFn == nil || typeAlias == nil {
		return
	}

	// Per #499: constructor calls always return immutable instances; the user
	// opts in to a mutable instance via the `mut` prefix at the call site.

	// Honor explicit lifetime params if the user already wrote them.
	if len(typeAlias.LifetimeParams) > 0 {
		return
	}

	// Locate the (single) in-body `ConstructorElem`. After Phase 4 there is
	// no other source of constructor params on a class. If synthesis or
	// parsing left no constructor in place, there's nothing to do.
	var ctorElem *ast.ConstructorElem
	for _, e := range classDecl.Body {
		if c, ok := e.(*ast.ConstructorElem); ok {
			ctorElem = c
			break
		}
	}
	// User-written ctors always parse with a body, and no synthesizer
	// produces a body-less ConstructorElem; the Body == nil guard is
	// defensive only.
	if ctorElem == nil || ctorElem.Fn == nil || ctorElem.Fn.Body == nil {
		return
	}
	// The first AST param is `mut self`. The remaining params are the
	// callable params and line up with `ctorFn.Params` (which
	// `inferConstructorSig` populates by dropping `self`).
	allCtorParams := ctorElem.Fn.Params
	if len(allCtorParams) == 0 {
		return
	}
	callableParams := allCtorParams[1:]
	if len(callableParams) == 0 {
		return
	}

	// VarIDs on the callable params and on every IdentExpr in the body
	// were already populated by the body phase's `runLivenessPrePass`
	// before this function was called; we just reuse them. To find
	// `self`'s VarID we walk the body for any `self` IdentExpr — the
	// extra-param treatment in `runLivenessPrePass` ensures every `self`
	// reference resolves to the same positive VarID. If the body never
	// references `self`, no `self.x = ...` assignment exists, so there
	// is nothing to capture.
	selfVarID := findSelfVarID(ctorElem.Fn.Body)
	if selfVarID == 0 {
		return
	}

	// Build the per-leaf index from callable-param patterns × types so
	// that destructured leaves are tracked individually (e.g. for
	// `constructor(self, [a, b]: [...])` each of `a`, `b` gets its own
	// leaf). Lifetime allocation below still happens at the top-level
	// param granularity, but per-leaf escape tracking lets the RHS
	// alias-source tracing find the right binding.
	leaves := collectParamLeaves(callableParams, ctorFn.Params)
	if len(leaves) == 0 {
		return
	}
	leafIndex := make(map[liveness.VarID]int)
	for i, l := range leaves {
		leafIndex[l.varID] = i
	}

	// Detect escapes: assignments `self.<…> = expr` where the RHS aliases
	// one of the callable params. `self`'s VarID is added as an extra
	// escape root because it is itself a positive-VarID parameter — the
	// non-local-only check would not recognize `self.x = p` as escaping.
	escapeRoots := set.NewSet[liveness.VarID]()
	escapeRoots.Add(selfVarID)
	escapingLeaves := detectEscapingLeafIndices(ctorElem.Fn.Body, leafIndex, escapeRoots)
	if escapingLeaves.Len() == 0 {
		return
	}

	// Allocate one lifetime per escaping leaf (in declaration order for
	// determinism). For an IdentPat top-level param the leaf type IS the
	// whole param type; for destructured patterns the leaf type is the
	// sub-position the rename pass bound (e.g. the tuple element type for
	// `[a, b]: [mut T, mut U]`).
	var lifetimeParams []*type_system.LifetimeVar
	leafLV := make(map[int]*type_system.LifetimeVar)
	for leafIdx, leaf := range leaves {
		if !escapingLeaves.Contains(leafIdx) {
			continue
		}
		if !typeCarriesLifetime(leaf.leafType) {
			continue
		}
		lv := c.FreshLifetimeVar(lifetimeParamName(len(lifetimeParams)))
		lifetimeParams = append(lifetimeParams, lv)
		setLifetimeOnType(leaf.leafType, lv)
		leafLV[leafIdx] = lv
	}

	if len(lifetimeParams) == 0 {
		return
	}

	typeAlias.LifetimeParams = lifetimeParams
	ctorFn.LifetimeParams = lifetimeParams

	// Attach LifetimeArgs to the constructor's return type so callers can
	// see which lifetime corresponds to which parameter.
	lifetimeArgs := make([]type_system.Lifetime, len(lifetimeParams))
	for i, lv := range lifetimeParams {
		lifetimeArgs[i] = lv
	}
	setLifetimeArgsOnType(ctorFn.Return, lifetimeArgs)

	// (#539) Walk `self.<field> = expr` assignments and attach per-slot
	// lifetimes to the field types on the instance. When constructor
	// params are unannotated, the param type IS the field-slot type
	// (same pointer) so the setLifetimeOnType call above already
	// surfaced lifetimes on the instance. With explicit param
	// annotations the param and field types are distinct, so the field
	// slots need a separate pass mirroring attachLifetimeToResult.
	attachFieldSlotLifetimes(ctorElem.Fn.Body, selfVarID, typeAlias.Type, leafIndex, leafLV)
}

// attachFieldSlotLifetimes walks a constructor body for `self.<field> = expr`
// assignments and attaches per-slot lifetimes to the corresponding field types
// on the class instance. Mirrors the slot-accumulation logic in
// attachLifetimeToResult, except the destination is a class field rather than
// the function's result type.
//
// Only direct `self.<field> = expr` and `self[<literal-key>] = expr`
// assignments are walked. Nested `self.<f>.<g> = expr` is rejected
// upstream by the field-initialization checker (every field must be
// initialized before being read), and `self[k] = expr` with a non-literal
// key cannot be statically resolved to a single field slot.
//
// 'static is intentionally not propagated here. InferConstructorLifetimes
// allocates a fresh LifetimeVar for every escaping leaf — including leaves
// that escape into module-level state — rather than pinning 'static, so
// there are no 'static leaves for this pass to forward. If the upstream
// allocation is ever taught to distinguish module-level escapes, this
// helper will need a markStatic path too.
func attachFieldSlotLifetimes(
	body *ast.Block,
	selfVarID liveness.VarID,
	instanceType type_system.Type,
	leafIndex map[liveness.VarID]int,
	leafLV map[int]*type_system.LifetimeVar,
) {
	// Nothing to attach if the body is empty or no escaping param leaf
	// got a LifetimeVar in the prior allocation pass.
	if body == nil || len(leafLV) == 0 {
		return
	}
	// The instance type is the class's TypeAlias.Type, which after Prune
	// is the ObjectType holding the field properties. Anything else (e.g.
	// a generic instantiation that hasn't resolved yet) is skipped.
	instanceObj, ok := type_system.Prune(instanceType).(*type_system.ObjectType)
	if !ok {
		return
	}

	// Accumulate per-slot lifetime contributions across all `self.<f> = …`
	// assignments before writing them out in finalize, so multiple
	// assignments to the same field union their lifetimes (rather than
	// overwriting each other) and repeated leaves dedupe.
	slots := newSlotAccumulator()
	v := &fieldAssignVisitor{
		selfVarID: selfVarID,
		onAssign: func(fieldName string, rhs ast.Expr) {
			// Map the field name to its slot in the instance type. If
			// the field doesn't exist or is a primitive (no lifetime to
			// carry), there's nothing to attach.
			fieldType := lookupInstanceFieldType(instanceObj, fieldName)
			if fieldType == nil || !typeCarriesLifetime(fieldType) {
				return
			}
			// Resolve the RHS to its underlying parameter leaves. Using
			// the checker-aware variant means call results like
			// `self.f = wrap(p)` propagate through the callee's already-
			// inferred return-aliases-its-arg lifetimes.
			src := determineCheckerAliasSource(rhs)
			if len(src.Leaves) == 0 {
				return
			}
			for _, leaf := range src.Leaves {
				// Only leaves that map back to one of *this* ctor's
				// escaping params...
				idx, ok := leafIndex[leaf.RootVarID]
				if !ok {
					continue
				}
				// ...and got a LifetimeVar, contribute.
				lv, ok := leafLV[idx]
				if !ok {
					continue
				}
				// Decide which slot of the field type the leaf lands on.
				// Fresh-rooted RHS like `[a, b]` or `{head: a}` carries
				// per-slot paths; alias-rooted RHS attaches at the top
				// of the field type. See pickResultSlot for the rule.
				slot := pickResultSlot(fieldType, src.Origin, leaf.Path)
				if !typeCarriesLifetime(slot) {
					continue
				}
				slots.add(slot, lv)
			}
		},
	}
	// Walk the body to collect contributions, then write the
	// (possibly-unioned) lifetimes onto each accumulated slot.
	body.Accept(v)
	slots.finalize()
}

// lookupInstanceFieldType returns the type of an instance field by name on
// a class instance ObjectType, or nil if no matching property exists. Both
// string-keyed and number-keyed properties are matched — number keys are
// compared after formatting via strconv.FormatFloat so the lookup string
// can come directly from a `self[<numeric-literal>] = …` lvalue.
//
// TODO(#543): symbol-keyed properties are not yet handled here. Resolving
// a symbol key from a `self[<sym>] = …` lvalue requires the same lookup
// machinery used elsewhere for symbol keys; once that lands, extend this
// helper to match SymObjTypeKeyKind too.
func lookupInstanceFieldType(obj *type_system.ObjectType, name string) type_system.Type {
	for _, elem := range obj.Elems {
		prop, ok := elem.(*type_system.PropertyElem)
		if !ok {
			continue
		}
		switch prop.Name.Kind {
		case type_system.StrObjTypeKeyKind:
			if prop.Name.Str == name {
				return prop.Value
			}
		case type_system.NumObjTypeKeyKind:
			if strconv.FormatFloat(prop.Name.Num, 'f', -1, 64) == name {
				return prop.Value
			}
		}
	}
	return nil
}

// fieldAssignVisitor invokes onAssign for every `self.<field> = expr` or
// `self[<literal-key>] = expr` assignment in a constructor body. Nested
// function bodies and class/func declarations are skipped so we only see
// the constructor's own assignments.
type fieldAssignVisitor struct {
	ast.DefaultVisitor
	selfVarID liveness.VarID
	onAssign  func(fieldName string, rhs ast.Expr)
}

func (v *fieldAssignVisitor) EnterExpr(e ast.Expr) bool {
	if be, ok := e.(*ast.BinaryExpr); ok && be.Op == ast.Assign {
		if name, ok := selfFieldLValueName(be.Left, v.selfVarID); ok {
			v.onAssign(name, be.Right)
		}
	}
	if _, ok := e.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

// selfFieldLValueName returns the field name targeted by an lvalue if and
// only if it is a direct field assignment on `self` — either `self.<name>`
// (MemberExpr) or `self[<literal-key>]` (IndexExpr with a string- or
// number-literal index). Other shapes (chained access, computed keys,
// non-self objects) return ok=false.
func selfFieldLValueName(lvalue ast.Expr, selfVarID liveness.VarID) (string, bool) {
	switch e := lvalue.(type) {
	case *ast.MemberExpr:
		ident, ok := e.Object.(*ast.IdentExpr)
		if !ok || liveness.VarID(ident.VarID) != selfVarID || e.Prop == nil {
			return "", false
		}
		return e.Prop.Name, true
	case *ast.IndexExpr:
		ident, ok := e.Object.(*ast.IdentExpr)
		if !ok || liveness.VarID(ident.VarID) != selfVarID {
			return "", false
		}
		lit, ok := e.Index.(*ast.LiteralExpr)
		if !ok {
			return "", false
		}
		switch k := lit.Lit.(type) {
		case *ast.StrLit:
			return k.Value, true
		case *ast.NumLit:
			return strconv.FormatFloat(k.Value, 'f', -1, 64), true
		}
	}
	return "", false
}

func (v *fieldAssignVisitor) EnterDecl(d ast.Decl) bool {
	switch d.(type) {
	case *ast.FuncDecl, *ast.ClassDecl:
		return false
	}
	return true
}

// forEachLeafBinding invokes fn for every identifier-binding leaf
// introduced by a pattern, walking through Tuple/Object/Rest containers.
// Leaves are IdentPat (the pattern's own Name+VarID) and ObjShorthandPat
// (the property's Key.Name plus the shorthand's own VarID). ObjRestPat,
// RestPat, and ObjKeyValuePat are container-only; this helper recurses
// through them.
//
// Note: walkPatternForLeaves cannot use this helper because it walks the
// pattern in *lockstep with the parameter's type* (slicing tuple/object/
// array sub-positions to compute each leaf's leafType) and intentionally
// skips ObjRestPat (a freshly-assembled container has no
// caller-provided lifetime). The two name-only walkers below share this
// traversal; lifetime walking does not.
func forEachLeafBinding(pat ast.Pat, fn func(name string, varID int)) {
	if pat == nil {
		return
	}
	switch p := pat.(type) {
	case *ast.IdentPat:
		fn(p.Name, p.VarID)
	case *ast.TuplePat:
		for _, sub := range p.Elems {
			forEachLeafBinding(sub, fn)
		}
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				forEachLeafBinding(e.Value, fn)
			case *ast.ObjShorthandPat:
				if e.Key != nil {
					fn(e.Key.Name, e.VarID)
				}
			case *ast.ObjRestPat:
				forEachLeafBinding(e.Pattern, fn)
			}
		}
	case *ast.RestPat:
		forEachLeafBinding(p.Pattern, fn)
	}
}

// collectPatternBindingNames adds every identifier name introduced by a
// pattern (recursively) to the provided set.
func collectPatternBindingNames(p ast.Pat, into set.Set[string]) {
	forEachLeafBinding(p, func(name string, _ int) {
		into.Add(name)
	})
}

// typeCarriesLifetime reports whether the given type can carry a lifetime.
// Walks past mutability wrappers. Unbounded type parameters are excluded,
// since the parameter might be instantiated to a primitive at the call
// site, in which case the lifetime would have nowhere to live. A
// constrained type parameter is treated as lifetime-bearing iff its
// constraint is itself lifetime-bearing — every legal instantiation of
// the parameter will then have a place to attach the lifetime.
func typeCarriesLifetime(t type_system.Type) bool {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		if ty.TypeAlias != nil && ty.TypeAlias.IsTypeParam {
			// Phase 10.2: walk into the bound. For type-parameter
			// scope entries, TypeAlias.Type holds the constraint
			// (or UnknownType for unbounded params, which falls
			// through to false). Use boundCarriesLifetime so a
			// constraint that itself names an alias resolving to a
			// primitive (e.g. `type Num = number; T: Num`) returns
			// false.
			if ty.TypeAlias.Type == nil {
				return false
			}
			return boundCarriesLifetime(ty.TypeAlias.Type)
		}
		return true
	case *type_system.ObjectType, *type_system.TupleType:
		_ = ty
		return true
	case *type_system.MutType:
		return typeCarriesLifetime(ty.Type)
	}
	return false
}

// boundCarriesLifetime is the constraint-walking variant of
// typeCarriesLifetime. Unlike the general check, it expands non-type-
// param TypeRefType bounds to their alias body so that a constraint
// like `T: Num` (where `type Num = number`) resolves to its primitive
// body and reports false. Outside the constraint walk we keep the
// conservative "TypeRefType always carries a lifetime" rule because
// real reference shapes (classes, parameterized aliases over objects)
// flow through it.
func boundCarriesLifetime(t type_system.Type) bool {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		if ty.TypeAlias != nil && ty.TypeAlias.IsTypeParam {
			if ty.TypeAlias.Type == nil {
				return false
			}
			return boundCarriesLifetime(ty.TypeAlias.Type)
		}
		if ty.TypeAlias != nil && ty.TypeAlias.Type != nil {
			return boundCarriesLifetime(ty.TypeAlias.Type)
		}
		return true
	case *type_system.ObjectType, *type_system.TupleType:
		_ = ty
		return true
	case *type_system.MutType:
		return boundCarriesLifetime(ty.Type)
	}
	return false
}

// cloneLifetimeBearing returns a shallow copy of t suitable for
// mutating its Lifetime field without affecting the original, or nil
// if t cannot actually carry a lifetime. Walks past MutType wrappers
// so the inner TypeRefType/ObjectType/TupleType is copied too. A
// TypeRefType is considered lifetime-bearing only if its alias body
// resolves to a lifetime-bearing shape (per boundCarriesLifetime), so
// callers can't accidentally attach a lifetime to e.g. a TypeRefType
// aliasing a primitive.
func cloneLifetimeBearing(t type_system.Type) type_system.Type {
	if !boundCarriesLifetime(t) {
		return nil
	}
	switch ty := type_system.Prune(t).(type) {
	case *type_system.MutType:
		inner := cloneLifetimeBearing(ty.Type)
		if inner == nil {
			return nil
		}
		return type_system.NewMutType(nil, inner)
	case *type_system.TypeRefType, *type_system.ObjectType, *type_system.TupleType:
		return ty.Copy()
	}
	return nil
}

// lifetimeBearingHasNoLifetime reports whether t can carry a Lifetime
// field but currently has none. Used by Phase 10.5 substitution to
// decide whether to transfer the use-site lifetime onto a resolved
// shape, leaving any pre-existing annotation in place for the caller
// to unify.
func lifetimeBearingHasNoLifetime(t type_system.Type) bool {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		return ty.Lifetime == nil
	case *type_system.ObjectType:
		return ty.Lifetime == nil
	case *type_system.TupleType:
		return ty.Lifetime == nil
	case *type_system.MutType:
		return lifetimeBearingHasNoLifetime(ty.Type)
	}
	return false
}

// setLifetimeArgsOnType attaches a list of lifetime arguments to a
// TypeRefType (e.g. Container<'a, 'b>), walking past mutability wrappers.
func setLifetimeArgsOnType(t type_system.Type, args []type_system.Lifetime) {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.TypeRefType:
		ty.LifetimeArgs = args
	case *type_system.MutType:
		setLifetimeArgsOnType(ty.Type, args)
	}
}

// determineCheckerAliasSource is a checker-aware wrapper around
// liveness.DetermineAliasSource that handles call expressions whose callee
// has lifetime parameters: the result is added to the alias set of each
// argument whose corresponding parameter shares a lifetime with the
// return type.
//
// For non-call expressions and calls whose callee has no lifetime
// information, this falls through to liveness.DetermineAliasSource.
func determineCheckerAliasSource(expr ast.Expr) liveness.AliasSource {
	// Recurse through pure projection nodes using the checker-aware
	// helper so that fresh-rooted leaves produced by an inner CallExpr
	// (via embeddedLifetimeAliasSource) survive the descent. Falling
	// through to liveness.DetermineAliasSource would re-derive the
	// inner alias source without lifetime information and lose the
	// per-slot leaves.
	switch e := expr.(type) {
	case *ast.MemberExpr:
		return liveness.ProjectStep(determineCheckerAliasSource(e.Object), liveness.PropertyOf{Key: e.Prop.Name})
	case *ast.IndexExpr:
		inner := determineCheckerAliasSource(e.Object)
		if step, ok := liveness.IndexLiteralStep(e); ok {
			return liveness.ProjectStep(inner, step)
		}
		return inner
	case *ast.TypeCastExpr:
		return determineCheckerAliasSource(e.Expr)
	case *ast.AwaitExpr:
		return liveness.ProjectStep(determineCheckerAliasSource(e.Arg), liveness.AwaitOf{})
	}

	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		return liveness.DetermineAliasSource(expr)
	}

	calleeType := callExpr.Callee.InferredType()
	fnType := extractFuncType(calleeType)
	if fnType == nil {
		return liveness.DetermineAliasSource(expr)
	}
	// Don't gate on fnType.LifetimeParams here: by the time the call is
	// type-checked, callee-side instantiation may have cleared
	// LifetimeParams while leaving the LifetimeVars themselves attached
	// to the param/return type positions. The presence of a lifetime on
	// the return type is the authoritative signal that this call carries
	// alias information.

	retLifetime := type_system.PruneLifetime(type_system.GetLifetime(fnType.Return))
	if retLifetime == nil {
		// Top-level lifetime is missing, but the return type may still
		// embed lifetimes inside its slots (e.g. wrap returns
		// `{head: 'a Point, tail: 'b Point}` with no top-level
		// Lifetime). Walk the return type to surface those embedded
		// slots as fresh-rooted leaves with per-slot paths so callers
		// like `return wrap(a, b)` can drive per-slot lifetime
		// attachment in their own signatures.
		return embeddedLifetimeAliasSource(fnType, callExpr)
	}

	// Build the set of LifetimeVar IDs that the return type carries.
	// PruneLifetime already followed the Instance pointers, so members are
	// either resolved LifetimeValues or unresolved LifetimeVars; we only
	// need the LifetimeVar IDs because that's what the parameter types
	// will (also) carry pre-resolution.
	retVarIDs := lifetimeVarIDs(retLifetime)
	if len(retVarIDs) == 0 {
		return liveness.DetermineAliasSource(expr)
	}

	// For each parameter whose lifetime is in retVarIDs, propagate the
	// argument's alias source into the result's alias source.
	var aggregated []liveness.VarID
	seen := set.NewSet[liveness.VarID]()
	for i, p := range fnType.Params {
		if i >= len(callExpr.Args) {
			break
		}
		paramLifetime := type_system.PruneLifetime(type_system.GetLifetime(p.Type))
		// Symmetric with the return-side handling above: use lifetimeVarIDs
		// so we correctly recognize a param whose lifetime is a
		// LifetimeUnion containing one or more vars also referenced by the
		// return. Today inferred params only ever get a single LifetimeVar
		// (see setLifetimeOnType call sites in InferLifetimes /
		// InferConstructorLifetimes), and user-annotated lifetimes aren't
		// yet propagated into Type.Lifetime by inferTypeAnn — so this
		// branch is currently only hit when the helper happens to see a
		// single var. The overlap check is defensive: once Phase 9
		// unification produces union bindings on params, or the type-ann
		// pipeline starts honoring user-written ('a | 'b) annotations,
		// this code path becomes reachable and the symmetric treatment
		// avoids silently dropping union-param aliasing.
		paramVarIDs := lifetimeVarIDs(paramLifetime)
		overlap := false
		for id := range paramVarIDs {
			if retVarIDs.Contains(id) {
				overlap = true
				break
			}
		}
		if !overlap {
			continue
		}
		argSource := determineCheckerAliasSource(callExpr.Args[i])
		switch argSource.RootKind() {
		case liveness.AliasSourceVariable, liveness.AliasSourceMultiple:
			for _, id := range argSource.UniqueVarIDs() {
				if !seen.Contains(id) {
					seen.Add(id)
					aggregated = append(aggregated, id)
				}
			}
		}
	}

	switch len(aggregated) {
	case 0:
		// None of the matching arguments could be tracked back to a local
		// variable — fall back to the default treatment.
		return liveness.DetermineAliasSource(expr)
	default:
		leaves := make([]liveness.AliasLeaf, len(aggregated))
		for i, id := range aggregated {
			leaves[i] = liveness.AliasLeaf{RootVarID: id}
		}
		// The callee returns one of its parameters at the top level, so
		// the call result aliases the corresponding argument root(s) — use
		// Alias origin (not Fresh) so RootKind() reports Variable for one
		// aggregated leaf and Multiple for several. That matches what the
		// alias tracker and transition checker expect for "this expression
		// directly aliases these variables."
		return liveness.AliasSource{Origin: liveness.AliasOriginAlias, Leaves: leaves}
	}
}

// embeddedLifetimeAliasSource handles calls whose return type carries
// no top-level Lifetime but has lifetimes embedded in its inner slots.
// For example, `wrap` returns `{head: 'a Point, tail: 'b Point}`: the
// outer ObjectType has no Lifetime, but the `head` and `tail` slots do.
//
// The function walks the return type to collect each (path, lifetimeVar)
// pair, then for every pair it finds the parameter whose lifetime
// matches and pulls in the corresponding argument's alias source,
// prepending the slot's path to each leaf. The result is a fresh-rooted
// source — calling `wrap(a, b)` constructs a new container whose root
// aliases nothing, but whose nested slots alias the arguments at the
// matching paths.
//
// If no embedded slot matches any parameter, the function falls back to
// the plain liveness handling, which classifies the call as fresh with
// no leaves.
//
// Downstream `MemberExpr` / `IndexExpr` projection consumes the matching
// front step from each leaf (see liveness.ProjectStep), so
// `wrap(a, b).head` correctly narrows to the `'a`-only leaf.
func embeddedLifetimeAliasSource(fnType *type_system.FuncType, callExpr *ast.CallExpr) liveness.AliasSource {
	slots := collectReturnLifetimeSlots(fnType.Return)
	if len(slots) == 0 {
		return liveness.DetermineAliasSource(callExpr)
	}

	// Per-(arg-leaf-root, slot-path) leaves. Dedupe by (RootVarID,
	// canonical path) so two slots that bind the same lifetime to the
	// same arg don't produce duplicate leaves.
	type leafKey struct {
		rootVarID liveness.VarID
		pathKey   string
	}
	seen := map[leafKey]bool{}
	var leaves []liveness.AliasLeaf
	for _, slot := range slots {
		for i, p := range fnType.Params {
			if i >= len(callExpr.Args) {
				break
			}
			paramVarIDs := lifetimeVarIDs(type_system.PruneLifetime(type_system.GetLifetime(p.Type)))
			if !paramVarIDs.Contains(slot.lifetimeVarID) {
				continue
			}
			argSource := determineCheckerAliasSource(callExpr.Args[i])
			for _, argLeaf := range argSource.Leaves {
				combined := slices.Concat(slot.path, argLeaf.Path)
				key := leafKey{rootVarID: argLeaf.RootVarID, pathKey: liveness.PathKey(combined)}
				if seen[key] {
					continue
				}
				seen[key] = true
				leaves = append(leaves, liveness.AliasLeaf{
					RootVarID: argLeaf.RootVarID,
					Path:      combined,
				})
			}
		}
	}

	if len(leaves) == 0 {
		return liveness.DetermineAliasSource(callExpr)
	}
	return liveness.AliasSource{Origin: liveness.AliasOriginFresh, Leaves: leaves}
}

// returnLifetimeSlot records one (path, lifetimeVarID) pair for a
// lifetime-bearing slot inside a return type. A slot whose lifetime is
// a LifetimeUnion produces multiple entries (one per LifetimeVar
// member) so that each var can be matched independently against
// param lifetimes.
type returnLifetimeSlot struct {
	path          []liveness.ProjectionStep
	lifetimeVarID int
}

// collectReturnLifetimeSlots walks a return type and returns one entry
// per (slot, LifetimeVar) — including the top-level slot when the type
// carries a Lifetime. Recurses into ObjectType properties and TupleType
// elements.
//
// TODO(#544): container TypeRefTypes (Array<T>, Promise<T>, etc.) are
// not descended into here, so call sites of explicitly-typed
// generic-returning functions like `fn wrap<'a>(p: 'a P) -> Array<'a P>`
// don't pick up the embedded lifetime. The downstream attachment
// vocabulary already supports ElementOf and AwaitOf (stepIntoSlot
// handles them); extending this walker to emit them is the missing
// piece. The literal-as-tuple case is unaffected because TupleType
// recursion + IndexOf already covers it.
func collectReturnLifetimeSlots(t type_system.Type) []returnLifetimeSlot {
	var out []returnLifetimeSlot
	walkReturnLifetimeSlots(t, nil, &out)
	return out
}

func walkReturnLifetimeSlots(t type_system.Type, path []liveness.ProjectionStep, into *[]returnLifetimeSlot) {
	pruned := type_system.Prune(t)
	if mut, ok := pruned.(*type_system.MutType); ok {
		pruned = type_system.Prune(mut.Type)
	}
	var lifetime type_system.Lifetime
	switch ty := pruned.(type) {
	case *type_system.TypeRefType:
		lifetime = ty.Lifetime
	case *type_system.ObjectType:
		lifetime = ty.Lifetime
	case *type_system.TupleType:
		lifetime = ty.Lifetime
	}
	if lifetime != nil {
		for id := range lifetimeVarIDs(type_system.PruneLifetime(lifetime)) {
			pathCopy := make([]liveness.ProjectionStep, len(path))
			copy(pathCopy, path)
			*into = append(*into, returnLifetimeSlot{path: pathCopy, lifetimeVarID: id})
		}
	}
	switch ty := pruned.(type) {
	case *type_system.ObjectType:
		for _, elem := range ty.Elems {
			prop, ok := elem.(*type_system.PropertyElem)
			if !ok {
				continue
			}
			var key string
			switch prop.Name.Kind {
			case type_system.StrObjTypeKeyKind:
				key = prop.Name.Str
			case type_system.NumObjTypeKeyKind:
				key = liveness.FormatNumKey(prop.Name.Num)
			// TODO(#543): SymObjTypeKeyKind not handled — symbol-keyed
			// properties need a representation in PropertyOf (or a new
			// step kind) before they can be walked here.
			default:
				continue
			}
			childPath := append(path, liveness.PropertyOf{Key: key})
			walkReturnLifetimeSlots(prop.Value, childPath, into)
		}
	case *type_system.TupleType:
		for i, elem := range ty.Elems {
			childPath := append(path, liveness.IndexOf{Index: i})
			walkReturnLifetimeSlots(elem, childPath, into)
		}
	}
}

// extractFuncType reaches into the (possibly wrapped) callee type to find
// the underlying FuncType. Mirrors the dispatch in inferCallExpr.
func extractFuncType(t type_system.Type) *type_system.FuncType {
	switch ty := type_system.Prune(t).(type) {
	case *type_system.FuncType:
		return ty
	case *type_system.TypeRefType:
		if ty.TypeAlias != nil {
			if obj, ok := type_system.Prune(ty.TypeAlias.Type).(*type_system.ObjectType); ok {
				return funcFromObjectType(obj)
			}
		}
	case *type_system.ObjectType:
		return funcFromObjectType(ty)
	}
	return nil
}

func funcFromObjectType(obj *type_system.ObjectType) *type_system.FuncType {
	for _, elem := range obj.Elems {
		switch e := elem.(type) {
		case *type_system.ConstructorElem:
			return e.Fn
		case *type_system.CallableElem:
			return e.Fn
		}
	}
	return nil
}

// lifetimeVarIDs returns the set of LifetimeVar IDs referenced by the
// given (pruned) lifetime. Returns a non-nil empty set when the lifetime
// is a resolved LifetimeValue with no associated variables.
func lifetimeVarIDs(lt type_system.Lifetime) set.Set[int] {
	ids := set.NewSet[int]()
	switch v := lt.(type) {
	case *type_system.LifetimeVar:
		ids.Add(v.ID)
	case *type_system.LifetimeUnion:
		for _, m := range v.Lifetimes {
			for id := range lifetimeVarIDs(type_system.PruneLifetime(m)) {
				ids.Add(id)
			}
		}
	}
	return ids
}

// returnExprVisitor walks a function body collecting return-statement
// expressions. It does not descend into nested functions (their returns
// belong to the inner scope).
type returnExprVisitor struct {
	ast.DefaultVisitor
	exprs []ast.Expr
}

func (v *returnExprVisitor) EnterStmt(stmt ast.Stmt) bool {
	if r, ok := stmt.(*ast.ReturnStmt); ok && r.Expr != nil {
		v.exprs = append(v.exprs, r.Expr)
	}
	return true
}

func (v *returnExprVisitor) EnterExpr(expr ast.Expr) bool {
	// Skip nested function bodies — their returns are not ours.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

func (v *returnExprVisitor) EnterDecl(decl ast.Decl) bool {
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}

// generatorYieldType returns the yield type T of a Generator<T, TReturn,
// TNext> or AsyncGenerator<T, TReturn, TNext> reference, walking past
// mutability wrappers. Returns nil if t is not a generator reference or
// has no type args.
func generatorYieldType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	name := type_system.QualIdentToString(tref.Name)
	if name != "Generator" && name != "AsyncGenerator" {
		return nil
	}
	if len(tref.TypeArgs) == 0 {
		return nil
	}
	return tref.TypeArgs[0]
}

// generatorReturnType returns the TReturn slot of a
// Generator<T, TReturn, TNext> or AsyncGenerator<T, TReturn, TNext>
// reference, walking past mutability wrappers. Returns nil if t is not
// a generator reference or doesn't have a second type arg.
func generatorReturnType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	name := type_system.QualIdentToString(tref.Name)
	if name != "Generator" && name != "AsyncGenerator" {
		return nil
	}
	if len(tref.TypeArgs) < 2 {
		return nil
	}
	return tref.TypeArgs[1]
}

// promiseValueType returns the resolved value type T of a
// Promise<T, E> reference, walking past mutability wrappers. Returns
// nil if t is not a Promise reference or has no type args.
func promiseValueType(t type_system.Type) type_system.Type {
	pt := stripMutabilityWrapper(type_system.Prune(t))
	tref, ok := pt.(*type_system.TypeRefType)
	if !ok {
		return nil
	}
	if type_system.QualIdentToString(tref.Name) != "Promise" {
		return nil
	}
	if len(tref.TypeArgs) == 0 {
		return nil
	}
	return tref.TypeArgs[0]
}

// collectYieldExprs walks a function body collecting the value
// expressions of every yield expression — both regular (`yield e`) and
// delegate (`yield from iter`). For a delegate yield, the iterator
// expression itself is the alias source: each yielded value is an
// element of the iterator and so borrows from it, meaning the relay
// generator's yield T inherits the iterator's lifetime. Bare `yield`
// (with no value) is skipped because there is no value to alias.
func collectYieldExprs(body *ast.Block) []ast.Expr {
	v := &yieldExprVisitor{}
	for _, stmt := range body.Stmts {
		stmt.Accept(v)
	}
	return v.exprs
}

type yieldExprVisitor struct {
	ast.DefaultVisitor
	exprs []ast.Expr
}

func (v *yieldExprVisitor) EnterExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.YieldExpr:
		// Both regular and delegate yields contribute alias-source
		// information. For `yield expr`, expr is the yielded value;
		// for `yield from iter`, iter is the source whose elements
		// are yielded one by one — propagating iter's lifetime to
		// each yielded element.
		if e.Value != nil {
			v.exprs = append(v.exprs, e.Value)
		}
		return true
	case *ast.FuncExpr:
		// Skip nested function bodies — their yields belong to the
		// inner generator (if any), not ours.
		return false
	}
	return true
}

func (v *yieldExprVisitor) EnterDecl(decl ast.Decl) bool {
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}
