package ecma262

import (
	"fmt"
	"io"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
)

// coercionAOs are the abstract operations whose `TypeError` reports that the
// value they were handed has the wrong dynamic type. FR11 discounts such a
// throw when the value is one Escalier already types, because a well-typed
// caller cannot reach the path that raises it. See
// planning/ecma-262/implementation_plan.md §9.2.
//
// Two groups sit here. The first are the coercions ECMA-262 applies to a value
// before working with it, and they raise on a value no coercion accepts, such
// as `ToString` on a Symbol or `ToObject` on `undefined`. The `This*Value`
// operations are the receiver's counterpart: `Number.prototype.toFixed` opens
// with `? ThisNumberValue(this value)`, which hands back the receiver's Number
// value and raises when the receiver is neither a Number nor a Number wrapper.
//
// Each entry checks the value at position 0, which coercionGuardArg names.
//
// `ToPrimitive` is the one operation FR11 names that is absent. Its only
// `Throw` step is the one reached when the object's own `@@toPrimitive` method
// returned an object rather than a primitive, which is the caller's code
// failing and not a check on the value `ToPrimitive` was handed. Nothing about
// a declared receiver type rules it out. `ToPrimitive` still appears mid-chain,
// where the base is the `ToObject` its `GetMethod` lookup reaches, and that
// base is a check the declaration does rule out.
//
// A brand check such as `RequireInternalSlot` is deliberately absent too,
// though a declared receiver type implies the slot as surely as it implies the
// Number value. FR11 is a heuristic and §9.4's validation harness is not built
// yet, so nothing would catch a wrong entry. The list grows by review, and
// TestCoercionAOsRaiseOnTheirFirstArgument is what an addition is read
// against.
var coercionAOs = set.FromSlice([]string{
	"RequireObjectCoercible",
	"ToNumber",
	"ToNumeric",
	"ToObject",
	"ToString",

	"ThisBigIntValue",
	"ThisBooleanValue",
	"ThisNumberValue",
	"ThisStringValue",
	"ThisSymbolValue",
})

// coercionGuardArg is the position of the value a coercion operation checks.
// Every entry in coercionAOs declares that one parameter and nothing else, and
// TestCoercionAOsRaiseOnTheirFirstArgument names it per operation.
const coercionGuardArg = 0

// coercionFilter drops the throw sites FR11 discounts and records what it did.
// summary supplies the sites and the origin map of every function a site's
// provenance chain runs through.
type coercionFilter struct {
	cfg     *CFG
	summary *ThrowSummary
	report  FilterReport
}

// FilterDecision is one adjudicated throw site. Method is the builtin the site
// belongs to and Site renders the site and its provenance chain as
// ThrowSite.String does. Coercion names the operation whose type check the
// chain bottoms out at, and Coerced spells where in Method the value it checked
// came from. Dropped says which way the decision went.
//
// Only a `TypeError` reaches a decision. Every other exception leaves the
// filter untouched, so recording it would say nothing a reviewer can act on.
//
// Coercion is empty where the chain bottoms out at something else, such as an
// explicit domain check or a method the algorithm called on an object the
// caller supplied. Coerced is empty where the chain does reach a coercion but
// the value it checked could not be threaded back to Method's own receiver or
// parameters.
type FilterDecision struct {
	Method   string
	Site     string
	Coercion string
	Coerced  string
	Dropped  bool
}

func (d FilterDecision) String() string {
	verdict := "kept"
	if d.Dropped {
		verdict = "dropped"
	}
	switch {
	case d.Coercion == "":
		return fmt.Sprintf("%s: %s %s", d.Method, verdict, d.Site)
	case d.Coerced == "":
		return fmt.Sprintf("%s: %s %s [%s, value untraced]", d.Method, verdict, d.Site, d.Coercion)
	default:
		return fmt.Sprintf("%s: %s %s [%s of %s]", d.Method, verdict, d.Site, d.Coercion, d.Coerced)
	}
}

