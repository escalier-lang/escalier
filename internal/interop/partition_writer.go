package interop

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/tidwall/btree"
)

// LibInput is one `.d.ts` file fed to PartitionLib.
//
// SourceFile is the basename used for routing decisions (e.g.
// "lib.dom.d.ts" — see Route and DOMResidualSources). Module is the
// already-parsed dts module.
type LibInput struct {
	SourceFile string
	Module     *dts_parser.Module
}

// PartitionResult is the bucketed output of PartitionLib: top-level
// dts statements grouped by their target pseudo-package, plus the
// dropped-name notes the converter logs per §6.1.
//
// Buckets is keyed by package URI ("std:array", "web:dom", …) so
// callers can iterate in a stable order via PackageList. Within each
// bucket, statements are kept in the order they were routed (source-
// file order, then statement order within a file), then interface-
// and namespace-merged so trio detection sees one declaration per
// name.
type PartitionResult struct {
	Buckets map[string][]dts_parser.Statement

	// Drops records (name, source-file basename) pairs for every
	// top-level declaration routed through ExplicitDrops. Callers may
	// surface these as informational warnings — they are intentional,
	// not errors.
	Drops []DropNote
}

// DropNote is one entry in PartitionResult.Drops.
type DropNote struct {
	Name       string
	SourceFile string
}

// PartitionLib routes every top-level declaration across the inputs
// into its target package per Route. Returns an error on the first
// unmapped symbol (§6.1 fail-safe).
//
// Inputs are processed in the order given so that interface-merge and
// namespace-merge results are stable. Routing keys off the input's
// SourceFile, not where a declaration physically lives in a nested
// namespace — top-level `declare namespace Intl { ... }` routes as a
// single unit to std:intl regardless of which lib file declared it.
func PartitionLib(inputs []LibInput) (*PartitionResult, error) {
	out := &PartitionResult{
		Buckets: make(map[string][]dts_parser.Statement),
	}
	for _, in := range inputs {
		if in.Module == nil {
			return nil, fmt.Errorf("partition: nil module for %s", in.SourceFile)
		}
		for _, stmt := range in.Module.Statements {
			name := topLevelName(stmt)
			if name == "" {
				// Statement carries no addressable top-level name
				// (ImportDecl, NamedExportStmt, ExportAllStmt, etc.).
				// The standalone converter already drops these for
				// MVP — keep parity here so PartitionLib doesn't push
				// unroutable statements into a bucket.
				continue
			}
			res := Route(name, in.SourceFile)
			switch {
			case res.Drop:
				out.Drops = append(out.Drops, DropNote{Name: name, SourceFile: in.SourceFile})
				continue
			case res.Unmapped:
				return nil, UnmappedError(name, in.SourceFile)
			}
			out.Buckets[res.Pkg.URI] = append(out.Buckets[res.Pkg.URI], stmt)
		}
	}
	for uri, stmts := range out.Buckets {
		out.Buckets[uri] = mergeDecls(stmts)
	}
	return out, nil
}

// topLevelName returns the addressable name of a top-level statement,
// or "" for statements that carry none (ImportDecl, re-export forms,
// ambient module/global blocks).
func topLevelName(stmt dts_parser.Statement) string {
	switch s := stmt.(type) {
	case *dts_parser.VarDecl:
		return s.Name.Name
	case *dts_parser.FuncDecl:
		return s.Name.Name
	case *dts_parser.ClassDecl:
		return s.Name.Name
	case *dts_parser.InterfaceDecl:
		return s.Name.Name
	case *dts_parser.TypeDecl:
		return s.Name.Name
	case *dts_parser.EnumDecl:
		return s.Name.Name
	case *dts_parser.NamespaceDecl:
		return s.Name.Name
	}
	return ""
}

