package dts_to_esc

import (
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/webidl"
)

// transferableAlias is the name of the union TypeScript writes for the values
// `postMessage` and `structuredClone` can transfer.
const transferableAlias = "Transferable"

// deriveTransferable replaces the arms of each `web:*` bucket's top-level
// `type Transferable = ...` with the arms defs gives it, and returns the
// buckets with that one statement swapped. A bucket without the alias is
// returned unchanged, and the input slices are not modified.
//
// The derived union lists every interface defs marks `[Transferable]` that some
// bucket declares at its top level, sorted by name, followed by each arm of the
// TypeScript union that names no WebIDL definition at all. `ArrayBuffer` is the
// one such arm, since ECMAScript and not WebIDL makes it transferable. An arm
// TypeScript lists that WebIDL does not mark `[Transferable]` is dropped.
//
// A `[Transferable]` interface no bucket declares has no type to name, so it is
// left out.
func deriveTransferable(
	buckets map[string][]dts_parser.Statement,
	defs map[string]webidl.Definition,
) map[string][]dts_parser.Statement {
	declared := declaredNames(buckets)
	out := make(map[string][]dts_parser.Statement, len(buckets))
	for uri, stmts := range buckets {
		out[uri] = stmts
		if !strings.HasPrefix(uri, "web:") {
			continue
		}
		for i, stmt := range stmts {
			td, ok := stmt.(*dts_parser.TypeDecl)
			if !ok || td.Name.Name != transferableAlias {
				continue
			}
			union, ok := td.TypeAnn.(*dts_parser.UnionType)
			if !ok {
				continue
			}
			derived := *union
			derived.Types = transferableArms(union.Types, union.Span(), declared, defs)
			alias := *td
			alias.TypeAnn = &derived
			replaced := make([]dts_parser.Statement, len(stmts))
			copy(replaced, stmts)
			replaced[i] = &alias
			out[uri] = replaced
		}
	}
	return out
}

// transferableArms returns the arms deriveTransferable gives the union whose
// TypeScript arms are tsArms. An arm TypeScript does not write takes span, the
// union's own, so a diagnostic about it points at the union it joined.
func transferableArms(
	tsArms []dts_parser.TypeAnn,
	span ast.Span,
	declared set.Set[string],
	defs map[string]webidl.Definition,
) []dts_parser.TypeAnn {
	// An arm TypeScript already writes keeps its node, so it keeps its span.
	written := map[string]dts_parser.TypeAnn{}
	for _, arm := range tsArms {
		if ref, ok := arm.(*dts_parser.TypeReference); ok {
			written[typeRefName(ref)] = arm
		}
	}

	var arms []dts_parser.TypeAnn
	for _, name := range transferableNames(defs) {
		if !declared.Contains(name) {
			continue
		}
		if arm, ok := written[name]; ok {
			arms = append(arms, arm)
			continue
		}
		arms = append(arms, &dts_parser.TypeReference{Name: dts_parser.NewIdent(name, span)})
	}
	for _, arm := range tsArms {
		ref, ok := arm.(*dts_parser.TypeReference)
		if !ok {
			arms = append(arms, arm)
			continue
		}
		if _, isWebIDL := defs[typeRefName(ref)]; !isWebIDL {
			arms = append(arms, arm)
		}
	}
	return arms
}

// transferableNames returns the names of the interfaces defs marks
// `[Transferable]`, sorted.
func transferableNames(defs map[string]webidl.Definition) []string {
	var names []string
	for name, def := range defs {
		if def.Transferable && def.Kind == webidl.KindInterface {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// declaredNames returns the name of every interface and class the buckets
// declare at their top level, which are the types a union arm can name.
func declaredNames(buckets map[string][]dts_parser.Statement) set.Set[string] {
	names := set.NewSet[string]()
	for _, stmts := range buckets {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *dts_parser.InterfaceDecl:
				names.Add(s.Name.Name)
			case *dts_parser.ClassDecl:
				names.Add(s.Name.Name)
			}
		}
	}
	return names
}
