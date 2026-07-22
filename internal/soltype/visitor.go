package soltype

import "fmt"

// EnterResult controls traversal after EnterType.
//
// A non-nil Type replaces the node: with SkipChildren=false it becomes the basis
// for the structural rebuild (its children are walked, so it MUST be the same
// concrete kind), with SkipChildren=true it is handed straight to ExitType without
// descending (and may be ANY kind). A nil Type keeps the node.
type EnterResult struct {
	Type         Type // nil = keep the node; non-nil = replace it
	SkipChildren bool // true = skip the structural rebuild, go straight to ExitType
}

// TypeVisitor is a polarity-threading rewriting visitor over soltype.Type. Unlike
// type_system's visitor it carries Polarity, and Accept flips it on contravariant
// positions (function parameters) so variance knowledge lives in ONE place rather
// than being re-spelled in every type→type transform (coalesce / extrude /
// freshenAbove). EnterType fires before child traversal — it may prune, replace,
// or take over the recursion via SkipChildren (the var node's bounds are a side
// graph, not tree children, so each transform handles TypeVarType itself in
// EnterType). ExitType fires after, bottom-up, as a function of the
// already-rewritten children.
type TypeVisitor interface {
	EnterType(t Type, pol Polarity) EnterResult
	ExitType(t Type, pol Polarity) Type
}

// Accept methods rebuild structural nodes IDENTITY-PRESERVINGLY: a fresh node is
// allocated only when a child actually changed; an unchanged subtree keeps its
// pointer (no needless allocation, and identity-keyed caches / seen-sets stay
// valid across a walk). The structural arms copy the child slice on first change
// (copy-on-write) so the original is never mutated.

// acceptLeaf handles a childless node (atoms, and TypeVarType — whose bounds are a
// side graph the transforms walk themselves in EnterType): EnterType may replace
// it, then ExitType finalizes. SkipChildren is irrelevant — there are no children.
func acceptLeaf(t Type, v TypeVisitor, pol Polarity) Type {
	cur := t
	if e := v.EnterType(t, pol); e.Type != nil {
		cur = e.Type
	}
	return v.ExitType(cur, pol)
}

// skipReplace resolves the node a SkipChildren EnterResult hands to ExitType: the
// replacement when one was given, else the original. No structural rebuild follows,
// so the replacement may be ANY kind (e.g. coalesce inlining a var to a union) —
// the same-kind requirement the descend path enforces does not apply here.
func skipReplace(t Type, e EnterResult) Type {
	if e.Type != nil {
		return e.Type
	}
	return t
}

// descendReplacement resolves the node the descend path rebuilds children from: the
// EnterType replacement when one was given, else the original. Because the children
// are walked with this node's child structure, a replacement MUST be the same
// concrete kind; a different-kind replacement under SkipChildren=false is a visitor
// contract violation, reported with a clear message rather than a bare
// type-assertion fault. (Use SkipChildren=true to replace with a different kind.)
func descendReplacement[T Type](original T, e EnterResult) T {
	if e.Type == nil {
		return original
	}
	repl, ok := e.Type.(T)
	if !ok {
		panic(fmt.Sprintf("soltype.Accept: EnterType replaced %T with %T under "+
			"SkipChildren=false; a same-kind replacement is required to descend "+
			"(set SkipChildren=true to replace with a different kind)", original, e.Type))
	}
	return repl
}

