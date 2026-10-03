package ast

import "github.com/escalier-lang/escalier/internal/set"

// FreeTypeRefs returns the `Name` and `Name<…>` references a type annotation makes that no
// binder written inside the annotation declares, in traversal order so a caller reporting
// one reports the leftmost.
//
// A binder covers the region the annotation that writes it spans. A `fn <U>(…)` quantifier
// covers its function annotation, a mapped key covers the object annotation holding it, and
// an `infer U` covers its conditional's Extends and Then operands.
//
// Both checkers read this to tell a reference that reaches an enclosing declaration's type
// parameter from one the annotation binds itself.
func FreeTypeRefs(ta TypeAnn) []*TypeRefTypeAnn {
	var scan freeTypeRefScan
	ta.Accept(&scan)
	return scan.free
}

// freeTypeRefScan is the visitor behind FreeTypeRefs. It tracks one name set per region the
// walk is inside, innermost last.
type freeTypeRefScan struct {
	DefaultVisitor
	scopes []set.Set[string]
	free   []*TypeRefTypeAnn
}

func (v *freeTypeRefScan) EnterTypeAnn(t TypeAnn) bool {
	switch n := t.(type) {
	case *TypeRefTypeAnn:
		if !v.shadowed(QualIdentToString(n.Name)) {
			v.free = append(v.free, n)
		}
	case *FuncTypeAnn:
		names := set.NewSet[string]()
		for _, tp := range n.TypeParams {
			names.Add(tp.Name)
		}
		v.push(names)
	case *ObjectTypeAnn:
		// A mapped type is an object element rather than a type annotation of its own, so
		// the walk reaches its key constraint, name, and value without entering the mapped
		// node. Read the key binder off the element here and scope it to the object
		// annotation.
		names := set.NewSet[string]()
		for _, elem := range n.Elems {
			if mapped, ok := elem.(*MappedTypeAnn); ok {
				names.Add(mapped.TypeParam.Name)
			}
		}
		v.push(names)
	case *CondTypeAnn:
		// The four operands are walked here rather than by the shared descent, since an
		// `infer U` clause covers only two of them. Check reads no capture, and a capture
		// named again in Else is a free reference.
		n.Check.Accept(v)
		v.push(set.FromSlice(InferAnnNames(n.Extends)))
		n.Extends.Accept(v)
		n.Then.Accept(v)
		v.pop()
		n.Else.Accept(v)
		return false
	}
	return true
}

// ExitTypeAnn closes the region opened on entering a function or object annotation. A
// conditional pops its own region inside EnterTypeAnn, since it drives its operands itself.
func (v *freeTypeRefScan) ExitTypeAnn(t TypeAnn) {
	switch t.(type) {
	case *FuncTypeAnn, *ObjectTypeAnn:
		v.pop()
	}
}

func (v *freeTypeRefScan) push(names set.Set[string]) {
	v.scopes = append(v.scopes, names)
}

func (v *freeTypeRefScan) pop() {
	v.scopes = v.scopes[:len(v.scopes)-1]
}

// shadowed reports whether any region the walk is inside declares name.
func (v *freeTypeRefScan) shadowed(name string) bool {
	for _, names := range v.scopes {
		if names.Contains(name) {
			return true
		}
	}
	return false
}

// InferAnnNames returns each `infer U` name the annotation writes, one entry per distinct
// name so a name written twice binds once. A caller resolving a conditional declares these
// so the clause that writes one and the Then branch's references to it share a declaration.
func InferAnnNames(ta TypeAnn) []string {
	f := &inferAnnFinder{seen: set.NewSet[string]()}
	ta.Accept(f)
	return f.names
}

// inferAnnFinder is the visitor behind InferAnnNames.
type inferAnnFinder struct {
	DefaultVisitor
	seen  set.Set[string]
	names []string
}

func (f *inferAnnFinder) EnterTypeAnn(ta TypeAnn) bool {
	if it, ok := ta.(*InferTypeAnn); ok && !f.seen.Contains(it.Name) {
		f.seen.Add(it.Name)
		f.names = append(f.names, it.Name)
	}
	return true
}
