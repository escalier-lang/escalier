package solver

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// The move engine implements affine move semantics. When an owned value flows out of
// its source binding at a flow site, ownership moves out of that binding, the binding
// is consumed, and any later use of it is a use-after-move error. The flow sites are a
// `val`/`var` binding, a reassignment, a `return`, a field or element store, a
// consuming argument, an escaping closure capture, and a module-level write.
//
// The engine has three parts.
//
//   - The flow sites call consumeOwned to record a move into c.fn.moveSites, the
//     per-statement consume map AnalyzeMoves folds into the branch-merged consumed
//     lattice from internal/liveness.
//   - A read of a reference-shaped binding calls recordUse and a field read calls
//     recordMemberUse, accumulating the read sites into c.fn.useSites.
//   - After the body walk, checkUseAfterMoves runs AnalyzeMoves once and replays
//     each recorded use against the lattice, reporting a UseAfterMoveError when a
//     conflicting place was Moved or MaybeMoved on a path reaching the read.
//
// Running the use check as a post-pass rather than inline is what makes conditional
// and loop moves correct. The lattice is a fixed point over the whole CFG, so a use
// that a later or back-edge move reaches is still caught.
//
// Moves and uses are tracked at field granularity, not just per binding (PR 7). A
// move or use names a movePlace: a root binding plus a path of field names, see the
// movePlace docstring for its shape and examples. The consumed lattice keys on the
// place rather than the binding. Moving `pair.a` consumes only that field's place, so
// the sibling `pair.b` stays usable while a later read of `pair.a` is a
// use-after-move. A whole binding is the path-empty place, which a move consumes as a
// unit. A use of place U conflicts
// with a moved place M when one path is a prefix of the other under one root, which
// catches a read of the moved field, a read of a field beneath it, and a read of the
// whole object that would expose it.
//
// Known limitation: the lattice only ever raises a binding to Moved, never lowers it.
// Reassigning a `var` after it was moved gives it a fresh value, but the lattice still
// reads it as Moved, so a use after `q = …` re-initializes a consumed q reports a
// spurious use-after-move. Clearing a moved binding at its reassignment is left to a
// later precision pass.

// UseAfterMoveError is reported when a binding is read after an owned value has
// moved out of it. It blames the read and points its related span at the move site.
// Conditional records whether only some reaching paths moved the binding, the
// MaybeMoved lattice state, so a diagnostic consumer can distinguish a definite
// use-after-move from a possible one.
type UseAfterMoveError struct {
	// Name is the moved place's source name, for the message — `pair.a` when the
	// field `pair.a` was consumed.
	Name string
	// ReadName is the read place's source name, set only for a partial-move read where
	// it differs from Name — `pair` when the whole object is read after `pair.a` moved.
	ReadName string
	// Partial is true when the read place is a strict ancestor of the moved place, so
	// the read exposes a field that moved out rather than the moved place itself. It
	// selects the partially-moved wording and pairs with ReadName.
	Partial bool
	// Conditional is true when the binding is moved on some but not all paths
	// reaching the use (the MaybeMoved lattice state), false when every reaching
	// path moved it (Moved).
	Conditional bool
	// use is the read being rejected; it self-blames from here.
	use ast.Node
	// moveSite is the consume that moved the binding, used for the related span. It
	// may be nil when the move node was not recorded.
	moveSite ast.Node
}

func (*UseAfterMoveError) isSolverError()   {}
func (e *UseAfterMoveError) Span() ast.Span { return e.use.Span() }
func (e *UseAfterMoveError) Related() []ast.Span {
	if e.moveSite == nil {
		return nil
	}
	return []ast.Span{e.moveSite.Span()}
}
func (e *UseAfterMoveError) Message() string {
	if e.Partial {
		return fmt.Sprintf("use of partially moved value '%s'; field '%s' was moved out", e.ReadName, e.Name)
	}
	return fmt.Sprintf("use of moved value '%s'", e.Name)
}

// moveUse is one recorded read of a reference-shaped place: the place read, the CFG
// point of the read, and the node to blame. The place carries the field path so a
// read of `pair.a.id` is tested against a partial move of `pair.a`, while a read of
// the sibling `pair.b` is not.
type moveUse struct {
	place movePlace
	ref   liveness.StmtRef
	node  ast.Node
}

