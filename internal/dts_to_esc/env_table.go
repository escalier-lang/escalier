package dts_to_esc

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// env_table.go says which environments each generated declaration exists on,
// and stamps `@env` on the ones that are not everywhere.
//
// Two sources answer it. A package's file name gives its declarations their
// default, and declEnvOverrides names the declarations that differ from their
// package. A declaration on every environment carries no decorator, since that
// is what an unannotated declaration already means.

// packageFileEnvs returns the environments a package's declarations default
// to, read from the name of the file the partition table writes it to.
//
// `web/dom.window.esc` says a page is the only place `web:dom` exists, and
// `web/fetch.esc` carries no suffix, so `web:fetch` exists everywhere. Putting
// the answer in the name means the tree states it where a reader already is,
// rather than in a table they have to find.
//
// The claim is coarse and deliberately so. `web:cache` and `web:indexeddb` are
// reachable from a worker in reality, and saying so needs a source the
// repository does not hold, so both are named for the window alone. Narrowing
// is the safe direction: the check asks whether a referent covers its referrer,
// so a referrer claiming less is never the cause of a report, and a referent
// claiming less produces one a reader can answer.
//
// A `std:*` package is the language surface and is everywhere, so none of them
// carries a suffix.
func packageFileEnvs(uri string) (set.Set[Env], error) {
	pkg, held := PackageForURI(uri)
	if !held {
		return nil, fmt.Errorf(
			"converter: %s is in no partition entry, so nothing names its file", uri)
	}
	return envsFromFileName(pkg.File)
}

// envsFromFileName reads the environments a package file's name claims. A name
// with no suffix claims every environment.
//
// The suffix is dot-separated, so `web/foo.window.service_worker.esc` names
// two. Each part is an environment or a group, the same vocabulary `@env`
// takes, since one spelling for the idea is what keeps them comparable.
func envsFromFileName(file string) (set.Set[Env], error) {
	stem := strings.TrimSuffix(filepath.Base(file), ".esc")
	_, suffix, found := strings.Cut(stem, ".")
	if !found {
		return AllEnvs(), nil
	}
	envs := set.NewSet[Env]()
	for _, part := range strings.Split(suffix, ".") {
		named, ok := envsNamed(part)
		if !ok {
			return nil, fmt.Errorf(
				"converter: %s names the environment %q, which is not one of %s",
				file, part, knownEnvNames())
		}
		envs = envs.Union(named)
	}
	return envs, nil
}

func windowOnly() set.Set[Env] { return set.FromSlice([]Env{EnvWindow}) }

