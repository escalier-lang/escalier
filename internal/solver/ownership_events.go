package solver

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file emits the ownership events of the checking model in
// planning/ownership_rules/requirements.md. Each event is attached to a node of the
// control-flow graph the liveness package builds for a body. A node is a statement,
// named by its liveness.StmtRef. The walk emits the events while it infers, since only
// the walk knows the type of each destination, and F2 needs that type to choose between
// a copy, a borrow, and a move.
//
// The holder of a copy, borrow, or move is the destination that receives the value, such
// as the binding `d` in `val d = c`. A flow of a value type, such as `val a = n` with
// `n: number`, makes no holder of a borrow, so it is recorded as a read of its source.
//
// A flow whose destination or source F3 has not settled yet is a provisional flow. The
// walk records a placeholder for it, and settleFlowsWaitingOn replaces the placeholder
// with the copy, borrow, or move F2 gives once F3 is settled. Three kinds of flow are
// provisional:
//
//   - A flow out of an unannotated parameter of a body still being walked. The body
//     decides whether the parameter is owned or a borrow.
//   - A flow into a parameter of a callee whose body is still being walked, or whose
//     signature the call cannot see yet.
//   - A closure capture. A closure that is returned or stored into module-level state
//     owns what it captures, and any other closure borrows it. A closure a local holds
//     may be returned through that local later, so the capture waits until the body
//     holding the closure is walked.

// borrowKind is the mutability of a borrow: `&` or `&mut`.
type borrowKind uint8

const (
	immutableBorrow borrowKind = iota
	mutableBorrow
)

// useAccess says whether a use event reads or writes its place.
type useAccess uint8

const (
	readAccess useAccess = iota
	writeAccess
)

// holderKind is the kind of destination that receives a flowing value.
type holderKind uint8

const (
	// placeHolder is a local binding or a field path rooted at one, such as `q` or `o.f`.
	placeHolder holderKind = iota
	// moduleHolder is a module-level binding, named by name.
	moduleHolder
	// argHolder is argument index of the call node.
	argHolder
	// receiverHolder is the receiver of the method call node.
	receiverHolder
	// returnHolder is the value the function returns.
	returnHolder
	// tempHolder is a value no flow site receives, such as the literal in
	// `({f: &p}).f`. node is that value's expression. A literal element or a closure
	// capture starts out held by the literal or closure, and the flow site that receives
	// the literal or closure retargets it to its own holder.
	tempHolder
)

// eventHolder is the destination that receives the value of a copy, borrow, or move.
// place holds the binding and field path of a placeHolder. For every other kind
// place.root is 0 and place.path is the field path within the destination, so the
// element `p` of `return {f: p}` is held at the path [f] of the return.
type eventHolder struct {
	kind  holderKind
	place movePlace
	name  string
	node  ast.Node
	index int
}

// extend returns h with the path segments appended to its place's path.
func (h eventHolder) extend(path []placeSeg) eventHolder {
	if len(path) > 0 {
		h.place.path = appendPath(h.place.path, path)
	}
	return h
}

// ownershipEvent is one event of the checking model. It is one of *copyEvent,
// *borrowEvent, *moveEvent, *useEvent, or *provisionalEvent. base returns the fields
// every kind shares.
type ownershipEvent interface {
	isOwnershipEvent()
	base() *eventBase
}

// eventBase holds the fields every ownership event has.
type eventBase struct {
	// place is the place a copy, borrow, or move takes its value from, or the place a use
	// reads or writes.
	place movePlace
	// node is the expression to blame for the event.
	node ast.Node
	// at is the CFG node the event is attached to.
	at liveness.StmtRef
}

// base returns the fields every ownership event shares.
func (b *eventBase) base() *eventBase { return b }

// flowBase holds the fields of an event that passes the value at place to a holder.
type flowBase struct {
	eventBase
	// holder receives the value.
	holder eventHolder
}

// flow returns the fields of the flow event.
func (f *flowBase) flow() *flowBase { return f }

// flowEvent is an ownership event that passes a value to a holder: a *copyEvent,
// *borrowEvent, *moveEvent, or *provisionalEvent.
type flowEvent interface {
	ownershipEvent
	flow() *flowBase
}

// copyEvent gives the holder the same value as the place, which stays usable.
type copyEvent struct {
	flowBase
}

// borrowEvent gives the holder a new borrow of the place.
type borrowEvent struct {
	flowBase
	// borrow is the mutability of the borrow.
	borrow borrowKind
	// lifetime is the lifetime of the borrow, or nil when the borrow's type carries none.
	lifetime soltype.Lifetime
}

// moveEvent gives the holder ownership of the place, which is consumed.
type moveEvent struct {
	flowBase
}

// useEvent reads or writes the place.
type useEvent struct {
	eventBase
	// access says whether the use reads or writes the place.
	access useAccess
	// fromRead marks a use that reading the expression node emitted. A later event that
	// reads the same expression replaces such a use.
	fromRead bool
}