// isBorrowType reports whether t is a borrow — a RefType carrying a lifetime.
// Owned-immutable collapses to the bare inner and owned-mutable is a RefType with
// a nil lifetime, so only a non-nil Lt marks a borrow.
func isBorrowType(t soltype.Type) bool {
	r, ok := t.(*soltype.RefType)
	return ok && r.Lt != nil
}

// isReferenceShaped reports whether t is a reference-shaped value: an object, tuple,
// borrow, owned RefType, or type-parameter variable. These are the values a move can
// consume, so every read of one is recorded as a use to test against the consumed
// lattice. Value types copy and are never consumed, so their reads are not tracked. A
// value type is a primitive, a function, or a promise.
//
// A bare type variable counts here but is excluded by isConcreteOwned, so the two are
// conservative in opposite directions on an unresolved variable. This side tracks the
// read so a use-after-move is not missed if the variable resolves to a movable shape.
func isReferenceShaped(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.ObjectType, *soltype.TupleType, *soltype.RefType, *soltype.TypeVarType:
		return true
	}
	return false
}

// isConcreteOwned reports whether t is a CONCRETE owned reference shape — an owned
// object, tuple, or owned RefType — excluding a bare type variable. A consuming
// parameter must be spelled as a concrete owned shape, so a fresh inference variable
// for an unannotated parameter does not consume its argument.
//
// Excluding the bare type variable is the opposite of isReferenceShaped, which includes
// it, so the two are conservative in opposite directions on an unresolved variable. This
// side consumes nothing it cannot confirm is owned, so the caller's argument is not
// over-consumed if the variable resolves to a value type.
func isConcreteOwned(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.ObjectType, *soltype.TupleType:
		return true
	case *soltype.RefType:
		return !isBorrowType(t)
	}
	return false
}

// isOwnedMovable reports whether t is an owned reference-shaped value, the kind a
// move at an owned destination consumes. Value types copy and borrows alias, so
// neither moves at an owned site. An owned object, tuple, or owned-mutable RefType
// moves, as does a bare type-parameter variable: generic code treats a `T` value as
// non-duplicable, the conservative affine assumption that makes
// `fn dup<T>(x: T) -> [T, T]` a double move.
//
// A borrow moves only when it escapes to a longer-lived region, which a module-level
// write forces; that case is consumed through consumeAtGlobalWrite, not here.
func isOwnedMovable(t soltype.Type) bool {
	if isBorrowType(t) {
		return false
	}
	return isReferenceShaped(t)
}

// placeSeg is one step of a movePlace path: a field reached from the place before it.
// The kind tags how the field is named. Every segment built today is a namedSeg
// carrying a field name, so a path models the same field chains as a plain name list.
// The tag leaves room for a symbol-keyed segment without conflating a symbol key with a
// string field of the same text. Adding that kind is part of the M7 computed-key work.
type placeSeg struct {
	kind placeSegKind
	name string
}

type placeSegKind uint8

const (
	// namedSeg names a field by its string name — a static member `pair.a` or a
	// string-literal index `obj["a"]`. The segment's name holds that field name.
	namedSeg placeSegKind = iota
)

// movePlace identifies the storage location the move engine tracks: a root binding
// plus a path of field segments reached from it by static member or constant-index
// access. The empty path is the whole binding, so whole-binding moves are the
// path-length-zero case and need no interning. A move of `pair.a` is the place
// {root: pair, path: [a]}; a later read of `pair.a.id` is {root: pair, path: [a, id]},
// and the two conflict because one path is a prefix of the other. A read of `pair.b`
// is {root: pair, path: [b]}, disjoint from `pair.a`, so a partial move of `pair.a`
// leaves it usable.
type movePlace struct {
	root liveness.VarID
	path []placeSeg
}

// exprPlace returns the place a place-expression names: a binding, or a chain of
// static member and constant-string index accesses rooted at one binding. ok is
// false for anything that is not such a place, such as a call result or a literal.
// A dynamic index falls back to its container place, since the checker cannot prove
// two dynamic indices disjoint — `arr[i]` and `arr[j]` both resolve to `arr`, so a
// move through one conservatively blocks a use through the other.
func exprPlace(e ast.Expr) (movePlace, bool) {
	switch e := e.(type) {
	case *ast.IdentExpr:
		if e.VarID <= 0 {
			return movePlace{}, false
		}
		return movePlace{root: liveness.VarID(e.VarID)}, true
	case *ast.MemberExpr:
		if e.OptChain || e.Prop == nil || e.Prop.Name == "" {
			return movePlace{}, false
		}
		base, ok := exprPlace(e.Object)
		if !ok {
			return movePlace{}, false
		}
		return extendPlace(base, e.Prop.Name), true
	case *ast.IndexExpr:
		if e.OptChain {
			return movePlace{}, false
		}
		base, ok := exprPlace(e.Object)
		if !ok {
			return movePlace{}, false
		}
		if name, ok := constStringKey(e.Index); ok {
			return extendPlace(base, name), true
		}
		// A dynamic index is approximated by its container.
		return base, true
	}
	return movePlace{}, false
}

