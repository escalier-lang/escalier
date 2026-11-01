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

// eventHolder is the destination that receives the value of a copy, borrow, or move. It
// pairs the target that holds the value with the field path within that target.
type eventHolder struct {
	// target is the destination that holds the value.
	target holderTarget
	// path is the field path within target that receives the value. The element `p` of
	// `return {f: p}` is held at the path [f] of the return. For a placeTarget it is the
	// path under the binding, so the holder of `o.f` has the path [f].
	path []placeSeg
}

// extend returns a copy of the holder with the segments appended to its path. It leaves
// the receiver unchanged.
func (h eventHolder) extend(path []placeSeg) eventHolder {
	if len(path) == 0 {
		return h
	}
	return eventHolder{target: h.target, path: appendPath(h.path, path)}
}

// holderTarget is the destination an eventHolder names. It is one of placeTarget,
// moduleTarget, argTarget, receiverTarget, returnTarget, or tempTarget.
type holderTarget interface {
	isHolderTarget()
}

// placeTarget is a local binding, such as the `q` of `q` or `q.f`.
type placeTarget struct {
	// root is the binding that receives the value.
	root liveness.VarID
}

// moduleTarget is a module-level binding.
type moduleTarget struct {
	// name is the name of the binding.
	name string
}

// argTarget is an argument of a call.
type argTarget struct {
	// call is the call that receives the argument.
	call *ast.CallExpr
	// index is the position of the argument in the call.
	index int
}

// receiverTarget is the receiver of a method call.
type receiverTarget struct {
	// call is the method call that receives the receiver.
	call *ast.CallExpr
}

// returnTarget is the value the function returns.
type returnTarget struct{}

// tempTarget is a value no flow site receives, such as the literal in `({f: &p}).f`. A
// literal element or a closure capture starts out held by the literal or closure, and the
// flow site that receives the literal or closure retargets it to its own holder.
type tempTarget struct {
	// node is the expression of the value.
	node ast.Node
}

func (placeTarget) isHolderTarget()    {}
func (moduleTarget) isHolderTarget()   {}
func (argTarget) isHolderTarget()      {}
func (receiverTarget) isHolderTarget() {}
func (returnTarget) isHolderTarget()   {}
func (tempTarget) isHolderTarget()     {}

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
	// unwritten holds each expression reported for filling a borrow destination without the
	// borrow written, such as the `p` of `val c: &{x: number} = p`. The flow out of such an
	// expression is read as a plain read of its place, so it neither moves the place nor
	// lends it, and the missing borrow is the one diagnostic its flow produces.
	unwritten set.Set[ast.Node]
	// emitted holds each expression whose events are emitted, with the CFG point they attach
	// to. The walk types a `match` guard once per path that reaches it, and the guard's
	// events are emitted once.
	emitted set.Set[emitSite]
	// temps maps the expression of each tempTarget to the indices of the events it holds.
	temps map[ast.Node][]int
	// tempRoots maps the expression of each tempTarget to the root of each place whose
	// value flows into it. retargetTemp records those roots against the flow site that
	// receives the temporary value.
	tempRoots map[ast.Node][]liveness.VarID
	// escaping holds the root of each place whose value flows into a return or a
	// module-level binding. flowsInto maps the root of each place whose value flows into a
	// local place to the roots of those places. A closure held by a place that reaches
	// escaping through flowsInto outlives the frame. See escapes.
	escaping  set.Set[liveness.VarID]
	flowsInto map[liveness.VarID][]liveness.VarID
	// tupleParts maps the placeKey of each place a tuple pattern destructures to the segment
	// of each element the pattern names. `val [x, ...rest] = b` maps `b` to `0` and `1...`.
	// The emitter records a pattern only when it names every element of a fixed-length
	// tuple, so a move of each part moves the whole value. A place that one pattern does not
	// cover, or that two patterns split differently, maps to nil. The arms of a `match` can
	// destructure one place in several ways, and moves out of different arms do not add up
	// to a move of the whole value.
	tupleParts map[string][]placeSeg
	// callees maps the callee expression of each call the walk has reached to the call, so
	// a consuming receiver read at a callee knows the call whose receiver holds it.
	callees map[ast.Expr]*ast.CallExpr
	// cfg is the control-flow graph of the body, and live is the liveness of each of its
	// locals over cfg. The dataflow pass in ownership_dataflow.go walks cfg and reads live to
	// decide whether a holder is used again.
	cfg  *liveness.CFG
	live *liveness.LivenessInfo
	// checked marks a table the dataflow pass has already walked. A package loaded during a
	// run is checked when its own declarations finish, before the module that imports it.
	checked bool
}