// provisionalEvent holds the slot of a provisional flow until F3 settles it. No event
// of this kind survives settleAllFlows.
type provisionalEvent struct {
	flowBase
}

func (*copyEvent) isOwnershipEvent()        {}
func (*borrowEvent) isOwnershipEvent()      {}
func (*moveEvent) isOwnershipEvent()        {}
func (*useEvent) isOwnershipEvent()         {}
func (*provisionalEvent) isOwnershipEvent() {}

// ownershipEvents holds the ownership events of one body, in the order the walk emitted
// them. names maps each VarID of the body to its source name, for rendering.
type ownershipEvents struct {
	events []ownershipEvent
	names  map[liveness.VarID]string
	// subsumed holds each read expression whose value another event already accounts
	// for, such as the `c` of `val d = c`, whose copy event reads it. The use event that
	// reading a subsumed expression emitted is dropped when the table is read.
	subsumed set.Set[ast.Node]
	// emitted holds each expression whose events are emitted, with the CFG point they attach
	// to. The walk types a `match` guard once per path that reaches it, and the guard's
	// events are emitted once.
	emitted set.Set[emitSite]
	// temps maps the expression of each tempHolder to the indices of the events it holds.
	temps map[ast.Node][]int
	// escaping holds the root of each place whose value flows into a return or a
	// module-level binding. flowsInto maps the root of each place whose value flows into a
	// local place to the roots of those places. A closure held by a place that reaches
	// escaping through flowsInto outlives the frame. See escapes.
	escaping  set.Set[liveness.VarID]
	flowsInto map[liveness.VarID][]liveness.VarID
}

// escapes reports whether the value held by the place rooted at root flows into a return
// or a module-level binding, directly or through other local places.
func (t *ownershipEvents) escapes(root liveness.VarID) bool {
	seen := set.NewSet[liveness.VarID]()
	stack := []liveness.VarID{root}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if t.escaping.Contains(r) {
			return true
		}
		if seen.Contains(r) {
			continue
		}
		seen.Add(r)
		stack = append(stack, t.flowsInto[r]...)
	}
	return false
}

// emitSite is one expression, the role it emits events in, and the CFG point they attach
// to. One expression can emit in two roles, such as the `q` of `{b: q}`, which is read and
// is also a literal element. root tells apart the captures one closure emits, and is 0 for
// any other expression.
type emitSite struct {
	node ast.Node
	role emitRole
	at   liveness.StmtRef
	root liveness.VarID
}

// emitRole is the role in which an expression emits events.
type emitRole uint8

const (
	readRole emitRole = iota
	borrowRole
	elementRole
	captureRole
	callRole
	assignRole
	bindingRole
	patternRole
	returnRole
)

// newOwnershipEvents returns an empty table whose places render through names.
func newOwnershipEvents(names map[liveness.VarID]string) *ownershipEvents {
	return &ownershipEvents{
		names:     names,
		subsumed:  set.NewSet[ast.Node](),
		emitted:   set.NewSet[emitSite](),
		temps:     map[ast.Node][]int{},
		escaping:  set.NewSet[liveness.VarID](),
		flowsInto: map[liveness.VarID][]liveness.VarID{},
	}
}

