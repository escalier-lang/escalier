package solver

import (
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// writtenLifetimes are the lifetimes the written parts of a declaration's signature hold,
// with the outlives relations among them as the resolved signature left them, before the
// body is walked. checkWrittenLifetimes compares them with the relations the body adds.
type writtenLifetimes struct {
	// order lists each lifetime in the order the signature first writes it.
	order []*soltype.LifetimeVar
	// holders maps each lifetime to the parts of the signature that write it.
	holders map[*soltype.LifetimeVar][]ltHolder
	// names maps each lifetime the signature names in its `<…>` list to that name.
	names map[*soltype.LifetimeVar]string
	// exposed holds the lifetimes some input writes inside a borrow's referent, as 'b in
	// `out: &mut {r: &'b T}`. A caller holds the other end of that borrow and sees what the
	// body stores there. A lifetime an owned input holds only outside any borrow, as the
	// `&mut`'s own lifetime in `mut p: {peer: &mut T}`, belongs to a value the function drops,
	// which may shrink its lifetimes, so a store into it is not one a caller sees.
	exposed set.Set[*soltype.LifetimeVar]
	// funcLts holds the lifetimes of the function types the signature writes, as `g` in
	// `g: fn () -> undefined`. A body that keeps such a function past the call is reported by
	// CallOnlyFuncKeptError, so these are not checked as a borrow that flows elsewhere.
	funcLts set.Set[*soltype.LifetimeVar]
	// bounds holds, for each lifetime the signature's lifetimes reach through their bounds,
	// how many upper bounds it had once the signature was resolved. A lifetime the body
	// mints is missing, so it adds no relation to the signature's own.
	bounds map[*soltype.LifetimeVar]int
}

// ltHolder is one part of a signature that writes a lifetime.
type ltHolder struct {
	// input names an input that holds the lifetime, quoted as `'b'`, or "the receiver". It
	// is empty for an output.
	input string
	// output names an output that holds the lifetime, "the return type" or "the thrown
	// type". It is empty for an input.
	output string
	// span is the part's written type, which an error about the part blames.
	span ast.Span
}

// snapshotWrittenLifetimes collects the lifetimes the written parts of sig hold, given the
// resolved receiver, parameters, return type and throws type, and records the relations
// among them. ret and throws are nil when sig leaves them out. It runs once the signature
// is resolved and before the body is walked. Lifetimes c.namedLifetimes binds that belong
// to an enclosing class or function, as classLts and outerLts give them, are left out.
func (c *checker) snapshotWrittenLifetimes(
	sig ast.FuncSig,
	recv *ast.MethodReceiver,
	self *soltype.FuncParam,
	params []*soltype.FuncParam,
	ret, throws soltype.Type,
	outer set.Set[*soltype.LifetimeVar],
) *writtenLifetimes {
	w := &writtenLifetimes{
		holders: map[*soltype.LifetimeVar][]ltHolder{},
		names:   map[*soltype.LifetimeVar]string{},
		funcLts: set.NewSet[*soltype.LifetimeVar](),
		exposed: set.NewSet[*soltype.LifetimeVar](),
		bounds:  map[*soltype.LifetimeVar]int{},
	}
	add := func(t soltype.Type, h ltHolder) {
		col := &writtenLifetimeCollector{funcLts: w.funcLts, exposed: w.exposed}
		t.Accept(col, soltype.Positive)
		for _, lv := range col.lts {
			if outer.Contains(lv) {
				continue
			}
			if _, seen := w.holders[lv]; !seen {
				w.order = append(w.order, lv)
			}
			w.holders[lv] = append(w.holders[lv], h)
		}
	}
	if self != nil && recv != nil {
		add(self.Type, ltHolder{input: "the receiver", span: recv.Span_})
	}
	for i, p := range sig.Params {
		if p.TypeAnn == nil || i >= len(params) {
			continue
		}
		name, _ := printer.Print(p.Pattern, printer.DefaultOptions())
		// The pattern `mut p` binds the name p.
		name = strings.TrimPrefix(name, "mut ")
		add(params[i].Type, ltHolder{input: "'" + name + "'", span: p.TypeAnn.Span()})
	}
	if sig.Return != nil && ret != nil {
		add(ret, ltHolder{output: "the return type", span: sig.Return.Span()})
	}
	if sig.Throws != nil && throws != nil {
		add(throws, ltHolder{output: "the thrown type", span: sig.Throws.Span()})
	}
	for _, bound := range c.signatureLifetimeNames(sig.LifetimeParams) {
		w.names[bound.v] = bound.param.Name
	}
	// Record the upper bounds of every lifetime the signature's lifetimes reach, so the
	// relations the resolved signature implies can be told from those the body adds.
	stack := append([]*soltype.LifetimeVar{}, w.order...)
	for len(stack) > 0 {
		lv := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := w.bounds[lv]; seen {
			continue
		}
		w.bounds[lv] = len(lv.UpperBounds)
		for _, ub := range lv.UpperBounds {
			if next, ok := ub.(*soltype.LifetimeVar); ok {
				stack = append(stack, next)
			}
		}
	}
	return w
}

// checkWrittenLifetimes reports each outlives relation the body requires between two
// lifetimes of the written signature w describes that the resolved signature does not
// imply. A relation between two lifetimes the signature names is left to
// checkSignatureImpliesBodyLifetimes, and a lifetime the body forces to 'static to the
// checks that report a borrow kept for 'static.
//
// `fn pick<'x>(a: &'x Flat, b: &Flat) -> &'x Flat { return b }` makes b's lifetime outlive
// 'x, which the signature does not say, and reports that 'b' flows into the return type.
func (c *checker) checkWrittenLifetimes(w *writtenLifetimes) {
	if w == nil {
		return
	}
	// A store into a `&mut` slot makes two lifetimes equal, which reads as a relation in each
	// direction, so a pair is reported once whichever way round it is found.
	type pair struct{ a, b *soltype.LifetimeVar }
	reported := set.NewSet[pair]()
	for _, sub := range w.order {
		if w.funcLts.Contains(sub) || reachesStatic(sub, nil) {
			continue
		}
		for _, super := range w.order {
			if sub == super || reported.Contains(pair{super, sub}) {
				continue
			}
			_, subNamed := w.names[sub]
			_, superNamed := w.names[super]
			if subNamed && superNamed {
				continue
			}
			if !outlivesThrough(sub, super, nil) || outlivesThrough(sub, super, w.bounds) {
				continue
			}
			if err := w.widenedError(sub, super); err != nil {
				c.report(err)
				reported.Add(pair{sub, super})
				break
			}
		}
	}
}

// widenedError returns the error that reports the body making sub outlive super, or nil
// when there is no part to name: sub is held by no input of the signature, or super is an
// output's lifetime that borrows from no input.
func (w *writtenLifetimes) widenedError(sub, super *soltype.LifetimeVar) *WrittenLifetimeWidenedError {
	var from string
	for _, h := range w.holders[sub] {
		if h.input != "" {
			from = h.input
			break
		}
	}
	if from == "" {
		return nil
	}
	for _, h := range w.holders[super] {
		if h.output == "" {
			continue
		}
		// An output borrowing from no input has a lifetime elision did not pick, which
		// AmbiguousReturnLifetimeError has already reported.
		sources := w.sources(super)
		if sources == "" {
			return nil
		}
		return &WrittenLifetimeWidenedError{From: from, Into: h.output, BorrowsFrom: sources, At: h.span}
	}
	if !w.exposed.Contains(super) {
		return nil
	}
	for _, h := range w.holders[super] {
		if h.input != "" && h.input != from {
			return &WrittenLifetimeWidenedError{From: from, Into: h.input, At: w.holders[sub][0].span}
		}
	}
	return nil
}

// sources names what the output lifetime lt borrows from as the signature writes it: its
// name when the signature names it, and otherwise each input that holds it or that the
// resolved signature makes outlive it.
func (w *writtenLifetimes) sources(lt *soltype.LifetimeVar) string {
	if name, ok := w.names[lt]; ok {
		return "'" + name
	}
	var inputs []string
	seen := set.NewSet[string]()
	addInputs := func(v *soltype.LifetimeVar) {
		for _, h := range w.holders[v] {
			if h.input != "" && !seen.Contains(h.input) {
				seen.Add(h.input)
				inputs = append(inputs, h.input)
			}
		}
	}
	addInputs(lt)
	for _, v := range w.order {
		if v != lt && outlivesThrough(v, lt, w.bounds) {
			addInputs(v)
		}
	}
	switch len(inputs) {
	case 0:
		return ""
	case 1:
		return inputs[0]
	}
	return strings.Join(inputs[:len(inputs)-1], ", ") + " and " + inputs[len(inputs)-1]
}

// outlivesThrough reports whether sub outlives super by a chain of upper bounds. When
// limit is not nil it reads only the first limit[v] upper bounds of each lifetime v, and
// none of a lifetime limit leaves out, so it sees the relations as limit recorded them.
func outlivesThrough(sub, super *soltype.LifetimeVar, limit map[*soltype.LifetimeVar]int) bool {
	seen := set.NewSet[*soltype.LifetimeVar]()
	stack := []*soltype.LifetimeVar{sub}
	for len(stack) > 0 {
		lv := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen.Contains(lv) {
			continue
		}
		seen.Add(lv)
		ubs := lv.UpperBounds
		if limit != nil {
			n, ok := limit[lv]
			if !ok {
				continue
			}
			ubs = ubs[:n]
		}
		for _, ub := range ubs {
			next, ok := ub.(*soltype.LifetimeVar)
			if !ok {
				continue
			}
			if next == super {
				return true
			}
			stack = append(stack, next)
		}
	}
	return false
}

// reachesStatic reports whether lt outlives 'static by a chain of upper bounds, which a
// body forces when it keeps a borrow for the whole program.
func reachesStatic(lt *soltype.LifetimeVar, limit map[*soltype.LifetimeVar]int) bool {
	seen := set.NewSet[*soltype.LifetimeVar]()
	stack := []*soltype.LifetimeVar{lt}
	for len(stack) > 0 {
		lv := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen.Contains(lv) {
			continue
		}
		seen.Add(lv)
		for _, ub := range lv.UpperBounds {
			switch ub := ub.(type) {
			case *soltype.StaticLifetime:
				return true
			case *soltype.LifetimeVar:
				stack = append(stack, ub)
			}
		}
	}
	return false
}

// writtenLifetimeCollector gathers, in order, the lifetime variables a written type holds
// outside any function type's parameters and return: a borrow's lifetime, a function type's
// own lifetime, and a class's or alias's lifetime arguments. A function type written in the
// signature is a signature of its own, whose inner lifetimes this signature does not fix.
type writtenLifetimeCollector struct {
	lts []*soltype.LifetimeVar
	// funcLts receives the lifetime of each function type at the signature's own level.
	funcLts set.Set[*soltype.LifetimeVar]
	// exposed receives each lifetime written inside a borrow's referent.
	exposed set.Set[*soltype.LifetimeVar]
	// refs holds, for each borrow around the type being visited, whether it is one.
	refs []bool
	// depth counts the function types around the type being visited.
	depth int
}

func (c *writtenLifetimeCollector) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	_, isRef := t.(*soltype.RefType)
	c.refs = append(c.refs, isRef)
	switch t := t.(type) {
	case *soltype.RefType:
		c.add(t.Lt)
	case *soltype.FuncType:
		if lv, ok := t.Lt.(*soltype.LifetimeVar); ok && c.depth == 0 {
			c.funcLts.Add(lv)
		}
		c.add(t.Lt)
		c.depth++
	case *soltype.ClassType:
		c.add(t.Lt)
		for _, a := range t.LifetimeArgs {
			c.add(a)
		}
	case *soltype.AliasType:
		for _, a := range t.LifetimeArgs {
			c.add(a)
		}
	}
	return soltype.EnterResult{}
}