// FilterReport is every decision one run of the filter made, for review rather
// than for the wire. facts.json records the surviving exceptions and not which
// sites produced them, and FR11 asks for the decisions themselves to be
// reported because the filter is a heuristic over throw provenance.
type FilterReport struct {
	// Decisions holds one entry per `TypeError` site the filter adjudicated,
	// grouped by method. See sortDecisions for the order within a method.
	Decisions []FilterDecision
	// UnderReported holds the methods whose channels the throw fixpoint could
	// not read whole, sorted. Both channels are published for such a method all
	// the same, missing whatever the unread step raises, which is the direction
	// FR5 prefers and FR10 asks to have flagged. It is the same list as
	// Facts.Unclassified(AxisThrows), reported here so a run names it.
	UnderReported []string
}

// Dropped returns the decisions that discounted a site, which is the list the
// §9.2 gate asks a reviewer to read.
func (r FilterReport) Dropped() []FilterDecision {
	var dropped []FilterDecision
	for _, decision := range r.Decisions {
		if decision.Dropped {
			dropped = append(dropped, decision)
		}
	}
	return dropped
}

// Counts tallies the decisions as adjudicated and dropped.
func (r FilterReport) Counts() (adjudicated, dropped int) {
	for _, decision := range r.Decisions {
		if decision.Dropped {
			dropped++
		}
	}
	return len(r.Decisions), dropped
}

// channels is what the filter concluded about one method: the exceptions that
// survive on each of the two exits, and whether the throw fixpoint read every
// step feeding them. A step it could not read leaves both channels
// under-reported, which FR5 prefers to over-reporting a throw and FR10 asks to
// have flagged.
type channels struct {
	raised  []Exception
	rejects []Exception
	settled bool
}

// filterThrows returns fn's two channels with the receiver-coercion
// `TypeError`s discounted. Both are filtered the same way, because a site's
// channel is settled by the exit it reaches and both carry coercion guards.
//
// The reject channel also carries what the combinator model supplies. Those
// rejections reach the returned promise through the promise-resolution
// machinery rather than through a step of fn's, so they have no site to
// adjudicate.
func (f *coercionFilter) filterThrows(fn *Func) channels {
	throws := f.summary.Of(fn)
	kept := set.NewSet[Exception]()
	for _, site := range throws.SyncSites() {
		if !f.discount(fn, site) {
			kept.Add(site.Exception)
		}
	}
	rejected := set.FromSlice(throws.Modeled)
	for _, site := range throws.RejectSites() {
		if !f.discount(fn, site) {
			rejected.Add(site.Exception)
		}
	}
	return channels{
		raised:  sortedExceptions(kept),
		rejects: sortedExceptions(rejected),
		settled: !throws.Incomplete,
	}
}

// discount reports whether site checks a value fn's declared types already
// settle, and records the decision when the site is one the filter adjudicates.
//
// Only the receiver is settled. A coercion of a parameter is unreachable
// exactly when the declared parameter type already is the coercion's target,
// which the shape-free facts cannot say, so the site is kept and the decision
// records the position for review. A method whose surviving throws are worth
// annotating states its filtered set directly in §4.4's curated layer, which
// §11 populates.
func (f *coercionFilter) discount(fn *Func, site ThrowSite) bool {
	if site.Exception != Class("TypeError") {
		return false
	}
	guard := f.coerced(fn, &site)
	dropped := guard.threaded && guard.value.Kind == OriginReceiver
	decision := FilterDecision{
		Method:   fn.Name,
		Site:     site.String(),
		Coercion: guard.ao,
		Dropped:  dropped,
	}
	if guard.threaded {
		decision.Coerced = originRef(guard.value)
	}
	f.report.Decisions = append(f.report.Decisions, decision)
	return dropped
}

// coercionGuard is what the base of a provenance chain checked. ao names the
// operation, and is empty where the chain bottoms out at something else. value
// is where the checked value came from in the frame the guard was read against,
// and threaded says the walk placed it there rather than losing it on the way
// out.
type coercionGuard struct {
	ao       string
	value    Origin
	threaded bool
}

