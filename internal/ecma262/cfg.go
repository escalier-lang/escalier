// Package ecma262 analyzes the ECMA-262 control-flow graph that
// tools/spec-extract serializes to cfg.json and derives the mutation and
// alias facts the builtin converter consumes. See
// planning/ecma-262/implementation_plan.md §4 for the analysis and
// Appendix A for the schema this file mirrors.
package ecma262

import (
	"encoding/json"
	"fmt"
	"os"
)

// CFG is a serialized control-flow graph: every builtin algorithm at a pinned
// spec revision, plus the abstract operations reachable from them.
type CFG struct {
	SpecTarget string  `json:"specTarget"` // pinned ecma262 git ref
	Funcs      []*Func `json:"funcs"`

	abstractOps map[string]*Func
	builtins    map[string]*Func
}

// FuncKind distinguishes the four algorithm shapes the spec defines. Only a
// BuiltinMethod has a receiver.
type FuncKind string

const (
	BuiltinMethod  FuncKind = "builtin-method"  // X.prototype.method; has a `this` value
	BuiltinStatic  FuncKind = "builtin-static"  // X.method; no receiver
	AbstractOp     FuncKind = "abstract-op"     // Set, ToObject, ArrayCreate, ...
	SyntaxDirected FuncKind = "syntax-directed" // evaluation semantics; not serialized
)

// Func is one algorithm. Params holds the declared parameters in order. A
// method's receiver is not among them; it is the `this` value, reached through
// an ExprThis.
type Func struct {
	Name    string   `json:"name"` // canonical spec key or abstract-operation name
	Kind    FuncKind `json:"kind"`
	Params  []string `json:"params"`  // declared parameters, in order, 0-based
	Promise bool     `json:"promise"` // the algorithm returns a promise
	Nodes   []*Node  `json:"nodes"`   // CFG nodes in flat order
}

// NodeKind names the statement shapes the serializer lowers a spec step to.
type NodeKind string

const (
	NodeLet       NodeKind = "let"       // bind Target = Source
	NodeCall      NodeKind = "call"      // optional Target = Callee(Args...)
	NodeSlotWrite NodeKind = "slotwrite" // write Value into Object.Slot
	NodeThrow     NodeKind = "throw"     // Throw a <ErrorType> exception
	NodeReturn    NodeKind = "return"    // return Value
	NodeBranch    NodeKind = "branch"    // control flow; carries no analyzed data
	NodeOpaque    NodeKind = "opaque"    // a step the serializer could not lower
)

// Guard is the completion-record guard on a call. `?` propagates an abrupt
// completion to the caller and `!` asserts that none arises.
type Guard string

const (
	GuardQuestion Guard = "?"     // Let x be ? Foo(...)
	GuardBang     Guard = "!"     // Let x be ! Foo(...)
	GuardPlain    Guard = "plain" // result not completion-checked
)

// Node is one step of an algorithm. Which fields carry a value depends on Kind.
type Node struct {
	Kind   NodeKind `json:"kind"`
	Target string   `json:"target,omitempty"` // Let target, or Call result binding
	Source *Expr    `json:"source,omitempty"` // Let
	// Callee is a Call's callee. It is either an abstract-operation name, which
	// resolves in the graph, or a formal parameter holding a function, which is
	// a callback such as the `callbackfn` in `? Call(callbackfn, ...)`. The
	// origin map tells the two apart: a callee whose origin is Param(k) is a
	// callback.
	Callee string  `json:"callee,omitempty"`
	Args   []*Expr `json:"args,omitempty"`   // Call
	Guard  Guard   `json:"guard,omitempty"`  // Call: ? / ! / plain
	Object *Expr   `json:"object,omitempty"` // SlotWrite: the written object
	// Slot is the written slot name without its brackets, so [[MapData]] is
	// "MapData".
	Slot      string `json:"slot,omitempty"`
	ErrorType string `json:"errorType,omitempty"` // Throw of a constructed error: "TypeError", ...
	// Value is the returned expression on a Return, the stored value on a
	// SlotWrite, and on a Throw the thrown expression, for the rare algorithm
	// that throws a value it did not construct.
	Value *Expr `json:"value,omitempty"`
}

// ExprKind names the value shapes an algorithm step reads or builds.
type ExprKind string

const (
	ExprVar  ExprKind = "var"  // a named value
	ExprThis ExprKind = "this" // the this value
	ExprLit  ExprKind = "lit"  // literal / primitive
	ExprCall ExprKind = "call" // nested abstract-operation call
	ExprSlot ExprKind = "slot" // READ of Object.Slot
	ExprProp ExprKind = "prop" // READ via Get(Object, Key) etc.
	// ExprAlloc is a record, list, or map the algorithm allocates, or a copy of
	// one. The value is fresh, and Args holds the operands stored into it.
	ExprAlloc ExprKind = "alloc"
)

// Expr is a value expression. Which fields carry a value depends on Kind.
type Expr struct {
	Kind   ExprKind `json:"kind"`
	Var    string   `json:"var,omitempty"`    // ExprVar; also names a closure passed as a value
	Callee string   `json:"callee,omitempty"` // ExprCall
	Args   []*Expr  `json:"args,omitempty"`   // ExprCall / ExprAlloc
	Object *Expr    `json:"object,omitempty"` // ExprSlot / ExprProp
	Slot   string   `json:"slot,omitempty"`   // ExprSlot
}

// LoadCFG reads and decodes a serialized control-flow graph.
func LoadCFG(path string) (*CFG, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return ParseCFG(data)
}

// ParseCFG decodes a serialized control-flow graph and indexes its functions
// by name.
func ParseCFG(data []byte) (*CFG, error) {
	cfg := &CFG{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("decoding cfg: %w", err)
	}
	cfg.abstractOps = make(map[string]*Func)
	cfg.builtins = make(map[string]*Func)
	for _, fn := range cfg.Funcs {
		if fn.Kind == AbstractOp {
			cfg.abstractOps[fn.Name] = fn
		} else {
			cfg.builtins[fn.Name] = fn
		}
	}
	return cfg, nil
}

// AbstractOp returns the abstract operation named name, or nil. A call's callee
// and a closure named by an ExprVar both resolve here. Names live in two spaces
// that a lookup must keep apart: `Set` is both the property-write abstract
// operation and the `Set` constructor.
func (c *CFG) AbstractOp(name string) *Func {
	return c.abstractOps[name]
}

// Builtin returns the builtin method or static named by its canonical spec key,
// such as "Array.prototype.push", or nil.
func (c *CFG) Builtin(name string) *Func {
	return c.builtins[name]
}
