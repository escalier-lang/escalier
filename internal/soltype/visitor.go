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

func (t *TypeVarType) Accept(v TypeVisitor, pol Polarity) Type { return acceptLeaf(t, v, pol) }
func (t *PrimType) Accept(v TypeVisitor, pol Polarity) Type    { return acceptLeaf(t, v, pol) }
func (t *LitType) Accept(v TypeVisitor, pol Polarity) Type     { return acceptLeaf(t, v, pol) }
func (t *Void) Accept(v TypeVisitor, pol Polarity) Type        { return acceptLeaf(t, v, pol) }
func (t *NeverType) Accept(v TypeVisitor, pol Polarity) Type   { return acceptLeaf(t, v, pol) }
func (t *UnknownType) Accept(v TypeVisitor, pol Polarity) Type { return acceptLeaf(t, v, pol) }
func (t *ErrorType) Accept(v TypeVisitor, pol Polarity) Type   { return acceptLeaf(t, v, pol) }

func (t *FuncType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := descendReplacement(t, e)
	params, changed := acceptParams(cur.Params, v, pol) // params contravariant
	ret := cur.Ret.Accept(v, pol)                       // return covariant
	out := cur
	if changed || ret != cur.Ret {
		out = &FuncType{Params: params, Ret: ret, Inexact: cur.Inexact}
	}
	return v.ExitType(out, pol)
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
		out = &UnionType{Types: types}
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

// acceptObjElems walks each property's type covariantly, copy-on-write like
// acceptParams. M4's elements are all PropertyElem; later member kinds add their
// own variance treatment here.
func acceptObjElems(es []ObjTypeElem, v TypeVisitor, pol Polarity) ([]ObjTypeElem, bool) {
	out := es
	changed := false
	for i, e := range es {
		p := AsProperty(e)
		pt := p.Type.Accept(v, pol)
		if pt != p.Type {
			if !changed {
				out = append([]ObjTypeElem(nil), es...)
				changed = true
			}
			out[i] = &PropertyElem{Name: p.Name, Type: pt, Optional: p.Optional}
		}
	}
	return out, changed
}
