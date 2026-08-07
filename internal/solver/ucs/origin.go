package ucs

import (
	"reflect"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// OriginKind names the surface flow-control form a node lowered from. Desugaring
// erases that distinction — `match`, `if val`, and `val … else` all become a split —
// so the tag is the only thing left that tells the three apart. A diagnostic reads
// it to choose its wording. An inexhaustive `match` asks for a catch-all branch,
// while an `if val` that cannot bind says the pattern may not match.
type OriginKind int

const (
	// OriginMatchArm is an arm of a `match` expression.
	OriginMatchArm OriginKind = iota
	// OriginIfVal is the pattern branch or the `else` of an `if val` expression.
	OriginIfVal
	// OriginValElse is the success path or the `else` of a
	// `val pat = init else { … }` declaration.
	OriginValElse
	// OriginGuard is an arm's `if` guard.
	OriginGuard
)

// String renders the kind as the phrase a diagnostic uses to name the construct.
func (k OriginKind) String() string {
	switch k {
	case OriginMatchArm:
		return "match arm"
	case OriginIfVal:
		return "if val"
	case OriginValElse:
		return "val else"
	case OriginGuard:
		return "guard"
	default:
		return "unknown origin"
	}
}

// Spanned is what the IR needs from a surface node: the span a diagnostic points
// at. It is narrower than ast.Node, which also demands an Accept method. The surface
// node a match arm lowers from is an *ast.MatchCase, and that type carries only a
// span, because a match arm is visited through its enclosing MatchExpr rather than
// on its own. Every ast.Node satisfies Spanned, so a caller can store one directly.
type Spanned interface {
	Span() ast.Span
}

// Origin is the diagnostics provenance every core and normalized node carries. It
// records which surface construct produced the node and which surface node a message
// about it should blame.
type Origin struct {
	// Kind names the surface construct the node lowered from.
	Kind OriginKind
	// Node is the surface node a diagnostic anchors its span to. It is nil on a
	// synthetic node, which has no source span. Read it through SourceSpan rather
	// than calling Node.Span() directly, so a missing node is a miss instead of a
	// panic.
	Node Spanned
	// Synthetic marks a node the desugarer minted rather than lowered from
	// something the user wrote, such as a fallthrough tail or an implicit `else`.
	// Kind still names the construct the node was minted for. A synthetic node has
	// no span of its own, so a consumer reads NearestSpan rather than inventing a
	// position the user cannot see.
	Synthetic bool
	// Cause is the origin this one was minted from, which is what makes a synthetic
	// node's provenance recoverable from the node alone. Following Cause reaches a
	// real surface node without walking the enclosing IR, so a diagnostic about an
	// implicit `else` can blame the `val … else` that produced it.
	//
	// It is set only on a synthetic origin, by InventedFrom, and is nil at the end
	// of the chain. A chain ends either at a real origin, whose Node a message can
	// blame, or at an Invented origin with no cause, which names no position at all.
	Cause *Origin
}

// At builds the origin of a node lowered from the surface node n, which is what a
// diagnostic about the node blames. Pass a real node; a node the desugarer invented
// gets its origin from Invented instead.
func At(kind OriginKind, n Spanned) Origin {
	if isNilSpanned(n) {
		return Invented(kind)
	}
	return Origin{Kind: kind, Node: n}
}

// Invented builds the origin of a node the desugarer minted with no source of its
// own and nothing to trace it back to. kind names the construct it was minted for.
// Prefer InventedFrom whenever the caller holds the origin it is minting from, since
// an origin built here can never yield a span.
func Invented(kind OriginKind) Origin {
	return Origin{Kind: kind, Synthetic: true}
}

// InventedFrom builds the origin of a node the desugarer minted while lowering
// cause. The node has no source text of its own, so it gets no Node, but cause keeps
// a real surface node reachable: NearestSpan follows the chain and returns the first
// span it finds. Lowering the implicit `else` of a `val … else` passes the
// declaration's origin, so a message about the invented tail blames the declaration.
func InventedFrom(kind OriginKind, cause Origin) Origin {
	return Origin{Kind: kind, Synthetic: true, Cause: &cause}
}

// Prov returns the origin itself. Every core and normalized node embeds Origin, so
// this method is what makes those nodes satisfy Term.
func (o Origin) Prov() Origin { return o }

// SourceSpan returns the span of the surface node this origin itself blames, and
// false when there is none. It does not follow the cause chain, so a synthetic origin
// always misses. Use it to ask whether the node maps to source text the user wrote;
// use NearestSpan to get a span to underline.
func (o Origin) SourceSpan() (ast.Span, bool) {
	return SpanOf(o.Node)
}

// NearestSpan returns the span of the closest real surface node this origin can
// reach, following Cause when the origin is synthetic, and false when the chain
// reaches its end without one. It is what a diagnostic underlines.
//
// The span it returns belongs to the construct the synthetic node was minted while
// lowering, which is wider than the node itself. Underlining the whole `val … else`
// for its invented tail is the intended behavior: the tail has no text of its own, so
// the declaration that produced it is the narrowest honest thing to point at.
//
// Cause is exported, so a caller can assign a chain that loops back on itself. The
// walk records the links it follows and stops on one it has already seen, which keeps
// a diagnostics path total. The set is keyed on the pointer rather than the Origin
// value, because Origin holds a Spanned whose dynamic type need not be comparable and
// `==` on two such values panics. It is allocated only once the walk follows a second
// link, so the common chain of one or two links costs nothing.
func (o Origin) NearestSpan() (ast.Span, bool) {
	var seen set.Set[*Origin]
	for cur := &o; cur != nil; cur = cur.Cause {
		if span, ok := SpanOf(cur.Node); ok {
			return span, true
		}
		if cur.Cause == nil {
			break
		}
		if seen == nil {
			seen = set.NewSet[*Origin]()
		}
		if seen.Contains(cur.Cause) {
			break
		}
		seen.Add(cur.Cause)
	}
	return ast.Span{}, false
}

// SpanOf returns n's span, and false when n names no node. It is the nil-safe way to
// read a Spanned field, including the surface-arm back-reference on a branch or leaf.
func SpanOf(n Spanned) (ast.Span, bool) {
	if isNilSpanned(n) {
		return ast.Span{}, false
	}
	return n.Span(), true
}

// isNilSpanned reports whether n holds no node. A plain `n == nil` catches only an
// empty interface, so it misses a nil pointer stored in one. A `(*ast.MatchCase)(nil)`
// assigned to a Spanned field compares non-nil and then panics on Span(). Reflection
// catches both cases, and every caller is on a diagnostics or printing path where the
// cost does not matter.
func isNilSpanned(n Spanned) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

// Term is any node of either IR: a core term, a normalized term, or a branch of
// either. Both ADTs' node interfaces embed it, so the printer and diagnostics can
// read provenance off a node without knowing which form they hold.
type Term interface {
	Prov() Origin
}