// noteFlowInto records that the value of the place rooted at root flows into holder, for
// escapes to read.
func (t *ownershipEvents) noteFlowInto(root liveness.VarID, holder eventHolder) {
	switch tg := holder.target.(type) {
	case returnTarget, moduleTarget:
		t.escaping.Add(root)
	case placeTarget:
		t.flowsInto[root] = append(t.flowsInto[root], tg.root)
	case tempTarget:
		t.tempRoots[tg.node] = append(t.tempRoots[tg.node], root)
	case argTarget, receiverTarget:
		// A value passed to a call goes to the callee's parameter, and escapes does not
		// follow it into the callee. A closure passed as an argument borrows what it
		// captures, as settleDest decides for a capture held by an argument.
	}
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
	receiverRole
	assignRole
	bindingRole
	patternRole
	returnRole
)

// newOwnershipEvents returns an empty table whose places render through names.
func newOwnershipEvents(names map[liveness.VarID]string) *ownershipEvents {
	return &ownershipEvents{
		names:      names,
		subsumed:   set.NewSet[ast.Node](),
		unwritten:  set.NewSet[ast.Node](),
		emitted:    set.NewSet[emitSite](),
		temps:      map[ast.Node][]int{},
		tempRoots:  map[ast.Node][]liveness.VarID{},
		escaping:   set.NewSet[liveness.VarID](),
		flowsInto:  map[liveness.VarID][]liveness.VarID{},
		tupleParts: map[string][]placeSeg{},
		callees:    map[ast.Expr]*ast.CallExpr{},
	}
}

// list returns the table's events in emission order, without the use events of
// subsumed reads. A flow out of an unwritten expression is listed as a read of its place.
func (t *ownershipEvents) list() []ownershipEvent {
	out := make([]ownershipEvent, 0, len(t.events))

	for _, ev := range t.events {
		if use, ok := ev.(*useEvent); ok && use.fromRead && t.subsumed.Contains(use.node) {
			continue
		}

		if fe, ok := ev.(flowEvent); ok && t.unwritten.Contains(fe.base().node) {
			ev = &useEvent{eventBase: *fe.base(), access: readAccess}
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

	switch tg := h.target.(type) {
	case placeTarget:
		return t.renderPlace(movePlace{root: tg.root, path: h.path})
	case moduleTarget:
		writeQuotedPlace(&b, tg.name, h.path)
		return b.String()
	case argTarget:
		fmt.Fprintf(&b, "arg(%s, %d)", renderCallee(tg.call), tg.index)
	case receiverTarget:
		fmt.Fprintf(&b, "arg(%s, self)", renderCallee(tg.call))
	case returnTarget:
		b.WriteString("return")
	case tempTarget:
		b.WriteString("temp")
	}

	writePath(&b, h.path)
	return b.String()
}

// writePath appends each segment of path to b the way writePlaceSeg writes it, so a field
// whose name is not an identifier renders in bracket notation, as `obj["a.b"]` does. A
// segment that names a tuple element renders in dot form instead. That is a position such
// as `0`, or a rest element's `1...`, which stands for every element from 1 on.
func writePath(b *strings.Builder, path []placeSeg) {
	for _, seg := range path {
		if _, isElement := canonicalIndex(strings.TrimSuffix(seg.name, "...")); isElement {
			b.WriteByte('.')
			b.WriteString(seg.name)
			continue
		}
		writePlaceSeg(b, seg)
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
