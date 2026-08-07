package ucs

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// PrintOptions configures how Print renders a term.
type PrintOptions struct {
	// Indent is the string one nesting level adds. Print substitutes two spaces when
	// it is empty.
	Indent string
	// ShowOrigins appends each node's Origin tag, written `[match arm]` or
	// `[synthetic if val]`. It is off by default so a shape snapshot stays readable.
	// A test that asserts provenance turns it on.
	ShowOrigins bool
	// ShowArms appends the surface arm each branch and leaf points back at, written
	// `arm=1:5-1:14` from the arm's span, or `arm=none` when the node carries no
	// back-reference. A test that asserts a merged or flattened split still blames
	// the arm the user typed turns it on.
	//
	// With ShowSpans also on, an arm whose span matches the one ShowSpans wrote
	// renders `arm=same` rather than repeating it. Desugaring gives a node the same
	// surface node for both, so that is the usual rendering, and a printed pair of
	// spans marks the case where normalization synthesized the origin.
	ShowArms bool
	// ShowSpans appends the span a node blames, written `at=2:12-2:17`. A synthetic
	// node has no span of its own, so it renders the one its cause chain reaches with
	// a `~` in place of the `=`, written `at~1:1-1:27`. A node that reaches no span at
	// all renders `at=none`.
	//
	// It renders on the same nodes ShowOrigins tags, which is every core and
	// normalized term. A scrutinee renders neither, so a test that needs a
	// scrutinee's span reads Prov().SourceSpan() rather than the printed form.
	//
	// This is what shows the span of a node that carries no arm back-reference, such
	// as a guard, whose origin points at the condition the user wrote.
	ShowSpans bool
}

// DefaultPrintOptions renders shape alone, with two-space indentation.
func DefaultPrintOptions() PrintOptions {
	return PrintOptions{Indent: "  "}
}

// Print renders a core or normalized term. The output is for snapshot tests and
// debugging. Nothing parses it back, so its shape is free to change with the IR.
func Print(t Term, opts PrintOptions) string {
	if opts.Indent == "" {
		opts.Indent = "  "
	}
	p := &printer{opts: opts}
	p.term(t)
	return p.sb.String()
}

func (n *CoreSplit) String() string    { return Print(n, DefaultPrintOptions()) }
func (n *CoreBranch) String() string   { return Print(n, DefaultPrintOptions()) }
func (n *CoreGuard) String() string    { return Print(n, DefaultPrintOptions()) }
func (n *CoreBind) String() string     { return Print(n, DefaultPrintOptions()) }
func (n *NormSplit) String() string    { return Print(n, DefaultPrintOptions()) }
func (n *NormBranch) String() string   { return Print(n, DefaultPrintOptions()) }
func (n *NormGuard) String() string    { return Print(n, DefaultPrintOptions()) }
func (n *NormBind) String() string     { return Print(n, DefaultPrintOptions()) }
func (n *BodyLeaf) String() string     { return Print(n, DefaultPrintOptions()) }
func (n *EscapeLeaf) String() string   { return Print(n, DefaultPrintOptions()) }
func (n *FallbackLeaf) String() string { return Print(n, DefaultPrintOptions()) }

// printer accumulates the rendered term. indent counts nesting levels, not
// characters. newline expands indent through opts.Indent.
type printer struct {
	sb     strings.Builder
	opts   PrintOptions
	indent int
}

func (p *printer) write(s string) { p.sb.WriteString(s) }

func (p *printer) newline() {
	p.sb.WriteString("\n")
	p.sb.WriteString(strings.Repeat(p.opts.Indent, p.indent))
}

// term dispatches on which IR the node belongs to. The leaf types belong to both, so
// the Core arm claims them. Both arms render a leaf the same way, so which arm wins
// does not change the output.
func (p *printer) term(t Term) {
	switch n := t.(type) {
	case Core:
		p.core(n)
	case Norm:
		p.norm(n)
	case *CoreBranch:
		p.coreBranch(n)
	case *NormBranch:
		p.normBranch(n)
	case *Scrutinee:
		p.write(scrutineeString(n))
	default:
		p.write(nodeKind(t))
	}
}

// branches writes the braced, indented body of a split or a normalized guard. write
// renders the entry at index i. A body with no entries collapses to `{}` so an
// unreachable split does not spend two lines saying nothing.
func (p *printer) branches(count int, write func(i int)) {
	if count == 0 {
		p.write(" {}")
		return
	}
	p.write(" {")
	p.indent++
	for i := range count {
		p.newline()
		write(i)
	}
	p.indent--
	p.newline()
	p.write("}")
}

func (p *printer) core(n Core) {
	switch n := n.(type) {
	case *CoreSplit:
		p.write("split " + scrutineeString(n.Scrutinee) + p.tags(n.Origin))
		p.branches(len(n.Branches), func(i int) { p.coreBranch(n.Branches[i]) })
		if n.Else != nil {
			p.write(" else ")
			p.core(n.Else)
		}
	case *CoreGuard:
		p.write("guard (" + exprString(n.Cond) + ")" + p.tags(n.Origin) + " => ")
		p.core(n.Cont)
	case *CoreBind:
		// coreBinds writes the whole bind clause, so it must run before the
		// continuation it returns is written.
		cont := p.coreBinds(n)
		p.core(cont)
	default:
		p.leaf(n)
	}
}

func (p *printer) coreBranch(b *CoreBranch) {
	p.write("pat " + patString(b.Pattern) + p.armTags(b.Origin, b.Arm) + " => ")
	p.core(b.Cont)
}