func (t *TypeVarType) Accept(v TypeVisitor, pol Polarity) Type   { return acceptLeaf(t, v, pol) }
func (t *PrimType) Accept(v TypeVisitor, pol Polarity) Type      { return acceptLeaf(t, v, pol) }
func (t *LitType) Accept(v TypeVisitor, pol Polarity) Type       { return acceptLeaf(t, v, pol) }
func (t *Void) Accept(v TypeVisitor, pol Polarity) Type          { return acceptLeaf(t, v, pol) }
func (t *NullType) Accept(v TypeVisitor, pol Polarity) Type      { return acceptLeaf(t, v, pol) }
func (t *UndefinedType) Accept(v TypeVisitor, pol Polarity) Type { return acceptLeaf(t, v, pol) }
func (t *NeverType) Accept(v TypeVisitor, pol Polarity) Type     { return acceptLeaf(t, v, pol) }
func (t *UnknownType) Accept(v TypeVisitor, pol Polarity) Type   { return acceptLeaf(t, v, pol) }
func (t *ErrorType) Accept(v TypeVisitor, pol Polarity) Type     { return acceptLeaf(t, v, pol) }
func (t *SkolemType) Accept(v TypeVisitor, pol Polarity) Type    { return acceptLeaf(t, v, pol) }

func (t *FuncType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	tparams, tpChanged := acceptTypeParams(cur.TypeParams, v, pol)
	self, selfChanged := acceptSelfParam(cur.SelfParam, v, pol) // receiver contravariant
	params, changed := acceptParams(cur.Params, v, pol)         // params contravariant
	ret := cur.Ret.Accept(v, pol)                               // return covariant
	out := cur
	if tpChanged || selfChanged || changed || ret != cur.Ret {
		// LifetimeParams carry through unchanged here, because Accept never walks a
		// lifetime. A lifetime-aware visitor freshens them in its EnterType, replacing
		// the whole FuncType before this rebuild, so cur already holds the freshened
		// params.
		out = &FuncType{SelfParam: self, Params: params, Ret: ret, Inexact: cur.Inexact, TypeParams: tparams, LifetimeParams: cur.LifetimeParams}
	}
	return v.ExitType(out, pol)
}

// acceptSelfParam walks a method's receiver type contravariantly, like an ordinary
// parameter, and returns the original *FuncParam when its type did not change. A nil
// receiver passes through unchanged, since a static method or plain function has none.
func acceptSelfParam(sp *FuncParam, v TypeVisitor, pol Polarity) (*FuncParam, bool) {
	if sp == nil {
		return nil, false
	}
	pt := sp.Type.Accept(v, pol.Flip())
	if pt == sp.Type {
		return sp, false
	}
	return &FuncParam{Pattern: sp.Pattern, Type: pt, Optional: sp.Optional, Rest: sp.Rest}, true
}

func (t *TupleType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	elems, changed := acceptTypes(cur.Elems, v, pol) // covariant
	out := cur
	if changed {
		out = &TupleType{Elems: elems, Inexact: cur.Inexact}
	}
	return v.ExitType(out, pol)
}

func (t *ObjectType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	elems, changed := acceptObjElems(cur.Elems, v, pol) // covariant
	out := cur
	if changed {
		out = &ObjectType{Elems: elems, Inexact: cur.Inexact}
	}
	return v.ExitType(out, pol)
}

func (t *PromiseType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	inner := cur.Inner.Accept(v, pol) // covariant, no auto-flatten
	out := cur
	if inner != cur.Inner {
		out = &PromiseType{Inner: inner}
	}
	return v.ExitType(out, pol)
}