func (c *writtenLifetimeCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	if _, ok := t.(*soltype.FuncType); ok {
		c.depth--
	}
	c.refs = c.refs[:len(c.refs)-1]
	return t
}

// add records lt when it is a variable at the signature's own level.
func (c *writtenLifetimeCollector) add(lt soltype.Lifetime) {
	if c.depth > 0 {
		return
	}
	lv, ok := lt.(*soltype.LifetimeVar)
	if !ok {
		return
	}
	c.lts = append(c.lts, lv)
	// The type being visited is the last entry of refs, so a borrow further out puts lv
	// inside a borrow's referent. A borrow's own lifetime is not inside its own referent.
	if slices.Contains(c.refs[:len(c.refs)-1], true) {
		c.exposed.Add(lv)
	}
}

// WrittenLifetimeWidenedError reports a body that makes a borrow an input of a written
// signature holds outlive another lifetime of the signature, which the signature does not
// say. From names the input, quoted as `'b'`, or "the receiver". Into names where the borrow
// flows: "the return type", "the thrown type", or another input. BorrowsFrom names what an
// output borrows from as the signature writes it, and is empty when Into is an input. At is
// the span of the part of the signature the error blames.
type WrittenLifetimeWidenedError struct {
	From        string
	Into        string
	BorrowsFrom string
	At          ast.Span
}

func (*WrittenLifetimeWidenedError) isSolverError()        {}
func (e *WrittenLifetimeWidenedError) Span() ast.Span      { return e.At }
func (e *WrittenLifetimeWidenedError) Related() []ast.Span { return nil }
func (e *WrittenLifetimeWidenedError) Message() string {
	if e.BorrowsFrom == "" {
		return e.From + " flows into " + e.Into + ", but the signature does not relate their lifetimes; name a lifetime on each and add a bound"
	}
	return e.From + " flows into " + e.Into + ", but " + e.Into + " borrows only from " + e.BorrowsFrom
}

// enclosingLifetimeVars returns the variables of the lifetimes an enclosing class or
// function binds, which a signature writes without fixing them itself.
func (c *checker) enclosingLifetimeVars() set.Set[*soltype.LifetimeVar] {
	vars := set.NewSet[*soltype.LifetimeVar]()
	for _, scope := range []map[string]*soltype.LifetimeVar{c.declLifetimes, c.enclosingLifetimes} {
		for _, lv := range scope {
			vars.Add(lv)
		}
	}
	return vars
}
