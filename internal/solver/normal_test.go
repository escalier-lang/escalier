package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// The normal-forms tests are isolated from constraint solving. Every case here
// pushes a type into DNF or CNF and reads the result back, so nothing in this file
// calls Constrain or records a bound. That keeps the module's behavior pinned
// independently of the solver PR5 (#1062) grafts it into.
//
// A case states its input as an Escalier type annotation and its expected normal
// form as the annotation the normalized type renders under, which is how the rest
// of the solver's tests are written. Negation has no surface syntax, so the two
// helpers below build one.

// not is `¬t`, the complement no annotation can spell.
func not(t soltype.Type) soltype.Type {
	return &soltype.NegationType{Inner: t}
}

// notSrc parses an annotation and complements it, so a case can write `¬number`
// as notSrc(t, "number").
func notSrc(t *testing.T, src string) soltype.Type {
	t.Helper()
	return not(parseType(t, src))
}

// normDNF renders the disjunctive normal form of ty, which is what a round-trip
// case asserts on.
func normDNF(c *Context, ty soltype.Type) string {
	return soltype.Print(c.mkDNF(ty, soltype.Positive).toType())
}

// normCNF renders the conjunctive normal form of ty.
func normCNF(c *Context, ty soltype.Type) string {
	return soltype.Print(c.mkCNF(ty, soltype.Negative).toType())
}

// TestDNFRoundTrip pushes an annotation into DNF and back, and asserts the
// annotation the result renders under. A row whose want repeats its in is a
// round-trip: normalization left the type alone. A row whose want differs records
// a normalization the module performs.
//
// A row that also appears in TestCNFRoundTrip is not a copy of it. See that test's
// comment for why one annotation exercises different code in the two forms.
func TestDNFRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "a primitive is one atom", in: "number", want: "number"},
		{name: "a literal is one atom", in: `"a"`, want: `"a"`},
		{name: "the bottom of the lattice", in: "never", want: "never"},
		{name: "the top of the lattice", in: "unknown", want: "unknown"},
		{name: "a record is one atom", in: "{x: number}", want: "{x: number}"},
		{name: "an inexact record keeps its open marker", in: "{x: number, ...}", want: "{x: number, ...}"},
		{name: "a tuple is one atom", in: "[number, string]", want: "[number, string]"},
		{name: "a function is one atom", in: "fn (x: number) -> string", want: "fn (x: number) -> string"},
		{name: "a borrow is an opaque atom", in: "mut {x: number}", want: "mut {x: number}"},
		{name: "a union of two primitives keeps both", in: "number | string", want: "number | string"},
		{
			name: "two exact records over disjoint fields meet to the bottom of the lattice",
			in:   "{x: number} & {y: number}",
			want: "never",
		},
		{
			name: "two inexact records merge field-wise",
			in:   "{x: number, ...} & {y: number, ...}",
			want: "{x: number, y: number, ...}",
		},
		{
			name: "two exact records over one field narrow that field",
			in:   "{x: number | string} & {x: string | boolean}",
			want: "{x: string}",
		},
		{
			name: "an optional field the other exact record caps out drops",
			in:   "{x: number, y?: string} & {x: number}",
			want: "{x: number}",
		},
		{
			name: "an exact record caps the fields an inexact one leaves open",
			in:   "{x: number, y?: string} & {x: number, ...}",
			want: "{x: number, y?: string}",
		},
		{
			name: "an inexact record requiring a field the exact one caps out meets to never",
			in:   "{x: number} & {y: number, ...}",
			want: "never",
		},
		{
			name: "a field either side requires is required on the meet",
			in:   "{x: number} & {x?: number}",
			want: "{x: number}",
		},
		{
			name: "two tuples of one length meet element-wise",
			in:   "[number, number | string] & [number, string]",
			want: "[number, string]",
		},
		{
			name: "two inexact tuples of different lengths meet at the longer length",
			in:   "[number, ...] & [number, boolean, ...]",
			want: "[number, boolean, ...]",
		},
		{
			name: "the shared prefix of such a meet narrows",
			in:   "[number | string, ...] & [string, boolean, ...]",
			want: "[string, boolean, ...]",
		},
		{
			name: "two exact tuples of different lengths meet to the bottom of the lattice",
			in:   "[number] & [number, boolean]",
			want: "never",
		},
		{
			name: "an exact tuple fixes the length an inexact one leaves open",
			in:   "[number, ...] & [number, boolean]",
			want: "[number, boolean]",
		},
		{
			name: "an exact tuple shorter than an inexact one's floor meets to never",
			in:   "[number] & [number, boolean, ...]",
			want: "never",
		},
		{
			name: "two disjoint primitives meet to the bottom of the lattice",
			in:   "number & string",
			want: "never",
		},
		{
			name: "a primitive narrows to a literal of its own family",
			in:   "number & 5",
			want: "5",
		},
		{
			name: "the same narrowing holds for the string family",
			in:   `string & "hello"`,
			want: `"hello"`,
		},
		{
			name: "two literals of one family are disjoint",
			in:   "true & false",
			want: "never",
		},
		{
			name: "a repeated member of an intersection dedups",
			in:   "{x: number} & {x: number}",
			want: "{x: number}",
		},
		{
			name: "a repeated member of a union dedups",
			in:   "{x: number} | {x: number}",
			want: "{x: number}",
		},
		{
			// The members are inexact so that each distributed meet is inhabited. Two exact
			// records over disjoint fields would meet to `never` and leave nothing to read.
			name: "an intersection distributes over a union",
			in:   "({x: number, ...} | {y: number, ...}) & {z: number, ...}",
			want: "{x: number, z: number, ...} | {y: number, z: number, ...}",
		},
		{
			name: "a primitive absorbs a literal of its own family",
			in:   "5 | number",
			want: "number",
		},
		{
			name: "the same holds for the boolean family",
			in:   "true | boolean",
			want: "boolean",
		},
		{
			name: "two records differing in one field widen that field",
			in:   "{x: number, y: boolean} | {x: string, y: boolean}",
			want: "{x: number | string, y: boolean}",
		},
		{
			name: "two records differing in two fields keep both atoms",
			in:   "{x: number, y: boolean} | {x: string, y: null}",
			want: "{x: number, y: boolean} | {x: string, y: null}",
		},
		{
			name: "a field optional on either side is optional on the join",
			in:   "{x: number} | {x?: number}",
			want: "{x?: number}",
		},
		{
			name: "a marker difference spends the same budget a type difference does",
			in:   "{x: number, y: boolean} | {x?: number, y: null}",
			want: "{x: number, y: boolean} | {x?: number, y: null}",
		},
		{
			name: "two tuples differing in one position widen that position",
			in:   "[number, boolean] | [string, boolean]",
			want: "[number | string, boolean]",
		},
		{
			name: "two tuples differing in two positions keep both atoms",
			in:   "[number, boolean] | [string, null]",
			want: "[number, boolean] | [string, null]",
		},
		{
			name: "two tuples of different lengths keep both atoms",
			in:   "[number] | [number, boolean]",
			want: "[number] | [number, boolean]",
		},
		// The three rows below join an atom with the open version of itself. The open
		// marker decides which side contains the other, and it points the opposite way
		// for an arrow than for a record or a tuple. widerByExactness states why.
		{
			name: "a record joins with its open version at the open one",
			in:   "{x: number} | {x: number, ...}",
			want: "{x: number, ...}",
		},
		{
			name: "a tuple joins with its open version at the open one",
			in:   "[number] | [number, ...]",
			want: "[number, ...]",
		},
		{
			name: "an arrow joins with its open version at the CLOSED one",
			in:   "(fn (x: number) -> boolean) | (fn (x: number, ...) -> boolean)",
			want: "fn (x: number) -> boolean",
		},
		{
			name: "and the meet of that arrow pair takes the open one",
			in:   "(fn (x: number) -> boolean) & (fn (x: number, ...) -> boolean)",
			want: "fn (x: number, ...) -> boolean",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, parseType(t, tt.in)))
		})
	}
}