// declEnvOverrides names the declarations whose environments differ from their
// package's default, keyed by the declaration name.
//
// A package-wide default is coarse, and a declaration that contradicts it is
// what the override is for. Each entry records why, since the reason is what a
// reader needs to judge whether a TypeScript bump has invalidated it.
//
// Every entry today separates the three worker kinds. TypeScript ships one
// lib.webworker.d.ts for all of them, so the lib reading answers "some worker"
// and cannot say which. What distinguishes them is the scope class a
// declaration belongs to, and its event map is what says which events that
// scope receives. These entries carry that reading, which no file in the lib
// set states.
var declEnvOverrides = map[packageDecl]set.Set[Env]{
	// A dedicated worker's scope, and the encoded-transform surface only it
	// receives. `DedicatedWorkerGlobalScopeEventMap` is what declares
	// `rtctransform`, so the three RTC declarations go with it.
	{"web:worker", "DedicatedWorkerGlobalScope"}:         dedicatedWorkerOnly(),
	{"web:worker", "DedicatedWorkerGlobalScopeEventMap"}: dedicatedWorkerOnly(),
	{"web:worker", "RTCTransformEvent"}:                  dedicatedWorkerOnly(),
	{"web:worker", "RTCRtpScriptTransformer"}:            dedicatedWorkerOnly(),
	{"web:worker", "onrtctransform"}:                     dedicatedWorkerOnly(),

	// A shared worker's scope. `connect` is the one event its map adds, and it
	// carries no type the other scopes lack.
	{"web:worker", "SharedWorkerGlobalScope"}:         sharedWorkerOnly(),
	{"web:worker", "SharedWorkerGlobalScopeEventMap"}: sharedWorkerOnly(),

	// A service worker's scope, the clients only it reaches, and the events only
	// its map declares. A page never receives a fetch, push or notification
	// event, and neither does a dedicated or shared worker.
	{"web:worker", "ServiceWorkerGlobalScope"}:         serviceWorkerOnly(),
	{"web:worker", "ServiceWorkerGlobalScopeEventMap"}: serviceWorkerOnly(),
	{"web:worker", "Client"}:                           serviceWorkerOnly(),
	{"web:worker", "Clients"}:                          serviceWorkerOnly(),
	{"web:worker", "WindowClient"}:                     serviceWorkerOnly(),
	{"web:worker", "ExtendableEvent"}:                  serviceWorkerOnly(),
	{"web:worker", "ExtendableEventInit"}:              serviceWorkerOnly(),
	{"web:worker", "ExtendableMessageEvent"}:           serviceWorkerOnly(),
	{"web:worker", "ExtendableMessageEventInit"}:       serviceWorkerOnly(),
	{"web:worker", "FetchEvent"}:                       serviceWorkerOnly(),
	{"web:worker", "FetchEventInit"}:                   serviceWorkerOnly(),
	{"web:worker", "NotificationEvent"}:                serviceWorkerOnly(),
	{"web:worker", "NotificationEventInit"}:            serviceWorkerOnly(),
	{"web:worker", "PushEvent"}:                        serviceWorkerOnly(),
	{"web:worker", "PushEventInit"}:                    serviceWorkerOnly(),
	{"web:worker", "PushMessageData"}:                  serviceWorkerOnly(),
	{"web:worker", "PushMessageDataInit"}:              serviceWorkerOnly(),
}

func dedicatedWorkerOnly() set.Set[Env] { return set.FromSlice([]Env{EnvDedicatedWorker}) }
func sharedWorkerOnly() set.Set[Env]    { return set.FromSlice([]Env{EnvSharedWorker}) }
func serviceWorkerOnly() set.Set[Env]   { return set.FromSlice([]Env{EnvServiceWorker}) }

// ConflictingDecls names the declarations both web libs declare at different
// types, which the run has no trustworthy environment reading for.
//
// The window lib's copy is what the tree carries and the worker lib's is
// skipped. For a name the two libs declare the same way that costs nothing, and
// the declaration reads as available everywhere, which is true of it. For a
// name they declare differently the tree holds the window's members under a
// name a worker also has, and neither reading is right: marking it for the
// window hides it from a worker that has it, and marking it for both claims its
// window-shaped contents are reachable from a worker.
//
// So a conflicting declaration carries no `@env`, and CheckEnvs reads no
// reference into or out of one. Checking against a reading the run knows is
// wrong would bury the findings that are real.
//
// Only the conflicts are listed. A shared name the two libs agree on is checked
// like any other, which is what keeps the suppression to the declarations that
// need it.
//
// What that gives up is coverage over the conflicts alone. Merging the two
// copies, using the per-member provenance the run already records, is what
// restores it. See #1633.
//
// A PartitionResult carries the set rather than a package variable holding it,
// so two runs in one process cannot see each other's.
type ConflictingDecls = set.Set[string]

// packageDecl addresses one declaration by the package holding it. The package
// is half the key because a bare name would widen a same-named declaration
// anywhere in the tree, which is the sort of reach nothing here should have.
type packageDecl struct {
	URI  string
	Name string
}

// PackageDeclEnvs returns the environments a declaration in uri exists on, from
// the tables alone. Callers with a partition in hand pass its lib reading to
// resolveDeclEnvs instead, which is the more specific answer.
func PackageDeclEnvs(uri, name string) set.Set[Env] {
	return declEnvsFrom(declEnvOverrides, uri, name, nil, nil)
}

