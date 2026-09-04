package dts_to_esc

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// A declaration is type-only when the lib set binds no value of its
// name. An `interface` with no companion `declare var`, and a `type`
// alias, are both type-only. The static half of a trio is not.
// TypeScript spells a class as `interface Foo`, `interface
// FooConstructor`, and `declare var Foo`, and that binding gives all
// three a runtime referent.
//
// The §6.1 partition table enumerates each package's members by hand,
// and hand-enumeration reaches for the names that have constructors. A
// type-only companion left off a sibling's member list falls through to
// `web:dom` under the DOM residual rule, away from the family it
// describes. `web:webgl` holds the rendering contexts while the
// extension interfaces `getExtension` returns sit in `web:dom`, so the
// return type comes from a package the caller did not import.
//
// The unmapped-symbol fail-safe cannot catch that. It trips on a name
// with no home, and the residual rule gives every one of these a home.
// The analysis below reports them instead.

// SoleReferrer is a type-only declaration that exactly one package
// other than its own references, with nothing else in the declaring
// package referencing it. Sole reference from a sibling is evidence
// the name belongs to that sibling rather than to where it landed.
type SoleReferrer struct {
	// Name is the bare TS declaration name.
	Name string

	// DeclaredIn is the package URI the partition routed Name to.
	DeclaredIn string

	// ReferencedBy is the package URI of the only referrer.
	ReferencedBy string
}

// UnreferencedDecl is a type-only declaration nothing in the lib set
// references. Each one needs a decision rather than a default: a
// package that wants it, or an overlay drop.
type UnreferencedDecl struct {
	// Name is the bare TS declaration name.
	Name string

	// DeclaredIn is the package URI the partition routed Name to.
	DeclaredIn string
}

// TypeOnlyRouting holds the type-only declarations whose referrers
// disagree with where the partition put them, and the ones nothing
// references. A declaration two or more packages share is in neither.
type TypeOnlyRouting struct {
	// SoleReferrer is sorted by declaring package, then referrer, then
	// name.
	SoleReferrer []SoleReferrer

	// Unreferenced is sorted by declaring package, then name.
	Unreferenced []UnreferencedDecl
}

// forPackage keeps only the entries declared in the package at uri.
func (r TypeOnlyRouting) forPackage(uri string) TypeOnlyRouting {
	var out TypeOnlyRouting
	for _, e := range r.SoleReferrer {
		if e.DeclaredIn == uri {
			out.SoleReferrer = append(out.SoleReferrer, e)
		}
	}
	for _, e := range r.Unreferenced {
		if e.DeclaredIn == uri {
			out.Unreferenced = append(out.Unreferenced, e)
		}
	}
	return out
}

// AnalyzeTypeOnlyRouting classifies every type-only declaration in the
// routed buckets by who references it.
func AnalyzeTypeOnlyRouting(result *PartitionResult) TypeOnlyRouting {
	valueNames := valueBoundNames(result)
	typeOnlyIn := typeOnlyDecls(result, valueNames)
	referrers := referrersOf(result, typeOnlyIn)

	var out TypeOnlyRouting
	for name, declaredIn := range typeOnlyIn {
		refs, ok := referrers[name]
		if !ok || refs.Len() == 0 {
			out.Unreferenced = append(out.Unreferenced,
				UnreferencedDecl{Name: name, DeclaredIn: declaredIn})
			continue
		}
		if refs.Len() != 1 || refs.Contains(declaredIn) {
			continue
		}
		out.SoleReferrer = append(out.SoleReferrer, SoleReferrer{
			Name:         name,
			DeclaredIn:   declaredIn,
			ReferencedBy: refs.ToSlice()[0],
		})
	}

	sort.Slice(out.SoleReferrer, func(i, j int) bool {
		a, b := out.SoleReferrer[i], out.SoleReferrer[j]
		if a.DeclaredIn != b.DeclaredIn {
			return a.DeclaredIn < b.DeclaredIn
		}
		if a.ReferencedBy != b.ReferencedBy {
			return a.ReferencedBy < b.ReferencedBy
		}
		return a.Name < b.Name
	})
	sort.Slice(out.Unreferenced, func(i, j int) bool {
		a, b := out.Unreferenced[i], out.Unreferenced[j]
		if a.DeclaredIn != b.DeclaredIn {
			return a.DeclaredIn < b.DeclaredIn
		}
		return a.Name < b.Name
	})
	return out
}

