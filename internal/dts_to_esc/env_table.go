package dts_to_esc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// env_table.go says which environments each generated declaration exists on,
// and stamps `@env` on the ones that are not everywhere.
//
// Two tables answer it. packageEnvs gives a package's declarations their
// default, and declEnvOverrides names the declarations that differ from their
// package. A declaration on every environment carries no decorator, since that
// is what an unannotated declaration already means.

// packageEnvs is the environments a package's declarations default to.
//
// It is total over the `web:*` packages a run emits, checked both ways, so a
// new package has to be classified rather than picking up "everywhere" by
// omission. A `std:*` package is the language surface and is everywhere, so the
// table says nothing about those.
//
// The claim is coarse and deliberately so. `web:cache` and `web:indexeddb` are
// reachable from a worker in reality, and saying so needs a source the
// repository does not hold, so both are marked for the window alone. Narrowing
// is the safe direction: the check asks whether a referent covers its referrer,
// so a referrer claiming less is never the cause of a report, and a referent
// claiming less produces one a reader can answer.
var packageEnvs = map[string]set.Set[Env]{
	"web:core": AllEnvs(),

	"web:fetch":       AllEnvs(),
	"web:url":         AllEnvs(),
	"web:streams":     AllEnvs(),
	"web:file":        AllEnvs(),
	"web:crypto":      AllEnvs(),
	"web:performance": AllEnvs(),
	"web:websocket":   AllEnvs(),
	"web:compression": AllEnvs(),
	"web:wasm":        AllEnvs(),

	"web:dom":            windowOnly(),
	"web:workers":        windowOnly(),
	"web:webgl":          windowOnly(),
	"web:web_audio":      windowOnly(),
	"web:web_rtc":        windowOnly(),
	"web:web_codecs":     windowOnly(),
	"web:indexeddb":      windowOnly(),
	"web:service_worker": windowOnly(),
	"web:push":           windowOnly(),
	"web:cache":          windowOnly(),
	"web:storage":        windowOnly(),
	"web:webauthn":       windowOnly(),
	"web:credentials":    windowOnly(),
	"web:payments":       windowOnly(),
}

func windowOnly() set.Set[Env] { return set.FromSlice([]Env{EnvWindow}) }

// declEnvOverrides names the declarations whose environments differ from their
// package's default, keyed by the declaration name.
//
// A package-wide default is coarse, and a declaration that contradicts it is
// what the override is for. Each entry records why, since the reason is what a
// reader needs to judge whether a TypeScript bump has invalidated it.
// It is empty today. `XMLHttpRequestBodyInit` was the one candidate, declared
// in `web:dom` and portable in substance, and it needs no entry because
// fetch.replace.esc keeps `web:fetch` from naming it. An entry earns its place
// when a reference across the package's boundary is worth its cost in closure
// size.
var declEnvOverrides = map[packageDecl]set.Set[Env]{}

// packageDecl addresses one declaration by the package holding it. The package
// is half the key because a bare name would widen a same-named declaration
// anywhere in the tree, which is the sort of reach nothing here should have.
type packageDecl struct {
	URI  string
	Name string
}

// PackageDeclEnvs returns the environments a declaration in uri exists on.
func PackageDeclEnvs(uri, name string) set.Set[Env] {
	return declEnvsFrom(declEnvOverrides, uri, name)
}

// declEnvsFrom is PackageDeclEnvs over a chosen override map, so a test reaches
// the override path while the committed one is empty.
func declEnvsFrom(overrides map[packageDecl]set.Set[Env], uri, name string) set.Set[Env] {
	if envs, held := overrides[packageDecl{URI: uri, Name: name}]; held {
		return envs
	}
	if envs, held := packageEnvs[uri]; held {
		return envs
	}
	return AllEnvs()
}