// TestCNFRoundTrip is the conjunctive twin of TestDNFRoundTrip. The two forms
// denote the same type, so a row here states the same annotation the DNF row
// states, rendered as an intersection of joins rather than a union of meets.
//
// Stating one annotation in both tables is not redundant, because the two forms
// reach the answer by different code. `5 | number` is ONE disjunct holding two
// atoms in its Rnf, so joinAtoms absorbs the literal. The same annotation in DNF is
// TWO conjuncts of one atom each, where absorbing the literal instead takes the
// whole-conjunct fusion canonicalConjuncts runs. `number & string` is the mirror
// image, two disjuncts here against one conjunct of two atoms there. So a row
// stated in only one table leaves the other form's path untested.
func TestCNFRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "a primitive is one atom", in: "number", want: "number"},
		{name: "the bottom of the lattice", in: "never", want: "never"},
		{name: "the top of the lattice", in: "unknown", want: "unknown"},
		{name: "a union is one disjunct", in: "number | string", want: "number | string"},
		// A tuple and a record are the un-fusable pair these two rows need. No merge
		// takes a pair of different kinds apart. Both also stay inhabited, since a
		// value carrying an `x` alongside its indexed elements satisfies each of them.
		{name: "an intersection is two disjuncts", in: "[number] & {x: number}", want: "[number] & {x: number}"},
		{
			name: "a union distributes over an intersection",
			in:   "([number] & {x: number}) | {z: number}",
			want: "([number] | {z: number}) & ({x: number} | {z: number})",
		},
		{
			name: "a primitive absorbs a literal of its own family",
			in:   "5 | number",
			want: "number",
		},
		{
			name: "the same holds for the string family",
			in:   `string | "hello"`,
			want: "string",
		},
		{
			name: "the same holds for the boolean family",
			in:   "true | boolean",
			want: "boolean",
		},
		{
			name: "two records differing in one field widen that field",
			in:   "{x: number, y: boolean} | {x: string, y: boolean}",
			want: "{x: number | string, y: boolean}",
		},
		{
			name: "two records differing in two fields keep both atoms",
			in:   "{x: number, y: boolean} | {x: string, y: null}",
			want: "{x: number, y: boolean} | {x: string, y: null}",
		},
		{
			name: "a field optional on either side is optional on the join",
			in:   "{x: number} | {x?: number}",
			want: "{x?: number}",
		},
		{
			name: "a marker difference spends the same budget a type difference does",
			in:   "{x: number, y: boolean} | {x?: number, y: null}",
			want: "{x: number, y: boolean} | {x?: number, y: null}",
		},
		{
			name: "two tuples differing in one position widen that position",
			in:   "[number, boolean] | [string, boolean]",
			want: "[number | string, boolean]",
		},
		{
			name: "two tuples of different lengths keep both atoms",
			in:   "[number] | [number, boolean]",
			want: "[number] | [number, boolean]",
		},
		{
			name: "the same holds when both are inexact, since the join needs one length",
			in:   "[number, ...] | [number, boolean, ...]",
			want: "[number, ...] | [number, boolean, ...]",
		},
		{
			name: "two disjoint primitives meet to the bottom of the lattice",
			in:   "number & string",
			want: "never",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normCNF(c, parseType(t, tt.in)))
		})
	}
}