// valueBoundNames are the names the lib set binds a value to. An
// interface sharing a name with one of them is a trio's instance side.
func valueBoundNames(result *PartitionResult) set.Set[string] {
	names := set.NewSet[string]()
	for _, stmts := range result.Buckets {
		for _, stmt := range stmts {
			switch stmt.(type) {
			case *dts_parser.VarDecl, *dts_parser.FuncDecl,
				*dts_parser.ClassDecl, *dts_parser.EnumDecl,
				*dts_parser.NamespaceDecl:
				if name := topLevelName(stmt); name != "" {
					names.Add(name)
				}
			}
		}
	}
	return names
}

// typeOnlyDecls maps each type-only declaration to the package URI
// declaring it. A `FooConstructor` whose `Foo` is value-bound is the
// static half of a trio, so it is left out along with `Foo` itself.
func typeOnlyDecls(result *PartitionResult, valueNames set.Set[string]) map[string]string {
	declaredIn := map[string]string{}
	for uri, stmts := range result.Buckets {
		for _, stmt := range stmts {
			switch stmt.(type) {
			case *dts_parser.InterfaceDecl, *dts_parser.TypeDecl:
			default:
				continue
			}
			name := topLevelName(stmt)
			if name == "" || valueNames.Contains(name) {
				continue
			}
			if instance, ok := strings.CutSuffix(name, "Constructor"); ok &&
				instance != "" && valueNames.Contains(instance) {
				continue
			}
			declaredIn[name] = uri
		}
	}
	return declaredIn
}

// referrersOf maps each type-only name to the packages whose
// declarations reference it. A declaration's reference to itself does
// not count, since a recursive interface says nothing about which
// package wants the name.
func referrersOf(
	result *PartitionResult,
	typeOnlyIn map[string]string,
) map[string]set.Set[string] {
	referrers := map[string]set.Set[string]{}
	for uri, stmts := range result.Buckets {
		for _, stmt := range stmts {
			from := topLevelName(stmt)
			walkStatementTypes(stmt, func(t dts_parser.TypeAnn) {
				walkTypeRefs(t, func(ref *dts_parser.TypeReference) {
					name := typeRefName(ref)
					if name == "" || name == from {
						return
					}
					if _, ok := typeOnlyIn[name]; !ok {
						return
					}
					if _, ok := referrers[name]; !ok {
						referrers[name] = set.NewSet[string]()
					}
					referrers[name].Add(uri)
				})
			})
		}
	}
	return referrers
}

// ReportTypeOnlyRouting prints the `web:dom` type-only declarations a
// single sibling is the sole referrer of, then the ones nothing
// references, and nothing at all when both are empty. Only `web:dom`
// is reported, because the DOM residual rule puts a name there with
// nobody deciding to, which is what makes a sole referrer evidence.
func ReportTypeOnlyRouting(result *PartitionResult, w io.Writer) error {
	routing := AnalyzeTypeOnlyRouting(result).forPackage(WebDOM.URI)

	// The report is assembled in memory and written once, matching
	// ReportPartition. A strings.Builder write cannot fail, so the body
	// stays free of error plumbing.
	var b strings.Builder

	// Consecutive entries share a referrer because AnalyzeTypeOnly
	// Routing sorts by referrer within a declaring package, so one pass
	// groups them.
	for i := 0; i < len(routing.SoleReferrer); {
		j := i
		for j < len(routing.SoleReferrer) &&
			routing.SoleReferrer[j].ReferencedBy == routing.SoleReferrer[i].ReferencedBy {
			j++
		}
		names := make([]string, 0, j-i)
		for _, e := range routing.SoleReferrer[i:j] {
			names = append(names, e.Name)
		}
		fmt.Fprintf(&b, "  %s: %d type-only decl%s only %s references (%s)\n",
			WebDOM.URI, len(names), plural(len(names)),
			routing.SoleReferrer[i].ReferencedBy, strings.Join(names, ", "))
		i = j
	}

	var names []string
	for _, e := range routing.Unreferenced {
		if UnreferencedDOMTypes.Contains(e.Name) {
			continue
		}
		names = append(names, e.Name)
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "  %s: %d type-only decl%s nothing references (%s)\n",
			WebDOM.URI, len(names), plural(len(names)), strings.Join(names, ", "))
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// plural returns the suffix that makes a count noun agree with n.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
