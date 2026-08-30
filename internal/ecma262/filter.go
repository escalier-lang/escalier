package ecma262

import (
	"fmt"
	"io"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
)

// langType is an ECMAScript language type of §6.1, as far as the filter needs
// to tell them apart. `Undefined` and `Null` are absent: no declaration hands a
// method a receiver of either, which is what makes `ToObject` and
// `RequireObjectCoercible` accept every receiver there is.
type langType string

const (
	typeBigInt  langType = "BigInt"
	typeBoolean langType = "Boolean"
	typeNumber  langType = "Number"
	typeObject  langType = "Object"
	typeString  langType = "String"
	typeSymbol  langType = "Symbol"
)

// everyLangType is the set an operation that raises on no receiver at all
// accepts. The coercions map shares it rather than each entry holding a copy,
// so it is read-only: a coercion that needs a smaller set builds one with
// everyLangTypeExcept.
var everyLangType = set.FromSlice([]langType{
	typeBigInt, typeBoolean, typeNumber, typeObject, typeString, typeSymbol,
})

// everyLangTypeExcept returns the language types outside the ones named, so a
// coercion that accepts all but one spells the one.
func everyLangTypeExcept(types ...langType) set.Set[langType] {
	return everyLangType.Difference(set.FromSlice(types))
}

// receiverTypes gives the language type a declaration hands the methods of one
// prototype as their receiver, keyed by the owner of the member key. An owner
// the table does not name holds objects, which every prototype but these five
// does. `String.prototype.charAt` takes a String, `Array.prototype.push` an
// Object.
//
// This is not a type channel and needs no join. The receiver's type is fixed by
// the member the declaration hangs off, so Normalize reads the owner off the
// spec key and the table answers from that alone.
var receiverTypes = map[string]langType{
	"BigInt":  typeBigInt,
	"Boolean": typeBoolean,
	"Number":  typeNumber,
	"String":  typeString,
	"Symbol":  typeSymbol,
}

// receiverType is the language type of the receiver a method of owner takes.
func receiverType(owner string) langType {
	if t, ok := receiverTypes[owner]; ok {
		return t
	}
	return typeObject
}

// coercion is what one abstract operation does to the value at
// coercionGuardArg, which is what FR11 needs to know to call a throw
// unreachable. See planning/ecma-262/implementation_plan.md §9.2.
type coercion struct {
	// accepts are the types the operation's own `Throw` step cannot report. A
	// receiver of one of them never reaches that step.
	accepts set.Set[langType]
	// returnsAtOnce are the types the operation hands back before reaching any
	// call, so nothing past it runs either. It is a subset of accepts, which
	// TestCoercionsReturnOnlyWhatTheyAccept holds.
	//
	// What pastReceiverIdentity needs is weaker: that nothing past the
	// operation raises for this type. Reaching no call at all is the stricter
	// test, and
	// it is the one a reader can check against the operation's own steps, so an
	// operation whose first step is a call returns nothing at once however
	// harmless that call turns out to be. The two come apart at `ToNumeric`.
	returnsAtOnce set.Set[langType]
}

// coercions are the operations whose `TypeError` reports the wrong dynamic type
// for the value they were handed, with what each does per receiver type.
//
// The first group are the coercions ECMA-262 applies to a value before working
// with it. `ToObject` and `RequireObjectCoercible` raise on `undefined` and
// `null` alone and call nothing, so both their columns hold every receiver
// type. `ToString` and
// `ToNumber` hand their own type straight back at their first step and reach
// `ToPrimitive` for an Object, which is why they accept more types than they
// return at once.
//
// The `This*Value` operations unwrap a wrapper receiver. Each returns its own
// primitive at its first step and raises on everything else, apart from an
// Object carrying the matching slot, which the graph gives no way to tell from
// any other Object. Reading them as raising on every Object keeps a throw the
// filter cannot prove unreachable.
//
// `ToPrimitive` is the coercion absent from the map. Its one `Throw` step is
// the one reached after an `@@toPrimitive` method has handed back an object, so
// it reports the caller's code failing rather than a wrong dynamic type, and no
// receiver type rules it out.
var coercions = map[string]coercion{
	"RequireObjectCoercible": {accepts: everyLangType, returnsAtOnce: everyLangType},
	"ToObject":               {accepts: everyLangType, returnsAtOnce: everyLangType},
	"ToString":               {accepts: everyLangTypeExcept(typeSymbol), returnsAtOnce: set.FromSlice([]langType{typeString})},
	"ToNumber":               {accepts: everyLangTypeExcept(typeSymbol, typeBigInt), returnsAtOnce: set.FromSlice([]langType{typeNumber})},
	// ToNumeric has no `Throw` step of its own, so it never bottoms out a chain
	// and accepts every type. It returns none at once because its first step is
	// `? ToPrimitive(value, number)`. Nothing past it does raise for a Number
	// or a BigInt, since ToPrimitive hands a primitive straight back and
	// ToNumber returns a Number unchanged, so the empty set keeps throws
	// pastReceiverIdentity could drop. That is the safe direction, and the graph
	// applies ToNumeric to a parameter rather than a receiver, so no drop turns
	// on it today.
	"ToNumeric": {accepts: everyLangType, returnsAtOnce: set.NewSet[langType]()},

	"ThisBigIntValue":  thisValue(typeBigInt),
	"ThisBooleanValue": thisValue(typeBoolean),
	"ThisNumberValue":  thisValue(typeNumber),
	"ThisStringValue":  thisValue(typeString),
	"ThisSymbolValue":  thisValue(typeSymbol),
}