// extendPlace returns base with one more named field segment appended, copying the
// path so sibling places built from the same base never share backing storage.
func extendPlace(base movePlace, field string) movePlace {
	path := make([]placeSeg, len(base.path)+1)
	copy(path, base.path)
	path[len(base.path)] = placeSeg{kind: namedSeg, name: field}
	return movePlace{root: base.root, path: path}
}

// placeKey is the interning key for a field place. Each segment is encoded as its
// kind and its length-prefixed name, so the encoding is injective for any field name:
// `{root, [a, b]}` and `{root, [ab]}` stay distinct regardless of which bytes a name
// contains, since the length prefix delimits each name rather than a separator the name
// might itself include.
func placeKey(p movePlace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d", p.root)
	for _, seg := range p.path {
		fmt.Fprintf(&b, "|%d:%d:%s", seg.kind, len(seg.name), seg.name)
	}
	return b.String()
}

// placeID maps a place to the VarID the consumed lattice keys on. A whole-binding
// place reuses its root VarID, so it shares the lattice key the binding already has.
// A field place is assigned a fresh synthetic VarID drawn from the module-wide
// counter — unique across every body, so it never collides with a real binding —
// and the mapping is stable within a body, so the same field named at a move site
// and at a use site resolves to one ID.
func (c *checker) placeID(p movePlace) liveness.VarID {
	if len(p.path) == 0 {
		return p.root
	}
	key := placeKey(p)
	if id, ok := c.fn.placeIDs[key]; ok {
		return id
	}
	id := liveness.VarID(c.varIDCounter)
	c.varIDCounter++
	c.fn.placeIDs[key] = id
	return id
}

