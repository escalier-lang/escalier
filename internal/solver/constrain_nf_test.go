package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- The conformance oracle for normal-form subtyping ---
//
// The two tables below, nfArrowCorpus and nfRecordCorpus, state for each
// subtyping case the verdict that is sound under a types-as-values reading. In
// that reading a type denotes the set of values that inhabit it, and
// `sub <: super` is true exactly when every value of sub is a value of super.
// Every verdict here is derived by hand from that reading. None is read off an
// implementation, neither Escalier's nor MLscript's. That is what makes the
// table an oracle. It measures the normal-form solver against truth rather than
// against a guess.
//
// Two shapes are pinned, both drawn from planning/ml_struct/.
//
//  1. Arrow rows — an intersection of function types against a single function
//     type. This is where a "merge the arms into one arrow" reading diverges from
//     the sound answer, and it becomes user-visible through conditional-type
//     `extends`.
//  2. Negative-position record-union rows — an ordinary type against a union of
//     records used as the supertype. MLstruct's normal form over-approximates
//     that supertype to `unknown`. These rows state the sound answers, so a port
//     that inherited the widening fails them.
//
// # The arrow decomposition
//
// The sound verdict for an arrow row comes from the Frisch-Castagna-Benzaken
// decomposition, which does not collapse the intersection to one arrow. An arrow
// has two outputs, the value it returns and the exception it raises, so a row is
// written `Aᵢ → Cᵢ raises Gᵢ` and the target `E → F raises H`. To decide
// `⋂ᵢ (Aᵢ → Cᵢ raises Gᵢ) <: (E → F raises H)`, both of these must hold.
//
//  1. The target domain is covered: `E <: ⋃ᵢ Aᵢ`. No input the target accepts may
//     fall outside every arm's domain.
//  2. For every subset P of the arms, either the inputs are still covered without
//     P — `E <: ⋃_{i∉P} Aᵢ` — or the arms in P produce outputs the target
//     tolerates, both `⋂_{i∈P} Cᵢ <: F` and `⋂_{i∈P} Gᵢ <: H`.
//
// Leg 2 reads concretely as follows. A value that has all of the arm types
// answers a given input the way every arm whose domain accepts that input does, so
// it returns something in each of their codomains and raises something each of
// their clauses admits. The subtyping therefore fails exactly when some group of
// arms is the only cover for part of E while their combined codomain escapes F or
// their combined clause escapes H.
//
// A row that writes no `throws` clause raises `never`, so its raises half of leg 2
// is `never <: never` and the row is decided by its codomain half alone.
//
// # The record-union widening
//
// A supertype union of two records with different field names has no single
// record that stands for it. MLscript's `RhsNf` holds one record slot, so
// `NormalForms.scala` bails on the second field and treats the whole disjunct as
// `unknown` — see planning/ml_struct/06-open-items.md finding 1. Under that
// widening every sub passes. The rows below that must fail are therefore the
// regression guard, since a port inheriting the widening answers "holds" on them.
//
// What triggers the bail is the second field being differently named. The last
// two rows vary exactly that. Their members share the field name `tag` and differ
// only in its literal type, which isolates differing field names as the cause
// rather than a record union in supertype position as such.
//
// # How the MLscript column was filled
//
// The mlscript column is derived from the two source reads recorded in
// planning/ml_struct/06-open-items.md, not from a run of hkust-taco/mlstruct. Each
// arrow row is what finding 2's rule answers: intersected arrows merge to
// `(A | B) -> (C & D)` and the plain arrow rule decides that one merged arrow, so
// the row's answer is the merged arrow's. A record row whose two members name
// DIFFERENT fields is what finding 1's widening answers: that supertype union
// becomes `unknown`, which every subtype satisfies, so the row answers "holds".
//
// The two tagged rows stay unobserved. Their members share the field name `tag`,
// and a same-named second field is not what makes the widening bail, so neither
// finding settles them. Filling those two needs a run.
//
// Every row carrying a `throws` clause stays unobserved as well. MLscript has no
// such clause, so it cannot state the row at all and no finding settles it.
//
// Two rows disagree with the oracle under those rules, and each says so in its
// why. Both are cases where the merged codomain collapses to `never` and the
// merged arrow accepts an input no single arm covers. That divergence is what
// caveat 4 in planning/ml_struct/02-caveats-and-mitigations.md asks the port to
// document, and the port follows the sound column.

