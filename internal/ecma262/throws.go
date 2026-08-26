package ecma262

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// invokers are the abstract operations that invoke a function value the calling
// algorithm holds, mapped to the argument position that value arrives at. The
// CFG never names a function value as a callee, so `? callbackfn(...)` reaches
// the graph as `? Call(callbackfn, thisArg, ...)` and the invoked function is an
// argument rather than the callee. The object internal method `[[Call]]`
// flattens to the same shape, with the function at the same position. See
// planning/ecma-262/implementation_plan.md §9.1 and requirements.md FR10.
//
// An entry names what the invoked function contributes and nothing else. The
// operation doing the invoking raises on its own account too. `Call` raises a
// TypeError when its first argument is not callable, and that reaches the
// caller the way any other callee's throws do.
var invokers = map[string]int{
	"Call":      0,
	"Construct": 0,
}

// completionCapture is the abstract operation the graph calls where an abrupt
// completion stops being control flow and becomes a value.
//
// `Iterator.prototype.forEach` is the shape. It runs `Call(procedure, ...)`
// under a plain guard, wraps the result as `Completion(%9)`, and hands that on
// to `? IteratorClose(iterated, result)`, which returns it for the `?` to
// re-raise. The exception does leave the method. It travels along a data edge
// the guards say nothing about, so the analysis cannot name the step that
// raised it.
//
// A function that captures a completion is incomplete for that reason. Naming
// the raising step needs the definitions of each value name, which §9.3 has to
// build anyway to tell a rejection site from a synchronous one.
const completionCapture = "Completion"

// objectInternalMethods are the internal methods of ECMA-262 Tables 4 and 5,
// which every object implements and whose implementation an exotic object or a
// Proxy chooses at runtime. The graph writes a dispatch to one as a call whose
// callee is the bare method name.
//
// A name here is also an abstract operation's name for four of the entries, so
// resolving `Get` by name alone finds the operation `Get(O, P)` rather than the
// dispatch `? O.[[Get]](P, O)` inside its body. The operation would then read as
// a call to itself and its own dispatch would go unreported. propagate reads
// the list to catch that.
var objectInternalMethods = set.FromSlice([]string{
	"Call",
	"Construct",
	"DefineOwnProperty",
	"Delete",
	"Get",
	"GetOwnProperty",
	"GetPrototypeOf",
	"HasProperty",
	"IsExtensible",
	"OwnPropertyKeys",
	"PreventExtensions",
	"Set",
	"SetPrototypeOf",
})

// RaisedKind classifies an exception an algorithm can raise. See
// planning/ecma-262/implementation_plan.md §9.1.
type RaisedKind uint8

const (
	// RaisedClass is an error class the algorithm constructs, as in `Throw a
	// *TypeError* exception`. It is the dominant form in the synchronous
	// channel, since every such step in ECMA-262 names a constructor.
	RaisedClass RaisedKind = iota
	// RaisedOrigin is a value the algorithm raises without constructing it,
	// resolved to the receiver or the parameter it arrived at.
	// `Generator.prototype.throw` raises its own first argument.
	RaisedOrigin
	// RaisedCallback is the effect of a function the caller supplied, rather
	// than a value. `Array.prototype.forEach` raises whatever its callback
	// raises. FR10 spells it `throwsOf:param:k`, and FR13 turns it into throws
	// polymorphism at the join.
	RaisedCallback
	// RaisedUnknown is a raised value the analysis could neither name nor
	// trace. See Untraced.
	RaisedUnknown
)

// Raised is one exception a function can raise. Class holds the error class
// name when Kind is RaisedClass. Origin holds the receiver or the parameter
// when Kind is RaisedOrigin or RaisedCallback. The two kinds differ in what
// that origin names. A RaisedOrigin origin names the raised value itself. A
// RaisedCallback origin names a function whose own throws travel outward.
type Raised struct {
	Kind   RaisedKind
	Class  string
	Origin Origin
}

// Untraced is the raised value the analysis could not place. It renders as
// `Unknown`, which is the vocabulary §9.1 uses, and FR6 spells it `unknown` on
// the wire.
var Untraced = Raised{Kind: RaisedUnknown}

// Class returns the exception a `Throw a *T* exception` step raises.
func Class(name string) Raised {
	return Raised{Kind: RaisedClass, Class: name}
}

