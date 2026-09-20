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
var declEnvOverrides = map[packageDecl]set.Set[Env]{
	// Declared in `web:dom` and portable in substance. Its arms are `Blob`,
	// `BufferSource`, `FormData`, `URLSearchParams` and `string`, all of which
	// every environment has. `web:fetch` names it for `BodyInit`, and that
	// reference is what the alias exists to serve.
	{URI: "web:dom", Name: "XMLHttpRequestBodyInit"}: AllEnvs(),
}

// packageDecl addresses one declaration by the package holding it. The package
// is half the key because a bare name would widen a same-named declaration
// anywhere in the tree, which is the sort of reach nothing here should have.
type packageDecl struct {
	URI  string
	Name string
}

// PackageDeclEnvs returns the environments a declaration in uri exists on.
func PackageDeclEnvs(uri, name string) set.Set[Env] {
	if envs, held := declEnvOverrides[packageDecl{URI: uri, Name: name}]; held {
		return envs
	}
	if envs, err := packageFileEnvs(uri); err == nil {
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
	envs := PackageDeclEnvs(uri, names[0])
	for _, name := range names[1:] {
		if other := PackageDeclEnvs(uri, name); !other.Equals(envs) {
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
