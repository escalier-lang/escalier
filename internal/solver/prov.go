package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Prov is the inverse of Info: soltype.Type → the origin that minted it. Sparse.
// Shared, interned, or synthesized types may be absent, an honest miss rather
// than a lie, following the leaf/interior split. Keyed by pointer identity,
// exactly like Info.
//
// The FromAST leaf variant and the FromInstantiation interior edge are
// implemented. The remaining interior variants FromBoundPropagation,
// FromExtrusion, and FromCoalesce ride along with the operations that mint them.
// That is why Origin is an interface, so each adds a variant rather than changing
// the map's value type.
type Prov map[soltype.Type]Origin

// Origin is a tagged sum naming the kind of hop that minted a type. The FromAST
// leaf and FromInstantiation are implemented; the rest are deferred.
type Origin interface{ isOrigin() }

// FromAST is the leaf: a direct AST cause for a freshly-minted type.
type FromAST struct {
	Node ast.Node
	Kind ASTOriginKind
}

func (FromAST) isOrigin() {}

// FromInstantiation is the first interior edge. A variable minted by
// instantiating a polymorphic scheme through freshenAbove copied it from From, the
// pre-freshening type. It carries no AST node of its own. The renderer that chases
// the From chain back to the nearest AST leaf is not yet implemented, so NodeFor
// still resolves only FromAST. The edge is minted but nothing reads it yet.
type FromInstantiation struct {
	From soltype.Type
}

func (FromInstantiation) isOrigin() {}

// ASTOriginKind tags WHY a node minted a type, so a renderer/LSP can phrase blame
// ("the literal here", "this argument", "field `x`") without re-deriving it from
// the AST node's concrete type. Current blame only needs the node, so the kind is
// forward-looking metadata.
type ASTOriginKind int

const (
	LiteralInference   ASTOriginKind = iota // a LitType from inferLiteral
	ParamBinding                            // a fresh param var from inferFunc
	Application                             // a fresh call-result var from inferCall
	TupleElem                               // a TupleType from inferTuple
	ObjectField                             // an ObjectType from inferObject
	FuncInference                           // a FuncType from inferFunc (the function expr/decl)
	CallShape                               // the synthesized FuncType{args,res} from inferCall (the CallExpr)
	MemberAccess                            // a fresh member-result var from inferMember (recorded against the .prop ident)
	AnnotationType                          // a fresh PrimType from resolveTypeAnn (number/string/boolean)
	WildcardAnnotation                      // a fresh var from a `_` type annotation (resolveTypeAnn), the inner the surrounding annotation infers
	AwaitResult                             // a fresh `await`-result var from inferAwait
	PromiseWrap                             // a PromiseType minted by wrapping an async function's external return
	ReturnJoin                              // a fresh return-join var from inferFunc (the union of every return point)
	IfElseBranch                            // a fresh branch-join var from inferIfElse (the union of cons / alt)
	MatchBranch                             // a fresh branch-join var from inferMatch (the union of every arm body)
	BorrowExprOrigin                        // a RefType minted by inferBorrow from a `&p` / `&mut p` expression
)

// NodeResolver resolves an operand type to the AST node that minted it. Prov
// implements it as a single map lookup. A future resolver can chase interior
// Origin edges such as FromInstantiation to the nearest AST leaf without changing
// any caller, the "follow the provenance chain" path.
//
// Named NodeResolver rather than Provenance to avoid shadowing the imported
// `provenance` package / `provenance.Provenance` binding-source marker used
// elsewhere in this package (per CLAUDE.md).
type NodeResolver interface {
	NodeFor(soltype.Type) (ast.Node, bool)
}

// NodeFor returns the AST node that minted t, when one was recorded. An
// unrecorded operand is an honest miss, such as a Void result, a shared atom
// resolved elsewhere, or a synthesized type.
func (p Prov) NodeFor(t soltype.Type) (ast.Node, bool) {
	if o, ok := p[t].(FromAST); ok {
		return o.Node, true
	}
	return nil, false
}

// hasProv reports whether t already carries a FromAST origin in the Prov table.
func (c *checker) hasProv(t soltype.Type) bool {
	_, ok := c.prov[t].(FromAST)
	return ok
}

// recordProv records that t was minted from node n for reason kind — the inverse
// of recordType (info.setType). Sparse by intent: only the node-derived
// construction sites call it; synthesized types such as coalesced or extruded
// types get no entry. Called only in the walk, never in constrain/coalesce, so
// the hot inference path never consults Prov, the perf invariant.
//
// Invariant: every type passed here is a FRESHLY-minted, unique pointer, so a
// record never collides with a different node. Blame correctness depends on it —
// a reused/interned/coalesced pointer recorded twice would silently overwrite an
// earlier node and mis-blame with no crash. The c.debugProv guard makes that
// violation loud in tests (re-recording the same pointer against a *different*
// node panics); it stays off in production so a span bug can never crash the
// compiler. Re-recording the same node is idempotent and allowed.
func (c *checker) recordProv(t soltype.Type, n ast.Node, kind ASTOriginKind) {
	if c.debugProv {
		if prev, ok := c.prov[t].(FromAST); ok && prev.Node != n {
			panic(fmt.Sprintf("recordProv: type %p re-recorded against a different node (was %T, now %T) — the unique-pointer invariant is violated", t, prev.Node, n))
		}
	}
	c.snapshotProv(t)
	c.prov[t] = FromAST{Node: n, Kind: kind}
}

// snapshotProv registers a probe rollback for a pending write to t's Prov entry,
// so a discarded speculative trial leaves Prov exactly as it was. A no-op when no
// probe is open. Shared by both Prov writers (recordProv, recordInstantiation) so
// every speculative side-table write is reversible; delegates to the same
// snapshotMapEntry helper as recordType's Info write.
func (c *checker) snapshotProv(t soltype.Type) {
	snapshotMapEntry(c, c.prov, t)
}

// recordInstantiation records the FromInstantiation interior edge minted by
// freshenAbove: nv (a freshly-minted instantiation var) was copied from `from`.
// It routes through the same debugProv unique-pointer guard as recordProv rather
// than writing c.prov directly, so an accidental double-mint against an existing
// entry is loud in tests. nv is always fresh here, so the guard never fires in
// practice — it backstops a future change that reuses a pointer.
func (c *checker) recordInstantiation(nv *soltype.TypeVarType, from soltype.Type) {
	if c.debugProv {
		if prev, ok := c.prov[nv]; ok {
			panic(fmt.Sprintf("recordInstantiation: var %p already has provenance (%T) — the unique-pointer invariant is violated", nv, prev))
		}
	}
	c.snapshotProv(nv)
	c.prov[nv] = FromInstantiation{From: from}
}