// Propagated returns the exception a step raises by handing on a value that
// came from o.
func Propagated(o Origin) Raised {
	return Raised{Kind: RaisedOrigin, Origin: o}
}

// CallbackThrows returns the exception a step raises by invoking the function
// that came from o, which is whatever that function itself raises.
func CallbackThrows(o Origin) Raised {
	return Raised{Kind: RaisedCallback, Origin: o}
}

func (r Raised) String() string {
	switch r.Kind {
	case RaisedClass:
		return r.Class
	case RaisedOrigin:
		return fmt.Sprintf("Origin(%s)", r.Origin)
	case RaisedCallback:
		return fmt.Sprintf("CallbackThrows(%s)", r.Origin)
	case RaisedUnknown:
		return "Unknown"
	default:
		return fmt.Sprintf("Raised(%d)", r.Kind)
	}
}

// less orders two raised values so a rendered set always reads the same way.
// The kinds sort in declaration order, which puts the error classes first, and
// two values of one kind sort by how they render.
func (r Raised) less(other Raised) bool {
	if r.Kind != other.Kind {
		return r.Kind < other.Kind
	}
	return r.String() < other.String()
}

// Root is where a throw site's value ultimately came from. Callee names the
// `?`-guarded operation the site propagated the value out of, and Inner is that
// operation's own site for the same value. Both are empty at a site that raised
// the value itself.
//
// The chain is kept whole rather than collapsed to the immediate callee, so a
// TypeError a method inherits through several `?`-guarded calls still records
// where it began. `Array.prototype.indexOf` reads its length through `?
// LengthOfArrayLike(O)`, and the site that call records runs back through
// `ToLength` and `ToIntegerOrInfinity` to a `ToNumber` coercion. §9.2's
// coercion filter walks that chain to its base to decide whether a throw is a
// coercion type-guard.
type Root struct {
	Callee string
	Inner  *ThrowSite
}

// Direct reports whether the site raised its value rather than propagating one.
func (r Root) Direct() bool {
	return r.Inner == nil
}

// ThrowSite is one place a function raises an exception. Node is the step that
// raises or propagates it and Index is that step's position in the function's
// node list, which §9.3 reads to decide whether the site reaches the
// synchronous exit or the reject sink of a returned promise.
type ThrowSite struct {
	Raised Raised
	Root   Root
	Node   Node
	Index  int
}

// Base returns the site that raised the value, walking Root back through every
// `?`-hop the value travelled. A direct site is its own base.
func (s ThrowSite) Base() ThrowSite {
	for s.Root.Inner != nil {
		s = *s.Root.Inner
	}
	return s
}

// base is Base over the stored sites rather than over copies of them. The
// fixpoint keys a site by the pointer it returns, so two chains that reach one
// step from different sources stay apart.
func (s *ThrowSite) base() *ThrowSite {
	for s.Root.Inner != nil {
		s = s.Root.Inner
	}
	return s
}

// String renders the site as its position, its raised value, and the chain of
// operations it propagated out of, innermost last: `#4 TypeError <- ToObject#5`.
func (s ThrowSite) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "#%d %s", s.Index, s.Raised)
	for root := s.Root; root.Inner != nil; root = root.Inner.Root {
		fmt.Fprintf(&sb, " <- %s#%d", root.Callee, root.Inner.Index)
	}
	return sb.String()
}

// Throws is what the fixpoint concluded about one function. Raised holds the
// exceptions it can raise, sorted, and Sites holds one entry per place it
// raises one, sorted by position.
//
// Incomplete marks a function with a step whose throws the analysis could not
// read. Four shapes leave it set: a prose step §3 could not lower, an object
// internal method whose implementation is chosen at runtime, a call into a
// function value the graph holds no body for, and a captured abrupt completion.
// FR10 asks for such a method to be flagged rather than guessed at, and §4.3
// turns the flag into `classified: false`.
//
// The flag is about this function's own steps and does not travel up the call
// graph, the same way §4.1's is. A consumer that wants the transitive answer
// takes it over the call graph itself. Recording it that way here would set the
// flag on most of the builtins, since nearly every algorithm reaches an object
// internal method through some callee.
type Throws struct {
	Raised     []Raised
	Sites      []ThrowSite
	Incomplete bool
}

