package ecma262

import (
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

func TestSignatureHolds(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sig  Signature
		pos  int
		want bool
	}{
		"FirstOfTwo":       {Signature{Params: 2}, 0, true},
		"LastOfTwo":        {Signature{Params: 2}, 1, true},
		"PastEnd":          {Signature{Params: 2}, 2, false},
		"NoParams":         {Signature{}, 0, false},
		"Negative":         {Signature{Params: 2}, -1, false},
		"InsideRest":       {Signature{Params: 1, Rest: true}, 4, true},
		"BeforeRest":       {Signature{Params: 2, Rest: true}, 0, true},
		"RestWithNoParams": {Signature{Rest: true}, 0, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.sig.Holds(tc.pos))
		})
	}
}

// A spec algorithm sits behind every overload of a member, so its
// algorithm-level claims carry over unchanged and only the claim that names a
// parameter position resolves per overload.
func TestMethodFactForSignature(t *testing.T) {
	t.Parallel()

	returnsParam1 := MethodFact{
		Classified: true,
		Receiver:   RecvBorrow,
		Returns:    AliasParam,
		ParamIndex: position(1),
	}

	tests := map[string]struct {
		fact MethodFact
		sig  Signature
		want string
	}{
		"PositionDeclared": {
			fact: returnsParam1,
			sig:  Signature{Params: 2},
			want: "receiver:borrow returns:param(1)",
		},
		// The overload declares one parameter, so the value the algorithm
		// hands back is one this signature cannot name.
		"PositionMissing": {
			fact: returnsParam1,
			sig:  Signature{Params: 1},
			want: "receiver:borrow returns:unknown",
		},
		"PositionInsideRest": {
			fact: returnsParam1,
			sig:  Signature{Params: 1, Rest: true},
			want: "receiver:borrow returns:param(1)",
		},
		// A claim that names no position applies to every overload as it is.
		"NoPositionClaimed": {
			fact: MethodFact{Classified: true, Receiver: RecvMutBorrow, Returns: AliasReceiver},
			sig:  Signature{},
			want: "receiver:mutBorrow returns:receiver",
		},
		"Unclassified": {
			fact: MethodFact{},
			sig:  Signature{},
			want: "unclassified",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.fact.ForSignature(tc.sig).String())
		})
	}
}

// ForSignature must not write through to the fact the index holds, or the
// first overload it resolves would rewrite the claim for every later one.
func TestMethodFactForSignatureLeavesTheFactAlone(t *testing.T) {
	t.Parallel()

	fact := MethodFact{Classified: true, Returns: AliasParam, ParamIndex: position(3)}
	require.Equal(t, "returns:unknown", strings.TrimPrefix(
		fact.ForSignature(Signature{Params: 1}).String(), "receiver: "))
	require.Equal(t, "receiver: returns:param(3)", fact.String())
}

// joinFixture is a hand-built fact set covering each keying shape the join
// has to resolve.
func joinFixture() *Facts {
	return &Facts{
		SpecTarget: "test",
		Methods: map[string]MethodFact{
			"Array.prototype.push":           {Classified: true, Receiver: RecvMutBorrow, Returns: AliasFresh},
			"Array.prototype [ @@iterator ]": {Classified: true, Receiver: RecvBorrow, Returns: AliasFresh},
			"Array.from":                     {Classified: true, Receiver: RecvNone, Returns: AliasFresh},
			"get Map.prototype.size":         {Classified: true, Receiver: RecvBorrow, Returns: AliasFresh},
			"Math.max":                       {Classified: true, Receiver: RecvNone, Returns: AliasFresh},
			"String.prototype.at":            {Classified: true, Receiver: RecvBorrow, Returns: AliasParam, ParamIndex: position(1)},
			"Set.prototype.add":              {Classified: false},
			"parseInt":                       {Classified: true, Receiver: RecvNone, Returns: AliasFresh},
		},
	}
}

func TestJoinLookup(t *testing.T) {
	t.Parallel()
	join := NewJoin(joinFixture())

	tests := map[string]struct {
		ref  MemberRef
		want string
	}{
		"Instance":  {MemberRef{Owner: "Array", Member: StrMember("push"), Sort: SortInstance}, "Array.prototype.push"},
		"Symbol":    {MemberRef{Owner: "Array", Member: SymMember("iterator"), Sort: SortInstance}, "Array.prototype [ @@iterator ]"},
		"Static":    {MemberRef{Owner: "Array", Member: StrMember("from"), Sort: SortStatic}, "Array.from"},
		"Namespace": {MemberRef{Owner: "Math", Member: StrMember("max"), Sort: SortNamespaceFunc}, "Math.max"},
		"Getter": {
			MemberRef{Owner: "Map", Member: StrMember("size"), Sort: SortInstance, Accessor: GetAccessor},
			"get Map.prototype.size",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			specName, fact, ok := join.Lookup(tc.ref)
			require.True(t, ok, "%s should resolve", tc.ref)
			require.Equal(t, tc.want, specName)
			require.True(t, fact.Classified)
		})
	}
}

// The accessor tag is not part of the address, so a plain method never
// resolves to the getter of the same name and the getter never resolves to a
// method's fact.
func TestJoinLookupSeparatesAccessorsFromMethods(t *testing.T) {
	t.Parallel()
	join := NewJoin(joinFixture())

	_, _, ok := join.Lookup(MemberRef{Owner: "Map", Member: StrMember("size"), Sort: SortInstance})
	require.False(t, ok)

	_, _, ok = join.Lookup(MemberRef{
		Owner: "Array", Member: StrMember("push"),
		Sort: SortInstance, Accessor: GetAccessor,
	})
	require.False(t, ok)
}