// nfVerdict is a subtyping answer. nfUnobserved is the zero value so a row that
// nobody has run against MLscript reads as blank rather than as "fails".
type nfVerdict int

const (
	nfUnobserved nfVerdict = iota
	nfHolds
	nfFails
)

func (v nfVerdict) String() string {
	switch v {
	case nfHolds:
		return "holds"
	case nfFails:
		return "fails"
	}
	return "unobserved"
}

// nfRow is one case of the corpus.
type nfRow struct {
	// name says which shape the row pins and titles its subtest.
	name string
	// sub and super are Escalier type annotations. They parse under one shared
	// environment, so a name listed in tvars means the same variable in both.
	sub, super string
	// tvars names the free type variables the row's annotations reference. The
	// runner mints one fresh variable per name.
	tvars []string
	// sound is the oracle: the verdict a types-as-values reading gives, derived by
	// hand. Later work is measured against this column, not against the column
	// below it.
	sound nfVerdict
	// why records the step that produces sound — which leg of the arrow
	// decomposition decides the row, or which part of the record-union reasoning.
	why string
	// mlscript is what MLscript answers, derived from the source reads as the
	// header section "How the MLscript column was filled" describes. Where the two
	// columns disagree, sound wins and the divergence is documented — see caveat 4
	// in planning/ml_struct/02-caveats-and-mitigations.md.
	mlscript nfVerdict
	// wantErrs is the full set of diagnostics the solver reports for the row, nil
	// when it reports none. It is empty exactly when sound is nfHolds, a
	// consistency the runner checks so the two columns cannot drift apart. A
	// message here says what a user sees. It need not name the step why gives,
	// because a row can reach the right verdict by another route.
	wantErrs []string
}

