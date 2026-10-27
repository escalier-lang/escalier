//go:build spike

// Package hand runs the ECMA-262 mutation analysis over third-party package
// members lowered by hand. It is the temperature check that precedes the spike
// in planning/third_party/source_analysis.md, and
// planning/third_party/hand_lowering.md reports what it found.
//
// Run it with:
//
//	go test -tags spike ./planning/third_party/spike/hand/
//
// Set WRITE_REPORT=1 to rewrite report.txt from the run.
//
// Each package file lowers a few exported members, plus the package-internal
// helpers they call, with the conventions below. The analysis itself is the
// one in internal/ecma262, which the builtins also run through.
//
//   - Every function is an abstract operation named `impl:<name>`. Its
//     parameter 0 is `this$`, the receiver, and its declared parameters follow.
//     A method's receiver mutation therefore shows up as a mutation of
//     position 0.
//   - A builtin call is a call to `builtin:<spec key>`. The harness writes an
//     abstract operation for it whose body performs the mutations the ECMA-262
//     facts publish for that builtin.
//   - A closure is lowered into the function that creates it. Its parameters
//     bind to `?`, a name the function never binds, so they read as Unknown.
//     The closure value itself is Alloc(), which the analysis holds to the
//     callback contract, so a call it is passed to charges nothing more for
//     it. Its body is already part of the caller.
//   - A call the lowering cannot resolve is a call to `?<reason>`, which names
//     no body, so the analysis marks the caller incomplete.
//   - A call through a parameter is a call to that parameter's name, which the
//     analysis treats as a callback. Its first argument is the receiver, as
//     for any other call.
//   - One of the package's own functions passed as a value is a name for its
//     abstract operation, as V("impl:keys"), and a builtin passed as a value
//     is V("builtin:String"). A call that hands it to a callback charges what
//     it mutates.
//   - A call through a function read out of state, such as a handler an
//     emitter stored when it was registered, is a stored callback. It names
//     the declared function type the library's types give the value, as
//     stored("", "Handler", V("handler"), Lit(), V("evt")). A package
//     function that the types say is stored as that type lists it with
//     implementing. No fixture has one, which each fixture checked against
//     the package source.
//   - A module-level binding is a GlobalExpr, written G(name). So is a
//     package function held in a variable, which the analysis does not follow.
//     A value a method's closure captures, such as mitt's `all`, is not
//     module-level, so it stays a name the method does not bind.
package hand

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Builders. They produce the cfg.json schema of Appendix A in
// planning/ecma-262/implementation_plan.md, which ecma262.ParseCFG reads.

type expr map[string]any
type node map[string]any

func V(name string) expr { return expr{"kind": "var", "var": name} }
func Lit() expr          { return expr{"kind": "lit"} }
func Prop(o expr) expr   { return expr{"kind": "prop", "object": o} }
func Alloc(args ...expr) expr {
	if len(args) == 0 {
		return expr{"kind": "alloc"}
	}
	return expr{"kind": "alloc", "args": args}
}

// G names a module-level value, such as a regex, a cache, or one of the
// package's own functions passed as a value.
func G(name string) expr { return expr{"kind": "global", "var": name} }

// Unknown reads a name no function binds, which the origin map reads as
// Unknown.
func Unknown() expr { return V("?") }

type fn struct {
	name       string
	params     []string
	nodes      []node
	implements []string
}

// implementing lists the declared function types f is stored as.
func (f *fn) implementing(types ...string) *fn {
	f.implements = append(f.implements, types...)
	return f
}

func impl(name string, params ...string) *fn {
	return &fn{name: "impl:" + name, params: append([]string{"this$"}, params...)}
}

func (f *fn) let(target string, source expr) *fn {
	f.nodes = append(f.nodes, node{"kind": "let", "target": target, "source": source})
	return f
}

func (f *fn) call(target, callee string, args ...expr) *fn {
	n := node{"kind": "call", "callee": callee, "guard": "plain"}
	if target != "" {
		n["target"] = target
	}
	if len(args) > 0 {
		n["args"] = args
	}
	f.nodes = append(f.nodes, n)
	return f
}