// AnnotateEnvs stamps `@env` on every declaration the tables narrow, and
// reports a package or declaration the tables name that the tree does not hold.
//
// A declaration on every environment is left alone. Annotating it would say
// what an unannotated declaration already says, and would put a decorator on
// most of the tree for no reader's benefit.
func AnnotateEnvs(mods map[string]*StandaloneModule) error {
	if err := checkEnvTablesMatchTheTree(mods); err != nil {
		return err
	}
	everywhere := AllEnvs()
	var scanErr error
	for _, uri := range sortedURIs(mods) {
		mods[uri].Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
			for _, decl := range ns.Decls {
				names := ast.DeclNames(decl)
				if len(names) == 0 {
					continue
				}
				// An overlay may write `@env` itself, to say something about a
				// declaration the tables cannot. That annotation wins, and stamping
				// beside it would leave two for CheckEnvs to reject.
				if hasEnvDecorator(decl) {
					continue
				}
				envs, err := declaredEnvs(uri, names)
				if err != nil {
					scanErr = err
					return false
				}
				if envs.Equals(everywhere) {
					continue
				}
				attachEnvDecorator(decl, envs)
			}
			return true
		})
		if scanErr != nil {
			return scanErr
		}
	}
	return nil
}

// declaredEnvs returns the environments one declaration exists on, given every
// name it binds.
//
// A destructuring `val` binds several names, and EnvIndex records the
// declaration's set against each of them. Resolving against the first alone
// would let a second name's override go unread here while the index honoured
// it, so one decorator has to answer for every name and the names have to
// agree.
func declaredEnvs(uri string, names []string) (set.Set[Env], error) {
	return declaredEnvsFrom(declEnvOverrides, uri, names)
}

// declaredEnvsFrom is declaredEnvs over a chosen override map, so a test reaches
// the disagreement while the committed one is empty.
func declaredEnvsFrom(
	overrides map[packageDecl]set.Set[Env], uri string, names []string,
) (set.Set[Env], error) {
	envs := declEnvsFrom(overrides, uri, names[0])
	for _, name := range names[1:] {
		if other := declEnvsFrom(overrides, uri, name); !other.Equals(envs) {
			return nil, fmt.Errorf(
				"converter: %s: one declaration binds %q and %q with different "+
					"environments; a decorator answers for the whole declaration, so "+
					"give them one entry or split the declaration",
				uri, names[0], name)
		}
	}
	return envs, nil
}

// hasEnvDecorator reports whether a declaration already carries `@env`.
func hasEnvDecorator(decl ast.Decl) bool {
	for _, dec := range ast.DeclDecorators(decl) {
		if dec.Name != nil && dec.Name.Name == EnvDecoratorName {
			return true
		}
	}
	return false
}

// attachEnvDecorator prepends `@env(...)` to a declaration's decorator list, so
// it prints above `@js` the way a modifier keyword prints below both.
func attachEnvDecorator(decl ast.Decl, envs set.Set[Env]) {
	args := make([]ast.Expr, 0, envs.Len())
	for _, env := range sortedEnvs(envs) {
		args = append(args, ast.NewLitExpr(ast.NewString(string(env), ast.Span{})))
	}
	dec := &ast.Decorator{Name: ast.NewIdentifier(EnvDecoratorName, ast.Span{}), Args: args}
	switch d := decl.(type) {
	case *ast.VarDecl:
		d.Decorators = append([]*ast.Decorator{dec}, d.Decorators...)
	case *ast.FuncDecl:
		d.Decorators = append([]*ast.Decorator{dec}, d.Decorators...)
	case *ast.ClassDecl:
		d.Decorators = append([]*ast.Decorator{dec}, d.Decorators...)
	case *ast.TypeDecl:
		d.Decorators = append([]*ast.Decorator{dec}, d.Decorators...)
	case *ast.InterfaceDecl:
		d.Decorators = append([]*ast.Decorator{dec}, d.Decorators...)
	case *ast.EnumDecl:
		d.Decorators = append([]*ast.Decorator{dec}, d.Decorators...)
	}
}

