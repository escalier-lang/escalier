package ucs

import (
	"slices"
	"strconv"
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
	ShowArms bool
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

// String renders the scrutinee's projection path, for example `l.start.x`.
func (s *Scrutinee) String() string { return scrutineeString(s) }

func (t *ObjectTest) String() string    { return testString(t) }
func (t *TupleTest) String() string     { return testString(t) }
func (t *LitTest) String() string       { return testString(t) }
func (t *ClassTest) String() string     { return testString(t) }
func (t *ExtractorTest) String() string { return testString(t) }

// printer accumulates the rendered term. indent counts nesting levels, not
// characters; newline expands it through opts.Indent.
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
		p.write("split " + scrutineeString(n.Scrutinee) + p.originTag(n.Origin))
		p.branches(len(n.Branches), func(i int) { p.coreBranch(n.Branches[i]) })
		if n.Else != nil {
			p.write(" else ")
			p.core(n.Else)
		}
	case *CoreGuard:
		p.write("guard (" + exprString(n.Cond) + ")" + p.originTag(n.Origin) + " => ")
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
	p.write("pat " + patString(b.Pattern) + p.originTag(b.Origin) + p.armTag(b.Arm) + " => ")
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
		p.write(n.Name + " = " + scrutineeString(n.Source) + p.originTag(n.Origin))
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
		p.write("split " + scrutineeString(n.Scrutinee) + p.originTag(n.Origin))
		p.branches(len(n.Branches), func(i int) { p.normBranch(n.Branches[i]) })
		p.write(" default ")
		p.norm(n.Default)
	case *NormGuard:
		p.write("guard (" + exprString(n.Cond) + ")" + p.originTag(n.Origin))
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
	p.write(testString(b.Test) + p.originTag(b.Origin) + p.armTag(b.Arm) + " => ")
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
		p.write(n.Name + " = " + scrutineeString(n.Source) + p.originTag(n.Origin))
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
		p.write("leaf " + bodyString(n.Body) + p.originTag(n.Origin) + p.armTag(n.Arm))
	case *EscapeLeaf:
		p.write("escape" + p.originTag(n.Origin) + p.armTag(n.Arm))
	case *FallbackLeaf:
		p.write("fallback " + bodyString(n.Body) + p.originTag(n.Origin) + p.armTag(n.Arm))
	default:
		p.write(nodeKind(t))
	}
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

// armTag renders a branch's or leaf's surface-arm back-reference as the arm's span,
// or nothing when the caller did not ask for it.
func (p *printer) armTag(arm Spanned) string {
	if !p.opts.ShowArms {
		return ""
	}
	s, ok := SpanOf(arm)
	if !ok {
		return " arm=none"
	}
	return " arm=" + s.String()
}

// scrutineeString renders a scrutinee's projection path. A root renders its target
// expression and every projection appends its step, so `l.start.x` reads as the
// chain of steps that reaches it.
func scrutineeString(s *Scrutinee) string {
	if s == nil {
		return "<nil>"
	}
	if s.IsRoot() {
		return exprString(s.Target)
	}
	base := scrutineeString(s.Parent)
	switch step := s.Step.(type) {
	case FieldStep:
		return base + "." + step.Name
	case IndexStep:
		return base + "." + strconv.Itoa(step.Index)
	case ResultStep:
		// The `#` keeps an extractor result apart from the tuple element `r.0`. The
		// solver resolves the two through different lookups, so a snapshot must not
		// let one stand in for the other.
		return base + ".#" + strconv.Itoa(step.Index)
	case SuffixStep:
		return base + "[" + strconv.Itoa(step.From) + "..]"
	case RemainderStep:
		keys := step.Exclude.ToSlice()
		slices.Sort(keys)
		return base + " \\ {" + strings.Join(keys, ", ") + "}"
	default:
		return base + "." + nodeKind(s.Step)
	}
}

// testString renders a branch's tag test. A structural test relaxed by a rest
// pattern renders a trailing `...`, matching how an inexact type prints.
func testString(t Test) string {
	switch t := t.(type) {
	case nil:
		return "<nil>"
	case *ObjectTest:
		parts := make([]string, 0, len(t.Keys)+1)
		for _, key := range t.Keys {
			// A trailing `?` marks a key the test tolerates being absent, matching how
			// an optional property prints in a type.
			if key.Optional {
				parts = append(parts, key.Name+"?")
			} else {
				parts = append(parts, key.Name)
			}
		}
		if t.Exactness == InexactPrefix {
			parts = append(parts, "...")
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *TupleTest:
		parts := make([]string, 0, t.Len+1)
		for range t.Len {
			parts = append(parts, "_")
		}
		if t.Exactness == InexactPrefix {
			parts = append(parts, "...")
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *LitTest:
		return litString(t.Lit)
	case *ClassTest:
		return ast.QualIdentToString(t.Name)
	case *ExtractorTest:
		args := make([]string, t.Arity)
		for i := range args {
			args[i] = "_"
		}
		return ast.QualIdentToString(t.Name) + "(" + strings.Join(args, ", ") + ")"
	default:
		return nodeKind(t)
	}
}