// to calls a package function. Its first argument is the receiver, which is
// Lit() for a call with none.
func (f *fn) to(target, name string, args ...expr) *fn { return f.call(target, "impl:"+name, args...) }

// builtin calls a builtin by its spec key. Its first argument is the receiver.
func (f *fn) builtin(target, key string, args ...expr) *fn {
	return f.call(target, "builtin:"+key, args...)
}

// unresolved lowers a call the lowering cannot name a body for. why names the
// reason and the call site, as "untyped:value.valueOf()".
func (f *fn) unresolved(target, why string, args ...expr) *fn {
	return f.call(target, "?"+why, args...)
}

// stored lowers a call through fnValue, a function read out of state whose
// declared type is via. Its first argument is the receiver.
func (f *fn) stored(target, via string, fnValue expr, args ...expr) *fn {
	name := "stored$" + via
	f.let(name, fnValue).call(target, name, args...)
	f.nodes[len(f.nodes)-1]["via"] = via
	return f
}

// set lowers `o.p = v` and `o[k] = v`.
func (f *fn) set(o, value expr) *fn { return f.call("", "Set", o, Lit(), value) }

func (f *fn) ret(value expr) *fn {
	f.nodes = append(f.nodes, node{"kind": "return", "value": value})
	return f
}

func (f *fn) throw(value expr) *fn {
	f.nodes = append(f.nodes, node{"kind": "throw", "value": value})
	return f
}

func (f *fn) branch() *fn {
	f.nodes = append(f.nodes, node{"kind": "branch"})
	return f
}

// ---------------------------------------------------------------------------
// Members

// Param kinds. A primitive parameter is settled by its type alone.
const (
	obj  = "obj"
	prim = "prim"
	cb   = "fn"
)

// member is one exported function or method under test. impl names the
// abstract operation that implements it. params gives each declared
// parameter's kind, from the package's `.d.ts`.
type member struct {
	name     string
	impl     string
	receiver bool
	params   []string
	// expect is the hand-derived answer from reading the source: which
	// parameters and whether the receiver the member really mutates. It is
	// what the hand check compares a settled answer against.
	expect string
}

type pkg struct {
	name    string
	version string
	funcs   []*fn
	members []member
	// jelly holds the functions as lowered with the call targets the Jelly
	// prototype resolved, in planning/third_party/spike/jelly/. Each one
	// replaces the function of the same name in funcs, or joins them when no
	// function has that name. Set JELLY=1 to run the check over them.
	jelly []*fn
}

// loweredFuncs returns p's functions, with the Jelly lowerings swapped in when
// JELLY is set.
func loweredFuncs(p pkg) []*fn {
	if os.Getenv("JELLY") == "" || len(p.jelly) == 0 {
		return p.funcs
	}
	replaced := map[string]*fn{}
	for _, f := range p.jelly {
		replaced[f.name] = f
	}
	var out []*fn
	for _, f := range p.funcs {
		if r, ok := replaced[f.name]; ok {
			out = append(out, r)
			delete(replaced, f.name)
		} else {
			out = append(out, f)
		}
	}
	for _, f := range p.jelly {
		if _, ok := replaced[f.name]; ok {
			out = append(out, f)
		}
	}
	return out
}

var packages []pkg

// ---------------------------------------------------------------------------
// Builtin stubs

