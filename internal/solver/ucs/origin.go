package ucs

import (
	"reflect"

	"github.com/escalier-lang/escalier/internal/ast"
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
	// Kind still names the construct the node was minted for. A consumer that needs
	// a span for a synthetic node takes it from the nearest enclosing real node or
	// emits none, rather than inventing a position the user cannot see.
	Synthetic bool
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
// own. kind names the construct it was minted for.
func Invented(kind OriginKind) Origin {
	return Origin{Kind: kind, Synthetic: true}
}

// Prov returns the origin itself. Every core and normalized node embeds Origin, so
// this method is what makes those nodes satisfy Term.
func (o Origin) Prov() Origin { return o }

// SourceSpan returns the span of the surface node the origin blames, and false when
// there is none. A synthetic node has no span, so a consumer falls back to the
// nearest enclosing real span or emits nothing rather than inventing a position.
func (o Origin) SourceSpan() (ast.Span, bool) {
	return SpanOf(o.Node)
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