// thisValue is the entry for an operation that unwraps a receiver of one
// primitive type and raises on every other.
func thisValue(t langType) coercion {
	own := set.FromSlice([]langType{t})
	return coercion{accepts: own, returnsAtOnce: own}
}

// coercionGuardArg is the position of the value a coercion operation checks.
// Every entry in coercions declares that one parameter and nothing else, and
// TestCoercionsRaiseOnTheirFirstArgument names it per operation.
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
	// PastCoercion says Coercion names an operation further out than the one
	// that raised. See pastReceiverIdentity.
	PastCoercion bool
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
	case d.PastCoercion:
		return fmt.Sprintf("%s: %s %s [past %s of %s]", d.Method, verdict, d.Site, d.Coercion, d.Coerced)
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

// filterThrows returns fn's two channels with the receiver-coercion
// `TypeError`s discounted. Both are filtered the same way, because a site's
// channel is decided by the exit it reaches and both carry coercion guards.
//
// The reject channel also carries what the combinator model supplies. Those
// rejections reach the returned promise through the promise-resolution
// machinery rather than through a step of fn's, so they have no site to
// adjudicate.
func (f *coercionFilter) filterThrows(fn *Func) (raised, rejects []Exception) {
	raw := f.summary.Of(fn)
	thrown := set.NewSet[Exception]()
	for _, site := range raw.SyncSites() {
		if !f.discount(fn, site) {
			thrown.Add(site.Exception)
		}
	}
	rejected := set.FromSlice(raw.Modeled)
	for _, site := range raw.RejectSites() {
		if !f.discount(fn, site) {
			rejected.Add(site.Exception)
		}
	}
	return sortedExceptions(thrown), sortedExceptions(rejected)
}

// discount reports whether site is unreachable for a well-typed caller, and
// records the decision where the filter has one to make. Only a `TypeError`
// does: every other class reports the values a caller passed rather than their
// types, so the filter leaves it alone.
//
// Only the receiver is decided. A coercion of a parameter is unreachable
// exactly when the declared parameter type already is the coercion's target,
// which the shape-free facts cannot say, so the site is kept and the decision
// records the position for review. A method whose surviving throws are worth
// annotating states its filtered set directly in §4.4's curated layer, which
// §11 populates.
func (f *coercionFilter) discount(fn *Func, site ThrowSite) bool {
	if site.Exception != Class("TypeError") {
		return false
	}
	received := f.receiverType(fn)
	guard := f.coerced(fn, &site)
	decision := FilterDecision{
		Method:   fn.Name,
		Site:     site.String(),
		Coercion: guard.ao,
		Dropped: guard.threaded && guard.value.Kind == OriginReceiver &&
			coercions[guard.ao].accepts.Contains(received),
	}
	if guard.threaded {
		decision.Coerced = originRef(guard.value)
	}
	// A site the base rule keeps can still lie past a coercion this receiver
	// leaves at its first step, so the steps that would raise it never run.
	if !decision.Dropped {
		if ao := f.pastReceiverIdentity(fn, &site, received); ao != "" {
			decision.Coercion, decision.Coerced = ao, originRef(Receiver)
			decision.Dropped, decision.PastCoercion = true, true
		}
	}
	f.report.Decisions = append(f.report.Decisions, decision)
	return decision.Dropped
}

// receiverType is the language type fn's declaration hands it as a receiver. A
// name Normalize cannot address is read as an Object, the type that decides the
// least, so an unaddressable name drops the fewest throws rather than the most.
func (f *coercionFilter) receiverType(fn *Func) langType {
	ref, ok := Normalize(fn.Name)
	if !ok {
		return typeObject
	}
	return receiverType(ref.Owner)
}

// pastReceiverIdentity returns the name of a coercion on site's chain that was
// handed the receiver and returns that type unchanged, or "" if there is none.
// Such a coercion returns before calling anything. The site was raised inside
// one of those calls, so it cannot be reached.
//
// `String.prototype.charAt` calls `? ToString(O)` on a String receiver, so the
// `? ToPrimitive(argument, string)` inside `ToString` never runs. Its `pos`
// argument goes through `ToNumber` instead, which is not the receiver, so every
// throw inside that one stands.
func (f *coercionFilter) pastReceiverIdentity(fn *Func, site *ThrowSite, received langType) string {
	hops := f.chain(fn, site)
	for depth, hop := range hops {
		callee := hop.site.Root.Callee
		if coercions[callee].returnsAtOnce.Contains(received) && f.threadsToReceiver(hops, depth) {
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
		if _, known := coercions[fn.Name]; !known {
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

// WriteFilterReport prints the run's tallies and every discounted site, so a
// reviewer reads what FR11's heuristic removed without reading what it left
// alone: a kept site is a throw the published fact already names.
//
// The tally is indented two spaces and each site four, which is the layout
// WriteCurationReport and WriteJoinReport use, so a caller printing all three
// gets one report per summary line.
func WriteFilterReport(report FilterReport, w io.Writer) error {
	adjudicated, dropped := report.Counts()
	_, err := fmt.Fprintf(w, "  coercion filter: %d TypeError sites adjudicated, %d dropped\n",
		adjudicated, dropped)
	if err != nil {
		return err
	}
	for _, decision := range report.Dropped() {
		if _, err := fmt.Fprintf(w, "    %s\n", decision); err != nil {
			return err
		}
	}
	return nil
}

// sortDecisions orders the report by method, leaving each method's decisions in
// the order the filter reached them: the synchronous sites by position, then
// the rejection sites by position. The graph fixes the order the methods
// themselves are walked in, so this only decides how the report reads.
func sortDecisions(decisions []FilterDecision) {
	sort.SliceStable(decisions, func(i, j int) bool {
		return decisions[i].Method < decisions[j].Method
	})
}