// builtinStubs writes an abstract operation for every `builtin:` callee or
// value the lowered functions name. A stub's parameter 0 is the receiver and
// the rest are the builtin's own parameters.
//
// A stub calls each parameter its builtin calls, with the shape
// `Array.prototype.forEach` uses: an element of the receiver, an index, and
// the receiver.
func builtinStubs(t *testing.T, funcs []*fn) []*fn {
	_, here, _, _ := runtime.Caller(0)
	cfg, err := ecma262.LoadCFG(filepath.Join(filepath.Dir(here), "../../../../internal/ecma262/cfg.json"))
	require.NoError(t, err)
	facts, err := ecma262.NewFacts(cfg)
	require.NoError(t, err)
	summary := ecma262.NewMutationSummary(cfg)

	needed := set.NewSet[string]()
	var values func(v any)
	values = func(v any) {
		switch v := v.(type) {
		case expr:
			if name, ok := v["var"].(string); ok && strings.HasPrefix(name, "builtin:") {
				needed.Add(strings.TrimPrefix(name, "builtin:"))
			}
			for _, child := range v {
				values(child)
			}
		case []expr:
			for _, child := range v {
				values(child)
			}
		}
	}
	for _, f := range funcs {
		for _, n := range f.nodes {
			if c, ok := n["callee"].(string); ok && strings.HasPrefix(c, "builtin:") {
				needed.Add(strings.TrimPrefix(c, "builtin:"))
			}
			for _, v := range n {
				values(v)
			}
		}
	}
	keys := needed.ToSlice()
	sort.Strings(keys)

	var stubs []*fn
	memo := map[*ecma262.Func][]int{}
	for _, key := range keys {
		b := cfg.Builtin(key)
		require.NotNil(t, b, "no builtin named %s in the ECMA-262 graph", key)
		stub := &fn{name: "builtin:" + key, params: []string{"recv"}}
		for _, p := range b.Params {
			stub.params = append(stub.params, "p_"+p)
		}
		fact, _ := facts.Of(key)
		if fact.Receiver == ecma262.RecvMutBorrow {
			stub.set(V("recv"), Lit())
		}
		m := summary.Of(b)
		for _, arg := range m.Args {
			stub.set(V(stub.params[arg+1]), Lit())
		}
		for _, i := range calledParams(cfg, b, memo) {
			stub.call("", stub.params[i+1], Lit(), Prop(V("recv")), Lit(), V("recv"))
		}
		// The stub returns what the builtin's fact says it returns, so a call
		// to it takes that origin. An unknown return leaves the stub with no
		// return at all, which reads as Unknown.
		for _, ref := range returnRefs(fact.Returns) {
			switch ref.Kind {
			case ecma262.AliasFresh:
				stub.ret(Alloc())
			case ecma262.AliasReceiver:
				stub.ret(V("recv"))
			case ecma262.AliasParam:
				stub.ret(V(stub.params[*ref.Index+1]))
			}
		}
		stubs = append(stubs, stub)
	}
	return stubs
}

// calledParams returns the positions of b's parameters that b calls. A
// parameter is called when it is a call's callee, when it is the function
// `Call` is given, or when it is passed at a position the callee calls in
// turn, as `Array.prototype.find` passes its predicate to
// `FindViaPredicate`. memo holds the answer for each function already read,
// and an operation still being read answers none, which breaks a cycle.
func calledParams(cfg *ecma262.CFG, b *ecma262.Func, memo map[*ecma262.Func][]int) []int {
	if out, ok := memo[b]; ok {
		return out
	}
	memo[b] = nil
	index := map[string]int{}
	for i, p := range b.Params {
		index[p] = i
	}
	param := func(e ecma262.Expr) (int, bool) {
		v, ok := e.(*ecma262.VarExpr)
		if !ok {
			return 0, false
		}
		i, ok := index[v.Var]
		return i, ok
	}
	called := set.NewSet[int]()
	for _, n := range b.Nodes {
		call, ok := n.(*ecma262.CallNode)
		if !ok {
			continue
		}
		if i, ok := index[call.Callee]; ok {
			called.Add(i)
			continue
		}
		if call.Callee == "Call" {
			if len(call.Args) > 0 {
				if i, ok := param(call.Args[0]); ok {
					called.Add(i)
				}
			}
			continue
		}
		callee := cfg.AbstractOp(call.Callee)
		if callee == nil {
			continue
		}
		for _, k := range calledParams(cfg, callee, memo) {
			if k < len(call.Args) {
				if i, ok := param(call.Args[k]); ok {
					called.Add(i)
				}
			}
		}
	}
	out := called.ToSlice()
	sort.Ints(out)
	memo[b] = out
	return out
}

// returnRefs lists the values a builtin's return fact names, one for a single
// kind and every member for a union.
func returnRefs(r ecma262.ReturnFact) []ecma262.AliasRef {
	if r.Kind == ecma262.AliasUnion {
		return r.Members
	}
	return []ecma262.AliasRef{{Kind: r.Kind, Index: r.Index}}
}