// mergeDecls performs TS-style declaration merging within a routed
// bucket: same-named InterfaceDecl entries collapse into a single
// InterfaceDecl whose Members is the concatenation; same-named
// NamespaceDecl entries collapse into a single NamespaceDecl whose
// Statements is the concatenation (then recursively merged). The
// first occurrence's doc, span, and type-params are kept; later
// duplicates' metadata is dropped.
//
// This is how the converter handles the canonical TS-shipping pattern
// where the *same* interface is declared across multiple `lib.*.d.ts`
// files to layer in new spec revisions. For example, `interface
// Array<T>` is declared in:
//
//   - lib.es5.d.ts                  (push, pop, slice, map, filter, …)
//   - lib.es2015.core.d.ts          (find, findIndex, fill, copyWithin, …)
//   - lib.es2015.iterable.d.ts      (entries, keys, values, [Symbol.iterator])
//   - lib.es2015.symbol.wellknown.d.ts ([Symbol.unscopables])
//   - lib.es2016.array.include.d.ts (includes)
//   - lib.es2022.array.d.ts         (at)
//   - lib.es2023.array.d.ts         (findLast, toReversed, toSorted, with, …)
//
// All seven `interface Array<T> { … }` declarations route to the same
// std:array bucket; mergeDecls concatenates their `Members` slices in
// statement order so that by the time `detectTrios` runs there is
// exactly one `interface Array<T>` (with every method from every lib
// year), one merged `interface ArrayConstructor`, and one `declare var
// Array: ArrayConstructor`. Trio fusion then produces a single
// `class Array<T>` with the union of all members.
//
// The same pattern applies to `Date`, `String`, `Number`, `Math`,
// `Object`, `Promise`, `RegExp`, the typed-array families, `JSON`,
// `Intl`, `Reflect`, `Symbol`, etc. — anywhere TS layers spec
// revisions across multiple lib files, mergeDecls collapses them.
//
// Other declaration kinds are not merged — a duplicated `declare var`
// / `declare class` across lib files is a TS-side error that we let
// fall through to the converter as two decls (the trio detector and
// later passes will flag it).
//
// Statement order is preserved by keeping each merged entry at its
// first occurrence's index.
func mergeDecls(stmts []dts_parser.Statement) []dts_parser.Statement {
	out := make([]dts_parser.Statement, 0, len(stmts))
	ifaceIdx := make(map[string]int) // name → index into out
	nsIdx := make(map[string]int)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dts_parser.InterfaceDecl:
			if i, ok := ifaceIdx[s.Name.Name]; ok {
				existing := out[i].(*dts_parser.InterfaceDecl)
				existing.Members = append(existing.Members, s.Members...)
				// Extends is concatenated without structural dedup. In
				// practice, TS lib augmentation files add members, not
				// extends clauses (only the initial declaration carries
				// the inheritance chain), so duplicates don't arise from
				// the pinned corpus. If that ever changes, dedup here on
				// a printed form of the TypeAnn.
				existing.Extends = append(existing.Extends, s.Extends...)
				continue
			}
			ifaceIdx[s.Name.Name] = len(out)
			out = append(out, s)
		case *dts_parser.NamespaceDecl:
			if i, ok := nsIdx[s.Name.Name]; ok {
				existing := out[i].(*dts_parser.NamespaceDecl)
				existing.Statements = append(existing.Statements, s.Statements...)
				continue
			}
			nsIdx[s.Name.Name] = len(out)
			out = append(out, s)
		default:
			out = append(out, stmt)
		}
	}
	// Recursively merge inside namespaces.
	for _, stmt := range out {
		if ns, ok := stmt.(*dts_parser.NamespaceDecl); ok {
			ns.Statements = mergeDecls(ns.Statements)
		}
	}
	return out
}

