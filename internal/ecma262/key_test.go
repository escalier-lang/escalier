package ecma262

import (
	"sort"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		key  string
		want MemberRef
	}{
		"InstanceMethod": {
			key:  "Array.prototype.push",
			want: MemberRef{Owner: "Array", Member: StrMember("push"), Sort: SortInstance},
		},
		"InstanceSymbol": {
			key:  "Array.prototype [ @@iterator ]",
			want: MemberRef{Owner: "Array", Member: SymMember("iterator"), Sort: SortInstance},
		},
		"InstanceGetter": {
			key: "get Map.prototype.size",
			want: MemberRef{
				Owner: "Map", Member: StrMember("size"),
				Sort: SortInstance, Accessor: GetAccessor,
			},
		},
		"InstanceSetter": {
			key: "set Iterator.prototype [ @@toStringTag ]",
			want: MemberRef{
				Owner: "Iterator", Member: SymMember("toStringTag"),
				Sort: SortInstance, Accessor: SetAccessor,
			},
		},
		"Static": {
			key:  "Array.from",
			want: MemberRef{Owner: "Array", Member: StrMember("from"), Sort: SortStatic},
		},
		"StaticSymbol": {
			key: "get Array [ @@species ]",
			want: MemberRef{
				Owner: "Array", Member: SymMember("species"),
				Sort: SortStatic, Accessor: GetAccessor,
			},
		},
		"NamespaceFunc": {
			key:  "Math.max",
			want: MemberRef{Owner: "Math", Member: StrMember("max"), Sort: SortNamespaceFunc},
		},
		"NamespaceFuncOutsideECMA262": {
			key: "Intl.getCanonicalLocales",
			want: MemberRef{
				Owner: "Intl", Member: StrMember("getCanonicalLocales"),
				Sort: SortNamespaceFunc,
			},
		},
		// A constructor segment after the namespace makes the owner a class,
		// so its members are instance and static members rather than
		// namespace functions.
		"NestedConstructorInstance": {
			key: "Intl.DateTimeFormat.prototype.format",
			want: MemberRef{
				Owner: "Intl.DateTimeFormat", Member: StrMember("format"),
				Sort: SortInstance,
			},
		},
		"NestedConstructorStatic": {
			key: "Intl.DateTimeFormat.supportedLocalesOf",
			want: MemberRef{
				Owner: "Intl.DateTimeFormat", Member: StrMember("supportedLocalesOf"),
				Sort: SortStatic,
			},
		},
		// %ArrayIteratorPrototype% has no name in the language, so it is the
		// root of the path instead of a `prototype` segment. Its members
		// still have a receiver.
		"NamelessPrototype": {
			key:  "ArrayIteratorPrototype.next",
			want: MemberRef{Owner: "ArrayIteratorPrototype", Member: StrMember("next"), Sort: SortInstance},
		},
		"NamelessPrototypeSymbol": {
			key: "AsyncIteratorPrototype [ @@asyncIterator ]",
			want: MemberRef{
				Owner: "AsyncIteratorPrototype", Member: SymMember("asyncIterator"),
				Sort: SortInstance,
			},
		},
		"UnderscoredMember": {
			key:  "get Object.prototype.__proto__",
			want: MemberRef{Owner: "Object", Member: StrMember("__proto__"), Sort: SortInstance, Accessor: GetAccessor},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := Normalize(tc.key)
			require.True(t, ok, "Normalize(%q) should key", tc.key)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNormalizeRejects(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Empty":              "",
		"BareGlobalFunction": "parseInt",
		"BareConstructor":    "Array",
		"AccessorOnly":       "get ",
		// The closures an algorithm defines are keyed by prose, not by a
		// dotted path, so they address no member of an owner.
		"AlgorithmClosure": "`Promise.all`ResolveElementFunction",
		// An internal method is keyed by the record type that holds it.
		"InternalMethod": "Record[OrdinaryObject].Set",
		// A numeric method is keyed with `::`, which leaves a member name
		// that is not an identifier.
		"NumericMethod":       "Number::toString",
		"UnterminatedSymbol":  "Array.prototype [ @@iterator",
		"NotAWellKnownSymbol": "Array.prototype [ iterator ]",
		"TrailingDot":         "Array.prototype.",
		"LeadingDot":          ".push",
	}

	for name, key := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, ok := Normalize(key)
			require.False(t, ok, "Normalize(%q) should not key", key)
		})
	}
}

// normalizeCommittedFacts runs every builtin in the committed control-flow
// graph through Normalize. It returns the address each keyed name resolved to
// and the names Normalize refused, sorted.
func normalizeCommittedFacts(t *testing.T) (map[string]MemberRef, []string) {
	t.Helper()
	keyed := make(map[string]MemberRef)
	var refused []string
	for name := range testFacts(t).Methods {
		if ref, ok := Normalize(name); ok {
			keyed[name] = ref
			continue
		}
		refused = append(refused, name)
	}
	sort.Strings(refused)
	return keyed, refused
}

// TestNormalizeKeysCommittedFacts is the §5 gate on the spec side: every
// builtin in the committed control-flow graph either keys to an address or
// addresses no member of an owner at all. The refused list is snapshotted
// whole, so a spec bump that introduces a shape the normalizer cannot read
// shows up here rather than silently dropping a builtin out of the join.
//
// The list holds three categories, none of which is a member of an owner:
// the constructors themselves, the functions the global object holds, and
// the closures an algorithm defines, whose spec names are prose.
func TestNormalizeKeysCommittedFacts(t *testing.T) {
	keyed, refused := normalizeCommittedFacts(t)

	require.NotEmpty(t, keyed)
	snaps.MatchSnapshot(t, strings.Join(refused, "\n"))
}

// Two spec names that key to the same address would make a lookup's answer
// depend on which of them an index happened to store. None of the committed
// facts collide, and this is what says so.
func TestNormalizeKeysAreDistinct(t *testing.T) {
	keyed, _ := normalizeCommittedFacts(t)

	owners := make(map[MemberRef]string, len(keyed))
	for name, ref := range keyed {
		if other, taken := owners[ref]; taken {
			require.Failf(t, "colliding spec keys",
				"%q and %q both key to %s", other, name, ref)
		}
		owners[ref] = name
	}
}
