package ucs

import "github.com/escalier-lang/escalier/internal/ast"

// The three leaf types end a branch of the core.
func (*BodyLeaf) isCore()     {}
func (*EscapeLeaf) isCore()   {}
func (*FallbackLeaf) isCore() {}

// BodyLeaf ends a branch with the body the user wrote for it: a `match` arm's body,
// or the consequent or `else` of an `if val`. Its bindings are scoped to Body and do
// not escape.
type BodyLeaf struct {
	// Body is the arm body. BodySpan reads the span a diagnostic about the body
	// blames, which is narrower than the arm span Origin.Node carries.
	Body ast.BlockOrExpr
	// Arm is the surface arm this leaf came from. It survives the merging and
	// flattening that normalization does to the splits above it, so a message such
	// as "unreachable arm" blames the arm the user typed rather than a rewritten
	// node. Origin.Node can point at a synthesized node after those rewrites; Arm
	// never does.
	Arm Spanned
	Origin
}

// EscapeLeaf ends the success path of a `val pat = init else { … }` declaration. It
// carries no body, because the declaration's bindings escape into the enclosing
// block and the rest of that block is the continuation. Modelling the escape as its
// own leaf is what keeps the typing walk from looking for a body that a
// `val … else` never has.
type EscapeLeaf struct {
	// Arm is the surface `val` declaration this leaf came from. See BodyLeaf.Arm for
	// why the back-reference is kept separate from Origin.
	Arm Spanned
	Origin
}

// BodySpan returns the span of an arm body, and false when the body holds neither an
// expression nor a block. A body written as a bare expression spans that expression;
// a block spans its braces. A leaf's Origin points at the whole surface arm, so a
// diagnostic that should underline only the body reads its span from here.
func BodySpan(b ast.BlockOrExpr) (ast.Span, bool) {
	if b.Expr != nil {
		return b.Expr.Span(), true
	}
	if b.Block != nil {
		return b.Block.Span, true
	}
	return ast.Span{}, false
}

// FallbackLeaf ends the `else` path of a `val pat = init else { … }` declaration.
// The `else` either diverges or evaluates to a fallback value. It is a distinct leaf
// from BodyLeaf so the coverage check does not count it as an arm that covers the
// scrutinee. It runs precisely when the pattern failed to match.
type FallbackLeaf struct {
	// Body is the `else` block. BodySpan reads its span.
	Body ast.BlockOrExpr
	// Arm is the surface `val` declaration this leaf came from. See BodyLeaf.Arm for
	// why the back-reference is kept separate from Origin.
	Arm Spanned
	Origin
}