// ConvertBucket runs the standalone-module conversion on a single
// routed bucket. The synthetic Module wraps the bucket's statements
// without a source path — every emitted decl carries its `@js("...")`
// derived from the original dts name, independent of where the file
// is written.
//
// Pre-conversion: same-name interfaces and namespaces are already
// concatenated by mergeDecls (PartitionLib). Here we additionally fuse
// `interface ReadonlyFoo` + `interface Foo` pairs into a single class
// `Foo` — readonly-twin members are appended to `Foo`'s member list if
// missing, and the standalone module's emitted class is then post-
// processed so that each method whose name appears on the twin gets a
// non-mutating `self` receiver (twins are positive evidence the method
// does not mutate, the same contract `mergeReadonlyVariant` in
// [internal/checker/prelude.go](../../internal/checker/prelude.go)
// established for the legacy prelude path).
//
// `ReadonlyFoo` itself is dropped from emission; a `type ReadonlyFoo<…>
// = Foo<…>` alias is synthesised in its place so user code that names
// the readonly variant in a type position still resolves.
//
// After conversion, rewriteReadonlyTwinRefs walks every TypeAnn slot
// in the emitted module and rewrites references to the twin names so
// they match Escalier's mut-modifier model: TS `ReadonlyFoo<…>` becomes
// `Foo<…>` (immutable view), TS `Foo<…>` becomes `mut Foo<…>` (mutable
// view). `T[]` is already desugared to `Array<T>` by convertTypeAnn so
// it flows through the same rewrite; `readonly T[]` is desugared to
// `ReadonlyArray<T>` for the same reason.
func ConvertBucket(stmts []dts_parser.Statement) (*StandaloneModule, error) {
	stmts, twins := fuseReadonlyTwins(stmts)
	mod, err := ConvertToStandaloneModule(&dts_parser.Module{Statements: stmts})
	if err != nil {
		return nil, err
	}
	applyReadonlyTwinReceivers(mod, twins)
	rewriteReadonlyTwinRefs(mod, twins)
	appendReadonlyAliases(mod, twins)
	return mod, nil
}

// readonlyTwin records one (Foo, ReadonlyFoo) pair detected at bucket
// scope. `nonMutatingNames` is the set of member names that appear on
// ReadonlyFoo — presence on the readonly twin is positive evidence the
// member does not mutate, so post-processing flips those members'
// receivers from `mut self` to `self` on the emitted class. Members on
// Foo whose names are absent from this set are left as `mut self`.
type readonlyTwin struct {
	mutableName      string
	readonlyName     string
	typeParams       []*dts_parser.TypeParam
	nonMutatingNames set.Set[string]
}