// list returns the table's events in emission order, without the use events of
// subsumed reads.
func (t *ownershipEvents) list() []ownershipEvent {
	out := make([]ownershipEvent, 0, len(t.events))
	for _, ev := range t.events {
		if use, ok := ev.(*useEvent); ok && use.fromRead && t.subsumed.Contains(use.node) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// byNode returns the table's events grouped by the CFG node they are attached to. Each
// node's events keep their emission order.
func (t *ownershipEvents) byNode() map[liveness.StmtRef][]ownershipEvent {
	out := map[liveness.StmtRef][]ownershipEvent{}
	for _, ev := range t.list() {
		at := ev.base().at
		out[at] = append(out[at], ev)
	}
	return out
}

// ownershipEventsOf returns the ownership events of the body of fn, or nil when no body
// of fn was walked. A script's top level is keyed by a nil fn.
func (i *Info) ownershipEventsOf(fn ast.Node) *ownershipEvents {
	return i.ownership[fn]
}

// renderOwnershipEvents renders the events of t one per line, each prefixed with the CFG
// node it is attached to, as renderStmtRef writes it. A borrow's lifetime renders as
// `'static`, as `'_` when the borrow's type carries none or an elided one, or as `'<n>`
// numbering the other lifetimes in order of first appearance.
func renderOwnershipEvents(t *ownershipEvents) []string {
	ltNames := map[soltype.Lifetime]string{}
	var out []string
	for _, ev := range t.list() {
		out = append(out, fmt.Sprintf("%s: %s", renderStmtRef(ev.base().at), t.renderEvent(ev, ltNames)))
	}
	return out
}

// renderStmtRef renders a CFG node as `b<block>.<index>`, or as `b<block>.entry` for the
// synthetic position before the block's first statement, which liveness numbers -1.
func renderStmtRef(at liveness.StmtRef) string {
	if at.StmtIdx < 0 {
		return fmt.Sprintf("b%d.entry", at.BlockID)
	}
	return fmt.Sprintf("b%d.%d", at.BlockID, at.StmtIdx)
}

// renderEvent renders one event in the notation of the checking model. A name is in
// backticks and a keyword is bare, so `val c = &p` renders as borrow(&, `p`, `c`, '1).
func (t *ownershipEvents) renderEvent(ev ownershipEvent, ltNames map[soltype.Lifetime]string) string {
	place := t.renderPlace(ev.base().place)
	switch ev := ev.(type) {
	case *copyEvent:
		return fmt.Sprintf("copy(%s, %s)", place, t.renderHolder(ev.holder))
	case *moveEvent:
		return fmt.Sprintf("move(%s, %s)", place, t.renderHolder(ev.holder))
	case *borrowEvent:
		kind := "&"
		if ev.borrow == mutableBorrow {
			kind = "&mut"
		}
		return fmt.Sprintf("borrow(%s, %s, %s, %s)", kind, place, t.renderHolder(ev.holder), renderLifetime(ev.lifetime, ltNames))
	case *useEvent:
		access := "read"
		if ev.access == writeAccess {
			access = "write"
		}
		return fmt.Sprintf("use(%s, %s)", place, access)
	case *provisionalEvent:
		return fmt.Sprintf("provisional(%s, %s)", place, t.renderHolder(ev.holder))
	default:
		panic(fmt.Sprintf("renderEvent: unexpected event %T", ev))
	}
}

// renderLifetime renders lt for renderEvent, naming it in ltNames on first sight.
func renderLifetime(lt soltype.Lifetime, ltNames map[soltype.Lifetime]string) string {
	if _, anon := lt.(*soltype.AnonLifetime); lt == nil || anon {
		// An AnonLifetime is the display marker for an elided lifetime, which names nothing.
		return "'_"
	}
	if soltype.IsStaticLifetime(lt) {
		return "'static"
	}
	if name, ok := ltNames[lt]; ok {
		return name
	}
	name := fmt.Sprintf("'%d", len(ltNames)+1)
	ltNames[lt] = name
	return name
}

// renderPlace renders a place as its root's name followed by its field path, in
// backticks so a name reads apart from the keywords an event renders, such as `read` or
// `return`. A root with no name renders bare as `var#<id>`.
func (t *ownershipEvents) renderPlace(p movePlace) string {
	var b strings.Builder
	name, ok := t.names[p.root]
	if !ok {
		fmt.Fprintf(&b, "var#%d", p.root)
		writePath(&b, p.path)
		return b.String()
	}
	writeQuotedPlace(&b, name, p.path)
	return b.String()
}

// writeQuotedPlace appends name followed by path to b, in backticks.
func writeQuotedPlace(b *strings.Builder, name string, path []placeSeg) {
	b.WriteByte('`')
	b.WriteString(name)
	writePath(b, path)
	b.WriteByte('`')
}

// renderHolder renders a holder: a place by name, a module-level binding by name, an
// argument as `arg(<callee>, <index>)`, a receiver as `arg(<method>, self)`, a return as
// `return`, and a temporary as `temp`. A path within the holder follows it. A name is in
// backticks, as renderPlace writes it, and a keyword is bare.
func (t *ownershipEvents) renderHolder(h eventHolder) string {
	var b strings.Builder
	switch h.kind {
	case placeHolder:
		return t.renderPlace(h.place)
	case moduleHolder:
		writeQuotedPlace(&b, h.name, h.place.path)
		return b.String()
	case argHolder:
		fmt.Fprintf(&b, "arg(%s, %d)", renderCallee(h.node), h.index)
	case receiverHolder:
		fmt.Fprintf(&b, "arg(%s, self)", renderCallee(h.node))
	case returnHolder:
		b.WriteString("return")
	case tempHolder:
		b.WriteString("temp")
	}
	writePath(&b, h.place.path)
	return b.String()
}

// writePath appends each segment of path to b as `.name`.
func writePath(b *strings.Builder, path []placeSeg) {
	for _, seg := range path {
		b.WriteByte('.')
		b.WriteString(seg.name)
	}
}

// renderCallee names the callee of the call node for rendering, as calleeName does, in
// backticks. Any other callee renders as the bare keyword `call`.
func renderCallee(node ast.Node) string {
	if ce, ok := node.(*ast.CallExpr); ok {
		if name := calleeName(ce.Callee); name != "" {
			return "`" + name + "`"
		}
	}
	return "call"
}
