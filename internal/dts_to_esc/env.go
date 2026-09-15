package dts_to_esc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// env.go says which environments a declaration exists on, and reports a
// declaration that reaches something absent from an environment it claims.
//
// It answers per declaration and per member. A package-wide answer is too
// coarse for the cases that matter: `web:performance` is available everywhere,
// yet `PerformanceTiming` is a window's alone, so one member differs from the
// rest of its package.

// Env names one environment a declaration may exist on.
//
// The four are the web environments the pinned lib set declares a global scope
// type for. A window and a worker are siblings rather than one containing the
// other, which is why this is a set per declaration and not a rank. #1599 has
// the surface counts that settle it.
//
// `node`, `deno` and `bun` are deliberately absent. Each needs a type source
// the repository does not hold, so annotating one would be a claim nothing can
// check. #1599 question 6 decides whether they become environments at all.
type Env string

const (
	EnvWindow          Env = "window"
	EnvDedicatedWorker Env = "dedicated_worker"
	EnvSharedWorker    Env = "shared_worker"
	EnvServiceWorker   Env = "service_worker"
)

// EnvDecoratorName is the decorator that carries a declaration's environments.
const EnvDecoratorName = "env"

// envVocabulary is every environment name, in the order a diagnostic lists them.
var envVocabulary = []Env{
	EnvWindow,
	EnvDedicatedWorker,
	EnvSharedWorker,
	EnvServiceWorker,
}

// envGroups are the names standing for several environments at once, so a
// declaration on every worker kind writes one tag rather than three. A group
// name and an environment name share one namespace, which is what lets the
// argument list mix them.
var envGroups = map[string][]Env{
	"worker": {EnvDedicatedWorker, EnvSharedWorker, EnvServiceWorker},
}

// AllEnvs returns the whole vocabulary, which is what an unannotated top-level
// declaration is available on.
func AllEnvs() set.Set[Env] {
	return set.FromSlice(envVocabulary)
}

// sortedEnvs returns envs in vocabulary order, so two diagnostics over the same
// set read the same.
func sortedEnvs(envs set.Set[Env]) []Env {
	out := make([]Env, 0, envs.Len())
	for _, env := range envVocabulary {
		if envs.Contains(env) {
			out = append(out, env)
		}
	}
	return out
}

// printEnvs renders a set for a diagnostic.
func printEnvs(envs set.Set[Env]) string {
	names := make([]string, 0, envs.Len())
	for _, env := range sortedEnvs(envs) {
		names = append(names, string(env))
	}
	return strings.Join(names, ", ")
}