// ---------------------------------------------------------------------------
// Analysis

type paramResult struct {
	settled bool
	mut     bool
}

type memberResult struct {
	member
	receiver paramResult
	params   []paramResult
	returns  string
	blockers []string
}

func (r memberResult) settled() bool {
	if r.member.receiver && !r.receiver.settled {
		return false
	}
	for _, p := range r.params {
		if !p.settled {
			return false
		}
	}
	return true
}

// trivial reports a member with nothing for the analysis to decide: no
// receiver and only primitive parameters.
func (r memberResult) trivial() bool {
	if r.member.receiver {
		return false
	}
	for _, k := range r.member.params {
		if k != prim {
			return false
		}
	}
	return true
}

func (r memberResult) answer() string {
	var parts []string
	if r.member.receiver {
		parts = append(parts, "self:"+describe(r.receiver))
	}
	for i, p := range r.params {
		kind := r.member.params[i]
		if kind == prim {
			parts = append(parts, fmt.Sprintf("p%d:prim", i))
			continue
		}
		parts = append(parts, fmt.Sprintf("p%d:%s", i, describe(p)))
	}
	return strings.Join(parts, " ")
}

func describe(p paramResult) string {
	switch {
	case p.mut:
		return "&mut"
	case p.settled:
		return "&"
	default:
		return "?"
	}
}

type run struct {
	cfg     *ecma262.CFG
	summary *ecma262.MutationSummary
	origins map[*ecma262.Func]*ecma262.OriginMap
	byName  map[string]*fn
}

func analyze(t *testing.T, p pkg) []memberResult {
	funcs := loweredFuncs(p)
	all := append(append([]*fn{}, funcs...), builtinStubs(t, funcs)...)
	byName := map[string]*fn{}
	var decoded []map[string]any
	for _, f := range all {
		byName[f.name] = f
		d := map[string]any{"name": f.name, "kind": "abstract-op", "params": f.params, "nodes": f.nodes}
		if len(f.implements) > 0 {
			d["implements"] = f.implements
		}
		decoded = append(decoded, d)
	}
	data, err := json.Marshal(map[string]any{"specTarget": p.name + "@" + p.version, "funcs": decoded})
	require.NoError(t, err)
	cfg, err := ecma262.ParseCFG(data)
	require.NoError(t, err)
	r := &run{cfg: cfg, summary: ecma262.NewMutationSummary(cfg), origins: ecma262.NewOriginMaps(cfg), byName: byName}

	var results []memberResult
	for _, m := range p.members {
		target := cfg.AbstractOp("impl:" + m.impl)
		require.NotNil(t, target, "member %s names no lowered function impl:%s", m.name, m.impl)
		results = append(results, r.classify(m, target))
	}
	return results
}

// classify reads one member's answer off the mutation summary. A position the
// summary charges is a proven `&mut`. Any other position is a proven `&` only
// when nothing the member reaches leaves the analysis blind: no unresolved
// call, no function passed to a callback that the analysis cannot read, and no
// write it could not place. A callback the member calls is the caller's to
// check, so it does not blind the analysis. The analysis carries an
// unplaced write up the call graph itself but not an unresolved call, so the
// harness walks the callees for those.
func (r *run) classify(m member, target *ecma262.Func) memberResult {
	mut := r.summary.Of(target)
	blockers := r.blockers(target)
	blind := len(blockers) > 0

	res := memberResult{member: m}
	charged := set.FromSlice(mut.Args)
	res.receiver = paramResult{mut: charged.Contains(0), settled: charged.Contains(0) || !blind}
	for i, kind := range m.params {
		pos := i + 1
		pr := paramResult{mut: charged.Contains(pos), settled: charged.Contains(pos) || !blind}
		if kind == prim {
			pr = paramResult{settled: true}
		}
		res.params = append(res.params, pr)
	}
	res.returns = returnAlias(r.origins[target])
	res.blockers = blockers
	return res
}