// nfArrowCorpus holds the arrow-intersection rows. Worked examples A and B come
// from planning/ml_struct/04-type-level-operators.md.
var nfArrowCorpus = []nfRow{
	{
		name:  "example A: distinct domains, shared codomain",
		sub:   "(fn (x: number) -> boolean) & (fn (x: string) -> boolean)",
		super: "fn (x: number | string) -> boolean",
		sound: nfHolds,
		why: "leg 1 holds since number | string is exactly the union of the two arm domains. Every " +
			"group of arms returns a boolean, so leg 2 holds through its codomain half",
		mlscript: nfHolds,
	},
	{
		name:  "example B: distinct domains, conflicting codomains",
		sub:   "(fn (x: number) -> boolean) & (fn (x: string) -> null)",
		super: "fn (x: number | string) -> boolean",
		sound: nfFails,
		why: "leg 2 fails for the group holding the string arm alone: a string input is covered by " +
			"no other arm, and that arm's codomain null is not a boolean. MLscript merges the arms " +
			"to (number | string) -> (boolean & null) = (number | string) -> `never` and accepts, " +
			"diverging from the sound answer (06-open-items.md finding 2)",
		mlscript: nfHolds,
		wantErrs: []string{
			"cannot constrain number <: string",
			"cannot constrain null <: boolean",
		},
	},
	{
		name:  "distinct domains, distinct codomains: the target unions both codomains",
		sub:   "(fn (x: number) -> boolean) & (fn (x: string) -> null)",
		super: "fn (x: number | string) -> boolean | null",
		sound: nfHolds,
		why: "the same arms as example B against a target that tolerates both codomains. Leg 1 " +
			"holds since the arm domains cover number | string. Leg 2 holds for each group: the " +
			"number arm alone returns a boolean, the string arm alone returns null, and the group " +
			"holding both returns boolean & null, which no value inhabits. Checking one arm at a " +
			"time rejects the row, since neither arm alone accepts both a number and a string",
		mlscript: nfHolds,
	},
	{
		name:  "shared domain, distinct codomains: the target takes their meet",
		sub:   "(fn (x: number) -> number | string) & (fn (x: number) -> string | boolean)",
		super: "fn (x: number) -> string",
		sound: nfHolds,
		why: "the (A -> B) & (A -> C) <: A -> (B & C) shape. Only the group holding both arms is " +
			"uncovered, and its combined codomain (number | string) & (string | boolean) is string, " +
			"which the target accepts",
		mlscript: nfHolds,
	},
	{
		name:  "shared domain, distinct codomains: the target misses their meet",
		sub:   "(fn (x: number) -> number) & (fn (x: number) -> number | string)",
		super: "fn (x: number) -> string",
		sound: nfFails,
		why: "the meet of the codomains is number, so a number input can yield a number where the " +
			"target promises a string. Leg 2 fails for the group holding both arms",
		mlscript: nfFails,
		wantErrs: []string{"cannot constrain number <: string"},
	},
	{
		name:  "shared domain, disjoint codomains: the meet is uninhabited",
		sub:   "(fn (x: number) -> number) & (fn (x: number) -> string)",
		super: "fn (x: number) -> boolean",
		sound: nfHolds,
		why: "the verdict is vacuous, and it is the corner an implementation is most likely to get " +
			"wrong. A value with both arm types would return both a number and a string on any " +
			"number input, and no value is both, so such a value never returns on a number input. " +
			"Its combined codomain is `never`, and `never` <: boolean, so leg 2 holds for the group " +
			"holding both arms",
		mlscript: nfHolds,
	},
	{
		name:  "overlapping domains, shared codomain",
		sub:   "(fn (x: number | string) -> boolean) & (fn (x: boolean) -> boolean)",
		super: "fn (x: number | boolean) -> boolean",
		sound: nfHolds,
		why: "leg 1 holds since number | boolean sits inside number | string | boolean, and every " +
			"group returns a boolean, so leg 2 holds through its codomain half. The overlap on " +
			"number changes nothing, since covering an input twice is not a conflict",
		mlscript: nfHolds,
	},
	{
		name:  "overlapping domains, one codomain escapes",
		sub:   "(fn (x: number | string) -> boolean) & (fn (x: boolean) -> number)",
		super: "fn (x: number | boolean) -> boolean",
		sound: nfFails,
		why: "leg 2 fails for the group holding the boolean arm alone: a boolean input is covered " +
			"by no other arm, and that arm returns a number. This is the second row where MLscript " +
			"diverges: it merges the arms to (number | string | boolean) -> (boolean & number), " +
			"whose codomain is `never`, and accepts (06-open-items.md finding 2)",
		mlscript: nfHolds,
		wantErrs: []string{"cannot constrain boolean <: number | string"},
	},
	{
		name:  "the target domain is not covered",
		sub:   "(fn (x: number) -> boolean) & (fn (x: string) -> boolean)",
		super: "fn (x: number | boolean) -> boolean",
		sound: nfFails,
		why: "leg 1 fails: a boolean input is outside both arm domains, so no arm says what the " +
			"value does with it. This is the leg that distinguishes the row from example A",
		mlscript: nfFails,
		wantErrs: []string{"cannot constrain boolean <: number | string"},
	},
	{
		name:  "nested arrows: the intersection sits in the codomain",
		sub:   "fn (x: number) -> ((fn (y: number) -> boolean) & (fn (y: string) -> boolean))",
		super: "fn (x: number) -> (fn (y: number | string) -> boolean)",
		sound: nfHolds,
		why: "the codomains are compared covariantly, and that comparison is example A, so the " +
			"decomposition has to run underneath an ordinary arrow rule rather than only at the top",
		mlscript: nfHolds,
	},
	{
		name: "nested arrows: the arms take functions",
		sub: "(fn (f: fn (x: number) -> number) -> boolean) & " +
			"(fn (f: fn (x: string) -> string) -> boolean)",
		super: "fn (f: (fn (x: number) -> number) | (fn (x: string) -> string)) -> boolean",
		sound: nfHolds,
		why: "example A one level up, with function types as the domains. Leg 1 holds since the " +
			"target domain is the union of the two arm domains, and every group returns a boolean",
		mlscript: nfHolds,
	},
	{
		name:  "an arm's codomain is a free type variable",
		sub:   "(fn (x: number) -> T) & (fn (x: string) -> T)",
		super: "fn (x: number | string) -> T",
		tvars: []string{"T"},
		sound: nfHolds,
		why: "example A with T for boolean. The verdict does not depend on what T is, since the " +
			"domains cover number | string and every group's combined codomain is T & T = T",
		mlscript: nfHolds,
	},
	{
		name: "shared codomain and raises: the target widens the clause",
		sub: `(fn (x: number) -> boolean throws "a") & ` +
			`(fn (x: string) -> boolean throws "a")`,
		super: `fn (x: number | string) -> boolean throws "a" | "b"`,
		sound: nfHolds,
		why: `example A with a clause on every arrow. The arms agree on both outputs, so they ` +
			`fuse to the one arrow fn (x: number | string) -> boolean throws "a", and the ` +
			`fused clause is compared covariantly against the target's wider one`,
	},
	{
		name:  "the reverse direction: the target narrows the clause",
		sub:   `fn (x: number | string) -> boolean throws "a" | "b"`,
		super: `fn (x: number | string) -> boolean throws "a"`,
		sound: nfFails,
		why: `the mirror of the row above, and the one that pins the direction. A call may raise ` +
			`"b", which the target's clause does not admit, so the covariant comparison of the ` +
			`two clauses fails`,
		wantErrs: []string{`cannot constrain "b" <: "a"`},
	},
	{
		name: "distinct domains, distinct raises: the target unions both clauses",
		sub: `(fn (x: number) -> boolean throws "a") & ` +
			`(fn (x: string) -> boolean throws "b")`,
		super: `fn (x: number | string) -> boolean throws "a" | "b"`,
		sound: nfHolds,
		why: `the arms disagree on what they raise, so no single arrow denotes their meet and the ` +
			`decomposition decides the row. Leg 1 holds since the arm domains cover ` +
			`number | string. Leg 2 holds for each group: the number arm alone raises "a", the ` +
			`string arm alone raises "b", and the group holding both raises "a" & "b", which no ` +
			`value inhabits`,
	},
	{
		name: "distinct domains: one arm's clause escapes the target",
		sub: `(fn (x: number) -> boolean throws "a") & ` +
			`(fn (x: string) -> boolean throws "b")`,
		super: `fn (x: number | string) -> boolean throws "a"`,
		sound: nfFails,
		why: `leg 2 fails for the group holding the string arm alone: a string input is covered by ` +
			`no other arm, and that arm raises "b", which the target's clause does not admit. ` +
			`This is the raises half of the leg that example B fails through its codomain half`,
		wantErrs: []string{
			"cannot constrain number <: string",
			`cannot constrain "b" <: "a"`,
		},
	},
	{
		name: "shared domain, distinct raises: the target takes their meet",
		sub: `(fn (x: number) -> boolean throws "a" | "b") & ` +
			`(fn (x: number) -> boolean throws "b" | "c")`,
		super: `fn (x: number) -> boolean throws "b"`,
		sound: nfHolds,
		why: `the raises twin of the shared-domain codomain row. A value with both arm types raises ` +
			`only what both clauses admit, so a call raises ("a" | "b") & ("b" | "c"), which is ` +
			`"b", and the target accepts it`,
	},
	{
		name: "shared domain, disjoint raises: the combined clause is uninhabited",
		sub: `(fn (x: number) -> boolean throws "a") & ` +
			`(fn (x: number) -> boolean throws "b")`,
		super: "fn (x: number) -> boolean",
		sound: nfHolds,
		why: `the verdict is vacuous, the raises twin of the disjoint-codomains row. A value with ` +
			`both arm types would have to raise both "a" and "b" on any number input, and no value ` +
			`is both, so such a value never raises on a number input. Its combined clause is ` +
			"`never`, which the target's unwritten clause admits",
	},
}