// TestNegationRoundTrip pins how a complement normalizes. The `¬` cases a reader
// should never see — a double negation and a complement of a lattice bound — are
// gone by the time the form is read back.
func TestNegationRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   func(t *testing.T) soltype.Type
		want string
	}{
		{
			name: "a complemented primitive",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "number") },
			want: "¬number",
		},
		{
			name: "a double complement cancels",
			in:   func(t *testing.T) soltype.Type { return not(notSrc(t, "number")) },
			want: "number",
		},
		{
			name: "the complement of the bottom of the lattice is the top",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "never") },
			want: "unknown",
		},
		{
			name: "the complement of the top of the lattice is the bottom",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "unknown") },
			want: "never",
		},
		{
			name: "a complemented union stays one negated atom list",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "number | string") },
			want: "¬(number | string)",
		},
		{
			name: "a complemented record",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "{x: number}") },
			want: "¬{x: number}",
		},
		{
			name: "a complement beside a positive atom",
			in: func(t *testing.T) soltype.Type {
				return newIntersection(nil, []soltype.Type{parseType(t, "{x: number, ...}"), notSrc(t, "{y: number}")})
			},
			want: "{x: number, ...} & ¬{y: number}",
		},
		{
			// The two atoms sit in Rnf, which the conjunct reads under a union, so
			// they fuse by the JOIN rules even though the surface type is a meet.
			// ¬5 & ¬number is ¬(5 | number), and the literal is absorbed.
			name: "two complements of one family absorb inside the negated part",
			in: func(t *testing.T) soltype.Type {
				return newIntersection(nil, []soltype.Type{notSrc(t, "5"), notSrc(t, "number")})
			},
			want: "¬number",
		},
		{
			name: "the same holds for the boolean family",
			in: func(t *testing.T) soltype.Type {
				return newIntersection(nil, []soltype.Type{notSrc(t, "true"), notSrc(t, "boolean")})
			},
			want: "¬boolean",
		},
		{
			// The join rules reach records too, so the field the two disagree on is
			// widened rather than the atoms being kept apart.
			name: "two complemented records fuse inside the negated part",
			in: func(t *testing.T) soltype.Type {
				return newIntersection(nil, []soltype.Type{
					notSrc(t, "{x: number, y: boolean}"),
					notSrc(t, "{x: string, y: boolean}"),
				})
			},
			want: "¬{x: number | string, y: boolean}",
		},
		{
			// No value is both a number and a string, so every value fails to be at
			// least one of them.
			name: "a union of two complements whose operands are disjoint is the top",
			in: func(t *testing.T) soltype.Type {
				return newUnion(nil, []soltype.Type{notSrc(t, "number"), notSrc(t, "string")}, false)
			},
			want: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, tt.in(t)))
		})
	}
}

// TestCNFNegationRoundTrip covers the negated structural part of a disjunct, the
// slot Conjunct.neg moves a conjunct's positive part into.
func TestCNFNegationRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   func(t *testing.T) soltype.Type
		want string
	}{
		{
			name: "a complemented record",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "{x: number}") },
			want: "¬{x: number}",
		},
		{
			name: "a complemented intersection is one disjunct negating both atoms",
			in: func(t *testing.T) soltype.Type {
				return notSrc(t, "[number] & {x: number}")
			},
			want: "¬([number] & {x: number})",
		},
		{
			name: "a complement beside a positive atom",
			in: func(t *testing.T) soltype.Type {
				return newUnion(nil, []soltype.Type{parseType(t, "number"), notSrc(t, "{x: number}")}, false)
			},
			want: "number | ¬{x: number}",
		},
		{
			// The dual of the DNF row: the two atoms sit in Lnf, which the disjunct
			// reads under an intersection, so they fuse by the MEET rules even though
			// the surface type is a join. ¬number | ¬5 is ¬(number & 5).
			name: "two complements of one family narrow inside the negated part",
			in: func(t *testing.T) soltype.Type {
				return newUnion(nil, []soltype.Type{notSrc(t, "number"), notSrc(t, "5")}, false)
			},
			want: "¬5",
		},
		{
			// The meet rules reach records too, so the two open records merge
			// field-wise inside the negated part.
			name: "two complemented records fuse inside the negated part",
			in: func(t *testing.T) soltype.Type {
				return newUnion(nil, []soltype.Type{
					notSrc(t, "{x: number, ...}"),
					notSrc(t, "{y: number, ...}"),
				}, false)
			},
			want: "¬{x: number, y: number, ...}",
		},
		{
			// No value is both a number and a string, so every value fails to be at
			// least one of them. The negated part meets to `never`, whose complement
			// is the identity of the intersection the disjuncts sit in, so the
			// disjunct drops and an empty CNF is the top.
			name: "a disjunct whose negated part is uninhabited drops",
			in: func(t *testing.T) soltype.Type {
				return newUnion(nil, []soltype.Type{notSrc(t, "number"), notSrc(t, "string")}, false)
			},
			want: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normCNF(c, tt.in(t)))
		})
	}
}