// declEnvsFrom answers where one bound name exists, reading three sources in
// order of how specific they are.
//
//  1. overrides, which names the declarations a reader had to settle by hand
//     because no source answers them.
//  2. The lib files that declared it, which is TypeScript's own statement and is
//     per declaration. A caller with no partition passes nil for both maps and
//     skips this.
//  3. packageEnvs, for a package no lib contributed to.
//
// The override map is a parameter so a test reaches the override path while the
// committed map is empty.
func declEnvsFrom(
	overrides map[packageDecl]set.Set[Env], uri, name string,
	declSources map[string]set.Set[int], sourceFiles map[int]string,
) set.Set[Env] {
	if envs, held := overrides[packageDecl{URI: uri, Name: name}]; held {
		return envs
	}
	if envs := DeclEnvsFromLibs(name, declSources, sourceFiles); envs != nil {
		return envs
	}
	if envs, err := packageFileEnvs(uri); err == nil {
		return envs
	}
	return AllEnvs()
}

// AnnotateEnvs stamps `@env` on every declaration the run can narrow, and
// reports a `web:*` package packageEnvs does not classify.
//
// The lib set answers first. Which file declared a name is TypeScript's own
// statement of where it exists, and it is per declaration where packageEnvs is
// per package. packageEnvs answers for a package no lib contributed to, which is
// a hand-authored one.
//
// A declaration on every environment is left alone. Annotating it would say what
// the absence of a decorator already says, and would put one on most of the
// tree.
//
// Members are not annotated. A declaration both web libs declare is taken from
// the window lib alone, so the run has no reading of where each of its members
// exists, and every member inherits its declaration. Annotating from the window
// copy's spans would mark the whole shared surface for a window. Per-member
// annotation waits on the two copies of a shared declaration being merged, which
// is the rest of #1633.
func AnnotateEnvs(
	mods map[string]*StandaloneModule,
	declSources map[string]set.Set[int],
	sourceFiles map[int]string,
) error {
	if err := checkEnvTablesMatchTheTree(mods); err != nil {
		return err
	}
	everywhere := AllEnvs()
	var scanErr error
	for _, uri := range sortedURIs(mods) {
		mod := mods[uri]
		mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
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
				envs, err := resolveDeclEnvs(uri, names, declSources, sourceFiles)
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

// resolveDeclEnvs returns the environments one declaration exists on, given every
// name it binds.
//
// A destructuring `val` binds several names, and EnvIndex records the
// declaration's set against each of them. Resolving against the first alone
// would let a second name's override go unread here while the index honoured
// it, so one decorator has to answer for every name and the names have to
// agree.
func resolveDeclEnvs(
	uri string, names []string,
	declSources map[string]set.Set[int], sourceFiles map[int]string,
) (set.Set[Env], error) {
	return declaredEnvsFrom(declEnvOverrides, uri, names, declSources, sourceFiles)
}

// declaredEnvsFrom is resolveDeclEnvs over a chosen override map, so a test
// reaches the disagreement while the committed one is empty.
func declaredEnvsFrom(
	overrides map[packageDecl]set.Set[Env], uri string, names []string,
	declSources map[string]set.Set[int], sourceFiles map[int]string,
) (set.Set[Env], error) {
	envs := declEnvsFrom(overrides, uri, names[0], declSources, sourceFiles)
	for _, name := range names[1:] {
		other := declEnvsFrom(overrides, uri, name, declSources, sourceFiles)
		if !other.Equals(envs) {
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
	// A namespace is the one declaration kind that carries no decorators, so it
	// has nothing to prepend to. Asking whether the kind can hold them leaves
	// this with no list of the kinds that can.
	setter, ok := decl.(ast.DecoratorSetter)
	if !ok {
		return
	}
	setter.SetDecorators(append([]*ast.Decorator{dec}, ast.DeclDecorators(decl)...))
}

// checkEnvTablesMatchTheTree reports a `web:*` package the run emits whose file
// name does not say which environments it exists on.
//
// A name with no suffix is a claim rather than an omission: it says the package
// exists everywhere. So what this catches is a name whose suffix does not parse,
// which is a typo in the partition table's path.
func checkEnvTablesMatchTheTree(mods map[string]*StandaloneModule) error {
	var unreadable []string
	for _, uri := range sortedURIs(mods) {
		if SchemeOf(uri) != "web" {
			continue
		}
		if _, err := packageFileEnvs(uri); err != nil {
			unreadable = append(unreadable, err.Error())
		}
	}
	if len(unreadable) == 0 {
		return nil
	}
	sort.Strings(unreadable)
	return fmt.Errorf("%s", strings.Join(unreadable, "; "))
}

// StaleEnvTableEntries returns each declEnvOverrides entry naming a package or
// declaration mods does not hold, sorted. It answers against a whole-tree run,
// which is where an entry a TypeScript bump left behind shows up.
//
// A package's own environments need no such check. They are read from the name
// of the file the partition table writes, so a package the tree does not hold
// has no file and no claim to go stale.
func StaleEnvTableEntries(mods map[string]*StandaloneModule) []string {
	var stale []string
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
//
// It is built from the two source sets the partition already names rather than
// listing the same basenames a third time, so a file the partition learns about
// cannot go unclassified here.
var libEnvs = buildLibEnvs()

func buildLibEnvs() map[string]set.Set[Env] {
	out := map[string]set.Set[Env]{}
	for _, file := range WindowLibSources.ToSlice() {
		out[file] = windowOnly()
	}
	for _, file := range WorkerLibSources.ToSlice() {
		out[file] = workerKinds()
	}
	return out
}

func workerKinds() set.Set[Env] {
	return set.FromSlice(envGroups["worker"])
}

// DeclEnvsFromLibs returns the environments a declaration exists on, read from
// the lib files that declared it.
//
// A declaration both web libs declare exists in both, whether the two copies
// merged or one replaced the other. A declaration no lib declared, or one a
// language lib declared, exists everywhere, which is what the nil return says.
func DeclEnvsFromLibs(
	name string, declSources map[string]set.Set[int], sourceFiles map[int]string,
) set.Set[Env] {
	ids, held := declSources[name]
	if !held {
		return nil
	}
	return envsOfSourceIDs(ids, sourceFiles)
}

// envsOfSourceIDs unions the environments each lib file answers for.
//
// A nil return means the declaration exists everywhere, either because a file
// is not one the tables narrow or because it is not a lib file at all.
func envsOfSourceIDs(ids set.Set[int], sourceFiles map[int]string) set.Set[Env] {
	envs := set.NewSet[Env]()
	for _, id := range ids.ToSlice() {
		file, held := sourceFiles[id]
		if !held {
			return nil
		}
		narrowed, named := libEnvs[file]
		if !named {
			return nil
		}
		envs = envs.Union(narrowed)
	}
	if envs.Len() == 0 {
		return nil
	}
	return envs
}

// MemberEnvsFromLibs returns the environments a converted member exists on,
// read from the lib files that declared it.
//
// A member two files declare carries both, which dedupeMembers recorded on the
// way through. A member one file declared carries its own span's id alone. A
// member the converter synthesized has no lib and exists wherever its
// declaration does, which is what the nil return says.
//
// Nothing in the run calls this yet. A worker lib's copy of a declaration a
// window lib also declares is skipped, so a shared declaration in the tree
// carries the window copy's spans alone and reading them would mark the whole
// shared surface for a window. Merging the two copies is what makes the reading
// answerable, and is the rest of #1633.
//
// The reading disagrees with packageEnvs, and is the better source. Over a
// conversion of lib.dom.d.ts and lib.webworker.d.ts with both copies merged,
// 2044 of 7762 members read as available outside the environments their package
// claims, most of them in `web:webgl` and `web:dom`. packageEnvs says both are a
// window's, and its own comment says that is for want of a source.
// TestTheLibReadingDisagreesWithThePackageTable measures the gap, so the merge
// has a number to work against.
func MemberEnvsFromLibs(
	mod *StandaloneModule, member ast.Node, sourceFiles map[int]string,
) set.Set[Env] {
	ids := mod.MemberSources[member]
	if ids == nil {
		ids = set.FromSlice([]int{member.Span().SourceID})
	}
	return envsOfSourceIDs(ids, sourceFiles)
}
