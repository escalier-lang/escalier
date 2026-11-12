package solver

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// returnLifetimes is the elision in force for a lifetime that a signature's return type
// leaves out, while the return type's own level resolves. A function type inside the return
// type is a signature of its own, so its positions are outside this elision.
type returnLifetimes struct {
	// inputs are the distinct input lifetimes, the ones the receiver and the parameters hold
	// at the signature's own level.
	inputs []soltype.Lifetime
	// borrow is the lifetime a returned borrow written with no lifetime takes: the borrowed
	// receiver's lifetime, or else the one input lifetime. It is nil when neither picks one.
	borrow soltype.Lifetime
	// owners names each parameter whose type holds an input lifetime, for the error a
	// returned borrow reports when borrow is nil.
	owners []string
	// suggestion is the signature to suggest in that error, or the empty string when there
	// is none to suggest.
	suggestion string
	// reported is set once the error is reported, so a return type with several borrows
	// reports it once.
	reported bool
}

// newReturnLifetimes returns the elision for the return type of a signature whose receiver
// is receiver, nil for none, and whose parameters are params, written as recv and sig write
// them. head is how a suggested signature starts, such as `fn pick` for a function, `peek`
// for a method, or the empty string for `fn` alone. It returns nil when the signature has no
// input lifetime.
func newReturnLifetimes(receiver *soltype.FuncParam, params []*soltype.FuncParam, recv *ast.MethodReceiver, sig *ast.FuncSig, head string) *returnLifetimes {
	r := &returnLifetimes{}
	seen := map[soltype.Lifetime]bool{}
	// collect records the input lifetimes t holds and reports whether it holds one.
	collect := func(t soltype.Type) bool {
		c := &inputLifetimeCollector{}
		t.Accept(c, soltype.Negative)
		holds := false
		for _, lt := range c.lts {
			// 'static outlives every lifetime, so a returned borrow written with no lifetime
			// is never given it, and it does not count against the one-input rule.
			if _, static := lt.(*soltype.StaticLifetime); static {
				continue
			}
			holds = true
			if !seen[lt] {
				seen[lt] = true
				r.inputs = append(r.inputs, lt)
			}
		}
		return holds
	}
	if receiver != nil {
		collect(receiver.Type)
		if ref, ok := receiver.Type.(*soltype.RefType); ok {
			r.borrow = ref.Lt
		}
	}
	for i, p := range params {
		if collect(p.Type) && i < len(sig.Params) {
			written, _ := printer.Print(sig.Params[i].Pattern, printer.DefaultOptions())
			r.owners = append(r.owners, written)
		}
	}
	// With no input lifetime, a returned borrow keeps a fresh lifetime that its uses decide,
	// since L1 lets a function return a borrow of its own local, as
	// `fn make() -> &{x: number} { val p = {x: 1} return &p }` does.
	if len(r.inputs) == 0 {
		return nil
	}
	if r.borrow == nil && len(r.inputs) == 1 {
		r.borrow = r.inputs[0]
	}
	if r.borrow == nil && len(r.owners) > 1 {
		r.suggestion = suggestReturnLifetime(recv, sig, head)
	}
	return r
}

// returnBorrowLifetime returns the lifetime a borrow written with no lifetime takes at the
// return type's own level. When elision picks none, it reports AmbiguousReturnLifetimeError
// against ann once and returns a fresh lifetime at lvl.
func (c *checker) returnBorrowLifetime(ann ast.Node, lvl int) soltype.Lifetime {
	r := c.retLts
	if r.borrow != nil {
		return r.borrow
	}
	if !r.reported {
		r.reported = true
		c.report(&AmbiguousReturnLifetimeError{Owners: r.owners, Suggestion: r.suggestion, Node: ann})
	}
	return c.ctx.freshLifetime(lvl)
}

// signatureHead returns how a signature suggested for node starts: `fn name` for a function
// declaration, the member's name for a method, and the empty string otherwise.
func signatureHead(node ast.Node, memberName string) string {
	if d, ok := node.(*ast.FuncDecl); ok {
		return "fn " + d.Name.Name
	}
	if memberName != "" {
		return memberName
	}
	return ""
}

// tieToInputs makes each input lifetime outlive lt, the lifetime of a function type the
// return type writes with no lifetime, so the returned function may hold any of the inputs.
// An input the signature leaves unnamed is made equal to lt, which a caller cannot tell
// apart from outliving it, since a borrow's lifetime may shrink at a call. The signature
// then renders one lifetime shared by those inputs and the result, as
// `fn <'a>(a: &'a Flat, b: &'a Flat) -> 'a fn () -> number`. A named input keeps its own
// lifetime and outlives lt, and 'static outlives every lifetime already.
func (c *checker) tieToInputs(lt soltype.Lifetime) {
	named := map[soltype.Lifetime]bool{}
	for _, v := range c.namedLifetimes {
		named[v] = true
	}
	for _, in := range c.retLts.inputs {
		if _, static := in.(*soltype.StaticLifetime); static {
			continue
		}
		c.ctx.constrainLt(in, lt)
		if !named[in] {
			c.ctx.constrainLt(lt, in)
		}
	}
}