// TestDeMorgan asserts that complementing a meet gives the join of the
// complements, and complementing a join gives the meet of them, after both sides
// are normalized. The two sides reach their normal forms by different routes —
// one through Conjunct.neg and one through the ordinary construction — so
// agreeing is a statement about the module, not about the printer.
func TestDeMorgan(t *testing.T) {
	tests := []struct {
		name string
		// operands returns the two types the row complements. It takes a Context so a
		// row can mint type variables that both sides share.
		operands func(t *testing.T, c *Context) (soltype.Type, soltype.Type)
	}{
		{
			name: "two type variables, which normalization cannot look inside",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return c.freshVar(0), c.freshVar(0)
			},
		},
		{
			name: "two records",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return parseType(t, "{x: number}"), parseType(t, "{y: number}")
			},
		},
		{
			name: "two primitives",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return parseType(t, "number"), parseType(t, "string")
			},
		},
		{
			name: "a record and a function",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return parseType(t, "{x: number}"), parseType(t, "fn (x: number) -> string")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			a, b := tt.operands(t, c)

			meet := newIntersection(nil, []soltype.Type{a, b})
			join := newUnion(nil, []soltype.Type{a, b}, false)
			negA, negB := not(a), not(b)

			require.Equal(t,
				normDNF(c, newUnion(nil, []soltype.Type{negA, negB}, false)),
				normDNF(c, not(meet)),
				"¬(A & B) and ¬A | ¬B normalize alike")
			require.Equal(t,
				normDNF(c, newIntersection(nil, []soltype.Type{negA, negB})),
				normDNF(c, not(join)),
				"¬(A | B) and ¬A & ¬B normalize alike")
		})
	}
}

// TestDeMorganOverAnInexactUnion pins the one shape the law above does not cover.
//
// An inexact union `A | B | ...` denotes A, B, and an open tail of unknown
// content, so complementing it excludes that tail too:
//
//	¬(A | B | ...)  is  ¬A ∩ ¬B ∩ ¬tail
//
// which is strictly narrower than `¬A & ¬B`. The two are therefore different
// types, and no flag on the intersection would make them agree. Inexactness on a
// union WIDENS it by an unknown member, and the complement of that is a
// NARROWING by an unknown set — something an intersection has no slot for.
// IntersectionType carries no exactness marker at all, since exactness is a
// property of the result rather than of the meet.
//
// So the complement keeps the whole union as one negated atom, tail and all,
// rather than distributing over its members and silently dropping the tail.
// Threading the marker through normalization is PR7's remit (#1064).
func TestDeMorganOverAnInexactUnion(t *testing.T) {
	c := &Context{}
	a, b := parseType(t, "{x: number}"), parseType(t, "{y: number}")
	meetOfComplements := normDNF(c, newIntersection(nil, []soltype.Type{not(a), not(b)}))
	require.Equal(t, "¬({x: number} | {y: number})", meetOfComplements)

	// The exact union obeys the law, which is what TestDeMorgan states over its
	// record row. Repeating it here is what makes the rows below a statement about
	// the marker rather than about the members.
	closed := newUnion(nil, []soltype.Type{a, b}, false)
	require.Equal(t, meetOfComplements, normDNF(c, not(closed)))

	// The inexact union does not, and its complement still carries the tail.
	open := newUnion(nil, []soltype.Type{a, b}, true)
	require.Equal(t, "¬({x: number} | {y: number} | ...)", normDNF(c, not(open)))
	require.NotEqual(t, meetOfComplements, normDNF(c, not(open)))
}

