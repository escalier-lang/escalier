package ast

import (
	"slices"
	"sort"
)

// AttachComments assigns every comment to one node in root and fills that
// node's leading, trailing, or dangling slot. It returns the comments no node
// could take, in source order.
//
// Run it after parsing rather than during it. A parser backtracks by restoring
// a saved lexer state, so it reaches the same region more than once, and a pass
// that attached while parsing would attach a comment once per attempt.
//
// Each comment goes to the first of these that applies. The first three
// consider only a node inside the innermost node the comment sits in, so a
// comment written inside a block stays in that block:
//
//  1. A node starting on the line the comment ends on takes it as leading.
//     This is the `/* n */ 3` case, where the comment introduces what follows
//     it on the line.
//  2. Otherwise a node ending on the line the comment starts on takes it as
//     trailing. This is the `val x = 3 // three` case.
//  3. Otherwise the next node in the file takes it as leading. This is a
//     comment written on its own line above a declaration.
//  4. Otherwise the innermost node enclosing the comment takes it as dangling.
//     A comment alone in an empty block has no sibling to bind to and lands
//     here.
//  5. Otherwise root takes it as dangling, when root is itself a node. A
//     comment above the first statement of a file or below the last is outside
//     every node in it and lands here.
//  6. Otherwise the comment is unattached and comes back in the result.
//
// Checking rule 1 before rule 2 is what keeps `f(a, /* b */ c)` from trailing
// the comment onto `a`. Checking rule 2 before rule 3 is what keeps a comment
// at the end of a line from leading the statement on the next line.
//
// A node whose span covers more than one node ending at the same offset takes
// the comment ahead of the nodes inside it, so `val x = 3 // three` trails the
// whole statement rather than the literal `3`. The same preference applies to
// a leading comment, which leads the statement rather than its first pattern.
//
// lineMap must cover the file the comments came from. Comments from another
// file are returned unattached.
func AttachComments(root Walkable, comments []*Comment, lineMap *LineMap) []*Comment {
	return attachComments(root, comments, func(int) *LineMap { return lineMap })
}

// AttachModuleComments attaches the comments every file of m holds, and
// returns the ones no node took, in source order.
//
// A Module is not a node, so it cannot stand in as a file's own owner the way
// a Script does. A comment below the last declaration of a file therefore
// comes back unattached. It is still reachable through Module.Comments, which
// keeps every comment the parse read whether or not a node took it.
func AttachModuleComments(m *Module) []*Comment {
	var all []*Comment
	for _, comments := range m.Comments {
		all = append(all, comments...)
	}
	return attachComments(m, all, func(sourceID int) *LineMap {
		if source, ok := m.Sources[sourceID]; ok {
			return source.LineMap()
		}
		return nil
	})
}

// attachComments is the shared body of the two entry points. lineMapFor
// returns the map covering one source, or nil for a source the caller does not
// hold, whose comments no node can take.
func attachComments(root Walkable, comments []*Comment, lineMapFor func(int) *LineMap) []*Comment {
	if len(comments) == 0 {
		return nil
	}

	// A module's comments arrive from a map, and two comments in different
	// files share an offset often. SourceID breaks that tie, so the order the
	// pass works in, and the order of the comments it hands back, is the same
	// on every run.
	sorted := make([]*Comment, len(comments))
	copy(sorted, comments)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i].span, sorted[j].span
		if a.Start.Offset != b.Start.Offset {
			return a.Start.Offset < b.Start.Offset
		}
		return a.SourceID < b.SourceID
	})

	nodes := collectNodes(root)
	slots := &attachment{
		leading:  map[Node][]*Comment{},
		trailing: map[Node][]*Comment{},
		dangling: map[Node][]*Comment{},
	}

	// A Script's span reaches from its first statement to its last, so it
	// encloses neither a comment above the first nor one below the last.
	// Holding it aside as the file's own owner is what gives those two a home.
	container, _ := root.(Node)

	var unattached []*Comment
	for _, comment := range sorted {
		switch {
		case nodes.place(comment, lineMapFor(comment.span.SourceID), slots):
		case container != nil && container.Span().SourceID == comment.span.SourceID:
			slots.dangling[container] = append(slots.dangling[container], comment)
		default:
			unattached = append(unattached, comment)
		}
	}
	slots.apply()
	return unattached
}