// knownEnvNames lists the vocabulary and the groups together, sorted, for the
// diagnostic an unknown tag produces.
func knownEnvNames() string {
	names := make([]string, 0, len(envVocabulary)+len(envGroups))
	for _, env := range envVocabulary {
		names = append(names, string(env))
	}
	for group := range envGroups {
		names = append(names, group)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// envsFromDecorator reads the environment set one `@env(...)` names.
//
// Every argument is a string literal naming an environment or a group. The
// vocabulary is closed so that a typo fails here rather than silently narrowing
// a declaration to nothing, which would then report at every reference to it.
func envsFromDecorator(dec *ast.Decorator) (set.Set[Env], error) {
	if len(dec.Args) == 0 {
		return nil, fmt.Errorf("`@%s` names no environment; list at least one of %s",
			EnvDecoratorName, knownEnvNames())
	}
	envs := set.NewSet[Env]()
	for _, arg := range dec.Args {
		lit, ok := arg.(*ast.LiteralExpr)
		if !ok {
			return nil, fmt.Errorf("`@%s` takes string-literal arguments", EnvDecoratorName)
		}
		str, ok := lit.Lit.(*ast.StrLit)
		if !ok {
			return nil, fmt.Errorf("`@%s` takes string-literal arguments", EnvDecoratorName)
		}
		named, ok := envsNamed(str.Value)
		if !ok {
			return nil, fmt.Errorf("`@%s` names unknown environment %q; the vocabulary is %s",
				EnvDecoratorName, str.Value, knownEnvNames())
		}
		envs = envs.Union(named)
	}
	return envs, nil
}

// envsNamed resolves one name from the vocabulary, which is an environment or
// a group standing for several. It is what keeps `@env("worker")` and a
// `.worker.esc` file name reading the same set.
func envsNamed(name string) (set.Set[Env], bool) {
	if group, isGroup := envGroups[name]; isGroup {
		return set.FromSlice(group), true
	}
	env := Env(name)
	if !AllEnvs().Contains(env) {
		return nil, false
	}
	return set.FromSlice([]Env{env}), true
}

// findEnvDecorator returns the `@env` in a decorator list, or nil for a list
// holding none. A second one is rejected: two lists would have to be unioned or
// intersected and the source says which neither way.
func findEnvDecorator(decorators []*ast.Decorator) (*ast.Decorator, error) {
	var found *ast.Decorator
	for _, dec := range decorators {
		if dec.Name == nil || dec.Name.Name != EnvDecoratorName {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("two `@%s` decorators; write one naming every environment",
				EnvDecoratorName)
		}
		found = dec
	}
	return found, nil
}

// DeclEnvs returns the environments a top-level declaration exists on.
//
// An unannotated declaration exists on every environment. That keeps the
// annotation to the exceptions, which is the shape the tree wants: most of
// `web:core` is everywhere and most of `web:dom` is a window.
func DeclEnvs(decl ast.Decl) (set.Set[Env], error) {
	dec, err := findEnvDecorator(ast.DeclDecorators(decl))
	if err != nil {
		return nil, err
	}
	if dec == nil {
		return AllEnvs(), nil
	}
	return envsFromDecorator(dec)
}

// MemberEnvs returns the environments a class member exists on, given the
// environments its class exists on.
//
// An unannotated member inherits its class's set, so every member of a
// window-only class is window-only without a marker of its own. An annotated
// member has to name a subset: a member cannot exist where the class holding it
// does not.
func MemberEnvs(elem ast.ClassElem, owner set.Set[Env]) (set.Set[Env], error) {
	dec, err := findEnvDecorator(ast.ClassElemDecorators(elem))
	if err != nil {
		return nil, err
	}
	if dec == nil {
		return owner, nil
	}
	envs, err := envsFromDecorator(dec)
	if err != nil {
		return nil, err
	}
	if outside := envs.Difference(owner); outside.Len() > 0 {
		return nil, fmt.Errorf(
			"member claims %s, which its class does not; a member exists only where its class does",
			printEnvs(outside))
	}
	return envs, nil
}

// EnvIndex maps every top-level declaration name in mods to the environments it
// exists on. An unannotated name maps to the whole vocabulary.
//
// The index is over declarations rather than members, since a reference names a
// declaration. A member's own narrowing is read where the reference is made,
// not where it is resolved.
//
// Several declarations may share one name. An overload set is one declaration
// per signature, and TypeScript's class idiom pairs an interface with a
// `declare var` of the same name. The entry is the intersection over them.
//
// That is right for the interface and `declare var` pair, where a reference
// reaches the name only where both halves exist, and too narrow for an overload
// set, where one arm is enough to call it. Telling them apart means indexing by
// sort rather than by name, since the pair is one type and one value while the
// overloads are all values. Intersection is the conservative half of that
// choice: it reports a reference the finer answer would allow, which is loud
// and answered with an override, where the union would pass one it should
// catch. Nothing reaches it today, since every declaration in a package takes
// that package's set and the override map is empty.
func EnvIndex(mods map[string]*StandaloneModule) (map[string]set.Set[Env], error) {
	index := map[string]set.Set[Env]{}
	for _, uri := range sortedURIs(mods) {
		var declErr error
		mods[uri].Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
			for _, decl := range ns.Decls {
				envs, err := DeclEnvs(decl)
				if err != nil {
					declErr = fmt.Errorf("converter: %s: %s: %w", uri, declNameOf(decl), err)
					return false
				}
				for _, name := range ast.DeclNames(decl) {
					if seen, held := index[name]; held {
						index[name] = seen.Intersection(envs)
						continue
					}
					index[name] = envs
				}
			}
			return true
		})
		if declErr != nil {
			return nil, declErr
		}
	}
	return index, nil
}

// EnvViolation is one reference from a declaration to something absent from an
// environment the referring declaration claims.
//
// It names the reference rather than the pair of packages, so a reader goes
// straight to the line to change.
type EnvViolation struct {
	// Package is the pseudo-package URI holding the reference.
	Package string
	// Decl is the referring declaration's name.
	Decl string
	// Member is the member holding the reference, or "" when the reference is
	// the declaration's own, such as an `extends` clause.
	Member string
	// Name is what the reference names.
	Name string
	// Missing are the environments the referrer claims and the referent lacks.
	Missing []Env
}

func (v EnvViolation) String() string {
	where := v.Decl
	if v.Member != "" {
		where = v.Decl + "." + v.Member
	}
	return fmt.Sprintf("%s: %s names %s, which is absent from %s",
		v.Package, where, v.Name, envList(v.Missing))
}

// envList renders the environments of a violation.
func envList(envs []Env) string {
	names := make([]string, 0, len(envs))
	for _, env := range envs {
		names = append(names, string(env))
	}
	return strings.Join(names, ", ")
}