// checkEnvTablesMatchTheTree reports a `web:*` package the run emits that
// packageEnvs does not classify.
//
// Only this direction is checked here, because a run over part of the lib set
// emits part of the tree and an entry for a package it did not reach is not
// stale. The other direction, an entry naming something the whole tree does not
// hold, is a property of the pinned lib set and
// TestEnvTablesMatchThePinnedLibSet reads it there.
func checkEnvTablesMatchTheTree(mods map[string]*StandaloneModule) error {
	var missing []string
	for _, uri := range sortedURIs(mods) {
		if SchemeOf(uri) != "web" {
			continue
		}
		if _, held := packageEnvs[uri]; !held {
			missing = append(missing, uri)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf(
		"converter: packageEnvs has no entry for %s; say which environments each "+
			"exists on in internal/dts_to_esc/env_table.go",
		strings.Join(missing, ", "))
}

// StaleEnvTableEntries returns each table entry naming a package or declaration
// mods does not hold, sorted. It answers against a whole-tree run, which is
// where an entry a TypeScript bump left behind shows up.
func StaleEnvTableEntries(mods map[string]*StandaloneModule) []string {
	var stale []string
	for uri := range packageEnvs {
		if _, held := mods[uri]; !held {
			stale = append(stale, fmt.Sprintf("packageEnvs names %s", uri))
		}
	}
	for key := range declEnvOverrides {
		mod, held := mods[key.URI]
		if !held {
			stale = append(stale, fmt.Sprintf(
				"declEnvOverrides names %s, which the tree does not hold", key.URI))
			continue
		}
		if !DeclaredNames(mod.Module).Contains(key.Name) {
			stale = append(stale, fmt.Sprintf(
				"declEnvOverrides names %q, which %s does not declare", key.Name, key.URI))
		}
	}
	sort.Strings(stale)
	return stale
}

// libEnvs is the environments a lib file's declarations exist on.
//
// TypeScript ships the window surface and the worker surface as alternatives a
// `tsconfig.json` picks between, so which file declared a member is the upstream
// statement of where it exists. A member both files declare exists in both.
//
// A file named here narrows; every other lib file is the language surface and
// exists everywhere, which is why the `lib.es*` set is absent.
var libEnvs = map[string]set.Set[Env]{
	"lib.dom.d.ts":                     windowOnly(),
	"lib.dom.iterable.d.ts":            windowOnly(),
	"lib.dom.asynciterable.d.ts":       windowOnly(),
	"lib.webworker.d.ts":               workerKinds(),
	"lib.webworker.importscripts.d.ts": workerKinds(),
	"lib.webworker.iterable.d.ts":      workerKinds(),
	"lib.webworker.asynciterable.d.ts": workerKinds(),
}

func workerKinds() set.Set[Env] {
	return set.FromSlice(envGroups["worker"])
}

// MemberEnvsFromLibs returns the environments a converted member exists on,
// read from the lib files that declared it.
//
// A member two files declare carries both, which dedupeMembers recorded on the
// way through. A member one file declared carries its own span's id alone. A
// member the converter synthesized has no lib and exists wherever its
// declaration does, which is what the nil return says.
//
// This is the lib set's reading and is not reconciled with packageEnvs. The two
// disagree, and the lib set is the better source: over a conversion of
// lib.dom.d.ts and lib.webworker.d.ts together, 2058 of 7762 members read as
// available outside the environments their package claims, most of them in
// `web:webgl` and `web:dom`. packageEnvs says both are a window's, and its own
// comment says that is for want of a source. Reconciling them is what #1633
// does when it undrops the worker lib; until then the worker lib contributes
// nothing and every member of a `web:*` package reads as a window's, which is
// what packageEnvs already says.
//
// TestTheLibReadingDisagreesWithThePackageTable measures the gap, so the
// reconciliation has a number to work against.
func MemberEnvsFromLibs(
	mod *StandaloneModule, member ast.Node, sourceFiles map[int]string,
) set.Set[Env] {
	ids := mod.MemberSources[member]
	if ids == nil {
		ids = set.FromSlice([]int{member.Span().SourceID})
	}
	envs := set.NewSet[Env]()
	for _, id := range ids.ToSlice() {
		file, held := sourceFiles[id]
		if !held {
			// A span the converter minted, so the member belongs to whatever
			// its declaration does rather than to a lib.
			return nil
		}
		narrowed, named := libEnvs[file]
		if !named {
			// A language lib, which every environment has.
			return nil
		}
		envs = envs.Union(narrowed)
	}
	if envs.Len() == 0 {
		return nil
	}
	return envs
}
