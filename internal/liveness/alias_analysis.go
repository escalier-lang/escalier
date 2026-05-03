package liveness

import (
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// ProjectionStep names one step from a root variable to a leaf slot inside
// a freshly-constructed composite value. The empty-path case (a leaf with
// no steps) means the leaf is the root itself — this is today's "this
// expression aliases VarID X" behavior.
type ProjectionStep interface{ projectionStep() }

// ElementOf is the step into an array literal element ([a, b]).
type ElementOf struct{}

// PropertyOf is the step into an object property ({k: a}).
type PropertyOf struct{ Key string }

// IndexOf is the step into a fixed tuple slot.
type IndexOf struct{ Index int }

// AwaitOf is the step through a Promise<T> value (await p).
type AwaitOf struct{}

// CastOf is the step through a type cast (p as T) — pass-through.
type CastOf struct{}

func (ElementOf) projectionStep()  {}
func (PropertyOf) projectionStep() {}
func (IndexOf) projectionStep()    {}
func (AwaitOf) projectionStep()    {}
func (CastOf) projectionStep()     {}

// AliasLeaf is one (root, path) pair contributing a lifetime to a specific
// slot of a surrounding fresh container.
type AliasLeaf struct {
	RootVarID VarID
	// Path is the sequence of projection steps from the root into the
	// freshly-constructed container surrounding this expression. An empty
	// path means the leaf is the root itself (the legacy single-var case).
	Path []ProjectionStep
}

// AliasSourceKind describes the kind of alias source an expression
// represents. Derived from the (Leaves, Fresh) shape — kept as a typed
// value so call sites can switch on it.
type AliasSourceKind int

const (
	AliasSourceFresh    AliasSourceKind = iota // new value, no alias
	AliasSourceVariable                        // aliases a specific variable
	AliasSourceMultiple                        // aliases one of several variables (conditional)
	AliasSourceUnknown                         // cannot determine statically
)

// AliasOrigin classifies whether the *root* of an expression's value is
// freshly constructed or an alias of an existing value. The leaf set
// describes nested slot aliases, but the origin determines whether the
// root itself participates in aliasing — this distinction matters for
// the alias tracker, which only merges sets at the root level.
type AliasOrigin int

const (
	// AliasOriginUnknown means the alias status of the value cannot be
	// determined statically. Zero value.
	AliasOriginUnknown AliasOrigin = iota
	// AliasOriginFresh means the value's root is brand-new — e.g. a
	// literal, an array literal, an object literal, a call result, a
	// function expression. Leaves on a fresh-origin source describe
	// aliasing of nested slots only; the root itself aliases nothing.
	AliasOriginFresh
	// AliasOriginAlias means the value is (or projects from) an
	// existing variable. The root aliases the leaf roots; leaf paths
	// describe *where in those roots* the value points. A direct
	// variable reference is the canonical case (single leaf, empty path).
	AliasOriginAlias
)

// AliasSource describes where a value comes from for alias tracking
// purposes. As of Phase 8.9, an alias source has both an Origin (root
// classification) and a *set* of leaves — each leaf names a root variable
// plus a projection path. The two carry complementary information:
//
//   - For Origin=Alias, leaves describe the (root, slot-in-root) the
//     expression refers to. `obj` is one leaf with empty path; `obj.k`
//     is one leaf with `[PropertyOf("k")]`.
//   - For Origin=Fresh, leaves describe (param-root, slot-in-new-container)
//     that the freshly-constructed root *captures*. `[a, b]` has two
//     leaves with `[IndexOf(0)]` and `[IndexOf(1)]` rooted at `a`/`b`.
//     The new container itself aliases nothing at the root.
type AliasSource struct {
	Origin AliasOrigin
	Leaves []AliasLeaf
}

// Kind returns a root-level view of the alias source for consumers
// whose data model is "which variable IDs does this value alias at the
// root?" — the alias tracker, transition checking, and the static-escape
// propagation, none of which understand projection paths. A fresh-origin
// value reports Fresh regardless of whether it has nested-slot leaves
// (the new container's root aliases nothing, even if `[a, b]` captures
// `a` and `b` at element slots); only Alias-origin leaves contribute to
// the Variable/Multiple classification.
//
// Lifetime attachment uses the path-aware Origin + Leaves view directly.
// Both views are load-bearing — neither is a backward-compat shim.
func (s AliasSource) RootKind() AliasSourceKind {
	switch s.Origin {
	case AliasOriginFresh:
		return AliasSourceFresh
	case AliasOriginUnknown:
		return AliasSourceUnknown
	}
	switch len(s.Leaves) {
	case 0:
		return AliasSourceUnknown
	case 1:
		return AliasSourceVariable
	default:
		return AliasSourceMultiple
	}
}

// UniqueVarIDs returns the deduplicated list of root variable IDs
// across all leaves, in leaf order. Provided for callers that only
// care about the flat root set (e.g. alias-set merging in the
// checker), so they don't have to dedupe themselves when the same
// root appears under multiple slot paths.
func (s AliasSource) UniqueVarIDs() []VarID {
	if len(s.Leaves) == 0 {
		return nil
	}
	seen := set.NewSet[VarID]()
	out := make([]VarID, 0, len(s.Leaves))
	for _, leaf := range s.Leaves {
		if seen.Contains(leaf.RootVarID) {
			continue
		}
		seen.Add(leaf.RootVarID)
		out = append(out, leaf.RootVarID)
	}
	return out
}

// freshSource is a small constructor for the common "definitely fresh"
// case so call sites stay readable.
func freshSource() AliasSource { return AliasSource{Origin: AliasOriginFresh} }

// freshContainerSource builds a fresh-origin source carrying nested-slot
// leaves — e.g. `[a, b]` → fresh root, two leaves with paths into the
// new container.
func freshContainerSource(leaves []AliasLeaf) AliasSource {
	return AliasSource{Origin: AliasOriginFresh, Leaves: leaves}
}

// unknownSource is the zero value — kept as a constructor for parity with
// freshSource so the intent is explicit at the call site.
func unknownSource() AliasSource { return AliasSource{} }

// rootSource builds an AliasSource for a single direct variable
// reference (empty projection path).
func rootSource(id VarID) AliasSource {
	return AliasSource{
		Origin: AliasOriginAlias,
		Leaves: []AliasLeaf{{RootVarID: id}},
	}
}

// DetermineAliasSource examines an expression and returns its alias source.
// When the expression is an IdentExpr, the VarID is read directly from the
// node (set by the rename pass in Phase 2).
func DetermineAliasSource(expr ast.Expr) AliasSource {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		if e.VarID > 0 {
			return rootSource(VarID(e.VarID))
		}
		// Non-local variable (VarID <= 0) — treat as unknown since we
		// can't track aliases across function boundaries.
		return unknownSource()

	// Fresh values: literals, object/array construction, function expressions
	case *ast.LiteralExpr:
		return freshSource()
	case *ast.ObjectExpr:
		return determineObjectAliasSource(e)
	case *ast.TupleExpr:
		return determineTupleAliasSource(e)
	case *ast.FuncExpr:
		return freshSource()
	case *ast.TemplateLitExpr:
		return freshSource()
	case *ast.TaggedTemplateLitExpr:
		return freshSource()
	case *ast.JSXElementExpr:
		return freshSource()
	case *ast.JSXFragmentExpr:
		return freshSource()

	// Function calls: treat as fresh for now (Phase 8 adds lifetime-based tracking)
	case *ast.CallExpr:
		return freshSource()

	// Unary/binary operations produce fresh primitive values
	case *ast.UnaryExpr:
		return freshSource()
	case *ast.BinaryExpr:
		return freshSource()

	// Type cast: pure pass-through. The Phase 8.9 spec models a CastOf
	// projection step, but it carries no slot information; treating it as
	// transparent matches today's behavior and keeps paths shorter.
	case *ast.TypeCastExpr:
		return DetermineAliasSource(e.Expr)

	// Await: append AwaitOf to each leaf so that lifetime attachment can
	// descend into Promise<T>'s inner T.
	case *ast.AwaitExpr:
		return appendStepToLeaves(DetermineAliasSource(e.Arg), AwaitOf{})

	// Property access: the value projects into the object. Append
	// PropertyOf(name) so each leaf records the additional descent.
	case *ast.MemberExpr:
		return appendStepToLeaves(DetermineAliasSource(e.Object), PropertyOf{Key: e.Prop.Name})
	case *ast.IndexExpr:
		return determineIndexAliasSource(e)

	// Conditionals: aliases all branches (Phase 7.4).
	case *ast.IfElseExpr:
		return determineConditionalAliasSource(e)
	case *ast.IfLetExpr:
		return unknownSource()
	case *ast.MatchExpr:
		return determineMatchAliasSource(e)

	// Do expressions, try-catch: complex control flow, treat as unknown
	case *ast.DoExpr:
		return unknownSource()
	case *ast.TryCatchExpr:
		return unknownSource()

	// Throw/yield don't produce values that get assigned
	case *ast.ThrowExpr:
		return freshSource()
	case *ast.YieldExpr:
		return unknownSource()

	// Array spread
	case *ast.ArraySpreadExpr:
		return freshSource()

	// Error expression
	case *ast.ErrorExpr:
		return unknownSource()

	default:
		return unknownSource()
	}
}