func (t *RefType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	// The inner is visited ONCE in the current polarity — the read view. When Mut,
	// the inner is also a write view (the contravariant constrain step in C2), but
	// the rewriting transforms visit it a single time and share fresh vars through
	// their own cache, exactly as the spike's extrude treated a Mut inner. The
	// lifetime is not a Type, so Accept never walks it; only the lifetime-aware
	// passes (D4) do.
	//
	// KNOWN GAP (D2): C2's constrain rule makes a Mut borrow's inner INVARIANT (it
	// adds both a read and a write constraint), but this single covariant visit means
	// extrude/freshenAbove wire an out-of-level inner var through only one bound
	// direction. This is inert today — no inference path mints a RefType, so extrude
	// never sees a real Mut borrow — and Accept is shared with coalesce, which WANTS a
	// single covariant visit, so the fix belongs in extrude/freshenAbove (not here)
	// once borrows originate (D2) and the case becomes reachable and testable.
	//
	// The OCCURRENCE-analysis half of this invariance is already handled separately,
	// not here: simplify.go's recordMutWriteView walks a Mut inner in the flipped
	// polarity for the occurrence and co-occurrence visitors, so a var written through
	// a mut field is retained as a type parameter rather than dropped (#737). That is a
	// record-only pass, so it can add the write view without the double visit coalesce
	// must avoid.
	inner := cur.Inner.Accept(v, pol)
	var out Type = cur
	if inner != cur.Inner {
		if ri, ok := inner.(RefInner); ok {
			out = &RefType{Mut: cur.Mut, Lt: cur.Lt, Inner: ri}
		} else {
			// The inner rewrote to a non-borrowable type — e.g. coalescing a borrowed
			// inference variable `mut β` whose β inlines to a union, never, or a
			// primitive. A borrow of a non-RefInner is meaningless: a `mut number` is a
			// JS no-op, exactly the degenerate case the RefInner set excludes. Peel the
			// wrapper and yield the bare inner, mirroring NewRef's collapse of the
			// (false, nil) cell.
			out = inner
		}
	}
	return v.ExitType(out, pol)
}

func (t *UnionType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	types, changed := acceptTypes(cur.Types, v, pol)
	out := cur
	if changed {
		out = &UnionType{Types: types, Inexact: cur.Inexact}
	}
	return v.ExitType(out, pol)
}

func (t *IntersectionType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	types, changed := acceptTypes(cur.Types, v, pol)
	out := cur
	if changed {
		out = &IntersectionType{Types: types}
	}
	return v.ExitType(out, pol)
}

func (t *ClassType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	args, changed := acceptTypes(cur.TypeArgs, v, pol) // type arguments covariant
	out := cur
	if changed {
		// Name and Final are the nominal identity, carried through unchanged.
		// LifetimeArgs and Lt are lifetimes, not Types, so Accept never walks them; a
		// lifetime-aware visitor freshens them in its EnterType, replacing the whole
		// ClassType before this rebuild, so cur already holds the freshened lifetimes.
		out = &ClassType{Name: cur.Name, TypeArgs: args, LifetimeArgs: cur.LifetimeArgs, Lt: cur.Lt, Final: cur.Final, Variant: cur.Variant}
	}
	return v.ExitType(out, pol)
}

func (t *AliasType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	args, changed := acceptTypes(cur.TypeArgs, v, pol) // type arguments covariant
	out := cur
	if changed {
		// Name is the handle's identity, carried through unchanged. An alias is
		// transparent, so its arguments walk covariantly like a class's, and variance
		// is resolved by expansion at subtyping time rather than stored on the handle.
		// LifetimeArgs are lifetimes, not Types, so Accept never walks them; a
		// lifetime-aware visitor freshens them in its EnterType, replacing the whole
		// AliasType before this rebuild, so cur already holds the freshened lifetimes.
		out = &AliasType{Name: cur.Name, TypeArgs: args, LifetimeArgs: cur.LifetimeArgs}
	}
	return v.ExitType(out, pol)
}

// acceptTypes walks each element covariantly, returning (originalSlice, false)
// when nothing changed and (copy-on-write slice, true) otherwise.
func acceptTypes(ts []Type, v TypeVisitor, pol Polarity) ([]Type, bool) {
	out := ts
	changed := false
	for i, el := range ts {
		ne := el.Accept(v, pol)
		if ne != el {
			if !changed {
				out = append([]Type(nil), ts...)
				changed = true
			}
			out[i] = ne
		}
	}
	return out, changed
}