// fuseReadonlyTwins detects every (Foo, ReadonlyFoo) interface pair in
// the bucket. For each pair:
//
//   - Members of ReadonlyFoo that do not appear by name on Foo are
//     appended to Foo's member list (so trio fusion picks them up on
//     the emitted class).
//   - The ReadonlyFoo InterfaceDecl is removed from the returned
//     statement slice. A synthesised `type ReadonlyFoo<…> = Foo<…>`
//     alias is emitted later by appendReadonlyAliases so the readonly
//     name still resolves in user code.
//
// Returns the adjusted statements and the per-class twin records the
// post-processor needs.
//
// Mutates input: the mutable InterfaceDecl pointers in `stmts` have
// their `Members` slices extended in place. Callers must not reuse
// `stmts` (or the InterfaceDecl pointers within it) after this call
// expecting the original member lists. Today the only caller is
// ConvertBucket, which feeds the result straight into the converter
// and discards it.
func fuseReadonlyTwins(stmts []dts_parser.Statement) ([]dts_parser.Statement, []readonlyTwin) {
	ifaceByName := make(map[string]*dts_parser.InterfaceDecl, len(stmts))
	for _, stmt := range stmts {
		if iface, ok := stmt.(*dts_parser.InterfaceDecl); ok {
			ifaceByName[iface.Name.Name] = iface
		}
	}

	var twins []readonlyTwin
	dropReadonly := set.NewSet[string]()
	// Iterate stmts (not ifaceByName) so detection order is deterministic.
	// Map iteration would randomize the order in which twins are
	// appended, which downstream determines the order ReadonlyFoo
	// members are folded onto Foo (mutates Members) and the order of
	// emitted `type ReadonlyFoo = Foo` aliases in appendReadonlyAliases —
	// both producing diff churn on committed converter output between
	// runs.
	for _, stmt := range stmts {
		iface, ok := stmt.(*dts_parser.InterfaceDecl)
		if !ok {
			continue
		}
		name := iface.Name.Name
		mutableName, found := strings.CutPrefix(name, "Readonly")
		if !found || mutableName == "" {
			continue
		}
		mutable, ok := ifaceByName[mutableName]
		if !ok {
			continue
		}

		// Build the name set of the readonly side, fold any missing
		// members onto the mutable side.
		nonMutatingNames := set.NewSet[string]()
		haveOnMutable := set.NewSet[string]()
		for _, m := range mutable.Members {
			if key := memberKey(m); key != "" {
				haveOnMutable.Add(key)
			}
		}
		for _, m := range iface.Members {
			key := memberKey(m)
			if key == "" {
				continue
			}
			nonMutatingNames.Add(key)
			if !haveOnMutable.Contains(key) {
				mutable.Members = append(mutable.Members, m)
				haveOnMutable.Add(key)
			}
		}

		twins = append(twins, readonlyTwin{
			mutableName:      mutableName,
			readonlyName:     name,
			typeParams:       iface.TypeParams,
			nonMutatingNames: nonMutatingNames,
		})
		dropReadonly.Add(name)
	}

	if dropReadonly.Len() == 0 {
		return stmts, nil
	}
	out := stmts[:0:len(stmts)]
	for _, stmt := range stmts {
		if iface, ok := stmt.(*dts_parser.InterfaceDecl); ok && dropReadonly.Contains(iface.Name.Name) {
			continue
		}
		out = append(out, stmt)
	}
	return out, twins
}

// memberKey returns the addressable name of an interface member for
// twin-name deduping. Computed keys like `[Symbol.iterator]` are
// stringified to their dotted form ("Symbol.iterator") so symbol-
// keyed methods participate in the Readonly merge (the legacy prelude
// path keyed on `ObjTypeKey` which covered both string and symbol
// names; this string form is the source-level equivalent). Returns ""
// for keyless members (call/construct/index signatures) — those don't
// participate in the Readonly merge and are never appended.
func memberKey(m dts_parser.InterfaceMember) string {
	switch s := m.(type) {
	case *dts_parser.MethodSignature:
		return propertyKeyString(s.Name)
	case *dts_parser.PropertySignature:
		return propertyKeyString(s.Name)
	case *dts_parser.GetterSignature:
		return propertyKeyString(s.Name)
	case *dts_parser.SetterSignature:
		return propertyKeyString(s.Name)
	}
	return ""
}

// propertyKeyString stringifies a PropertyKey for member-name matching.
// Plain idents and string literals return their text; ComputedKey
// expressions are restricted to well-known-symbol member access
// (`[Symbol.iterator]`, `[Symbol.asyncIterator]`, …) and returned in
// dotted form ("Symbol.iterator"). Any other computed-key shape
// panics: every computed key in the pinned TS lib corpus is a
// `Symbol.*` access, so a new shape is a corpus-change canary —
// silently returning "" would drop the member from the readonly-twin
// fold and miscategorise its receiver. If a legitimate non-Symbol
// shape ever shows up, extend this function (and exprDottedName) to
// cover it explicitly.
func propertyKeyString(pk dts_parser.PropertyKey) string {
	switch k := pk.(type) {
	case *dts_parser.Ident:
		return k.Name
	case *dts_parser.StringLiteral:
		return k.Value
	case *dts_parser.ComputedKey:
		dotted := exprDottedName(k.Expr)
		if strings.HasPrefix(dotted, "Symbol.") {
			return dotted
		}
		panic(fmt.Sprintf(
			"propertyKeyString: computed key %T is not a well-known-symbol "+
				"member access (got %q from expr %T); every computed key in "+
				"the pinned TS lib is `[Symbol.*]` — extend "+
				"propertyKeyString/exprDottedName to cover this new shape, "+
				"otherwise the member would be silently dropped from the "+
				"readonly-twin fold",
			pk, dotted, k.Expr))
	}
	return ""
}