// attachment holds what each node is owed until every comment has an owner.
// Collecting first and setting after keeps a second run over the same tree
// from appending a comment a node already holds.
type attachment struct {
	leading  map[Node][]*Comment
	trailing map[Node][]*Comment
	dangling map[Node][]*Comment
}

// lead gives comment to n's leading slot, unless n already holds the same text
// as its own JSDoc. The parser retains a declaration's leading `/** ... */` on
// the node, and a printer writes that doc before the node's leading comments,
// so storing it in both places would write it twice. The comment is still
// listed in the file's comment list either way.
//
// It always reports the comment placed, since a node holding the text as its
// doc is as much its owner as one holding it in a slot.
func (a *attachment) lead(n Node, comment *Comment) bool {
	if documented, ok := n.(Documented); ok && comment.IsDoc() && documented.Doc() == comment.Text {
		return true
	}
	a.leading[n] = append(a.leading[n], comment)
	return true
}

func (a *attachment) apply() {
	for node, comments := range a.leading {
		node.SetLeadingComments(comments)
	}
	for node, comments := range a.trailing {
		node.SetTrailingComments(comments)
	}
	for node, comments := range a.dangling {
		node.SetDanglingComments(comments)
	}
}

// nodeIndex holds the nodes of one tree, grouped by the source each came from. A
// comment's neighbours and enclosing node always come from its own source, so each
// search reads only that source's nodes.
type nodeIndex struct {
	sources map[int]*sourceNodes
}

// sourceNodes holds the nodes of one source in the two orders the placement rules
// search: byStart finds the node after a comment, and byEnd finds the node before
// it. Both put an enclosing node ahead of the nodes it encloses when several share
// the offset being searched for, so the widest node wins.
//
// enclosing sweeps byStart forward as comments arrive, so it keeps its place in
// next and the nodes that may still enclose a comment in open.
type sourceNodes struct {
	byStart []Node
	byEnd   []Node
	// next is the position in byStart of the first node not yet added to open.
	next int
	// open holds the nodes before next that end at or after the start of the last
	// comment enclosing was asked about, in byStart order.
	open []Node
}

// collectNodes gathers every node in root that covers a real range of source.
// A synthesized span carries no offsets and would sit at the start of the
// file, ahead of the comments meant for real nodes, so it is left out.
func collectNodes(root Walkable) *nodeIndex {
	c := &nodeCollector{DefaultVisitor: DefaultVisitor{}}
	root.Accept(c)

	counts := map[int]int{}
	for _, node := range c.nodes {
		counts[node.Span().SourceID]++
	}
	x := &nodeIndex{sources: make(map[int]*sourceNodes, len(counts))}
	for id, n := range counts {
		x.sources[id] = &sourceNodes{byStart: make([]Node, 0, n)}
	}
	for _, node := range c.nodes {
		source := x.sources[node.Span().SourceID]
		source.byStart = append(source.byStart, node)
	}
	for _, source := range x.sources {
		source.byEnd = slices.Clone(source.byStart)
		sort.SliceStable(source.byStart, func(i, j int) bool {
			a, b := source.byStart[i].Span(), source.byStart[j].Span()
			if a.Start.Offset != b.Start.Offset {
				return a.Start.Offset < b.Start.Offset
			}
			return a.End.Offset > b.End.Offset
		})
		sort.SliceStable(source.byEnd, func(i, j int) bool {
			a, b := source.byEnd[i].Span(), source.byEnd[j].Span()
			if a.End.Offset != b.End.Offset {
				return a.End.Offset < b.End.Offset
			}
			return a.Start.Offset < b.Start.Offset
		})
	}
	return x
}

// place gives comment to a node and reports whether one took it. It applies
// rules 1 through 4 of the order AttachComments documents. A comment it turns
// down is one no node in the tree covers, which AttachComments then offers to
// the file's own container.
//
// The comments of one source must be placed in order of their start offsets.
func (x *nodeIndex) place(comment *Comment, lineMap *LineMap, slots *attachment) bool {
	if lineMap == nil {
		return false
	}
	span := comment.span
	source, ok := x.sources[span.SourceID]
	if !ok {
		return false
	}

	// inner is the innermost node the comment sits inside. A neighbour outside
	// it cannot own the comment. Were it allowed to, the last comment in a
	// block would lead whatever follows the closing brace.
	inner := source.enclosing(span)
	next := source.after(span.End.Offset)
	prev := source.before(span.Start.Offset)

	if next != nil && enclosedBy(inner, next) &&
		lineMap.SameLine(span.End.Offset, next.Span().Start.Offset) {
		return slots.lead(next, comment)
	}
	if prev != nil && enclosedBy(inner, prev) &&
		lineMap.SameLine(prev.Span().End.Offset, span.Start.Offset) {
		slots.trailing[prev] = append(slots.trailing[prev], comment)
		return true
	}
	if next != nil && enclosedBy(inner, next) {
		return slots.lead(next, comment)
	}
	if inner != nil {
		slots.dangling[inner] = append(slots.dangling[inner], comment)
		return true
	}
	return false
}