// nfRecordCorpus holds the negative-position record-union rows. Each one uses a
// union of records as the supertype, the position where MLscript's normal form
// widens to `unknown`.
var nfRecordCorpus = []nfRow{
	{
		name:     "a member of the union",
		sub:      "{x: number}",
		super:    "{x: number} | {y: number}",
		sound:    nfHolds,
		why:      "sanity: the sub is one of the union's members, so the union works at all",
		mlscript: nfHolds,
	},
	{
		name:  "a primitive against the union",
		sub:   "number",
		super: "{x: number} | {y: number}",
		sound: nfFails,
		why: "a number is not a record, so it inhabits neither member. This is the regression " +
			"guard. Under the widening the supertype becomes `unknown` and the row answers " +
			`"holds" (06-open-items.md finding 1)`,
		mlscript: nfHolds,
		wantErrs: []string{"cannot constrain number <: object | object"},
	},
	{
		name:  "the empty record against the union",
		sub:   "{}",
		super: "{x: number} | {y: number}",
		sound: nfFails,
		why: "the empty record has neither x nor y, so it satisfies neither member. It is the " +
			"closest miss a record can be, which is why it is worth pinning next to the primitive",
		mlscript: nfHolds,
		wantErrs: []string{"cannot constrain object <: object | object"},
	},
	{
		name:     "an unrelated field against the union",
		sub:      "{z: number}",
		super:    "{x: number} | {y: number}",
		sound:    nfFails,
		why:      "control: a record shaped like the members but named differently satisfies neither",
		mlscript: nfHolds,
		wantErrs: []string{"cannot constrain object <: object | object"},
	},
	{
		name:  "a matching tag against the tagged union",
		sub:   `{tag: "a"}`,
		super: `{tag: "a"} | {tag: "b"}`,
		sound: nfHolds,
		why: "the sub is the first member. The union's members share the field name `tag`, so the " +
			"row differs from the ones above only in what the widening reacts to",
	},
	{
		name:     "an unmatched tag against the tagged union",
		sub:      `{tag: "c"}`,
		super:    `{tag: "a"} | {tag: "b"}`,
		sound:    nfFails,
		why:      `"c" is neither "a" nor "b", so the sub inhabits neither member`,
		wantErrs: []string{"cannot constrain object <: object | object"},
	},
}

