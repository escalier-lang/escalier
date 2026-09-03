package ast

import "sort"

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

// nodeIndex holds the nodes of one tree in the two orders the placement rules
// search: byStart finds the node after a comment, and byEnd finds the node
// before it. Both put an enclosing node ahead of the nodes it encloses when
// several share the offset being searched for, so the widest node wins.
type nodeIndex struct {
	byStart []Node
	byEnd   []Node
}

// collectNodes gathers every node in root that covers a real range of source.
// A synthesized span carries no offsets and would sit at the start of the
// file, ahead of the comments meant for real nodes, so it is left out.
func collectNodes(root Walkable) *nodeIndex {
	c := &nodeCollector{DefaultVisitor: DefaultVisitor{}}
	root.Accept(c)

	byStart := c.nodes
	byEnd := make([]Node, len(byStart))
	copy(byEnd, byStart)

	sort.SliceStable(byStart, func(i, j int) bool {
		a, b := byStart[i].Span(), byStart[j].Span()
		if a.Start.Offset != b.Start.Offset {
			return a.Start.Offset < b.Start.Offset
		}
		return a.End.Offset > b.End.Offset
	})
	sort.SliceStable(byEnd, func(i, j int) bool {
		a, b := byEnd[i].Span(), byEnd[j].Span()
		if a.End.Offset != b.End.Offset {
			return a.End.Offset < b.End.Offset
		}
		return a.Start.Offset < b.Start.Offset
	})
	return &nodeIndex{byStart: byStart, byEnd: byEnd}
}

// place gives comment to a node and reports whether one took it. It applies
// rules 1 through 4 of the order AttachComments documents. A comment it turns
// down is one no node in the tree covers, which AttachComments then offers to
// the file's own container.
func (x *nodeIndex) place(comment *Comment, lineMap *LineMap, slots *attachment) bool {
	if lineMap == nil {
		return false
	}
	span := comment.span

	// inner is the innermost node the comment sits inside. A neighbour outside
	// it cannot own the comment. Were it allowed to, the last comment in a
	// block would lead whatever follows the closing brace.
	inner := x.enclosing(span)
	next := x.after(span.End.Offset, span.SourceID)
	prev := x.before(span.Start.Offset, span.SourceID)

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
func (x *nodeIndex) after(offset, sourceID int) Node {
	i := sort.Search(len(x.byStart), func(i int) bool {
		return x.byStart[i].Span().Start.Offset >= offset
	})
	for ; i < len(x.byStart); i++ {
		if x.byStart[i].Span().SourceID == sourceID {
			return x.byStart[i]
		}
	}
	return nil
}

// before returns the last node of sourceID ending at or before offset,
// preferring the widest of the nodes that end there.
//
// A module holds the nodes of all its files in one index. The node nearest a
// comment by offset therefore often belongs to another file, and the one this
// looks for lies further back. The scan walks back over every node ending at
// or before offset until it reaches one of sourceID.
func (x *nodeIndex) before(offset, sourceID int) Node {
	i := sort.Search(len(x.byEnd), func(i int) bool {
		return x.byEnd[i].Span().End.Offset > offset
	})
	for i--; i >= 0; i-- {
		if x.byEnd[i].Span().SourceID != sourceID {
			continue
		}
		// byEnd puts a node ahead of the nodes it encloses when they end
		// together, so walking back to the start of the run reaches the widest.
		end := x.byEnd[i].Span().End.Offset
		found := x.byEnd[i]
		for j := i - 1; j >= 0 && x.byEnd[j].Span().End.Offset == end; j-- {
			if x.byEnd[j].Span().SourceID == sourceID {
				found = x.byEnd[j]
			}
		}
		return found
	}
	return nil
}

// enclosing returns the innermost node whose span contains span.
func (x *nodeIndex) enclosing(span Span) Node {
	var found Node
	for _, node := range x.byStart {
		outer := node.Span()
		if outer.Start.Offset > span.Start.Offset {
			break
		}
		if outer.SourceID != span.SourceID || outer.End.Offset < span.End.Offset {
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