// after returns the first node starting at or past offset, preferring the
// widest of the nodes that start there.
func (s *sourceNodes) after(offset int) Node {
	i := sort.Search(len(s.byStart), func(i int) bool {
		return s.byStart[i].Span().Start.Offset >= offset
	})
	if i == len(s.byStart) {
		return nil
	}
	return s.byStart[i]
}

// before returns the last node ending at or before offset, preferring the
// widest of the nodes that end there.
func (s *sourceNodes) before(offset int) Node {
	i := sort.Search(len(s.byEnd), func(i int) bool {
		return s.byEnd[i].Span().End.Offset > offset
	}) - 1
	if i < 0 {
		return nil
	}
	// byEnd puts a node ahead of the nodes it encloses when they end together,
	// so walking back to the start of the run reaches the widest.
	end := s.byEnd[i].Span().End.Offset
	for i > 0 && s.byEnd[i-1].Span().End.Offset == end {
		i--
	}
	return s.byEnd[i]
}

// enclosing returns the innermost node whose span contains span. Each call must
// pass a span starting at or after the start of the span the previous call passed.
func (s *sourceNodes) enclosing(span Span) Node {
	for s.next < len(s.byStart) && s.byStart[s.next].Span().Start.Offset <= span.Start.Offset {
		s.open = append(s.open, s.byStart[s.next])
		s.next++
	}
	// A node ending before span starts encloses neither span nor any later one,
	// so it leaves open for good.
	kept := s.open[:0]
	var found Node
	for _, node := range s.open {
		outer := node.Span()
		if outer.End.Offset < span.Start.Offset {
			continue
		}
		kept = append(kept, node)
		if outer.End.Offset < span.End.Offset {
			continue
		}
		// Two nodes can cover the same range, as a statement does with the
		// declaration inside it. byStart lists an enclosing node before the
		// nodes it encloses, so taking the last of an equal-width run reaches
		// the deepest of them.
		if found == nil || spanWidth(outer) <= spanWidth(found.Span()) {
			found = node
		}
	}
	s.open = kept
	return found
}

func spanWidth(s Span) int { return s.End.Offset - s.Start.Offset }

// enclosedBy reports whether candidate lies inside inner. A nil inner means no
// node encloses the comment being placed, which leaves every candidate open.
func enclosedBy(inner, candidate Node) bool {
	return inner == nil || inner.Span().ContainsSpan(candidate.Span())
}

// nodeCollector gathers the nodes a walk reaches. It records on the way in, so
// a parent lands in the slice before its children, which is the order the two
// sorts in collectNodes preserve for nodes sharing an offset.
type nodeCollector struct {
	DefaultVisitor
	nodes []Node
}

func (c *nodeCollector) add(n Node) {
	span := n.Span()
	if span.End.Offset <= span.Start.Offset {
		return
	}
	c.nodes = append(c.nodes, n)
}

func (c *nodeCollector) EnterExpr(e Expr) bool               { c.add(e); return true }
func (c *nodeCollector) EnterStmt(s Stmt) bool               { c.add(s); return true }
func (c *nodeCollector) EnterDecl(d Decl) bool               { c.add(d); return true }
func (c *nodeCollector) EnterTypeAnn(t TypeAnn) bool         { c.add(t); return true }
func (c *nodeCollector) EnterPat(p Pat) bool                 { c.add(p); return true }
func (c *nodeCollector) EnterClassElem(e ClassElem) bool     { c.add(e); return true }
func (c *nodeCollector) EnterObjExprElem(e ObjExprElem) bool { c.add(e); return true }

// A RestSpreadTypeAnn member reaches the collector through EnterTypeAnn, so
// this covers the other seven ObjTypeAnnElem variants.
func (c *nodeCollector) EnterObjTypeAnnElem(e ObjTypeAnnElem) bool { c.add(e); return true }