// String renders the raised set as a space-separated list, so a test can assert
// a summary in one line. A function that raises nothing and hides nothing reads
// "none".
func (t Throws) String() string {
	parts := make([]string, 0, len(t.Raised)+1)
	for _, r := range t.Raised {
		parts = append(parts, r.String())
	}
	if t.Incomplete {
		parts = append(parts, "incomplete")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// SitesString renders one site per line, so a test can assert a whole
// provenance chain.
func (t Throws) SitesString() string {
	var sb strings.Builder
	for _, site := range t.Sites {
		fmt.Fprintln(&sb, site)
	}
	return sb.String()
}

// siteKey identifies a site by the step that raises it, the value raised there,
// and the site that value ultimately came from. base is nil at a step that
// raises the value itself.
//
// Two chains that reach one step from different sources are two sites. §9.2
// decides whether to keep a throw from where its chain bottoms out, so a
// dropped chain would read there as a source the algorithm does not have.
// `Array.prototype.join` is the shape. Its `? ToString(element)` raises a
// TypeError from the coercion itself and another from the `@@toPrimitive`
// method lookup below it, and only the first is a coercion type-guard.
//
// Keying on the base is also what bounds the Root chains. The sites a value can
// come from are finite, while a cycle in the call graph would nest a chain one
// hop deeper on every pass and the fixpoint would never settle.
//
// A site therefore records one route back to its source rather than every
// route. Where a callee reaches one raising step from two of its own steps, the
// route kept is the one the fixpoint reached first. A consumer that needs a
// particular route walks the call graph from Base itself.
type siteKey struct {
	index  int
	raised Raised
	base   *ThrowSite
}

// throwFacts is one function's throw set while the fixpoint is still running.
// sites holds one entry per key. order holds the same entries in the order the
// fixpoint found them, so a caller reads them back the same way on every run.
type throwFacts struct {
	sites      map[siteKey]*ThrowSite
	order      []*ThrowSite
	raised     set.Set[Raised]
	incomplete bool
}

// ThrowSummary holds the throw facts of every function in a graph. It answers,
// for one function, which exceptions it can raise and where, counting the ones
// it raises itself and the ones its `?`-guarded calls hand on.
type ThrowSummary struct {
	facts map[*Func]*throwFacts
}

// NewThrowSummary runs the throw fixpoint over cfg.
//
// There is no seed. Every exception begins at a `Throw` step and travels
// outward through the `?` guards, which propagate an abrupt completion to the
// caller. A `!` guard asserts that no abrupt completion arises and a plain call
// leaves the result unchecked, so neither contributes.
//
// A call that invokes a function the caller supplied contributes that
// function's own throws as a RaisedCallback effect rather than a value, which
// is what makes `Array.prototype.forEach` raise whatever its callback raises.
// Every other `?`-guarded call carries the callee's sites, with a parametric
// raised value threaded back through the arguments to the calling function's
// own formals. A summary that grows re-enqueues its callers.
func NewThrowSummary(cfg *CFG) *ThrowSummary {
	a := &throwAnalysis{
		summary: &ThrowSummary{facts: make(map[*Func]*throwFacts, len(cfg.Funcs))},
		origins: make(map[*Func]*OriginMap, len(cfg.Funcs)),
		callers: make(map[*Func][]*Func, len(cfg.Funcs)),
	}
	for _, fn := range cfg.Funcs {
		a.summary.facts[fn] = &throwFacts{
			sites:  make(map[siteKey]*ThrowSite),
			raised: set.NewSet[Raised](),
		}
		a.origins[fn] = NewOriginMap(fn)
	}
	for _, fn := range cfg.Funcs {
		for _, callee := range a.callees(cfg, fn) {
			a.callers[callee] = append(a.callers[callee], fn)
		}
	}

	a.run(cfg)
	return a.summary
}

// Of returns fn's throws. A function the summary was not computed for reads
// back empty, the same as one that raises nothing.
func (s *ThrowSummary) Of(fn *Func) Throws {
	f := s.facts[fn]
	if f == nil {
		return Throws{}
	}

	var raised []Raised
	if f.raised.Len() > 0 {
		raised = f.raised.ToSlice()
		sort.Slice(raised, func(i, j int) bool { return raised[i].less(raised[j]) })
	}

	var sites []ThrowSite
	if len(f.order) > 0 {
		sites = make([]ThrowSite, 0, len(f.order))
		for _, site := range f.order {
			sites = append(sites, *site)
		}
		// Two sites of one step that raise the same value are ordered by the
		// route each records. Without that tiebreak the order would be the one
		// the fixpoint happened to find them in.
		sort.Slice(sites, func(i, j int) bool {
			if sites[i].Index != sites[j].Index {
				return sites[i].Index < sites[j].Index
			}
			if sites[i].Raised != sites[j].Raised {
				return sites[i].Raised.less(sites[j].Raised)
			}
			return sites[i].String() < sites[j].String()
		})
	}

	return Throws{Raised: raised, Sites: sites, Incomplete: f.incomplete}
}

// throwAnalysis is the state the fixpoint runs over. origins and callers are
// built once and read from then on. Only summary changes as the fixpoint
// iterates.
type throwAnalysis struct {
	summary *ThrowSummary
	origins map[*Func]*OriginMap
	callers map[*Func][]*Func
}

// callees returns the functions whose throws reach fn, in call order and
// without repeats. Only a `?`-guarded call propagates. An unguarded one is left
// out, so fn is not walked again when that callee's summary moves.
func (a *throwAnalysis) callees(cfg *CFG, fn *Func) []*Func {
	var called []*Func
	seen := set.NewSet[*Func]()
	for _, node := range fn.Nodes {
		call, ok := node.(*CallNode)
		if !ok || call.Guard != GuardQuestion {
			continue
		}
		target := resolveCallee(cfg, a.origins[fn], call.Callee)
		if target == nil || seen.Contains(target) {
			continue
		}
		seen.Add(target)
		called = append(called, target)
	}
	return called
}

// run iterates the transfer function until no summary moves. It terminates
// because a summary only ever grows. A site enters the map under a key drawn
// from finite sets, as siteKey describes, and never leaves. The incomplete flag
// goes from false to true at most once.
func (a *throwAnalysis) run(cfg *CFG) {
	queue := make([]*Func, len(cfg.Funcs))
	copy(queue, cfg.Funcs)
	queued := set.FromSlice(queue)

	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		queued.Remove(fn)

		if !a.transfer(cfg, fn) {
			continue
		}
		for _, caller := range a.callers[fn] {
			if queued.Contains(caller) {
				continue
			}
			queued.Add(caller)
			queue = append(queue, caller)
		}
	}
}

// transfer walks fn once, recording every exception it can see fn raise, and
// reports whether fn's throws grew.
func (a *throwAnalysis) transfer(cfg *CFG, fn *Func) bool {
	f := a.summary.facts[fn]
	origin := a.origins[fn]
	changed := false

	for i, node := range fn.Nodes {
		switch node := node.(type) {
		case *ThrowNode:
			changed = f.add(raisedAt(origin, node), Root{}, node, i) || changed
		case *CallNode:
			if node.Callee == completionCapture {
				changed = f.markIncomplete() || changed
			}
			if node.Guard == GuardQuestion {
				changed = a.propagate(cfg, fn, f, origin, node, i) || changed
			}
		case *OpaqueNode:
			// A step §3 could not lower, such as a prose step. It may raise an
			// exception of its own.
			changed = f.markIncomplete() || changed
		default:
			// No other node shape raises or propagates an exception.
		}
	}
	return changed
}

// raisedAt returns what a Throw step raises. An algorithm that constructs its
// error names the class, and one that hands on a value it was given resolves to
// where that value came from.
func raisedAt(origin *OriginMap, node *ThrowNode) Raised {
	if node.ErrorType != "" {
		return Class(node.ErrorType)
	}
	return raisedOf(origin, node.Value)
}

// raisedOf reads the origin map against a raised value. A receiver or a
// parameter names the value, and anything else is a value the analysis could
// not trace.
//
// An interior value is left untraced. It was read out of the backing store of
// the receiver or a parameter and is a different value from the one that names
// it, so reporting it as that parameter would name the wrong type at the join.
func raisedOf(origin *OriginMap, e Expr) Raised {
	switch o := origin.Eval(e); o.Kind {
	case OriginReceiver, OriginParam:
		if o.Interior {
			return Untraced
		}
		return Propagated(o)
	default:
		return Untraced
	}
}

// propagate records what one `?`-guarded call hands on to fn, and reports
// whether that grew fn's throws.
func (a *throwAnalysis) propagate(cfg *CFG, fn *Func, f *throwFacts, origin *OriginMap, node *CallNode, index int) bool {
	changed := false
	position, invoking := invokers[node.Callee]
	if invoking {
		changed = a.propagateInvoked(f, origin, node, position, index)
	}

	target := resolveCallee(cfg, origin, node.Callee)
	if target == nil {
		// An object internal method that shares its name with no operation, or
		// a function the caller supplied under a callee's name. Either runs code
		// the graph does not hold.
		return f.markIncomplete() || changed
	}
	if target == fn && !invoking && objectInternalMethods.Contains(node.Callee) {
		// The abstract operation `Get(O, P)` is the dispatch `? O.[[Get]](P, O)`
		// and nothing else, and the graph writes that dispatch as a call to the
		// bare name `Get`, which resolves back to the operation. So the step
		// reads as a call to itself and a Proxy trap's throws stay out of the
		// set. An invoker is left out because propagateInvoked already reports
		// what the function a `[[Call]]` runs raises.
		changed = f.markIncomplete() || changed
	}

	for _, site := range a.summary.facts[target].order {
		if invoking && site.Raised.Kind == RaisedCallback {
			// The invoking operation's own callback effect is the effect of the
			// function this very call invokes, which propagateInvoked already
			// recorded against the origin the caller passed. Threading it back
			// through the arguments a second time would repeat the site where
			// the origin map can place that function and replace a precise
			// `incomplete` with an `Unknown` where it cannot.
			continue
		}
		raised := remap(origin, node.Args, site.Raised)
		changed = f.add(raised, Root{Callee: node.Callee, Inner: site}, node, index) || changed
	}
	return changed
}

// propagateInvoked records what the function a call invokes hands on. That
// function is the argument at position. What the invoking operation raises on
// its own account is propagate's business rather than this one's.
//
// A function that reached the algorithm as its receiver or one of its
// parameters raises whatever the algorithm's own caller passed in, which is the
// RaisedCallback effect. Any other function value was read off a property or
// out of a slot, so the graph holds no body for it and its throws cannot be
// read.
func (a *throwAnalysis) propagateInvoked(f *throwFacts, origin *OriginMap, node *CallNode, position, index int) bool {
	if position >= len(node.Args) {
		return f.markIncomplete()
	}
	switch o := origin.Eval(node.Args[position]); o.Kind {
	case OriginReceiver, OriginParam:
		if o.Interior {
			return f.markIncomplete()
		}
		return f.add(CallbackThrows(o), Root{}, node, index)
	default:
		return f.markIncomplete()
	}
}

// remap threads a callee's parametric raised value back to the calling
// function's own formals, so a `Param(0)` the callee named becomes whatever the
// call passed there. It leaves an error class alone, since a class name means
// the same thing in every function.
//
// A callee is an abstract operation and has no receiver, so a raised value
// standing for one cannot be threaded and is left untraced. So is a value the
// call passes at a position it does not fill, or one it fills with something
// that is neither the receiver nor a parameter of the caller.
func remap(origin *OriginMap, args []Expr, r Raised) Raised {
	if r.Kind != RaisedOrigin && r.Kind != RaisedCallback {
		return r
	}
	if r.Origin.Kind != OriginParam || r.Origin.Index >= len(args) {
		return Untraced
	}
	switch caller := origin.Eval(args[r.Origin.Index]); caller.Kind {
	case OriginReceiver, OriginParam:
		if caller.Interior {
			return Untraced
		}
		if r.Kind == RaisedCallback {
			return CallbackThrows(caller)
		}
		return Propagated(caller)
	default:
		return Untraced
	}
}

// add records one site and reports whether it was new.
func (f *throwFacts) add(raised Raised, root Root, node Node, index int) bool {
	key := siteKey{index: index, raised: raised}
	if root.Inner != nil {
		key.base = root.Inner.base()
	}
	if _, seen := f.sites[key]; seen {
		return false
	}
	site := &ThrowSite{Raised: raised, Root: root, Node: node, Index: index}
	f.sites[key] = site
	f.order = append(f.order, site)
	f.raised.Add(raised)
	return true
}

func (f *throwFacts) markIncomplete() bool {
	if f.incomplete {
		return false
	}
	f.incomplete = true
	return true
}