// acceptParams walks each parameter's type CONTRAVARIANTLY (pol flipped),
// copy-on-write like acceptTypes: a changed param gets a fresh *FuncParam (surface
// markers Optional/Rest ride through), unchanged params keep their pointer.
func acceptParams(ps []*FuncParam, v TypeVisitor, pol Polarity) ([]*FuncParam, bool) {
	out := ps
	changed := false
	for i, p := range ps {
		pt := p.Type.Accept(v, pol.Flip())
		if pt != p.Type {
			if !changed {
				out = append([]*FuncParam(nil), ps...)
				changed = true
			}
			out[i] = &FuncParam{Pattern: p.Pattern, Type: pt, Optional: p.Optional, Rest: p.Rest}
		}
	}
	return out, changed
}

// acceptTypeParams rewrites each type parameter's binding variable and its default so a
// rewriting visitor copies both. The main such visitor is freshenAbove. The binding
// variable is visited as a Type and must stay a *TypeVarType. freshenAbove replaces it
// with a fresh variable whose bounds it has already freshened, so the constraint carried
// as the variable's upper bound rides along on the copy. The default is visited
// covariantly and may reference the variable or an earlier parameter, which Accept
// rewrites through the visitor's cache. Copy-on-write mirrors acceptParams. A changed
// parameter gets a fresh *TypeParam and unchanged ones keep their pointer.
func acceptTypeParams(tps []*TypeParam, v TypeVisitor, pol Polarity) ([]*TypeParam, bool) {
	out := tps
	changed := false
	for i, tp := range tps {
		nv := acceptTypeParamVar(tp.Var, v, pol)
		def := tp.Default
		if def != nil {
			def = def.Accept(v, pol)
		}
		if nv != tp.Var || def != tp.Default {
			if !changed {
				out = append([]*TypeParam(nil), tps...)
				changed = true
			}
			out[i] = &TypeParam{Name: tp.Name, Var: nv, Default: def}
		}
	}
	return out, changed
}

// acceptTypeParamVar visits a type parameter's binding variable and requires the result
// to stay a *TypeVarType, since the TypeParams slot can hold only a variable. freshenAbove
// meets this contract: it rewrites a variable to a fresh variable. A visitor that instead
// inlines a variable to a non-variable form, such as the display coalescer, must skip a
// type parameter's binder before rewriting a generic function. Otherwise it trips this
// guard, which fails loudly rather than desyncing the binder from its uses in the params
// and return.
func acceptTypeParamVar(tv *TypeVarType, v TypeVisitor, pol Polarity) *TypeVarType {
	nt := tv.Accept(v, pol)
	nv, ok := nt.(*TypeVarType)
	if !ok {
		panic(fmt.Sprintf("soltype.Accept: a FuncType type parameter rewrote to %T, "+
			"not *TypeVarType; a bound parameter must stay a variable", nt))
	}
	return nv
}

// acceptObjElems walks each member, copy-on-write like acceptParams: a member
// whose type changed gets a fresh element, unchanged members keep their pointer.
// Each kind threads polarity by its variance — a property or getter reads
// covariantly, a setter writes contravariantly, and a method delegates to
// FuncType.Accept, which flips polarity on its own parameters.
func acceptObjElems(es []ObjTypeElem, v TypeVisitor, pol Polarity) ([]ObjTypeElem, bool) {
	out := es
	changed := false
	for i, e := range es {
		ne := AcceptObjElem(e, v, pol)
		if ne != e {
			if !changed {
				out = append([]ObjTypeElem(nil), es...)
				changed = true
			}
			out[i] = ne
		}
	}
	return out, changed
}

