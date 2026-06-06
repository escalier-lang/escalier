package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Prov is the inverse of Info: soltype.Type → the origin that minted it. Sparse —
// shared/interned/synthesized types may be absent (an honest miss, not a lie: see
// the leaf/interior split in planning/simple_sub/m2.5-implementation-plan.md §3.8).
// Keyed by pointer identity, exactly like Info.
//
// M2.5 ships only the FromAST leaf variant. The interior edge variants
// (FromBoundPropagation/FromInstantiation/FromExtrusion/FromCoalesce) ride along
// with the M3+ operations that mint them — which is why Origin is an interface,
// so M3 adds a variant rather than changing the map's value type.
type Prov map[soltype.Type]Origin

// Origin is a tagged sum naming the kind of hop that minted a type. M2.5 ships only
// the FromAST leaf; the interior edge variants are deferred to M3+.
type Origin interface{ isOrigin() }

// FromAST is the leaf: a direct AST cause for a freshly-minted type.
type FromAST struct {
	Node ast.Node
	Kind ASTOriginKind
}

func (FromAST) isOrigin() {}

// ASTOriginKind tags WHY a node minted a type, so a renderer/LSP can phrase blame
// ("the literal here", "this argument", "field `x`") without re-deriving it from
// the AST node's concrete type. M2.5's blame only needs the node, so the kind is
// forward-looking metadata.
type ASTOriginKind int

const (
	LiteralInference ASTOriginKind = iota // a LitType from inferLiteral
	ParamBinding                          // a fresh param var from inferFunc
	Application                           // a fresh call-result var from inferCall
	TupleElem                             // a TupleType from inferTuple
	ObjectField                           // a RecordType from inferObject
	FuncInference                         // a FuncType from inferFunc (the function expr/decl)
	CallShape                             // the synthesized FuncType{args,res} from inferCall (the CallExpr)
	MemberAccess                          // a fresh member-result var from inferMember (recorded against the .prop ident)
	AnnotationType                        // a fresh PrimType from resolveTypeAnn (number/string/boolean)
)

// NodeResolver resolves an operand type to the AST node that minted it. M2.5's
// Prov implements it as a single map lookup; M3+ can supply a resolver that
// chases interior Origin edges (FromInstantiation, …) to the nearest AST leaf
// without changing any caller — that is the "follow the provenance chain" path.
//
// Named NodeResolver rather than Provenance to avoid shadowing the imported
// `provenance` package / `provenance.Provenance` binding-source marker used
// elsewhere in this package (per CLAUDE.md).
type NodeResolver interface {
	NodeFor(soltype.Type) (ast.Node, bool)
}

// NodeFor returns the AST node that minted t, when one was recorded. An
// unrecorded operand (a Void result, a shared atom resolved elsewhere, or an
// M3+ synthesized type) is an honest miss.
func (p Prov) NodeFor(t soltype.Type) (ast.Node, bool) {
	if o, ok := p[t].(FromAST); ok {
		return o.Node, true
	}
	return nil, false
}

// recordProv records that t was minted from node n for reason kind — the inverse
// of recordType (info.setType). Sparse by intent: only the node-derived
// construction sites call it; synthesized types (coalesced/extruded, M3+) get no
// entry. Called only in the walk (construction), never in constrain/coalesce, so
// the hot inference path never consults Prov (the perf invariant, §3.9).
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
	c.prov[t] = FromAST{Node: n, Kind: kind}
}