// blockResultExpr returns the result expression of a block (the last
// statement if it's an ExprStmt), or nil if the block is empty or ends
// with a non-expression statement.
func blockResultExpr(b ast.Block) ast.Expr {
	if len(b.Stmts) == 0 {
		return nil
	}
	if exprStmt, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt); ok {
		return exprStmt.Expr
	}
	return nil
}

// blockOrExprResultExpr returns the result expression from a BlockOrExpr.
func blockOrExprResultExpr(boe *ast.BlockOrExpr) ast.Expr {
	if boe == nil {
		return nil
	}
	if boe.Expr != nil {
		return boe.Expr
	}
	if boe.Block != nil {
		return blockResultExpr(*boe.Block)
	}
	return nil
}

// collectBranchSources collects alias sources from a list of expressions,
// deduplicating leaves across branches by (root, projection path) so that
// fresh-container shapes like {head:a} vs {tail:a} (same root, different
// slot) are preserved. The merged Origin is Fresh when every contributing
// branch's value root is fresh; Alias when at least one branch's root
// itself aliases an existing variable.
func collectBranchSources(exprs []ast.Expr) AliasSource {
	seen := set.NewSet[string]()
	var leaves []AliasLeaf
	allFresh := true
	hasAliasOrigin := false

	for _, expr := range exprs {
		if expr == nil {
			// A branch with no result expression — treat as unknown.
			return unknownSource()
		}
		source := DetermineAliasSource(expr)
		if len(source.Leaves) > 0 {
			allFresh = false
			if source.Origin == AliasOriginAlias {
				hasAliasOrigin = true
			}
			for _, leaf := range source.Leaves {
				key := leafKey(leaf.RootVarID, leaf.Path)
				if seen.Contains(key) {
					continue
				}
				seen.Add(key)
				leaves = append(leaves, leaf)
			}
		} else if source.Origin != AliasOriginFresh {
			// Unknown — treat like fresh for alias purposes. We can't
			// determine what this branch aliases, but that's no reason
			// to discard alias info from the branches we DO know about.
			allFresh = false
		}
	}

	if len(leaves) == 0 {
		if allFresh {
			return freshSource()
		}
		return unknownSource()
	}
	// If every contributing branch produced a fresh-rooted value, the
	// merged result is also fresh-rooted: leaf paths describe descents
	// into the fresh container, and lifetime attachment must follow them
	// into the return type. If any branch's root itself aliases an
	// existing variable, the merged root aliases too — the legacy
	// Variable/Multiple kind matters there.
	origin := AliasOriginFresh
	if hasAliasOrigin {
		origin = AliasOriginAlias
	}
	return AliasSource{Origin: origin, Leaves: leaves}
}