// blockers lists what leaves fn, or anything it calls, unable to prove the
// absence of a mutation.
func (r *run) blockers(root *ecma262.Func) []string {
	found := set.NewSet[string]()
	seen := set.NewSet[*ecma262.Func]()
	var walk func(f *ecma262.Func)
	walk = func(f *ecma262.Func) {
		if seen.Contains(f) {
			return
		}
		seen.Add(f)
		origins := r.origins[f]
		for _, n := range f.Nodes {
			switch n := n.(type) {
			case *ecma262.CallNode:
				if strings.HasPrefix(n.Callee, "?") {
					found.Add("unresolved " + n.Callee[1:])
					continue
				}
				if origins.Of(n.Callee).Kind == ecma262.OriginParam {
					// A callback, which the caller checks.
					continue
				}
				if n.Via != "" {
					// A stored callback. One the caller supplied is the
					// caller's to check, and the package's own implementers
					// are walked like any callee.
					for _, impl := range r.cfg.Funcs {
						if set.FromSlice(impl.Implements).Contains(n.Via) {
							walk(impl)
						}
					}
					continue
				}
				callee := r.cfg.AbstractOp(n.Callee)
				if callee == nil {
					if n.Callee != "Set" && n.Callee != "DeletePropertyOrThrow" {
						found.Add("unresolved " + n.Callee)
					}
				}
				var mutated []int
				if n.Callee == "Set" || n.Callee == "DeletePropertyOrThrow" {
					mutated = []int{0}
				} else if callee != nil {
					mutated = r.summary.Of(callee).Args
				}
				for _, pos := range mutated {
					if pos < len(n.Args) && origins.Eval(n.Args[pos]).Kind == ecma262.OriginUnknown {
						why, name := unknownWhy(f, n.Args[pos])
						found.Add(fmt.Sprintf("write to unknown in %s: %s%s", strings.TrimPrefix(f.Name, "impl:"), why, name))
					}
				}
				if callee != nil {
					for _, pos := range r.summary.Of(callee).Callbacks {
						if pos >= len(n.Args) {
							continue
						}
						if v, ok := n.Args[pos].(*ecma262.VarExpr); ok {
							if passed := r.cfg.AbstractOp(v.Var); passed != nil {
								walk(passed)
								continue
							}
						}
						switch o := origins.Eval(n.Args[pos]); o.Kind {
						case ecma262.OriginParam, ecma262.OriginFresh:
						default:
							why, name := unknownWhy(f, n.Args[pos])
							found.Add(fmt.Sprintf("callback value unknown in %s: %s%s", strings.TrimPrefix(f.Name, "impl:"), why, name))
						}
					}
					walk(callee)
				}
			case *ecma262.OpaqueNode:
				found.Add("opaque " + strings.Join(n.Text, "; "))
			}
		}
	}
	walk(root)
	if m := r.summary.Of(root); found.Len() == 0 && (m.Unattributable || m.Incomplete) {
		found.Add(fmt.Sprintf("analysis flag %s", m))
	}
	out := found.ToSlice()
	sort.Strings(out)
	return out
}

// unknownWhy names what made a mutated value Unknown, read off how the lowered
// function binds it.
func unknownWhy(f *ecma262.Func, e ecma262.Expr) (why, name string) {
	switch e := e.(type) {
	case *ecma262.PropExpr:
		return "property read", ""
	case *ecma262.GlobalExpr:
		return "module-level value", " (" + e.Var + ")"
	case *ecma262.VarExpr:
		if e.Var == "?" {
			return "closure parameter", ""
		}
		var kinds []string
		for _, n := range f.Nodes {
			switch n := n.(type) {
			case *ecma262.CallNode:
				if n.Target == e.Var {
					kinds = append(kinds, "call result")
				}
			case *ecma262.LetNode:
				if n.Target == e.Var {
					kinds = append(kinds, sourceKind(n.Source))
				}
			}
		}
		kinds = set.FromSlice(kinds).ToSlice()
		sort.Strings(kinds)
		if len(kinds) == 0 {
			return "free name", " (" + e.Var + ")"
		}
		return strings.Join(kinds, "+"), " (" + e.Var + ")"
	default:
		return fmt.Sprintf("%T", e), ""
	}
}