// coerced walks site's provenance chain to its base and threads the value the
// coercion there checked back out to fn. It returns that operation's name and
// where in fn the value came from.
//
// The chain runs outward from the step that raised. `Array.prototype.push`
// begins `Let O be ? ToObject(this value)`, so the site in `push` propagates
// from `ToObject`'s own site, that site raises the `TypeError` itself, and
// `ToObject` checks its first parameter. Reading `push`'s call at that position
// finds `this value`, so the value is the receiver and the throw is discounted.
//
// The operation comes back unnamed when the chain bottoms out somewhere else,
// which is an explicit domain check or a step below a coercion. `ToString` on
// an object calls `ToPrimitive`, which calls the object's `@@toPrimitive`
// method, and a `TypeError` from that method is the caller's own code failing
// rather than a type the declaration rules out.
//
// The operation is named and the value untraced where the walk reaches a value
// that is neither the receiver nor a parameter. `LengthOfArrayLike(O)` coerces
// `Get(O, "length")`, which is read out of the receiver's backing store rather
// than being the receiver, so the coercion stands.
func (f *coercionFilter) coerced(fn *Func, site *ThrowSite) coercionGuard {
	if site.Root.Inner == nil {
		if !coercionAOs.Contains(fn.Name) {
			return coercionGuard{}
		}
		return coercionGuard{ao: fn.Name, value: Param(coercionGuardArg), threaded: true}
	}
	origins := f.summary.originsOf(fn)
	callee := resolveCallee(f.cfg, origins, site.Root.Callee)
	if callee == nil {
		return coercionGuard{}
	}
	inner := f.coerced(callee, site.Root.Inner)
	switch {
	case inner.ao == "":
		return coercionGuard{}
	case !inner.threaded || inner.value.Kind != OriginParam || inner.value.Interior:
		return coercionGuard{ao: inner.ao}
	}
	call, ok := site.Node.(*CallNode)
	if !ok || inner.value.Index >= len(call.Args) {
		return coercionGuard{ao: inner.ao}
	}
	switch outer := origins.Eval(call.Args[inner.value.Index]); {
	case outer.Interior:
		return coercionGuard{ao: inner.ao}
	case outer.Kind == OriginReceiver, outer.Kind == OriginParam:
		return coercionGuard{ao: inner.ao, value: outer, threaded: true}
	default:
		return coercionGuard{ao: inner.ao}
	}
}

// WriteFilterReport prints the run's tallies, every discounted site, and every
// method whose channels are published short. A reviewer reads what FR11's
// heuristic removed and what the analysis never saw, without reading what it
// left alone: a kept site is a throw the published fact already names.
//
// The tally is indented two spaces and each site four, which is the layout
// WriteCurationReport and WriteJoinReport use, so a caller printing all three
// gets one report per summary line.
func WriteFilterReport(report FilterReport, w io.Writer) error {
	adjudicated, dropped := report.Counts()
	_, err := fmt.Fprintf(w, "  coercion filter: %d TypeError sites adjudicated, %d dropped, %d methods under-reported\n",
		adjudicated, dropped, len(report.UnderReported))
	if err != nil {
		return err
	}
	for _, decision := range report.Dropped() {
		if _, err := fmt.Fprintf(w, "    %s\n", decision); err != nil {
			return err
		}
	}
	for _, name := range report.UnderReported {
		if _, err := fmt.Fprintf(w, "    %s: a step the throw analysis could not read leaves both channels short\n", name); err != nil {
			return err
		}
	}
	return nil
}

// sortDecisions orders the report by method, leaving each method's decisions in
// the order the filter reached them: the synchronous sites by position, then
// the rejection sites by position. The graph fixes the order the methods
// themselves are walked in, so this only settles how the report reads.
func sortDecisions(decisions []FilterDecision) {
	sort.SliceStable(decisions, func(i, j int) bool {
		return decisions[i].Method < decisions[j].Method
	})
}