// CheckEnvs returns every reference that reaches a declaration absent from an
// environment the reference's own declaration or member claims, sorted.
//
// A reference to something no package declares is skipped. That covers a type
// parameter, a builtin, and a name the lib set uses without declaring, none of
// which carry environments.
func CheckEnvs(mods map[string]*StandaloneModule) ([]EnvViolation, error) {
	index, err := EnvIndex(mods)
	if err != nil {
		return nil, err
	}
	var violations []EnvViolation
	for _, uri := range sortedURIs(mods) {
		found, err := checkModuleEnvs(uri, mods[uri].Module, index)
		if err != nil {
			return nil, err
		}
		violations = append(violations, found...)
	}
	sort.Slice(violations, func(i, j int) bool {
		a, b := violations[i], violations[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Decl != b.Decl {
			return a.Decl < b.Decl
		}
		if a.Member != b.Member {
			return a.Member < b.Member
		}
		return a.Name < b.Name
	})
	return violations, nil
}

// checkModuleEnvs reports every violating reference in one package.
func checkModuleEnvs(
	uri string, module *ast.Module, index map[string]set.Set[Env],
) ([]EnvViolation, error) {
	var violations []EnvViolation
	var scanErr error
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			declEnvs, err := DeclEnvs(decl)
			if err != nil {
				scanErr = fmt.Errorf("converter: %s: %s: %w", uri, declNameOf(decl), err)
				return false
			}
			name := declNameOf(decl)
			cls, isClass := decl.(*ast.ClassDecl)
			if !isClass {
				violations = append(violations,
					envViolations(uri, name, "", declEnvs, declRefNames(decl), index)...)
				continue
			}
			// A class's members are checked one at a time, each against its own
			// environments, so a window-only member of a portable class reports
			// where a check over the whole class would not.
			for _, elem := range cls.Body {
				memberEnvs, err := MemberEnvs(elem, declEnvs)
				if err != nil {
					scanErr = fmt.Errorf("converter: %s: %s: %w", uri, name, err)
					return false
				}
				violations = append(violations, envViolations(
					uri, name, classElemLabel(elem), memberEnvs,
					classElemRefNames(cls, elem), index)...)
			}
			violations = append(violations,
				envViolations(uri, name, "", declEnvs, classOwnRefs(cls), index)...)
		}
		return true
	})
	return violations, scanErr
}

// classOwnRefs returns the names a class writes outside its members: the
// superclass, each implemented interface, and each type parameter's bound and
// default.
func classOwnRefs(cls *ast.ClassDecl) set.Set[string] {
	names := set.NewSet[string]()
	// One collector across every clause, with the class's own type parameters
	// pushed once. `class Box<E> extends Base<E>` names Base and not E, and
	// `extends crypto.Crypto` names Crypto and not the global `crypto`.
	c := &declRefCollector{names: names, bound: map[string]int{}}
	c.push(cls.TypeParams)
	collect := func(t ast.TypeAnn) {
		if t == nil {
			return
		}
		t.Accept(c)
	}
	// Extends and Implements hold a concrete pointer rather than the interface,
	// so a nil one has to be dropped before it reaches collect. Wrapping it would
	// make a non-nil interface over a nil pointer, which the guard there misses.
	if cls.Extends != nil {
		collect(cls.Extends)
	}
	for _, impl := range cls.Implements {
		if impl != nil {
			collect(impl)
		}
	}
	for _, tp := range cls.TypeParams {
		collect(tp.Constraint)
		collect(tp.Default)
	}
	return names
}

// envViolations reports each name in refs that the index says is absent from an
// environment envs claims.
func envViolations(
	uri, decl, member string,
	envs set.Set[Env],
	refs set.Set[string],
	index map[string]set.Set[Env],
) []EnvViolation {
	names := refs.ToSlice()
	sort.Strings(names)
	var violations []EnvViolation
	for _, name := range names {
		target, declared := index[name]
		if !declared {
			continue
		}
		missing := envs.Difference(target)
		if missing.Len() == 0 {
			continue
		}
		violations = append(violations, EnvViolation{
			Package: uri, Decl: decl, Member: member,
			Name: name, Missing: sortedEnvs(missing),
		})
	}
	return violations
}

// sortedURIs returns the package URIs of mods in a fixed order, so a run
// reports the same list twice.
func sortedURIs(mods map[string]*StandaloneModule) []string {
	uris := make([]string, 0, len(mods))
	for uri := range mods {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	return uris
}

// declNameOf returns a declaration's name for a diagnostic, or "<unnamed>" for
// one binding nothing.
func declNameOf(decl ast.Decl) string {
	names := ast.DeclNames(decl)
	if len(names) == 0 {
		return "<unnamed>"
	}
	return strings.Join(names, ", ")
}

// classElemLabel names a member for a diagnostic. A call signature has no name,
// so it is labelled by what it is.
func classElemLabel(elem ast.ClassElem) string {
	switch e := elem.(type) {
	case *ast.FieldElem:
		return objKeyLabel(e.Name)
	case *ast.MethodElem:
		return objKeyLabel(e.Name)
	case *ast.GetterElem:
		return objKeyLabel(e.Name)
	case *ast.SetterElem:
		return objKeyLabel(e.Name)
	case *ast.ConstructorElem:
		return "constructor"
	case *ast.CallableElem:
		return "call signature"
	default:
		return "member"
	}
}

// objKeyLabel renders a member's key, falling back to a placeholder for a
// computed key no name reads.
func objKeyLabel(key ast.ObjKey) string {
	if ident, ok := key.(*ast.IdentExpr); ok {
		return ident.Name
	}
	if str, ok := key.(*ast.StrLit); ok {
		return str.Value
	}
	return "<computed>"
}