// determineConditionalAliasSource determines alias sources for an if-else
// expression by collecting sources from both branches.
func determineConditionalAliasSource(expr *ast.IfElseExpr) AliasSource {
	consExpr := blockResultExpr(expr.Cons)
	altExpr := blockOrExprResultExpr(expr.Alt)

	// If there's no alt branch, the else produces undefined (a fresh
	// value). Only the consequent may contribute alias sources.
	if expr.Alt == nil {
		return collectBranchSources([]ast.Expr{consExpr})
	}

	return collectBranchSources([]ast.Expr{consExpr, altExpr})
}

// appendStepToLeaves returns a new AliasSource whose leaves each have
// the given step appended to their projection path. Origin is preserved
// — descending through `obj.field` keeps the alias-of-existing-root
// classification, and descending through `await fresh` keeps fresh.
// Sources with no leaves (Fresh or Unknown with no captures) pass
// through unchanged.
func appendStepToLeaves(src AliasSource, step ProjectionStep) AliasSource {
	if len(src.Leaves) == 0 {
		return src
	}
	out := AliasSource{Origin: src.Origin, Leaves: make([]AliasLeaf, len(src.Leaves))}
	for i, leaf := range src.Leaves {
		newPath := make([]ProjectionStep, len(leaf.Path)+1)
		copy(newPath, leaf.Path)
		newPath[len(leaf.Path)] = step
		out.Leaves[i] = AliasLeaf{RootVarID: leaf.RootVarID, Path: newPath}
	}
	return out
}