// TestUnreducedAtomsKeepTheirOpenMarkerApart guards the one pair widerByExactness must
// refuse. A `{...S}` does not know its own field names and a `[...P]` does not know
// its own positions, so neither says what the exact side caps. The constraint rules
// treat such an atom as inert and relate two of them only when they are equal, so
// fusing the pair leaves those rules an atom they cannot take apart.
//
// Each source below returns its own argument, which has to check.
func TestUnreducedAtomsKeepTheirOpenMarkerApart(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "Tuple", src: `fn go<P>(x: [...P]) -> [...P] | [...P, ...] { return x }`},
		{name: "Object", src: `fn go<S>(x: {...S}) -> {...S} | {...S, ...} { return x }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
		})
	}
}

// TestComplementsKeepTheOpenMarker covers exactness and negation threading through
// normalization at once. The two are orthogonal, which is what makes the pairing
// easy to break. A change that gets either one right on its own can still drop the
// other where they cross.
//
// `¬{x: number}` excludes the records whose only field is an x. `¬{x: number, ...}`
// excludes every record carrying an x, which is a strictly larger set, so the two
// complements denote different types. Normalization has to keep them apart as atoms
// and still apply the containment between them when they meet or join.
func TestComplementsKeepTheOpenMarker(t *testing.T) {
	c := &Context{}
	closed, open := parseType(t, "{x: number}"), parseType(t, "{x: number, ...}")

	// Each complement round-trips with its marker intact.
	require.Equal(t, "¬{x: number}", normDNF(c, not(closed)))
	require.Equal(t, "¬{x: number, ...}", normDNF(c, not(open)))

	// The two complements are distinct types, so nothing downstream may dedup one
	// into the other. compareType is checked alongside equalType because the merges
	// order atoms before they compare them.
	require.False(t, equalType(not(closed), not(open)))
	require.NotEqual(t, 0, compareType(not(closed), not(open)))

	// `{x: number}` is contained in `{x: number, ...}`, so complementing flips which
	// side absorbs. The meet of the complements excludes the larger set and the join
	// of them excludes the smaller one.
	require.Equal(t, "¬{x: number, ...}",
		normDNF(c, newIntersection(nil, []soltype.Type{not(closed), not(open)})))
	require.Equal(t, "¬{x: number}",
		normDNF(c, newUnion(nil, []soltype.Type{not(closed), not(open)}, false)))
}

// TestExactUnionNormalizesClosed contrasts a closed union with the open union of
// the same members. `"a" | "b"` names its whole member set, so normalization takes
// it apart into one conjunct per member. `"a" | "b" | ...` also admits an open tail
// that no atom stands for, so it stays whole.
//
// The conjunct counts are the substantive half. A meet distributes over the closed
// union's members and cannot distribute over the open one, which is the cost the
// mkDNF arm's comment weighs.
func TestExactUnionNormalizesClosed(t *testing.T) {
	c := &Context{}
	members := parseTypes(t, `"a"`, `"b"`)
	closed := newUnion(nil, members, false)
	open := newUnion(nil, members, true)

	require.Equal(t, `"a" | "b"`, normDNF(c, closed))
	require.Len(t, c.mkDNF(closed, soltype.Positive).Conjuncts, 2)

	require.Equal(t, `"a" | "b" | ...`, normDNF(c, open))
	require.Len(t, c.mkDNF(open, soltype.Positive).Conjuncts, 1)
}

// TestValueAtomsAnswerEqualAtoms checks the value-family merges on their own,
// rather than through the module's entry points.
//
// meetAtoms and joinAtoms answer equal atoms before either of these runs, so the
// case is unreachable in normal use. It is pinned anyway because getting it wrong
// is silent and severe: without the equality rule, `"hello"` met with `"hello"`
// reads as two distinct literals of one family and collapses to `never`.
func TestValueAtomsAnswerEqualAtoms(t *testing.T) {
	tests := []struct {
		name string
		// atom mints the value afresh on each call, so a case compares two separate
		// allocations carrying one value — what a merge sees when the same type
		// reaches it down two routes.
		atom func() soltype.Type
	}{
		{name: "a string literal", atom: func() soltype.Type { return strLit("hello") }},
		{name: "a number literal", atom: func() soltype.Type { return numLit(5) }},
		{name: "a primitive", atom: func() soltype.Type { return str() }},
		{name: "null", atom: func() soltype.Type { return &soltype.NullType{} }},
		{name: "undefined", atom: func() soltype.Type { return &soltype.UndefinedType{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := tt.atom(), tt.atom()

			met, ok := meetValueAtoms(a, b)
			require.True(t, ok, "an atom met with itself fuses")
			require.True(t, equalType(a, met), "met to %s", soltype.Print(met))

			joined, ok := joinValueAtoms(a, b)
			require.True(t, ok, "an atom joined with itself fuses")
			require.True(t, equalType(a, joined), "joined to %s", soltype.Print(joined))
		})
	}
}

// TestUnionKeepsUnmergeableRecords is the caveat-4 regression guard. Two records
// with different field names have no single record that denotes their union, so
// both normal forms keep two members rather than widening. MLscript's RhsNf holds
// one record slot and answers `unknown` for the supertype case, which makes every
// subtype pass against it — planning/ml_struct/06-open-items.md finding 1.
func TestUnionKeepsUnmergeableRecords(t *testing.T) {
	c := &Context{}
	ty := parseType(t, "{x: number} | {y: number}")

	// Subtype position. The union is a two-conjunct DNF, one conjunct per record.
	d := c.mkDNF(ty, soltype.Positive)
	require.Len(t, d.Conjuncts, 2)
	require.Len(t, d.Conjuncts[0].Lnf.Atoms, 1)
	require.Len(t, d.Conjuncts[1].Lnf.Atoms, 1)
	require.Equal(t, "{x: number} | {y: number}", soltype.Print(d.toType()))

	// Supertype position, the one MLscript widens. The union is a single disjunct
	// holding BOTH records, not a disjunct that gave up and became `unknown`.
	n := c.mkCNF(ty, soltype.Negative)
	require.Len(t, n.Disjuncts, 1)
	require.Len(t, n.Disjuncts[0].Rnf.Atoms, 2)
	require.Equal(t, "{x: number} | {y: number}", soltype.Print(n.toType()))
}

// TestInexactUnionStaysOneAtom covers the open tail an `A | B | ...` carries. The
// tail has no atom to stand for it, so the union is not taken apart and the DNF
// holds one conjunct rather than one per written member.
func TestInexactUnionStaysOneAtom(t *testing.T) {
	c := &Context{}
	open := newUnion(nil, parseTypes(t, "number", "string"), true)

	d := c.mkDNF(open, soltype.Positive)
	require.Len(t, d.Conjuncts, 1)
	require.Len(t, d.Conjuncts[0].Lnf.Atoms, 1)
	require.Equal(t, "number | string | ...", soltype.Print(d.toType()))

	n := c.mkCNF(open, soltype.Negative)
	require.Len(t, n.Disjuncts, 1)
	require.Len(t, n.Disjuncts[0].Rnf.Atoms, 1)
	require.Equal(t, "number | string | ...", soltype.Print(n.toType()))

	// The exact union of the same members IS taken apart, which is what makes the
	// two rows above a statement about the marker rather than about the members.
	closed := newUnion(nil, parseTypes(t, "number", "string"), false)
	require.Len(t, c.mkDNF(closed, soltype.Positive).Conjuncts, 2)
}

// TestVariablesStayInTheirSlots checks that a type variable is recorded as a
// variable rather than as a structural atom, positively or negatively, and that
// holding one in both slots makes the conjunct uninhabited.
func TestVariablesStayInTheirSlots(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)

	positive := c.mkDNF(v, soltype.Positive)
	require.Len(t, positive.Conjuncts, 1)
	require.True(t, positive.Conjuncts[0].Vars.Contains(v))
	require.Empty(t, positive.Conjuncts[0].Lnf.Atoms)

	negative := c.mkDNF(not(v), soltype.Positive)
	require.Len(t, negative.Conjuncts, 1)
	require.True(t, negative.Conjuncts[0].NVars.Contains(v))
	require.Empty(t, negative.Conjuncts[0].Rnf.Atoms)

	// `v ∩ ¬v` admits no value, so the conjunct is dropped and the DNF is empty.
	both := newIntersection(nil, []soltype.Type{v, not(v)})
	require.Empty(t, c.mkDNF(both, soltype.Positive).Conjuncts)
	require.Equal(t, "never", normDNF(c, both))

	// `v ∪ ¬v` admits every value, so the disjunct is dropped and the CNF is empty.
	either := newUnion(nil, []soltype.Type{v, not(v)}, false)
	require.Empty(t, c.mkCNF(either, soltype.Negative).Disjuncts)
	require.Equal(t, "unknown", normCNF(c, either))
}

// TestBaseReadsTheSingleClassTag covers the slot the nominal meet writes to. One
// tag in a conjunct is the base, and the meet leaves at most one there.
func TestBaseReadsTheSingleClassTag(t *testing.T) {
	c := &Context{}
	c.registerClass("Point", &ClassDef{})
	c.registerClass("Line", &ClassDef{})
	point := classTag("Point")
	line := classTag("Line")

	one := c.mkDNF(point, soltype.Positive)
	require.Len(t, one.Conjuncts, 1)
	base, ok := one.Conjuncts[0].Lnf.Base()
	require.True(t, ok)
	require.Same(t, point, base)

	// Two tags naming one class fuse through glbClass, so the conjunct still has a
	// single base.
	same := c.mkDNF(newIntersection(nil, []soltype.Type{point, classTag("Point")}), soltype.Positive)
	require.Len(t, same.Conjuncts, 1)
	require.Len(t, same.Conjuncts[0].Lnf.Atoms, 1)

	// No declared edge relates Point to Line, so no value carries both tags and the
	// conjunct is dropped from the DNF.
	unrelated := c.mkDNF(newIntersection(nil, []soltype.Type{point, line}), soltype.Positive)
	require.Empty(t, unrelated.Conjuncts)
	require.Equal(t, "never", normDNF(c, newIntersection(nil, []soltype.Type{point, line})))
}

// classTag is a bare nominal handle, the smallest ClassType a normal form can
// carry as an atom.
func classTag(name string) *soltype.ClassType {
	return &soltype.ClassType{
		Name: name, TypeArgs: nil, LifetimeArgs: nil, Lt: nil, Final: false, Variant: false,
	}
}

// TestNominalAtomsStayDistinct guards the kinds compareType has no structural arm
// for. Those atoms all compare equal to one another under it, so a normal form
// that treated a zero from the comparator as equality would delete every class tag
// but one, and would collapse two conjuncts that negate different tags.
func TestNominalAtomsStayDistinct(t *testing.T) {
	point, line, arc := classTag("Point"), classTag("Line"), classTag("Arc")

	t.Run("a union of three tags keeps all three, in one order", func(t *testing.T) {
		for _, order := range permutations([]string{"Point", "Line", "Arc"}) {
			c := &Context{}
			members := make([]soltype.Type, len(order))
			for i, name := range order {
				members[i] = classTag(name)
			}
			d := c.mkDNF(&soltype.UnionType{Types: members, Inexact: false}, soltype.Positive)
			require.Len(t, d.Conjuncts, 3, "order %v", order)
			require.Equal(t, "Arc | Line | Point", soltype.Print(d.toType()), "order %v", order)
		}
	})

	t.Run("two conjuncts negating different tags both survive", func(t *testing.T) {
		c := &Context{}
		shape := parseType(t, "{a: number, ...}")
		withoutPoint := newIntersection(nil, []soltype.Type{shape, not(point)})
		withoutLine := newIntersection(nil, []soltype.Type{shape, not(line)})

		d := c.mkDNF(newUnion(nil, []soltype.Type{withoutPoint, withoutLine}, false), soltype.Positive)
		require.Len(t, d.Conjuncts, 2)
		require.Equal(t,
			"{a: number, ...} & ¬Line | {a: number, ...} & ¬Point",
			soltype.Print(d.toType()))
	})

	t.Run("an intersection of two tags keeps both, in one order", func(t *testing.T) {
		for _, members := range [][]soltype.Type{{point, arc}, {arc, point}} {
			c := &Context{}
			d := c.mkDNF(&soltype.IntersectionType{Types: members}, soltype.Positive)
			require.Len(t, d.Conjuncts, 1)
			require.Equal(t, "Arc & Point", soltype.Print(d.toType()))
		}
	})
}

// TestFuncMerge pins which intersected function atoms fuse into one arrow and
// which stay apart. The two fusing cases are exact. Keeping the rest apart is the
// shape decision that lets PR5 (#1062) apply the Frisch-Castagna-Benzaken arrow
// decomposition rather than inheriting MLscript's unsound merge —
// planning/ml_struct/06-open-items.md finding 2.
func TestFuncMerge(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "shared codomain: the domains join",
			in:   "(fn (x: number) -> boolean) & (fn (x: string) -> boolean)",
			want: "fn (x: number | string) -> boolean",
		},
		{
			name: "shared domain: the codomains meet",
			in:   "(fn (x: number) -> number | string) & (fn (x: number) -> string | boolean)",
			want: "fn (x: number) -> string",
		},
		{
			name: "shared domain, disjoint codomains: the meet is uninhabited",
			in:   "(fn (x: number) -> number) & (fn (x: number) -> string)",
			want: "fn (x: number) -> never",
		},
		{
			name: "no shared domain or codomain: both arms are kept",
			in:   "(fn (x: number) -> boolean) & (fn (x: string) -> null)",
			want: "(fn (x: number) -> boolean) & (fn (x: string) -> null)",
		},
		{
			name: "a shared codomain over two parameters is not fused position by position",
			in: "(fn (x: number, y: number) -> boolean) & " +
				"(fn (x: string, y: string) -> boolean)",
			want: "(fn (x: number, y: number) -> boolean) & (fn (x: string, y: string) -> boolean)",
		},
		{
			name: "a shared domain over two parameters still meets the codomains",
			in: "(fn (x: number, y: string) -> number | string) & " +
				"(fn (x: number, y: string) -> string | boolean)",
			want: "fn (x: number, y: string) -> string",
		},
		{
			name: "arms differing in the trailing open marker fuse to the open one",
			in:   "(fn (x: number) -> boolean) & (fn (x: string, ...) -> boolean)",
			want: "fn (x: number | string, ...) -> boolean",
		},
		{
			name: "a union of two arrows keeps both, since no single arrow denotes it",
			in:   "(fn (x: number) -> boolean) | (fn (x: number) -> null)",
			want: "(fn (x: number) -> boolean) | (fn (x: number) -> null)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, parseType(t, tt.in)))
		})
	}
}

// TestCNFFusesDisjuncts covers the two rules tryMergeInter applies. Its dual,
// tryMergeUnion over a DNF, is covered by the record and tuple rows of
// TestDNFRoundTrip.
func TestCNFFusesDisjuncts(t *testing.T) {
	t.Run("disjuncts agreeing on their negated part meet their positive parts", func(t *testing.T) {
		c := &Context{}
		// (number | string) & (number | boolean). The two disjuncts differ in one
		// atom, string against boolean, and those meet to `never`, which is the
		// identity of the union the atom sits in, so the position drops.
		in := newIntersection(nil, []soltype.Type{
			newUnion(nil, parseTypes(t, "number", "string"), false),
			newUnion(nil, parseTypes(t, "number", "boolean"), false),
		})
		require.Equal(t, "number", normCNF(c, in))
	})

	t.Run("disjuncts agreeing on their positive part join their negated parts", func(t *testing.T) {
		c := &Context{}
		// (¬{x} | number) & (¬({x} & {y}) | number). The first disjunct's negated
		// part is the narrower of the two, since ¬{x} implies ¬({x} & {y}), so it
		// stands for both.
		x := parseType(t, "{x: number}")
		y := parseType(t, "{y: number}")
		in := newIntersection(nil, []soltype.Type{
			newUnion(nil, []soltype.Type{not(x), parseType(t, "number")}, false),
			newUnion(nil, []soltype.Type{not(newIntersection(nil, []soltype.Type{x, y})), parseType(t, "number")}, false),
		})
		require.Equal(t, "number | ¬{x: number}", normCNF(c, in))
	})
}

// TestCanonicalOrderIsPermutationStable builds one type from its members in every
// order and asserts each order reaches the same normal form. The members are
// assembled into raw lattice nodes rather than through newUnion and
// newIntersection, so the smart constructors' own sorting cannot be what makes the
// orders agree.
//
// The last three rows are the ones with teeth. Their members admit two DIFFERENT
// exact fusions, and taking either one rules the other out. The three arrows fuse
// either their two number-domain arms, meeting the codomains, or their two
// boolean-codomain arms, joining the domains. Scanning in canonical order is what
// settles which fusion a given member SET reaches.
func TestCanonicalOrderIsPermutationStable(t *testing.T) {
	tests := []struct {
		name string
		// members are the annotations the row combines.
		members []string
		// union says whether to combine them with `|` rather than `&`.
		union bool
		want  string
	}{
		{
			name:    "a union of records",
			members: []string{"{y: number}", "{x: number}", "{z: number}"},
			union:   true,
			want:    "{x: number} | {y: number} | {z: number}",
		},
		{
			// Three records would not do here. Exact records over disjoint fields meet to
			// `never`, which leaves no member order to compare, so the row draws one atom
			// from each of three kinds no merge fuses.
			name:    "an intersection of atoms no merge fuses",
			members: []string{"{x: number}", "fn () -> boolean", "[number]"},
			want:    "[number] & {x: number} & (fn () -> boolean)",
		},
		{
			name:    "an intersection of inexact records, which fuses into one",
			members: []string{"{y: number, ...}", "{x: number, ...}", "{z: number, ...}"},
			want:    "{x: number, y: number, z: number, ...}",
		},
		{
			name:    "a union mixing primitives and a literal one of them absorbs",
			members: []string{"5", "string", "number"},
			union:   true,
			want:    "number | string",
		},
		{
			name: "a union of records where two different fusions compete",
			members: []string{
				"{x: number, y: boolean}",
				"{x: string, y: boolean}",
				"{x: number, y: null}",
			},
			union: true,
			want:  "{x: number, y: boolean | null} | {x: string, y: boolean}",
		},
		{
			name: "a union of tuples where two different fusions compete",
			members: []string{
				"[number, boolean]",
				"[string, boolean]",
				"[number, null]",
			},
			union: true,
			want:  "[number, boolean | null] | [string, boolean]",
		},
		{
			name: "an intersection of arrows where two different fusions compete",
			members: []string{
				"fn (x: number) -> string",
				"fn (x: number) -> boolean",
				"fn (x: string) -> boolean",
			},
			want: "(fn (x: number) -> never) & (fn (x: string) -> boolean)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, order := range permutations(tt.members) {
				c := &Context{}
				parts := parseTypes(t, order...)
				var raw soltype.Type
				if tt.union {
					raw = &soltype.UnionType{Types: parts, Inexact: false}
				} else {
					raw = &soltype.IntersectionType{Types: parts}
				}
				require.Equal(t, tt.want, normDNF(c, raw), "order %v", order)
			}
		})
	}
}

// TestMkDeepNormalizesChildren contrasts the shallow form with the deep one. The
// shallow form settles the Boolean structure at the top level and leaves an atom's
// children alone; the deep form normalizes every position, including the
// contravariant ones a function's parameters sit in.
func TestMkDeepNormalizesChildren(t *testing.T) {
	tests := []struct {
		name string
		// build assembles the input around a doubly-complemented sub-part, the
		// simplest thing normalization removes.
		build func(t *testing.T) soltype.Type
		// shallow is the form mkDNF reaches, which leaves the sub-part untouched.
		shallow string
		// deep is the form mkDeepDNF reaches.
		deep string
	}{
		{
			name: "a complement in a return type",
			build: func(t *testing.T) soltype.Type {
				return &soltype.FuncType{
					SelfParam: nil,
					Params:    []*soltype.FuncParam{identParam("x", num())},
					Ret:       not(notSrc(t, "string")),
					Throws:    nil, Inexact: false, TypeParams: nil, LifetimeParams: nil,
				}
			},
			shallow: "fn (x: number) -> ¬¬string",
			deep:    "fn (x: number) -> string",
		},
		{
			name: "a complement in a parameter, which sits at the flipped polarity",
			build: func(t *testing.T) soltype.Type {
				return &soltype.FuncType{
					SelfParam: nil,
					Params:    []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: not(notSrc(t, "number")), Optional: false, Rest: false}},
					Ret:       str(),
					Throws:    nil, Inexact: false, TypeParams: nil, LifetimeParams: nil,
				}
			},
			shallow: "fn (x: ¬¬number) -> string",
			deep:    "fn (x: number) -> string",
		},
		{
			name: "an uninhabited meet in a field",
			build: func(t *testing.T) soltype.Type {
				return exactObj(propElem("x", newIntersection(nil, parseTypes(t, "number", "string"))))
			},
			shallow: "{x: number & string}",
			deep:    "{x: never}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			in := tt.build(t)
			require.Equal(t, tt.shallow, normDNF(c, in))
			require.Equal(t, tt.deep, soltype.Print(c.mkDeepDNF(in, soltype.Positive).toType()))
			require.Equal(t, tt.deep, soltype.Print(c.mkDeepCNF(in, soltype.Negative).toType()))
		})
	}
}

// TestDeepNormalizeKeepsBorrows pins that normalizing a borrow's inner never
// costs the borrow its wrapper. soltype's visitor peels a `mut` whose rewritten
// inner is not borrowable, which is right for coalescing and would silently drop
// mutability here.
func TestDeepNormalizeKeepsBorrows(t *testing.T) {
	tests := []struct {
		name string
		// build assembles the borrow, since `mut` over a union has no annotation
		// form the test parser accepts alongside the members these rows need.
		build func(t *testing.T) soltype.Type
		want  string
	}{
		{
			name: "an inner that normalizes to a borrowable type is rewritten",
			build: func(t *testing.T) soltype.Type {
				return mutRef(newUnion(nil, parseTypes(t, "{x: number, ...}", "{y: number, ...}"), false).(soltype.RefInner))
			},
			want: "mut ({x: number, ...} | {y: number, ...})",
		},
		{
			name: "an inner that normalizes to a bare primitive keeps the borrow as written",
			build: func(t *testing.T) soltype.Type {
				return mutRef(newUnion(nil, parseTypes(t, "number", "5"), false).(soltype.RefInner))
			},
			want: "mut (number | 5)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, soltype.Print(c.mkDeepDNF(tt.build(t), soltype.Positive).toType()))
		})
	}
}

// permutations returns every ordering of items. The member lists are three long,
// so the six orderings are cheap to run in full.
func permutations(items []string) [][]string {
	if len(items) <= 1 {
		return [][]string{items}
	}
	var out [][]string
	for i := range items {
		rest := make([]string, 0, len(items)-1)
		rest = append(rest, items[:i]...)
		rest = append(rest, items[i+1:]...)
		for _, tail := range permutations(rest) {
			order := make([]string, 0, len(items))
			order = append(order, items[i])
			order = append(order, tail...)
			out = append(out, order)
		}
	}
	return out
}

// TestDeepNormalizeKeepsKnots covers the identity discipline a μ-knot needs. The
// solver decides two recursive types by recognizing a pair of knots it is already
// comparing, and it recognizes them by node identity, so normalization hands a
// knot back as written and no fusion rebuilds one.
func TestDeepNormalizeKeepsKnots(t *testing.T) {
	// μX0.({head: number, tail: undefined} | {head: number, tail: X0}), the shape a
	// recursive list builder infers. Its body is the union whose members would
	// otherwise fuse into `{head: number, tail: undefined | X0}`.
	knot := muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
		return newUnion(nil, []soltype.Type{
			exactObj(propElem("head", num()), propElem("tail", &soltype.UndefinedType{})),
			exactObj(propElem("head", num()), propElem("tail", ref)),
		}, false)
	})

	t.Run("the knot itself is handed back", func(t *testing.T) {
		c := &Context{}
		require.Same(t, knot, c.mkDeepDNF(knot, soltype.Positive).toType())
	})

	t.Run("a union of atoms carrying the knot keeps both", func(t *testing.T) {
		// The two members differ in one field, which is what joinObjects fuses. They
		// stay apart because the fused field would hold the knot in a fresh node.
		c := &Context{}
		members := newUnion(nil, []soltype.Type{
			exactObj(propElem("head", num()), propElem("tail", &soltype.UndefinedType{})),
			exactObj(propElem("head", num()), propElem("tail", knot)),
		}, false)
		require.Equal(t,
			"{head: number, tail: undefined} | {head: number, tail: μX0.({head: number, tail: undefined} | {head: number, tail: X0})}",
			soltype.Print(c.mkDeepDNF(members, soltype.Positive).toType()))
	})
}
