package soltype

// EnterResult controls traversal after EnterType.
//
// A non-nil Type replaces the node: with SkipChildren=false it becomes the basis
// for the structural rebuild (its children are walked), with SkipChildren=true it
// is handed straight to ExitType without descending. A nil Type keeps the node.
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
// the same-kind type assertion the descend path needs does not apply here.
func skipReplace(t Type, e EnterResult) Type {
	if e.Type != nil {
		return e.Type
	}
	return t
}

func (t *TypeVarType) Accept(v TypeVisitor, pol Polarity) Type { return acceptLeaf(t, v, pol) }
func (t *PrimType) Accept(v TypeVisitor, pol Polarity) Type    { return acceptLeaf(t, v, pol) }
func (t *LitType) Accept(v TypeVisitor, pol Polarity) Type     { return acceptLeaf(t, v, pol) }
func (t *Void) Accept(v TypeVisitor, pol Polarity) Type        { return acceptLeaf(t, v, pol) }
func (t *NeverType) Accept(v TypeVisitor, pol Polarity) Type   { return acceptLeaf(t, v, pol) }
func (t *UnknownType) Accept(v TypeVisitor, pol Polarity) Type { return acceptLeaf(t, v, pol) }

func (t *FuncType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := t
	if e.Type != nil {
		// Descending into the replacement's children: it must be the same kind.
		cur = e.Type.(*FuncType)
	}
	params := cur.Params // copy-on-write: alias until a param's type changes
	changed := false
	for i, p := range cur.Params {
		pt := p.Type.Accept(v, pol.Flip()) // params contravariant
		if pt != p.Type {
			if !changed {
				params = append([]*FuncParam(nil), cur.Params...)
				changed = true
			}
			// Surface markers (Optional / Rest) ride through unchanged.
			params[i] = &FuncParam{Pattern: p.Pattern, Type: pt, Optional: p.Optional, Rest: p.Rest}
		}
	}
	ret := cur.Ret.Accept(v, pol) // return covariant
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
	cur := t
	if e.Type != nil {
		cur = e.Type.(*TupleType)
	}
	elems, changed := acceptTypes(cur.Elems, v, pol) // covariant
	out := cur
	if changed {
		out = &TupleType{Elems: elems}
	}
	return v.ExitType(out, pol)
}

func (t *RecordType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := t
	if e.Type != nil {
		cur = e.Type.(*RecordType)
	}
	fields := cur.Fields // copy-on-write
	changed := false
	for i, f := range cur.Fields {
		ft := f.Type.Accept(v, pol) // fields covariant
		if ft != f.Type {
			if !changed {
				fields = append([]*RecordField(nil), cur.Fields...)
				changed = true
			}
			fields[i] = &RecordField{Name: f.Name, Type: ft}
		}
	}
	out := cur
	if changed {
		out = &RecordType{Fields: fields}
	}
	return v.ExitType(out, pol)
}

func (t *PromiseType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := t
	if e.Type != nil {
		cur = e.Type.(*PromiseType)
	}
	inner := cur.Inner.Accept(v, pol) // covariant, no auto-flatten
	out := cur
	if inner != cur.Inner {
		out = &PromiseType{Inner: inner}
	}
	return v.ExitType(out, pol)
}

func (t *UnionType) Accept(v TypeVisitor, pol Polarity) Type {
	e := v.EnterType(t, pol)
	if e.SkipChildren {
		return v.ExitType(skipReplace(t, e), pol)
	}
	cur := t
	if e.Type != nil {
		cur = e.Type.(*UnionType)
	}
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
	cur := t
	if e.Type != nil {
		cur = e.Type.(*IntersectionType)
	}
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
