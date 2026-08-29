package ecma262

import (
	"fmt"
	"io"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
)

// coercionAOs are the abstract operations whose `TypeError` reports the wrong
// dynamic type for the value at coercionGuardArg, which a declared receiver
// type rules out. `ToPrimitive` is absent: its throw reports an `@@toPrimitive`
// method handing back an object, which no declared type rules out. See §9.2 of
// planning/ecma-262/implementation_plan.md for the reasoning behind each entry.
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

// receiverIdentityCoercions maps a coercion to the owner whose receiver it
// hands straight back. `ToString(O)` returns `O` at its first step when `O` is
// already a String, so on a `String.prototype` method the `ToPrimitive` call
// further down its body never runs and nothing under that call can raise.
//
// This is what separates the two rules the filter applies. The base rule drops
// the coercion's own type check, which a declared receiver type excludes
// whatever that type is. This one drops everything the coercion would reach
// below it, which is sound only where the receiver's type is already the
// coercion's target, so it reads the owner of the member key to check that.
//
// The owner is not a type channel. It is fixed by the member the declaration
// hangs off, so it comes out of the spec key by Normalize and needs no join.
//
// The entries are the coercions with a body to reach past. `ToObject`,
// `RequireObjectCoercible`, and the `This*Value` operations return or raise
// without calling anything, so there is nothing under them to drop.
var receiverIdentityCoercions = map[string]string{
	"ToNumber": "Number",
	"ToString": "String",
}

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

// FilterDecision is one adjudicated throw site, which is only ever a
// `TypeError`. Site renders the chain as ThrowSite.String does. Coercion names
// the operation whose check the chain bottoms out at, empty where it bottoms
// out elsewhere, and Coerced spells where in Method that operation's value came
// from, empty where the walk could not thread it back.
type FilterDecision struct {
	Method   string
	Site     string
	Coercion string
	Coerced  string
	Dropped  bool
	// Under marks a site the coercion did not raise but sits below, dropped
	// because the receiver's type makes that coercion an identity and the steps
	// it would reach past one unreachable. See underReceiverIdentity.
	Under bool
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
	case d.Under:
		return fmt.Sprintf("%s: %s %s [under %s of %s]", d.Method, verdict, d.Site, d.Coercion, d.Coerced)
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
	// UnderReported holds the methods the throw fixpoint could not read whole,
	// sorted by name. Facts.Unclassified(AxisThrows) names the same ones.
	UnderReported []UnderReport
}

// UnderReport is one method whose channels are published short because the
// throw fixpoint could not read every step. Rejects is set only for an
// algorithm that builds a promise; any other has no reject sink to miss.
type UnderReport struct {
	Method  string
	Rejects bool
}

func (u UnderReport) String() string {
	channels := "its throws"
	if u.Rejects {
		channels = "its throws and its rejections"
	}
	return fmt.Sprintf("%s: a step the throw analysis could not read leaves %s short", u.Method, channels)
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
	decision := FilterDecision{
		Method:   fn.Name,
		Site:     site.String(),
		Coercion: guard.ao,
		Dropped:  guard.threaded && guard.value.Kind == OriginReceiver,
	}
	if guard.threaded {
		decision.Coerced = originRef(guard.value)
	}
	// A site the base rule keeps can still sit under a coercion the receiver's
	// type makes an identity, which never reaches the steps below it.
	if !decision.Dropped {
		if ao := f.underReceiverIdentity(fn, &site); ao != "" {
			decision.Coercion, decision.Coerced = ao, originRef(Receiver)
			decision.Dropped, decision.Under = true, true
		}
	}
	f.report.Decisions = append(f.report.Decisions, decision)
	return decision.Dropped
}

// underReceiverIdentity returns the coercion the site's chain passes through
// whose receiver argument makes it an identity, or the empty string when it
// passes through no such call. Every step that coercion would reach past the
// identity is unreachable, and so is the site under them.
//
// `String.prototype.charAt` runs `? ToString(O)` on a receiver the declaration
// types as a String. `ToString` hands a String straight back at its first step,
// so the `? ToPrimitive(argument, string)` further down its body never runs,
// nor does the `@@toPrimitive` lookup and call under that.
//
// Both guards have to hold. The coercion has to be an identity for this
// owner's receiver, and the value it was handed has to be that receiver rather
// than a parameter that happens to reach the same operation. `charAt` coerces
// `pos` through `ToNumber` on the same algorithm, and that stands.
func (f *coercionFilter) underReceiverIdentity(fn *Func, site *ThrowSite) string {
	ref, ok := Normalize(fn.Name)
	if !ok {
		return ""
	}
	hops := f.chain(fn, site)
	for depth, hop := range hops {
		callee := hop.site.Root.Callee
		if receiverIdentityCoercions[callee] == ref.Owner && f.threadsToReceiver(hops, depth) {
			return callee
		}
	}
	return ""
}

// hop is one link of a provenance chain: the function a propagating step sits
// in, and the site that step recorded.
type hop struct {
	fn   *Func
	site *ThrowSite
}

// chain returns the propagating steps of site's chain, outermost first. A site
// that raised its own value has none.
func (f *coercionFilter) chain(fn *Func, site *ThrowSite) []hop {
	var hops []hop
	for site.Root.Inner != nil {
		callee := resolveCallee(f.cfg, f.summary.originsOf(fn), site.Root.Callee)
		if callee == nil {
			break
		}
		hops = append(hops, hop{fn, site})
		fn, site = callee, site.Root.Inner
	}
	return hops
}

// threadsToReceiver reports whether the value the call at hops[depth] passes at
// coercionGuardArg came from the receiver of the outermost function. It reads
// each frame's origin map the way coerced does, carrying the position it finds
// to the next frame out.
func (f *coercionFilter) threadsToReceiver(hops []hop, depth int) bool {
	pos := coercionGuardArg
	for i := depth; i >= 0; i-- {
		call, ok := hops[i].site.Node.(*CallNode)
		if !ok || pos >= len(call.Args) {
			return false
		}
		outer := f.summary.originsOf(hops[i].fn).Eval(call.Args[pos])
		switch {
		case outer.Interior:
			return false
		case i == 0:
			return outer.Kind == OriginReceiver
		case outer.Kind != OriginParam:
			return false
		}
		pos = outer.Index
	}
	return false
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
// coercion there checked back out to fn, returning that operation and where in
// fn the value came from. The operation is unnamed where the base is not a
// coercion, and the value untraced where a hop lands on something that is
// neither the receiver nor a parameter, such as `Get(O, "length")`.
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
	for _, under := range report.UnderReported {
		if _, err := fmt.Fprintf(w, "    %s\n", under); err != nil {
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