// determineTupleAliasSource computes the alias source for a tuple/array
// literal `[e0, e1, ...]`. The root is freshly constructed; each element's
// leaves are folded in with `IndexOf(i)` prepended to their existing path.
// The lifetime-attachment side decides whether to interpret these as
// element-of-array or per-slot tuple positions based on the surrounding
// type.
func determineTupleAliasSource(expr *ast.TupleExpr) AliasSource {
	var leaves []AliasLeaf
	seen := set.NewSet[string]()
	for i, elem := range expr.Elems {
		if elem == nil {
			continue
		}
		child := DetermineAliasSource(elem)
		for _, leaf := range child.Leaves {
			newPath := make([]ProjectionStep, 0, len(leaf.Path)+1)
			newPath = append(newPath, IndexOf{Index: i})
			newPath = append(newPath, leaf.Path...)
			key := leafKey(leaf.RootVarID, newPath)
			if seen.Contains(key) {
				continue
			}
			seen.Add(key)
			leaves = append(leaves, AliasLeaf{RootVarID: leaf.RootVarID, Path: newPath})
		}
	}
	if len(leaves) == 0 {
		return freshSource()
	}
	return freshContainerSource(leaves)
}

// determineObjectAliasSource computes the alias source for an object
// literal `{k0: e0, k1: e1, ...}`. The root is freshly constructed; each
// property value's leaves are folded in with `PropertyOf(key)` prepended.
// Spread elements, methods, getters/setters, and computed keys without
// a static name are skipped — their alias contributions can't be slotted
// to a known property of the new container.
func determineObjectAliasSource(expr *ast.ObjectExpr) AliasSource {
	var leaves []AliasLeaf
	seen := set.NewSet[string]()
	for _, elem := range expr.Elems {
		prop, ok := elem.(*ast.PropertyExpr)
		if !ok || prop.Value == nil {
			continue
		}
		key, ok := staticPropertyKey(prop.Name)
		if !ok {
			continue
		}
		child := DetermineAliasSource(prop.Value)
		for _, leaf := range child.Leaves {
			newPath := make([]ProjectionStep, 0, len(leaf.Path)+1)
			newPath = append(newPath, PropertyOf{Key: key})
			newPath = append(newPath, leaf.Path...)
			leafK := leafKey(leaf.RootVarID, newPath)
			if seen.Contains(leafK) {
				continue
			}
			seen.Add(leafK)
			leaves = append(leaves, AliasLeaf{RootVarID: leaf.RootVarID, Path: newPath})
		}
	}
	if len(leaves) == 0 {
		return freshSource()
	}
	return freshContainerSource(leaves)
}