// exprDottedName returns the dotted form of a member-access chain
// ("Symbol.iterator", "Foo.Bar.Baz") rooted at an identifier. Returns
// "" for shapes that can't be reduced to a simple chain.
func exprDottedName(e dts_parser.Expr) string {
	switch n := e.(type) {
	case *dts_parser.IdentExpr:
		return n.Name
	case *dts_parser.MemberExpr:
		left := exprDottedName(n.Object)
		if left == "" {
			return ""
		}
		return left + "." + n.Prop.Name
	}
	return ""
}

// applyReadonlyTwinReceivers walks the top-level Decls of every
// namespace in the standalone module and, for any ClassDecl whose
// name matches a twin's mutableName, sets `Receiver.Mut = false` on
// each instance MethodElem whose name appears in the twin's
// nonMutatingNames set. Static members and non-method elems are left
// alone.
//
// Only top-level classes within each namespace are visited — a class
// nested inside another class or further down inside another decl
// won't be inspected. That's fine for the current corpus: every
// readonly twin in the pinned TS lib (ReadonlyArray, ReadonlyMap,
// ReadonlySet, plus their mutable siblings) is declared at the top
// level. If a future twin lands inside a namespace member, this pass
// will need to recurse.
func applyReadonlyTwinReceivers(mod *StandaloneModule, twins []readonlyTwin) {
	if len(twins) == 0 {
		return
	}
	byName := make(map[string]readonlyTwin, len(twins))
	for _, t := range twins {
		byName[t.mutableName] = t
	}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			cd, ok := decl.(*ast.ClassDecl)
			if !ok {
				continue
			}
			twin, ok := byName[cd.Name.Name]
			if !ok {
				continue
			}
			for _, elem := range cd.Body {
				me, ok := elem.(*ast.MethodElem)
				if !ok || me.Static || me.Receiver == nil {
					continue
				}
				name := classElemName(me.Name)
				if name == "" {
					continue
				}
				if twin.nonMutatingNames.Contains(name) {
					me.Receiver.Mut = false
				}
			}
		}
		return true
	})
}

// classElemName extracts the textual name from a class-elem name slot
// for twin-name matching. Plain idents and string literals return
// their text; ComputedKey expressions are restricted to well-known-
// symbol member access (`[Symbol.iterator]`, …) and returned in
// dotted form ("Symbol.iterator"). Any other computed-key shape
// panics — same corpus-change-canary rationale as propertyKeyString:
// every computed key in the pinned TS lib is `Symbol.*`, so a new
// shape would silently dodge the receiver-flip pass and miscategorise
// the method. Extend this function (and astExprDottedName) if a
// legitimate non-Symbol shape ever shows up.
func classElemName(name ast.ObjKey) string {
	switch k := name.(type) {
	case *ast.IdentExpr:
		return k.Name
	case *ast.StrLit:
		return k.Value
	case *ast.ComputedKey:
		dotted := astExprDottedName(k.Expr)
		if strings.HasPrefix(dotted, "Symbol.") {
			return dotted
		}
		panic(fmt.Sprintf(
			"classElemName: computed key %T is not a well-known-symbol "+
				"member access (got %q from expr %T); every computed key in "+
				"the pinned TS lib is `[Symbol.*]` — extend "+
				"classElemName/astExprDottedName to cover this new shape, "+
				"otherwise the method would silently bypass the "+
				"readonly-twin receiver-flip pass",
			name, dotted, k.Expr))
	}
	return ""
}