func sourceKind(e ecma262.Expr) string {
	switch e := e.(type) {
	case *ecma262.PropExpr:
		return "property read"
	case *ecma262.AllocExpr, *ecma262.LitExpr:
		return "fresh"
	case *ecma262.VarExpr:
		if e.Var == "?" {
			return "closure parameter"
		}
		return "copy"
	default:
		return fmt.Sprintf("%T", e)
	}
}

// returnAlias reads what every return hands back, in member positions: the
// receiver, a 0-based declared parameter, a fresh value, or unknown.
func returnAlias(origins *ecma262.OriginMap) string {
	f := origins.Func()
	kinds := set.NewSet[string]()
	for _, n := range f.Nodes {
		ret, ok := n.(*ecma262.ReturnNode)
		if !ok {
			continue
		}
		o := origins.Eval(ret.Value)
		switch {
		case o.Kind == ecma262.OriginGlobal:
			kinds.Add("global")
		case o.Interior:
			kinds.Add("unknown")
		case o.Kind == ecma262.OriginParam && o.Index == 0:
			kinds.Add("self")
		case o.Kind == ecma262.OriginParam:
			kinds.Add(fmt.Sprintf("p%d", o.Index-1))
		case o.Kind == ecma262.OriginFresh:
			kinds.Add("fresh")
		default:
			kinds.Add("unknown")
		}
	}
	if kinds.Contains("unknown") {
		return "unknown"
	}
	out := kinds.ToSlice()
	sort.Strings(out)
	if len(out) > 1 {
		// Fresh drops out of a union, since an owned value bounds no lifetime.
		var kept []string
		for _, k := range out {
			if k != "fresh" {
				kept = append(kept, k)
			}
		}
		out = kept
	}
	return strings.Join(out, "|")
}

// ---------------------------------------------------------------------------
// Report