// leafKey returns a stable string representation of a (root, path) pair
// suitable for use as a deduplication key. Two leaves with the same root
// and equivalent path produce equal keys.
func leafKey(root VarID, path []ProjectionStep) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(int(root)))
	b.WriteByte('|')
	b.WriteString(PathKey(path))
	return b.String()
}

// PathKey returns a deterministic, collision-free string encoding of a
// projection path. Property keys are length-prefixed so user-supplied
// strings containing the step delimiter ('|') or step-tag prefix ('p:')
// can't collide with a path of two property steps that happen to render
// the same when concatenated.
func PathKey(path []ProjectionStep) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	for i, step := range path {
		if i > 0 {
			b.WriteByte('|')
		}
		switch s := step.(type) {
		case ElementOf:
			b.WriteString("e")
		case PropertyOf:
			b.WriteString("p:")
			b.WriteString(strconv.Itoa(len(s.Key)))
			b.WriteByte(':')
			b.WriteString(s.Key)
		case IndexOf:
			b.WriteString("i:")
			b.WriteString(strconv.Itoa(s.Index))
		case AwaitOf:
			b.WriteString("a")
		case CastOf:
			b.WriteString("c")
		}
	}
	return b.String()
}

// staticPropertyKey returns the static string form of an object key, or
// false if the key is computed (and thus not statically known).
func staticPropertyKey(k ast.ObjKey) (string, bool) {
	switch n := k.(type) {
	case *ast.IdentExpr:
		return n.Name, true
	case *ast.StrLit:
		return n.Value, true
	case *ast.NumLit:
		// Numeric keys are valid object keys; stringify for the path.
		// Use Go's default float formatting — adequate for path equality
		// since both sides go through the same conversion.
		return formatNumKey(n.Value), true
	}
	return "", false
}

// formatNumKey stringifies a numeric object key. Kept separate so the
// formatting choice has one place to live if it later needs to align
// with another stringifier (e.g. printer output).
func formatNumKey(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// determineIndexAliasSource computes the alias source for `obj[i]`. The
// expression projects into obj; we append `IndexOf(i)` only when the
// index is a constant integer literal (the only case where we can name
// the slot statically). Non-constant indexes fall back to a transparent
// descent into the object — the same conservative approximation as
// before Phase 8.9.
func determineIndexAliasSource(expr *ast.IndexExpr) AliasSource {
	src := DetermineAliasSource(expr.Object)
	if lit, ok := expr.Index.(*ast.LiteralExpr); ok {
		if num, ok := lit.Lit.(*ast.NumLit); ok {
			if i, isInt := floatAsInt(num.Value); isInt {
				return appendStepToLeaves(src, IndexOf{Index: i})
			}
		}
	}
	return src
}

// floatAsInt returns the int form of a float64 if it represents a
// non-negative integer index, and false otherwise.
func floatAsInt(v float64) (int, bool) {
	i := int(v)
	if float64(i) == v && i >= 0 {
		return i, true
	}
	return 0, false
}

// determineMatchAliasSource determines alias sources for a match expression
// by collecting sources from all case bodies.
func determineMatchAliasSource(expr *ast.MatchExpr) AliasSource {
	if len(expr.Cases) == 0 {
		return unknownSource()
	}

	branchExprs := make([]ast.Expr, len(expr.Cases))
	for i, matchCase := range expr.Cases {
		branchExprs[i] = blockOrExprResultExpr(&matchCase.Body)
	}

	return collectBranchSources(branchExprs)
}