// TestConstrainNFArrowCorpus runs the arrow-intersection rows.
func TestConstrainNFArrowCorpus(t *testing.T) {
	runNFCorpus(t, nfArrowCorpus)
}

// TestConstrainNFRecordCorpus runs the negative-position record-union rows.
func TestConstrainNFRecordCorpus(t *testing.T) {
	runNFCorpus(t, nfRecordCorpus)
}

// runNFCorpus checks each row against the solver, asserting the diagnostics the
// row records. A row where MLscript is known to disagree with the oracle logs the
// disagreement, so a verbose run reads as the divergence roster caveat 4 asks the
// port to document.
func runNFCorpus(t *testing.T, corpus []nfRow) {
	t.Helper()
	for _, tt := range corpus {
		t.Run(tt.name, func(t *testing.T) {
			// A row states an oracle verdict. Leaving sound at its zero value would
			// otherwise read as nfFails below and join the corpus green without ever
			// having been derived.
			require.NotEqual(t, nfUnobserved, tt.sound, "a row states a sound verdict")

			c := &Context{}
			env := make(map[string]soltype.Type, len(tt.tvars))
			for _, name := range tt.tvars {
				env[name] = c.freshVar(0)
			}
			sub := parseTypeIn(t, env, tt.sub)
			super := parseTypeIn(t, env, tt.super)

			t.Logf("sound: %s — %s", tt.sound, tt.why)
			if tt.mlscript != nfUnobserved && tt.mlscript != tt.sound {
				t.Logf("MLscript answers %s here; the port follows the sound column", tt.mlscript)
			}
			// The two columns state the same thing about a row that runs, so a row
			// that reports errors while claiming to hold is a table mistake rather
			// than a solver result worth reporting.
			if tt.sound == nfHolds {
				require.Empty(t, tt.wantErrs, "a row that holds records no diagnostics")
			} else {
				require.NotEmpty(t, tt.wantErrs, "a row that fails records the diagnostics it produces")
			}
			require.Equal(t, tt.wantErrs, Messages(c.Constrain(sub, super)))
		})
	}
}