// pathPrefixRelated reports whether two field paths under one root conflict: one is
// a prefix of the other. Moving `pair.a` conflicts with a use of `pair.a` itself
// when the paths are equal, of a field beneath it like `pair.a.id` when the move
// path is a prefix, and of the whole `pair` when the use path is a prefix, since the
// read would expose the moved field. A use of the disjoint sibling `pair.b` shares
// no prefix and does not conflict.
func pathPrefixRelated(a, b []placeSeg) bool {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderPlace names a place for a diagnostic: the root binding's name followed by
// its field path, so a whole-binding move renders as `pair` and a field move as
// `pair.a`. A segment whose name is not a valid identifier renders in bracket
// notation, so a constant-index access such as `obj["a.b"]` reads back as
// `obj["a.b"]` rather than collapsing into the `obj.a.b` nested access.
func (c *checker) renderPlace(p movePlace) string {
	var b strings.Builder
	b.WriteString(c.varIDToName(p.root))
	for _, seg := range p.path {
		if isDotPlaceSegment(seg.name) {
			b.WriteByte('.')
			b.WriteString(seg.name)
		} else {
			b.WriteByte('[')
			b.WriteString(strconv.Quote(seg.name))
			b.WriteByte(']')
		}
	}
	return b.String()
}

// isDotPlaceSegment reports whether a field name can be rendered with dot notation:
// a valid Escalier identifier, with a leading letter or underscore and
// letter/underscore/digit runes thereafter. Any other name, such as `a.b` or
// `foo-bar` from a constant-string index, renders in bracket notation instead.
func isDotPlaceSegment(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// moveNameOf names the moved value behind a lattice VarID for a diagnostic. A field
// move resolves through movePlaces to its rendered path; a whole-binding move falls
// back to the binding name.
func (c *checker) moveNameOf(id liveness.VarID) string {
	if p, ok := c.fn.movePlaces[id]; ok {
		return c.renderPlace(p)
	}
	return c.varIDToName(id)
}

// currentStmtRef resolves the statement currently being walked to its CFG point.
// It is the program point a use or move records against. ok is false outside a
// function body or when the statement has no CFG ref.
func (c *checker) currentStmtRef() (liveness.StmtRef, bool) {
	if c.fn == nil || c.fn.stmtToRef == nil || c.fn.currentStmt == nil {
		return liveness.StmtRef{}, false
	}
	ref, ok := c.fn.stmtToRef[c.fn.currentStmt]
	return ref, ok
}

// recordUse records a read of identifier e whose value is reference-shaped, so
// checkUseAfterMoves can later test it against the consumed lattice. Borrows are
// recorded alongside owned values, because a borrow stored into a 'static global is
// consumed too. A read of a value type, a non-local, or a binding outside a function
// body records nothing.
func (c *checker) recordUse(e *ast.IdentExpr, t soltype.Type) {
	if c.fn == nil || c.fn.cfg == nil || e.VarID <= 0 {
		return
	}
	if !isReferenceShaped(t) {
		return
	}
	ref, ok := c.currentStmtRef()
	if !ok {
		return
	}
	p := movePlace{root: liveness.VarID(e.VarID)}
	c.fn.useSites = append(c.fn.useSites, moveUse{place: p, ref: ref, node: e})
}

// recordMemberUse records a read of a field place so the use-after-move scan can
// test it against a partial move of the same place or a prefix of it. The whole
// member or index expression names the place, even when the field read yields a
// value type, because reading `pair.a.id` still dereferences `pair.a` and is a
// use-after-move if `pair.a` was moved. A receiver that is not a tracked place, such
// as a namespace member, names no movable root and records nothing.
func (c *checker) recordMemberUse(e ast.Expr) {
	if c.fn == nil || c.fn.cfg == nil {
		return
	}
	p, ok := exprPlace(e)
	if !ok || len(p.path) == 0 {
		return
	}
	ref, ok := c.currentStmtRef()
	if !ok {
		return
	}
	c.fn.useSites = append(c.fn.useSites, moveUse{place: p, ref: ref, node: e})
}

// consumeOwned records a move of the owned place the source expression names, at the
// given program point, blaming moveNode for the consume. The source must be a place —
// a binding or a field path such as `pair.a` — whose value is owned-movable. A borrow,
// value type, fresh literal, or non-place expression consumes nothing here. A field
// path consumes only that field, the partial move from PR 7.
//
// It does NOT force the moved value's borrows to 'static. A return or local store
// flows the value out at the call's own lifetime, not 'static, so forcing here would
// wrongly collapse a finite param-lifetime borrow — `fn (p: &'a {x}) -> &'a {x}`
// returns p at 'a, not 'static. The 'static forcing runs only where the destination
// is genuinely permanent, the module-level write in consumeAtGlobalWrite.
func (c *checker) consumeOwned(source ast.Expr, sourceT soltype.Type, moveNode ast.Node, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.cfg == nil || sourceT == nil {
		return
	}
	p, ok := exprPlace(source)
	if !ok {
		return
	}
	if !isOwnedMovable(sourceT) {
		return
	}
	c.recordMovePlace(p, moveNode, ref)
}

// movesSourceInto reports whether flowing source into a destination of type destT
// moves the owned place source names: source is a place — a binding or a field path
// — bound to an owned-movable value and the destination takes ownership rather than
// borrowing. A borrow destination keeps the source aliased and governed by the
// exclusivity rule, not consumed. A borrow destination is either a `&` annotation or
// an explicit `&source` initializer, which is a BorrowExpr rather than a place and so
// names no owned source. It is the shared move-or-borrow decision for `val`/`var`
// bindings and reassignments.
func (c *checker) movesSourceInto(source ast.Expr, destT soltype.Type) bool {
	if source == nil || isBorrowType(destT) {
		return false
	}
	if _, ok := exprPlace(source); !ok {
		return false
	}
	return isOwnedMovable(c.info.TypeOf(source))
}

// consumeBindingInit moves the owned source a `val`/`var` initializer names into
// the new binding. `val q = p` for an owned p consumes p; a borrow binding leaves
// p usable, whether the borrow comes from a `&` annotation — `val q: &{x} = p` — or
// an explicit `&p` initializer, which is a BorrowExpr rather than a plain
// identifier and so names no owned source to consume.
func (c *checker) consumeBindingInit(vd *ast.VarDecl, bindingT soltype.Type, stmt ast.Stmt) {
	if !c.movesSourceInto(vd.Init, bindingT) {
		return
	}
	ref, ok := c.fn.stmtToRef[stmt]
	if !ok {
		return
	}
	c.consumeOwned(vd.Init, c.info.TypeOf(vd.Init), vd.Init, ref)
}

// consumeAtGlobalWrite consumes the source place of a module-level store. A store
// into a 'static global permanently transfers the value, so it consumes the source
// whether owned or a borrow — using the source afterward could mutate what the global
// now reads, the leak the affine rule closes. A value-type source copies and a
// non-place source names no storage, so neither consumes.
func (c *checker) consumeAtGlobalWrite(source ast.Expr, sourceT soltype.Type, moveNode ast.Node, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.cfg == nil || sourceT == nil {
		return
	}
	p, ok := exprPlace(source)
	if !ok {
		return
	}
	if !isReferenceShaped(sourceT) {
		return
	}
	c.recordMovePlace(p, moveNode, ref)
}

// consumeIntoLiteral moves an owned place built into a fresh object or tuple literal,
// recording the move at ref, the literal's statement. Storing an owned value into the
// literal transfers ownership into it, so a later use of the source is a
// use-after-move. A value-type element copies and a non-place element names no
// storage, so neither consumes. hasRef is false when the literal has no resolvable
// statement point, in which case nothing is recorded rather than mis-attributing the
// move to the zero StmtRef.
//
// inferTuple and inferObject call it for each element built from a source place:
//
//	val mut a = {foo: "hello"}
//	val ys = [a]   // a moves into the tuple
//	a.foo          // ERROR: use of moved value 'a'
//
// A field-path element consumes only that field, the partial move from PR 7:
//
//	val mut pair = {a: {foo: "hi"}, b: {bar: 1}}
//	val ys = [pair.a]   // pair.a moves into the tuple
//	pair.a              // ERROR: use of moved value 'pair.a'
//	pair.b              // OK: sibling untouched
func (c *checker) consumeIntoLiteral(el ast.Expr, elemT soltype.Type, ref liveness.StmtRef, hasRef bool) {
	if !hasRef {
		return
	}
	c.consumeOwned(el, elemT, el, ref)
}

// recordMovePlace consumes the place at the given program point, blaming moveNode. It
// resolves the place to its lattice VarID, registers the mapping so the
// use-after-move scan can recover the path for the prefix test, and records the move
// into the consumed lattice through recordMove.
func (c *checker) recordMovePlace(p movePlace, moveNode ast.Node, ref liveness.StmtRef) {
	id := c.placeID(p)
	c.fn.movePlaces[id] = p
	c.recordMove(id, moveNode, ref)
}

// recordMove marks varID consumed at ref. A second move of the same place at the same
// program point is an intra-statement reuse — `return [x, x]`, `f(x, x)`, the `dup`
// double move — so it is reported immediately as a use-after-move rather than waiting
// for the lattice, which is statement-granular and cannot order two moves within one
// statement.
func (c *checker) recordMove(varID liveness.VarID, moveNode ast.Node, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.moveSites == nil || varID <= 0 {
		return
	}
	at := c.fn.moveSites[ref]
	if at == nil {
		at = set.NewSet[liveness.VarID]()
		c.fn.moveSites[ref] = at
	}
	if at.Contains(varID) {
		c.report(&UseAfterMoveError{
			Name:     c.moveNameOf(varID),
			use:      moveNode,
			moveSite: c.fn.moveNodes[varID],
		})
		return
	}
	at.Add(varID)
	if c.fn.moveNodes == nil {
		c.fn.moveNodes = map[liveness.VarID]ast.Node{}
	}
	c.fn.moveNodes[varID] = moveNode
}

// checkUseAfterMoves runs the consumed-lattice dataflow over the function body's
// CFG and reports a UseAfterMoveError for every recorded read of a binding the
// lattice finds Moved or MaybeMoved at the read's program point. It runs once,
// after the whole body is walked, so a move on a later or loop-back path is
// already recorded when a use is checked.
//
// StateBefore reads the place's state just before the read's statement, so a move
// recorded AT that statement — the consume in `val q = p`, where reading p and
// moving it share one statement — does not flag its own source read.
//
// It also resolves the phase/exclusivity diagnostics against the same lattice:
// resolvePhaseTransitions emits each conflict the walk recorded unless the lattice
// finds its source moved on every path reaching the transition, where the
// use-after-move subsumes it.
func (c *checker) checkUseAfterMoves() {
	if c.fn == nil || c.fn.cfg == nil {
		return
	}
	if len(c.fn.useSites) == 0 && len(c.fn.moveSites) == 0 && len(c.fn.pendingTransitions) == 0 {
		return
	}
	info := liveness.AnalyzeMoves(c.fn.cfg, c.fn.moveSites)
	for _, u := range c.fn.useSites {
		state, movedID := c.movedConflict(info, u)
		if state == liveness.NotMoved {
			continue
		}
		// The read place is a partial-move read when it is a strict ancestor of the
		// moved place: reading the whole `pair` after `pair.a` moved exposes the moved
		// field. ReadName then names the read; otherwise the read is the moved place
		// itself or a field beneath it, and Name alone is enough.
		moved := c.fn.movePlaces[movedID]
		partial := len(u.place.path) < len(moved.path)
		readName := ""
		if partial {
			readName = c.renderPlace(u.place)
		}
		c.report(&UseAfterMoveError{
			Name:        c.moveNameOf(movedID),
			ReadName:    readName,
			Partial:     partial,
			Conditional: state == liveness.MaybeMoved,
			use:         u.node,
			moveSite:    c.fn.moveNodes[movedID],
		})
	}
	c.resolvePhaseTransitions(info)
}

// movedConflict finds the strongest consumed state among the moved places that
// conflict with the use, returning that state and the conflicting move's lattice
// VarID. A moved place conflicts when it shares the use's root and one path is a
// prefix of the other, so a use of `pair.a.id` conflicts with a move of `pair.a`, of
// `pair`, or of `pair.a.id`, while a use of `pair.b` conflicts with none of them. An
// unconditional Moved is preferred over a conditional MaybeMoved when both reach the
// use, so the diagnostic reports the definite use-after-move.
//
// When two conflicting moves share the strongest state — a whole-object read that
// exposes both moved `pair.a` and moved `pair.b` — the lower lattice id wins, which
// is the earlier move in source order, since the synthetic ids are allocated as the
// walk meets each place. The tiebreak makes the blamed move deterministic across the
// unordered movePlaces map iteration.
func (c *checker) movedConflict(info *liveness.MoveInfo, u moveUse) (liveness.MoveState, liveness.VarID) {
	best := liveness.NotMoved
	var bestID liveness.VarID
	for id, p := range c.fn.movePlaces {
		if p.root != u.place.root || !pathPrefixRelated(p.path, u.place.path) {
			continue
		}
		s := info.StateBefore(u.ref, id)
		if s == liveness.NotMoved {
			continue
		}
		// Keep this conflict only if it is a stronger blame target than the best so
		// far: the first one found, an unconditional Moved that supersedes a MaybeMoved,
		// or the same state with a lower lattice id, the tiebreak that makes the choice
		// deterministic across the unordered movePlaces map iteration.
		if !(best == liveness.NotMoved ||
			(s == liveness.Moved && best == liveness.MaybeMoved) ||
			(s == best && id < bestID)) {
			continue
		}
		best = s
		bestID = id
	}
	return best, bestID
}

// resolvePhaseTransitions emits the phase/exclusivity conflicts the walk recorded,
// deciding each against the now-complete consumed lattice. A mutable owned value sits
// in one of two phases that never overlap: an immutable phase with any number of `&`
// borrows, or a mutable phase with any number of `&mut` borrows. A recorded conflict
// is a borrow of the opposite phase still live across the transition.
//
// The lattice makes the decision path-sensitive:
//   - Moved on every path reaching the transition: the move engine already reports a
//     use-after-move at that point, which subsumes the conflict, so it is dropped.
//   - MaybeMoved, moved on some reaching paths but not all: the non-moving paths are a
//     real phase conflict, so the error is kept. The moving paths read as the
//     conditional use-after-move the move engine reports there, so both diagnostics
//     stand, each describing the paths it governs.
//   - NotMoved: an ordinary phase conflict, kept.
//
// The query is scoped to this body for free. Module-wide VarID numbering means this
// body's lattice never marks another body's source moved, so a conflict whose source
// lives in a different body reads NotMoved here and is kept.
func (c *checker) resolvePhaseTransitions(info *liveness.MoveInfo) {
	for _, e := range c.fn.pendingTransitions {
		if e.sourceVarID > 0 && info.StateBefore(e.ref, e.sourceVarID) == liveness.Moved {
			continue
		}
		c.errs = append(c.errs, e)
	}
	c.fn.pendingTransitions = nil
}