func TestHandLowering(t *testing.T) {
	var sb strings.Builder
	type tally struct{ members, nontrivial, settled, settledNontrivial, returns, checked, agreed, partial, partialAgreed int }
	var total tally
	blockerCounts := map[string]int{}
	var openThemes []set.Set[string]
	var openBlockers [][]string
	var openNames []string

	for _, p := range packages {
		results := analyze(t, p)
		var tl tally
		fmt.Fprintf(&sb, "## %s@%s\n\n", p.name, p.version)
		for _, r := range results {
			tl.members++
			if !r.trivial() {
				tl.nontrivial++
			}
			if r.settled() {
				tl.settled++
				if !r.trivial() {
					tl.settledNontrivial++
				}
			}
			if r.returns != "unknown" {
				tl.returns++
			}
			status := "open"
			if r.settled() {
				status = "settled"
			}
			check := ""
			if r.settled() && r.expect != "" {
				tl.checked++
				if r.expect == r.answer() {
					tl.agreed++
					check = " (matches source)"
				} else {
					check = fmt.Sprintf(" (MISMATCH, source says %s)", r.expect)
				}
			}
			if !r.settled() && r.expect != "" {
				// An open member can still carry settled positions, such as a
				// proven `&mut`. Each one is checked against the source.
				want := strings.Fields(r.expect)
				for i, got := range strings.Fields(r.answer()) {
					if strings.HasSuffix(got, ":?") || strings.HasSuffix(got, ":prim") {
						continue
					}
					tl.partial++
					if i < len(want) && want[i] == got {
						tl.partialAgreed++
					} else {
						check += fmt.Sprintf(" (MISMATCH at %s, source says %s)", got, r.expect)
					}
				}
			}
			fmt.Fprintf(&sb, "%-34s %-8s %s; returns %s%s\n", r.name, status, r.answer(), r.returns, check)
			classes := set.NewSet[string]()
			themes := set.NewSet[string]()
			for _, b := range r.blockers {
				fmt.Fprintf(&sb, "    blocked by %s\n", b)
				classes.Add(blockerClass(b))
				themes.Add(themeOf(blockerClass(b)))
			}
			if !r.settled() {
				for _, c := range classes.ToSlice() {
					blockerCounts[c]++
				}
				openThemes = append(openThemes, themes)
				openBlockers = append(openBlockers, r.blockers)
				openNames = append(openNames, p.name+" "+r.name)
			}
		}
		fmt.Fprintf(&sb, "\nsettled %d/%d members, %d/%d excluding trivial ones; return alias known for %d/%d\n\n",
			tl.settled, tl.members, tl.settledNontrivial, tl.nontrivial, tl.returns, tl.members)
		total.members += tl.members
		total.nontrivial += tl.nontrivial
		total.settled += tl.settled
		total.settledNontrivial += tl.settledNontrivial
		total.returns += tl.returns
		total.checked += tl.checked
		total.agreed += tl.agreed
		total.partial += tl.partial
		total.partialAgreed += tl.partialAgreed
	}
	fmt.Fprintf(&sb, "## Total\n\nsettled %d/%d members, %d/%d excluding trivial ones; return alias known for %d/%d\n",
		total.settled, total.members, total.settledNontrivial, total.nontrivial, total.returns, total.members)
	fmt.Fprintf(&sb, "hand check: %d/%d settled members match the source, and %d/%d settled positions of open members\n\nblocker classes, counted once per open member they block:\n", total.agreed, total.checked, total.partialAgreed, total.partial)
	classes := make([]string, 0, len(blockerCounts))
	for c := range blockerCounts {
		classes = append(classes, c)
	}
	sort.Slice(classes, func(i, j int) bool {
		if blockerCounts[classes[i]] != blockerCounts[classes[j]] {
			return blockerCounts[classes[i]] > blockerCounts[classes[j]]
		}
		return classes[i] < classes[j]
	})
	for _, c := range classes {
		fmt.Fprintf(&sb, "  %3d  %s\n", blockerCounts[c], c)
	}

	// The ladder counts the open members that would settle once every blocker
	// in the listed themes were gone, adding one theme per row. A member
	// settles on a row when all of its blockers fall in the themes so far.
	fmt.Fprintf(&sb, "\nopen members each theme blocks, and those it alone blocks:\n")
	for _, theme := range themeOrder {
		blocked, alone := 0, 0
		for _, ts := range openThemes {
			if ts.Contains(theme) {
				blocked++
				if ts.Len() == 1 {
					alone++
				}
			}
		}
		fmt.Fprintf(&sb, "  %-66s %2d  %2d\n", theme, blocked, alone)
	}
	fmt.Fprintf(&sb, "\nopen members whose every blocker falls in the themes so far, cumulative:\n")
	fixed := set.NewSet[string]()
	for _, theme := range themeOrder {
		fixed.Add(theme)
		n := 0
		for _, ts := range openThemes {
			if ts.Len() > 0 && ts.Difference(fixed).Len() == 0 {
				n++
			}
		}
		fmt.Fprintf(&sb, "  + %-62s %2d/%d\n", theme, n, len(openThemes))
	}

	writeJellyLadder(t, &sb, openNames, openBlockers)

	t.Log("\n" + sb.String())
	if os.Getenv("WRITE_REPORT") != "" {
		_, here, _, _ := runtime.Caller(0)
		name := "report.txt"
		if os.Getenv("JELLY") != "" {
			name = "report-jelly.txt"
		}
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(here), name), []byte(sb.String()), 0o644))
	}
}

// themeOrder lists the blocker themes in the order the ladder adds them, which
// runs from the analysis's own limits outward to code it cannot see.
var themeOrder = []string{
	"A. origin lattice: writes through properties, call results and joins",
	"B. module-level and captured state",
	"C. callbacks the package stores and calls later",
	"D. dynamic dispatch and functions built at runtime",
	"E. code outside the package",
	"F. not lowered for this check",
}