// astExprDottedName is the ast-side mirror of exprDottedName.
func astExprDottedName(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.MemberExpr:
		left := astExprDottedName(n.Object)
		if left == "" {
			return ""
		}
		return left + "." + n.Prop.Name
	}
	return ""
}

// appendReadonlyAliases adds `type ReadonlyFoo<…> = Foo<…>` to the
// module's root namespace for each twin. The alias carries the same
// type-parameter signature as the original readonly interface so user
// code that wrote `ReadonlyArray<number>` still type-checks. No
// `@js("...")` decorator: the readonly name has no runtime referent
// (it is a type-only alias for the mutable class).
//
// TODO(#668): once reference sites are rewritten, decide whether to keep
// this alias as a TS-migration aid or drop it (no converter-emitted code
// would reference the readonly name anymore).
func appendReadonlyAliases(mod *StandaloneModule, twins []readonlyTwin) {
	if len(twins) == 0 {
		return
	}
	root, ok := mod.Module.Namespaces.Get("")
	if !ok {
		return
	}
	for _, twin := range twins {
		typeParams, err := convertTypeParams(twin.typeParams)
		if err != nil {
			// Intentional silent drop. In Escalier's mutability model
			// `Array<T>` corresponds to TS's `ReadonlyArray<T>` and
			// `mut Array<T>` corresponds to TS's `Array<T>` — the
			// `ReadonlyFoo` alias only exists as a migration aid for
			// users coming from TS source. Keeping `ReadonlyFoo`
			// resolvable when we can synthesise it is helpful; failing
			// to synthesise it is fine because the canonical Escalier
			// spelling is `Foo` (immutable) anyway. Better the readonly
			// name go missing than the whole bucket fail to emit.
			continue
		}
		// Build `Foo<T, …>` reference using the same param names.
		args := make([]ast.TypeAnn, 0, len(typeParams))
		for _, tp := range typeParams {
			args = append(args, ast.NewRefTypeAnn(
				ast.NewIdentifier(tp.Name, ast.Span{}),
				nil, ast.Span{}))
		}
		alias := ast.NewTypeDecl(
			ast.NewIdentifier(twin.readonlyName, ast.Span{}),
			typeParams,
			ast.NewRefTypeAnn(
				ast.NewIdentifier(twin.mutableName, ast.Span{}),
				args, ast.Span{}),
			true, // export
			true, // declare
			ast.Span{},
		)
		root.Decls = append(root.Decls, alias)
	}
}

// WritePartitionedTree converts each bucket and writes it to
// `outDir/<pkg.File>` (e.g. outDir/std/array.esc, outDir/web/dom.esc).
// Empty buckets are skipped. Parent directories (std/, web/) are
// created as needed; existing files are overwritten (the caller — §7
// bootstrap or §6.4 re-run — owns the policy decision about whether
// to overwrite hand-edits).
//
// Returns a sorted list of the URIs written, for caller-side reporting.
func WritePartitionedTree(result *PartitionResult, outDir string) ([]string, error) {
	uris := make([]string, 0, len(result.Buckets))
	for uri := range result.Buckets {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	written := make([]string, 0, len(uris))
	for _, uri := range uris {
		pkg, ok := PackageForURI(uri)
		if !ok {
			return nil, fmt.Errorf("WritePartitionedTree: unknown package URI %q "+
				"(every bucket should come from Route, which only returns "+
				"URIs in PackageList)", uri)
		}
		mod, err := ConvertBucket(result.Buckets[uri])
		if err != nil {
			return nil, fmt.Errorf("converting bucket %s: %w", uri, err)
		}
		dest := filepath.Join(outDir, filepath.FromSlash(pkg.File))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("creating package dir for %s: %w", uri, err)
		}
		f, err := os.Create(dest)
		if err != nil {
			return nil, fmt.Errorf("creating %s: %w", dest, err)
		}
		writeErr := WriteStandaloneModule(mod, f)
		closeErr := f.Close()
		if writeErr != nil {
			return nil, fmt.Errorf("writing %s: %w", dest, writeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing %s: %w", dest, closeErr)
		}
		written = append(written, uri)
	}
	return written, nil
}

// ScaffoldNodeDir creates outDir/node/ with a README explaining that
// the directory is reserved for future Node.js runtime APIs and ships
// without `.esc` files. Per §6.1/§6.3 the subtree is intentionally
// unpopulated; the README documents *why* it exists so readers don't
// mistake it for an unfinished migration.
func ScaffoldNodeDir(outDir string) error {
	dir := filepath.Join(outDir, "node")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating node dir: %w", err)
	}
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); err == nil {
		return nil
	}
	const body = "# `node:*` pseudo-package scheme\n\n" +
		"Reserved for Node.js runtime APIs. No `.esc` files are populated yet.\n"
	return os.WriteFile(readme, []byte(body), 0o644)
}