// coreBinds renders a run of consecutive binds as one `bind a = …, b = …;` clause
// and returns what follows the run, so a branch that introduces several leaves
// stays on one line.
func (p *printer) coreBinds(n *CoreBind) Core {
	p.write("bind ")
	for i := 0; ; i++ {
		if i > 0 {
			p.write(", ")
		}
		p.write(bindTarget(n.Name, n.Pat) + " = " + scrutineeString(n.Source) + p.tags(n.Origin))
		next, ok := n.Cont.(*CoreBind)
		if !ok {
			break
		}
		n = next
	}
	p.write("; ")
	return n.Cont
}

func (p *printer) norm(n Norm) {
	switch n := n.(type) {
	case *NormSplit:
		p.write("split " + scrutineeString(n.Scrutinee) + p.tags(n.Origin))
		p.branches(len(n.Branches), func(i int) { p.normBranch(n.Branches[i]) })
		p.write(" default ")
		p.norm(n.Default)
	case *NormGuard:
		p.write("guard (" + exprString(n.Cond) + ")" + p.tags(n.Origin))
		p.branches(1, func(int) { p.norm(n.Cont) })
		p.write(" default ")
		p.norm(n.Default)
	case *NormBind:
		// normBinds writes the whole bind clause, so it must run before the
		// continuation it returns is written.
		cont := p.normBinds(n)
		p.norm(cont)
	default:
		p.leaf(n)
	}
}

func (p *printer) normBranch(b *NormBranch) {
	p.write(testString(b.Test) + p.armTags(b.Origin, b.Arm) + " => ")
	p.norm(b.Cont)
}

// normBinds renders a run of consecutive binds as one clause, the same way
// coreBinds does, and returns what follows the run.
func (p *printer) normBinds(n *NormBind) Norm {
	p.write("bind ")
	for i := 0; ; i++ {
		if i > 0 {
			p.write(", ")
		}
		p.write(bindTarget(n.Name, n.Pat) + " = " + scrutineeString(n.Source) + p.tags(n.Origin))
		next, ok := n.Cont.(*NormBind)
		if !ok {
			break
		}
		n = next
	}
	p.write("; ")
	return n.Cont
}

// leaf renders a terminal of either IR. A nil term is a split tail with no covering
// continuation, written `✗`.
func (p *printer) leaf(t Term) {
	switch n := t.(type) {
	case nil:
		p.write("✗")
	case *BodyLeaf:
		p.write("leaf " + bodyString(n.Body) + p.armTags(n.Origin, n.Arm))
	case *EscapeLeaf:
		p.write("escape" + p.armTags(n.Origin, n.Arm))
	case *FallbackLeaf:
		p.write("fallback " + bodyString(n.Body) + p.armTags(n.Origin, n.Arm))
	default:
		p.write(nodeKind(t))
	}
}

// bindTarget renders what a bind introduces. A named bind writes its identifier. A
// bind with no name holds a sub-pattern that is not flattened into a split yet, so it
// writes the pattern: `bind {x, y} = l.start` says the branch has still to match
// `{x, y}` against the projection `l.start`.
func bindTarget(name string, pat ast.Pat) string {
	if name != "" {
		return name
	}
	if pat == nil {
		return "_"
	}
	return patString(pat)
}

// originTag renders a node's provenance, or nothing when the caller did not ask for
// it.
func (p *printer) originTag(o Origin) string {
	if !p.opts.ShowOrigins {
		return ""
	}
	if o.Synthetic {
		return " [synthetic " + o.Kind.String() + "]"
	}
	return " [" + o.Kind.String() + "]"
}

// spanTag renders the span a node blames, or nothing when the caller did not ask for
// it. A node with a surface node of its own renders that node's span after `at=`. A
// synthetic node renders the span its cause chain reaches after `at~`, so a reader
// can tell an inherited span from an owned one.
func (p *printer) spanTag(o Origin) string {
	if !p.opts.ShowSpans {
		return ""
	}
	if span, ok := o.SourceSpan(); ok {
		return " at=" + span.String()
	}
	if span, ok := o.NearestSpan(); ok {
		return " at~" + span.String()
	}
	return " at=none"
}

// armTag renders a branch's or leaf's surface-arm back-reference as the arm's span,
// or nothing when the caller did not ask for it. A node carrying no back-reference
// renders `arm=none`.
//
// An arm whose span matches the one spanTag already wrote renders `arm=same` rather
// than repeating the coordinates. That is the shape straight out of desugaring,
// where a node's origin and its back-reference are the same surface node. The two
// part ways once normalization synthesizes an origin over a merged or flattened
// split, and then both spans print, which is the case a reader is looking for.
func (p *printer) armTag(o Origin, arm Spanned) string {
	if !p.opts.ShowArms {
		return ""
	}
	span, ok := SpanOf(arm)
	if !ok {
		return " arm=none"
	}
	if shown, wrote := p.shownSpan(o); wrote && shown == span {
		return " arm=same"
	}
	return " arm=" + span.String()
}

// shownSpan returns the span spanTag wrote for o, and false when it wrote none.
// NearestSpan yields a node's own span when it has one and the span its cause chain
// reaches otherwise, which is the same order spanTag renders in.
func (p *printer) shownSpan(o Origin) (ast.Span, bool) {
	if !p.opts.ShowSpans {
		return ast.Span{}, false
	}
	return o.NearestSpan()
}

// tags renders the annotations that follow a node with no surface-arm
// back-reference: its origin and the span it blames.
func (p *printer) tags(o Origin) string {
	return p.originTag(o) + p.spanTag(o)
}

// armTags renders the same annotations for a branch or leaf, followed by its
// surface-arm back-reference.
func (p *printer) armTags(o Origin, arm Spanned) string {
	return p.tags(o) + p.armTag(o, arm)
}