// themeOf groups a blocker class into one of themeOrder's themes.
func themeOf(class string) string {
	switch {
	case class == "write to unknown: free name":
		return themeOrder[1]
	case strings.HasPrefix(class, "write to unknown"), class == "callback value unknown: module-level value":
		// A module-level function value is one zod's `$constructor` builds
		// at load, which belongs with the functions built at runtime.
		if class == "callback value unknown: module-level value" {
			return themeOrder[3]
		}
		return themeOrder[0]
	case strings.HasPrefix(class, "callback value unknown"):
		// A function passed on after a join or a property read, as
		// date-fns's `context || argument`.
		return themeOrder[0]
	case class == "unresolved: closure-param":
		return themeOrder[2]
	case class == "unresolved: external-package", class == "unresolved: host-api", class == "unresolved: builtin-unkeyed":
		return themeOrder[4]
	case class == "unresolved: not-lowered":
		return themeOrder[5]
	default:
		return themeOrder[3]
	}
}

// blockerClass groups a blocker by its kind, dropping the site it names.
func blockerClass(b string) string {
	switch {
	case strings.HasPrefix(b, "unresolved "):
		reason := strings.TrimPrefix(b, "unresolved ")
		return "unresolved: " + strings.SplitN(reason, ":", 2)[0]
	case strings.HasPrefix(b, "callback value unknown"):
		why := b[strings.LastIndex(b, ": ")+2:]
		return "callback value unknown: " + strings.SplitN(why, " (", 2)[0]
	case strings.HasPrefix(b, "write to unknown"):
		why := b[strings.LastIndex(b, ": ")+2:]
		return "write to unknown: " + strings.SplitN(why, " (", 2)[0]
	default:
		return b
	}
}

func TestPrintStubs(t *testing.T) {
	if os.Getenv("PRINT_STUBS") == "" {
		t.Skip()
	}
	var all []*fn
	for _, p := range packages {
		all = append(all, p.funcs...)
	}
	for _, s := range builtinStubs(t, all) {
		t.Logf("%s(%s): %v", s.name, strings.Join(s.params, ", "), s.nodes)
	}
}

// jellySite is one entry of planning/third_party/spike/jelly/sites.json: a
// blocker this check found and what the Jelly prototype said about its call
// site.
type jellySite struct {
	Package string `json:"package"`
	Site    string `json:"site"`
	Blocker string `json:"blocker"`
	Outcome string `json:"outcome"`
	Jelly   string `json:"jelly"`
}

// writeJellyLadder reports how many open members would settle once Jelly's
// call targets replace the blockers it resolved. A blocker Jelly resolves to
// package code or to a builtin is dropped. One it traces to a stored callback
// counts under theme C, one it traces to a method of an object the caller
// supplies counts as dynamic dispatch, and one it traces outside the package
// counts as outside code. The count is an upper bound, since a resolved target
// can bring blockers of its own.
func writeJellyLadder(t *testing.T, sb *strings.Builder, names []string, blockers [][]string) {
	_, here, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(here), "../jelly/sites.json"))
	require.NoError(t, err)
	var sites []jellySite
	require.NoError(t, json.Unmarshal(data, &sites))
	byBlocker := map[string]jellySite{}
	for _, s := range sites {
		byBlocker[s.Blocker] = s
	}

	themesAfter := func(bs []string) set.Set[string] {
		themes := set.NewSet[string]()
		for _, b := range bs {
			theme := themeOf(blockerClass(b))
			if s, ok := byBlocker[b]; ok {
				switch s.Outcome {
				case "resolved", "builtin":
					continue
				case "stored":
					theme = themeOrder[2]
				case "caller-method":
					theme = themeOrder[3]
				case "external", "dependency":
					theme = themeOrder[4]
				}
			}
			themes.Add(theme)
		}
		return themes
	}

	var settled []string
	remaining := map[string]int{}
	for i, bs := range blockers {
		themes := themesAfter(bs)
		if themes.Len() == 0 {
			settled = append(settled, names[i])
		}
		for _, theme := range themesAfter(bs).ToSlice() {
			remaining[theme]++
		}
	}
	fmt.Fprintf(sb, "\nwith the Jelly prototype's call targets, of %d open members:\n", len(blockers))
	fmt.Fprintf(sb, "  %2d would settle: %s\n", len(settled), strings.Join(settled, ", "))
	fmt.Fprintf(sb, "  open members each theme still blocks:\n")
	for _, theme := range themeOrder {
		fmt.Fprintf(sb, "    %-66s %2d\n", theme, remaining[theme])
	}
}