// The sort is part of the address, so a static never picks up the fact for
// the instance member of the same name.
func TestJoinLookupSeparatesSortsApart(t *testing.T) {
	t.Parallel()
	join := NewJoin(joinFixture())

	_, _, ok := join.Lookup(MemberRef{Owner: "Array", Member: StrMember("push"), Sort: SortStatic})
	require.False(t, ok)
}

// Two spec names that key to the same triple would make the join's answer
// depend on map iteration order. The sorted-first name takes the slot and the
// other is reported as unjoinable rather than dropped.
func TestJoinIndexesOneNamePerTriple(t *testing.T) {
	t.Parallel()

	join := NewJoin(&Facts{
		SpecTarget: "test",
		Methods: map[string]MethodFact{
			"Array.prototype [ @@iterator ]":    {Classified: true, Receiver: RecvBorrow},
			"Array.prototype  [  @@iterator  ]": {Classified: true, Receiver: RecvMutBorrow},
		},
	})

	name, fact, ok := join.Lookup(MemberRef{
		Owner: "Array", Member: SymMember("iterator"), Sort: SortInstance,
	})
	require.True(t, ok)
	require.Equal(t, "Array.prototype  [  @@iterator  ]", name)
	require.Equal(t, RecvMutBorrow, fact.Receiver)
	require.Equal(t, []string{"Array.prototype [ @@iterator ]"}, join.unjoinable)
}

func TestJoinMatch(t *testing.T) {
	t.Parallel()

	decls := []Declaration{
		{
			Ref:        MemberRef{Owner: "Array", Member: StrMember("push"), Sort: SortInstance},
			Signatures: []Signature{{Params: 1, Rest: true}},
		},
		{
			// Two overloads of one algorithm: the shorter one cannot name
			// the parameter the fact returns.
			Ref:        MemberRef{Owner: "String", Member: StrMember("at"), Sort: SortInstance},
			Signatures: []Signature{{Params: 1}, {Params: 2}},
		},
		{
			Ref:        MemberRef{Owner: "Map", Member: StrMember("size"), Sort: SortInstance, Accessor: GetAccessor},
			Signatures: []Signature{{}},
		},
		{
			Ref:        MemberRef{Owner: "Array", Member: StrMember("toSorted"), Sort: SortInstance},
			Signatures: []Signature{{Params: 1}},
		},
	}

	report := NewJoin(joinFixture()).Match(Declarations{
		Keyed:   decls,
		Unkeyed: []string{"parseInt"},
	})

	var lines []string
	for _, match := range report.Matched {
		perSig := make([]string, 0, len(match.PerSignature))
		for _, fact := range match.PerSignature {
			perSig = append(perSig, fact.String())
		}
		lines = append(lines, match.SpecName+" -> "+match.Decl.Ref.String()+
			" receiverApplies:"+boolWord(match.ReceiverApplies())+
			" [ "+strings.Join(perSig, " | ")+" ]")
	}
	for _, ref := range report.DeclsWithoutFact {
		lines = append(lines, "no fact: "+ref.String())
	}
	for _, name := range report.FactsWithoutDecl {
		lines = append(lines, "no declaration: "+name)
	}
	for _, name := range report.UnjoinableFacts {
		lines = append(lines, "unjoinable fact: "+name)
	}
	for _, path := range report.UnkeyedDecls {
		lines = append(lines, "unkeyed declaration: "+path)
	}

	snaps.MatchInlineSnapshot(t, strings.Join(lines, "\n"), snaps.Inline(`Array.prototype.push -> instance Array.push receiverApplies:yes [ receiver:mutBorrow returns:fresh ]
String.prototype.at -> instance String.at receiverApplies:yes [ receiver:borrow returns:unknown | receiver:borrow returns:param(1) ]
get Map.prototype.size -> get instance Map.size receiverApplies:no [ receiver:borrow returns:fresh ]
no fact: instance Array.toSorted
no declaration: Array.from
no declaration: Array.prototype [ @@iterator ]
no declaration: Math.max
no declaration: Set.prototype.add
unjoinable fact: parseInt
unkeyed declaration: parseInt`))
}

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func TestWriteJoinReport(t *testing.T) {
	t.Parallel()

	report := NewJoin(joinFixture()).Match(Declarations{
		Keyed: []Declaration{
			{Ref: MemberRef{Owner: "Array", Member: StrMember("push"), Sort: SortInstance}, Signatures: []Signature{{Params: 1, Rest: true}}},
			{Ref: MemberRef{Owner: "Set", Member: StrMember("add"), Sort: SortInstance}, Signatures: []Signature{{Params: 1}}},
			{Ref: MemberRef{Owner: "Array", Member: StrMember("toSorted"), Sort: SortInstance}, Signatures: []Signature{{Params: 1}}},
		},
		Unkeyed: []string{"parseInt"},
	})

	var out strings.Builder
	require.NoError(t, WriteJoinReport(report, &out))
	snaps.MatchInlineSnapshot(t, out.String(), snaps.Inline(`  join: 2 matched (1 classified), 1 declarations without a fact, 5 facts without a declaration, 1 unkeyed declarations, 1 unjoinable facts
    no fact: instance Array.toSorted
    no declaration: Array.from
    no declaration: Array.prototype [ @@iterator ]
    no declaration: Math.max
    no declaration: String.prototype.at
    no declaration: get Map.prototype.size
    unkeyed declaration: parseInt
`))
}