// AcceptObjElem rewrites one object member through the visitor, returning the original
// pointer when no child changed and threading polarity by the member's variance, as
// acceptObjElems does for a whole member list. It is exported so a caller projecting a
// single member can reuse this logic without wrapping the member in an ObjectType. It
// panics on an unknown element kind, matching AsProperty.
func AcceptObjElem(e ObjTypeElem, v TypeVisitor, pol Polarity) ObjTypeElem {
	switch e := e.(type) {
	case *PropertyElem:
		pt := e.Type.Accept(v, pol) // covariant read
		if pt == e.Type {
			return e
		}
		return &PropertyElem{Name: e.Name, Type: pt, Optional: e.Optional, Readonly: e.Readonly}
	case *GetterElem:
		self, selfChanged := acceptSelfParam(e.SelfParam, v, pol) // receiver contravariant
		rt := e.Type.Accept(v, pol)                               // covariant read
		if !selfChanged && rt == e.Type {
			return e
		}
		return &GetterElem{Name: e.Name, SelfParam: self, Type: rt}
	case *SetterElem:
		self, selfChanged := acceptSelfParam(e.SelfParam, v, pol) // receiver contravariant
		pt := e.Param.Accept(v, pol.Flip())                       // contravariant write
		if !selfChanged && pt == e.Param {
			return e
		}
		return &SetterElem{Name: e.Name, SelfParam: self, Param: pt}
	case *MethodElem:
		sigs, changed := acceptSignatures(e.Signatures, v, pol)
		if !changed {
			return e
		}
		return &MethodElem{Name: e.Name, Signatures: sigs, Static: e.Static}
	case *ConstructorElem:
		nf, ok := e.Fn.Accept(v, pol).(*FuncType) // params contravariant, via FuncType.Accept
		if !ok {
			panic(fmt.Sprintf("AcceptObjElem: constructor signature rewrote to non-FuncType %T", e.Fn))
		}
		if nf == e.Fn {
			return e
		}
		return &ConstructorElem{Fn: nf}
	}
	panic(fmt.Sprintf("AcceptObjElem: unhandled ObjTypeElem %T", e))
}

// acceptSignatures walks each signature of a method's overload set through
// FuncType.Accept, copy-on-write like acceptObjElems. A signature is always a
// FuncType and FuncType.Accept rewrites it to a FuncType, so a non-FuncType result
// is a visitor-contract violation and panics with a clear message.
func acceptSignatures(sigs []*FuncType, v TypeVisitor, pol Polarity) ([]*FuncType, bool) {
	out := sigs
	changed := false
	for i, sig := range sigs {
		ns := sig.Accept(v, pol)
		if ns == sig {
			continue
		}
		nf, ok := ns.(*FuncType)
		if !ok {
			panic(fmt.Sprintf("acceptSignatures: method signature rewrote to non-FuncType %T", ns))
		}
		if !changed {
			out = append([]*FuncType(nil), sigs...)
			changed = true
		}
		out[i] = nf
	}
	return out, changed
}

// HasTypeVar reports whether t contains any TypeVarType in its structure.
// It drives the shared TypeVisitor so every kind that participates in Accept
// participates here, with no parallel switch to keep in sync as new kinds land.
// The visitor sets found on the first TypeVarType it sees and returns
// SkipChildren so the walk short-circuits.
func HasTypeVar(t Type) bool {
	s := &typeVarSeeker{}
	t.Accept(s, Positive)
	return s.found
}

// typeVarSeeker is the read-only TypeVisitor that backs HasTypeVar. It rewrites
// nothing. EnterType returns SkipChildren once it has seen a TypeVarType so
// later sibling subtrees are not walked.
type typeVarSeeker struct{ found bool }

func (s *typeVarSeeker) EnterType(t Type, _ Polarity) EnterResult {
	if s.found {
		return EnterResult{SkipChildren: true}
	}
	if _, ok := t.(*TypeVarType); ok {
		s.found = true
		return EnterResult{SkipChildren: true}
	}
	return EnterResult{}
}

func (s *typeVarSeeker) ExitType(t Type, _ Polarity) Type { return t }

// HasLifetimeVar reports whether t contains any LifetimeVar in a borrow's
// lifetime slot, the lifetime-sort twin of HasTypeVar. It reuses freeLifetimeVars.
func HasLifetimeVar(t Type) bool {
	return len(freeLifetimeVars(t)) > 0
}