// resolveReturnAnn resolves ann, a signature's return type, under the elision r gives a
// lifetime the return type leaves out.
func (c *checker) resolveReturnAnn(scope *Scope, ann ast.TypeAnn, lvl int, r *returnLifetimes) (soltype.Type, bool) {
	saved := c.retLts
	c.retLts = r
	defer func() { c.retLts = saved }()
	return c.resolveTypeAnn(scope, ann, lvl)
}

// suggestReturnLifetime renders sig with a lifetime `'x` added to its binder list, to the
// first parameter whose written type is a borrow with no lifetime, and to the return type,
// which has to be such a borrow too. recv is a method's receiver, nil for none, which leads
// the parameter list. head starts the rendering, as `fn pick` or `peek`, and `fn ` stands
// in when it is empty. It returns the empty string when sig has no such parameter or return
// type.
func suggestReturnLifetime(recv *ast.MethodReceiver, sig *ast.FuncSig, head string) string {
	ret, ok := sig.Return.(*ast.RefTypeAnn)
	if !ok || ret.Lifetime != nil {
		return ""
	}
	render := func(n ast.Node) string {
		written, _ := printer.Print(n, printer.CompactOptions())
		return written
	}
	withX := func(ref *ast.RefTypeAnn) string {
		copied := *ref
		copied.Lifetime = ast.NewLifetimeAnn("x", ast.Span{})
		return render(&copied)
	}
	binders := []string{"'x"}
	for _, lp := range sig.LifetimeParams {
		binders = append(binders, "'"+lp.Name)
	}
	for _, tp := range sig.TypeParams {
		binders = append(binders, tp.Name)
	}
	params := make([]string, len(sig.Params))
	added := false
	for i, p := range sig.Params {
		name := render(p.Pattern)
		if p.Optional {
			name += "?"
		}
		switch ref, isRef := p.TypeAnn.(*ast.RefTypeAnn); {
		case isRef && ref.Lifetime == nil && !added:
			params[i] = name + ": " + withX(ref)
			added = true
		case p.TypeAnn != nil:
			params[i] = name + ": " + render(p.TypeAnn)
		default:
			params[i] = name
		}
	}
	if !added {
		return ""
	}
	// A borrowed receiver gives a returned borrow its lifetime, so only a consuming one
	// reaches here.
	if recv != nil {
		self := "self"
		if recv.Mut {
			self = "mut self"
		}
		params = append([]string{self}, params...)
	}
	if head == "" {
		head = "fn "
	}
	return head + "<" + strings.Join(binders, ", ") + ">(" + strings.Join(params, ", ") + ") -> " + withX(ret)
}

// inputLifetimeCollector gathers, in order, the lifetimes a type holds outside any function
// type's parameters and return: a borrow's lifetime, a function type's own lifetime, and a
// class's or alias's lifetime arguments.
type inputLifetimeCollector struct {
	lts []soltype.Lifetime
	// depth counts the function types around the type being visited.
	depth int
}

func (c *inputLifetimeCollector) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.RefType:
		c.add(t.Lt)
	case *soltype.FuncType:
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

func (c *inputLifetimeCollector) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	if _, ok := t.(*soltype.FuncType); ok {
		c.depth--
	}
	return t
}

// add records lt when it sits at the signature's own level. A nil lifetime holds no borrow.
func (c *inputLifetimeCollector) add(lt soltype.Lifetime) {
	if lt == nil || c.depth > 0 {
		return
	}
	if _, anon := lt.(*soltype.AnonLifetime); anon {
		return
	}
	c.lts = append(c.lts, lt)
}

// AmbiguousReturnLifetimeError reports a borrow that a return type writes with no lifetime
// when elision picks none: the signature has no borrowed receiver, and its parameters hold
// no lifetime or more than one. Node is the borrow, which carries the blame span.
type AmbiguousReturnLifetimeError struct {
	// Owners names each parameter whose type holds an input lifetime.
	Owners []string
	// Suggestion is a signature that writes the lifetime, or the empty string for none.
	Suggestion string
	Node       ast.Node
}

func (*AmbiguousReturnLifetimeError) isSolverError()        {}
func (e *AmbiguousReturnLifetimeError) Span() ast.Span      { return e.Node.Span() }
func (e *AmbiguousReturnLifetimeError) Related() []ast.Span { return nil }
func (e *AmbiguousReturnLifetimeError) Message() string {
	var msg string
	switch len(e.Owners) {
	case 0, 1:
		msg = "the return type borrows, and the signature's inputs hold several lifetimes, " +
			"so elision cannot tell which it takes; write a lifetime"
	default:
		quoted := make([]string, len(e.Owners))
		for i, o := range e.Owners {
			quoted[i] = "'" + o + "'"
		}
		msg = "the return type borrows from " + strings.Join(quoted[:len(quoted)-1], ", ") + " or " +
			quoted[len(quoted)-1] + ", and elision cannot tell which; write a lifetime"
	}
	if e.Suggestion != "" {
		msg += ", as in `" + e.Suggestion + "`"
	}
	return msg
}