// DiscoverLibFiles returns the basenames of every `lib.*.d.ts` file in
// dir that the converter should ingest. The pinned TS install lives in
// node_modules/.pnpm/typescript@<ver>/node_modules/typescript/lib; the
// playground keeps a smaller curated copy under playground/public/types/.
// Both layouts pass any `lib.*.d.ts` basename — there is no upstream
// list to consult, so "everything matching the glob, sorted" is the
// convention.
//
// `lib.*.full.d.ts` aggregator files (lib.es2018.full.d.ts, …) are
// excluded — they are TS-side bundles that triple-slash-include the
// per-year files, contributing zero new declarations of their own.
// Ingesting them would route every Array/Promise member twice.
func DiscoverLibFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading lib dir %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "lib.") || !strings.HasSuffix(name, ".d.ts") {
			continue
		}
		if strings.HasSuffix(name, ".full.d.ts") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// ParseLibFiles reads and parses every name in basenames as a dts
// module rooted at dir. Returns one LibInput per file in the same
// order. Per-file parse errors are joined into a single error with
// the offending filenames; the caller decides whether to proceed.
func ParseLibFiles(dir string, basenames []string) ([]LibInput, error) {
	var inputs []LibInput
	var parseErrs []string
	for _, name := range basenames {
		path := filepath.Join(dir, name)
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		source := &ast.Source{Path: path, Contents: string(contents)}
		p := dts_parser.NewDtsParser(source)
		mod, errs := p.ParseModule()
		if len(errs) > 0 {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", name, errs))
			continue
		}
		inputs = append(inputs, LibInput{SourceFile: name, Module: mod})
	}
	if len(parseErrs) > 0 {
		return inputs, fmt.Errorf("parse errors: %s", strings.Join(parseErrs, "; "))
	}
	return inputs, nil
}

// ReportPartition prints a short summary of a PartitionResult to w:
// per-package decl count, drop count. Used by the CLI to give the
// operator a sense of what landed where without dumping the full
// output. The btree.Map keeps the package list sorted.
func ReportPartition(result *PartitionResult, w io.Writer) error {
	counts := btree.Map[string, int]{}
	for uri, stmts := range result.Buckets {
		counts.Set(uri, len(stmts))
	}
	var iterErr error
	counts.Scan(func(uri string, n int) bool {
		if _, err := fmt.Fprintf(w, "  %s: %d decls\n", uri, n); err != nil {
			iterErr = err
			return false
		}
		return true
	})
	if iterErr != nil {
		return iterErr
	}
	if len(result.Drops) > 0 {
		if _, err := fmt.Fprintf(w, "  drops: %d (", len(result.Drops)); err != nil {
			return err
		}
		names := make([]string, 0, len(result.Drops))
		seen := map[string]bool{}
		for _, d := range result.Drops {
			if !seen[d.Name] {
				seen[d.Name] = true
				names = append(names, d.Name)
			}
		}
		sort.Strings(names)
		if _, err := fmt.Fprintf(w, "%s)\n", strings.Join(names, ", ")); err != nil {
			return err
		}
	}
	return nil
}
