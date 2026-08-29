// Package ecma262 reads the ECMA-262 control-flow graph that
// tools/spec-extract serializes to cfg.json and derives the mutation, alias,
// and throw facts the builtin converter consumes. cfg.go models the graph and
// origin.go maps each value an algorithm names to where that value came from.
// mutation.go charges every mutation the graph holds to the receiver or the
// parameter it lands on, throws.go collects the exceptions each algorithm can
// raise, and classify.go combines the mutation summary and the origin map into
// one fact per builtin. See planning/ecma-262/implementation_plan.md §4 and
// §9.1 for the analyses, Appendix A for the serialized graph, and Appendix B
// for the facts.
package ecma262

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/escalier-lang/escalier/internal/set"
)

// CFG is a control-flow graph read back from cfg.json. It holds every builtin
// algorithm at a pinned spec revision, plus the abstract operations reachable
// from them.
type CFG struct {
	SpecTarget string // pinned ecma262 git ref
	Funcs      []*Func

	abstractOps map[string]*Func
	builtins    map[string]*Func
}

// FuncKind distinguishes the four algorithm shapes the spec defines. Only a
// BuiltinMethod has a receiver. A SyntaxDirected operation is the runtime
// semantics of the language rather than a library surface, so the serializer
// drops it and ParseCFG rejects one that reaches it.
type FuncKind string

const (
	BuiltinMethod  FuncKind = "builtin-method"  // X.prototype.method; has a `this` value
	BuiltinStatic  FuncKind = "builtin-static"  // X.method; no receiver
	AbstractOp     FuncKind = "abstract-op"     // Set, ToObject, ArrayCreate, ...
	SyntaxDirected FuncKind = "syntax-directed" // evaluation semantics; not serialized
)

// Func is one algorithm. Params holds the declared parameters in order. A
// method's receiver is not among them; it is the `this` value, reached through
// a ThisExpr.
//
// Variadic is the position of the rest parameter, the formal that takes the
// arguments the head does not name one by one. `Array.prototype.push (
// ...items )` declares one at position 0. The position is carried rather than
// read off the end of Params, because such a formal need not come last.
// `Function ( ...parameterArgs, bodyArg )` declares one at position 0 and an
// ordinary formal after it.
type Func struct {
	Name     string   // canonical spec key or abstract-operation name
	Kind     FuncKind //
	Params   []string // declared parameters, in order, 0-based
	Variadic *int     // rest-parameter position; nil when the head declares none
	Promise  bool     // the algorithm returns a promise
	Nodes    []Node   // CFG nodes in flat order

	// Digest fingerprints this algorithm as cfg.json serializes it. A curated
	// entry records the digest of the algorithm its author reviewed. A spec
	// bump then flags the entries whose algorithm it rewrote and leaves the
	// rest alone. See curated.go.
	Digest string
}

// Node is one step of an algorithm.
//
//sumtype:decl
type Node interface {
	isNode()
}

func (*LetNode) isNode()       {}
func (*CallNode) isNode()      {}
func (*SlotWriteNode) isNode() {}
func (*ThrowNode) isNode()     {}
func (*ReturnNode) isNode()    {}
func (*BranchNode) isNode()    {}
func (*OpaqueNode) isNode()    {}

// LetNode binds Target to Source. Serialized as kind "let".
type LetNode struct {
	Target string
	Source Expr
}

// CallNode calls Callee, binding its result to Target when the algorithm names
// the result. Serialized as kind "call".
//
// Callee is either an abstract-operation name, which resolves in the graph, or
// a formal parameter holding a function, which is a callback such as the
// `callbackfn` in `? Call(callbackfn, ...)`. The origin map tells the two
// apart: a callee whose origin is a parameter is a callback.
type CallNode struct {
	Target string // empty when the result is discarded
	Callee string
	Args   []Expr
	Guard  Guard
}

// SlotWriteNode writes Value into Object's Slot. Slot is the name without its
// brackets, so [[MapData]] is "MapData". Serialized as kind "slotwrite".
//
// Value is the operand §8.1 reads to see a parameter escape into a container.
// The serializer cannot always name it, so it can be nil: a closure's argument
// prologue writes an incoming argument the algorithm never named, as in
// `NewPromiseCapability:clo0` storing into `__args__.[[resolve]]`. Slot is
// empty on the few writes whose slot the algorithm computes.
type SlotWriteNode struct {
	Object Expr
	Slot   string
	Value  Expr // nil when the graph does not name the stored value
}

// ThrowNode throws. ErrorType names the error the algorithm constructs, such as
// "TypeError". Value is set instead for the rare algorithm that throws a value
// it did not construct. Serialized as kind "throw".
type ThrowNode struct {
	ErrorType string
	Value     Expr // nil when the algorithm constructs the error
}

// ReturnNode returns Value. Serialized as kind "return".
type ReturnNode struct {
	Value Expr
}

// BranchNode is control flow. The analysis never interprets one, so it carries
// no data. Serialized as kind "branch".
type BranchNode struct{}

// OpaqueNode is a step the serializer could not lower, which leaves the
// analysis unable to see the whole algorithm. Serialized as kind "opaque".
//
// Text is the phrase the serializer could not formalize, spelled as the
// specification writes it, such as "Let _a_ be the first _k_ - _f_ code units
// of _m_." from Number.prototype.toFixed. An assertion contributes its
// condition without the "Assert: " that precedes it in the step.
//
// The prose is the evidence for what the analysis loses by giving up on the
// step. A step binding a name over numbers loses nothing. A step replacing the
// elements of a slot loses a mutation. Each unformalized phrase is its own
// entry, since one step can carry several.
type OpaqueNode struct {
	Text []string
}

// NodeKind is the tag each Node variant serializes under.
type NodeKind string

const (
	NodeLet       NodeKind = "let"
	NodeCall      NodeKind = "call"
	NodeSlotWrite NodeKind = "slotwrite"
	NodeThrow     NodeKind = "throw"
	NodeReturn    NodeKind = "return"
	NodeBranch    NodeKind = "branch"
	NodeOpaque    NodeKind = "opaque"
)

// Guard is the completion-record guard on a call. `?` propagates an abrupt
// completion to the caller and `!` asserts that none arises.
type Guard string

const (
	GuardQuestion Guard = "?"     // Let x be ? Foo(...)
	GuardBang     Guard = "!"     // Let x be ! Foo(...)
	GuardPlain    Guard = "plain" // result not completion-checked
)

// Expr is a value an algorithm step reads or builds.
//
//sumtype:decl
type Expr interface {
	isExpr()
}

func (*VarExpr) isExpr()   {}
func (*ThisExpr) isExpr()  {}
func (*LitExpr) isExpr()   {}
func (*CallExpr) isExpr()  {}
func (*SlotExpr) isExpr()  {}
func (*PropExpr) isExpr()  {}
func (*AllocExpr) isExpr() {}

// VarExpr names a value. The name also reaches a closure passed as a value,
// which resolves against the abstract operations the way a callee does.
// Serialized as kind "var".
type VarExpr struct {
	Var string
}

// ThisExpr is the `this` value. Serialized as kind "this".
type ThisExpr struct{}

// LitExpr is a literal or primitive. Serialized as kind "lit".
type LitExpr struct{}

// CallExpr is a call nested inside an expression. The compiled IR states every
// call as its own node, so the serializer emits none of these, but Appendix A
// reserves the shape. Serialized as kind "call".
type CallExpr struct {
	Callee string
	Args   []Expr
}

// SlotExpr reads Object's Slot. Serialized as kind "slot".
type SlotExpr struct {
	Object Expr
	Slot   string
}

// PropExpr reads a property off Object, through Get(Object, Key) or a similar
// step. Serialized as kind "prop".
type PropExpr struct {
	Object Expr
}

// AllocExpr is a record, list, or map the algorithm allocates, or a copy of
// one. The value is fresh, and Args holds the operands stored into it.
// Serialized as kind "alloc".
type AllocExpr struct {
	Args []Expr
}

// ExprKind is the tag each Expr variant serializes under.
type ExprKind string

const (
	ExprVar   ExprKind = "var"
	ExprThis  ExprKind = "this"
	ExprLit   ExprKind = "lit"
	ExprCall  ExprKind = "call"
	ExprSlot  ExprKind = "slot"
	ExprProp  ExprKind = "prop"
	ExprAlloc ExprKind = "alloc"
)

// LoadCFG reads and decodes a serialized control-flow graph.
func LoadCFG(path string) (*CFG, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return ParseCFG(data)
}

// ParseCFG decodes a serialized control-flow graph and indexes its functions by
// name. Anything the analysis cannot walk or address is an error here rather
// than a silently dropped entry or a panic further in: a missing function,
// node, or operand, an unnamed function, a tag that names no variant, a field
// the tagged kind does not use, and a name repeated within one index.
func ParseCFG(data []byte) (*CFG, error) {
	var decoded decodeCFG
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decoding cfg: %w", err)
	}

	cfg := &CFG{
		SpecTarget:  decoded.SpecTarget,
		Funcs:       make([]*Func, 0, len(decoded.Funcs)),
		abstractOps: make(map[string]*Func),
		builtins:    make(map[string]*Func),
	}
	for i, df := range decoded.Funcs {
		fn, err := df.toFunc(i)
		if err != nil {
			return nil, fmt.Errorf("decoding cfg: %w", err)
		}
		var index map[string]*Func
		switch fn.Kind {
		case AbstractOp:
			index = cfg.abstractOps
		case BuiltinMethod, BuiltinStatic:
			index = cfg.builtins
		case SyntaxDirected:
			fallthrough
		default:
			return nil, fmt.Errorf("decoding cfg: %s has kind %q, which the analysis cannot index", fn.Name, fn.Kind)
		}
		if _, dup := index[fn.Name]; dup {
			return nil, fmt.Errorf("decoding cfg: two %s functions named %s", fn.Kind, fn.Name)
		}
		index[fn.Name] = fn
		cfg.Funcs = append(cfg.Funcs, fn)
	}
	return cfg, nil
}

// AbstractOp returns the abstract operation of that name, or nil. A call's
// callee and a closure named by a VarExpr both resolve here. Names live in two
// spaces that a lookup has to keep apart. `Set` is both the property-write
// abstract operation and the `Set` constructor.
func (c *CFG) AbstractOp(name string) *Func {
	return c.abstractOps[name]
}

// Builtin returns the builtin method or static of that canonical spec key, such
// as "Array.prototype.push", or nil.
func (c *CFG) Builtin(name string) *Func {
	return c.builtins[name]
}

// The decode types mirror the serialized schema in Appendix A, where a node and
// an expression are each one object tagged by kind. encoding/json cannot pick a
// variant of a sealed interface on its own, so decoding lands here first and the
// conversions below rebuild the graph as the sum types above. A decoded
// function marshals back once, to fingerprint it for Func.Digest; nothing else
// writes this schema.

type decodeCFG struct {
	SpecTarget string        `json:"specTarget"`
	Funcs      []*decodeFunc `json:"funcs"`
}

type decodeFunc struct {
	Name     string        `json:"name"`
	Kind     FuncKind      `json:"kind"`
	Params   []string      `json:"params"`
	Variadic *int          `json:"variadic"`
	Promise  bool          `json:"promise"`
	Nodes    []*decodeNode `json:"nodes"`
}

type decodeNode struct {
	Kind      NodeKind      `json:"kind"`
	Target    string        `json:"target"`
	Source    *decodeExpr   `json:"source"`
	Callee    string        `json:"callee"`
	Args      []*decodeExpr `json:"args"`
	Guard     Guard         `json:"guard"`
	Object    *decodeExpr   `json:"object"`
	Slot      string        `json:"slot"`
	ErrorType string        `json:"errorType"`
	Value     *decodeExpr   `json:"value"`
	Text      []string      `json:"text"`
}

type decodeExpr struct {
	Kind   ExprKind      `json:"kind"`
	Var    string        `json:"var"`
	Callee string        `json:"callee"`
	Args   []*decodeExpr `json:"args"`
	Object *decodeExpr   `json:"object"`
	Slot   string        `json:"slot"`
}

// field pairs a schema field name with whether the decoded object carries it.
type field struct {
	name string
	set  bool
}

// checkFields rejects a field the kind does not use. One decode struct covers
// every kind, so a field the schema moved to another kind would otherwise be
// read as absent and the change would pass unnoticed. used names the fields the
// kind does carry, and fields returns them in schema order so a given graph
// always fails on the same one.
func checkFields(shape string, fields []field, used ...string) error {
	carried := set.FromSlice(used)
	for _, f := range fields {
		if f.set && !carried.Contains(f.name) {
			return fmt.Errorf("%s carries %q", shape, f.name)
		}
	}
	return nil
}

func (d *decodeNode) fields() []field {
	return []field{
		{"target", d.Target != ""},
		{"source", d.Source != nil},
		{"callee", d.Callee != ""},
		{"args", d.Args != nil},
		{"guard", d.Guard != ""},
		{"object", d.Object != nil},
		{"slot", d.Slot != ""},
		{"errorType", d.ErrorType != ""},
		{"value", d.Value != nil},
		{"text", d.Text != nil},
	}
}

func (d *decodeNode) check(used ...string) error {
	return checkFields(fmt.Sprintf("a %s node", d.Kind), d.fields(), used...)
}

func (d *decodeExpr) fields() []field {
	return []field{
		{"var", d.Var != ""},
		{"callee", d.Callee != ""},
		{"args", d.Args != nil},
		{"object", d.Object != nil},
		{"slot", d.Slot != ""},
	}
}

func (d *decodeExpr) check(used ...string) error {
	return checkFields(fmt.Sprintf("a %s expression", d.Kind), d.fields(), used...)
}

func (d *decodeFunc) toFunc(index int) (*Func, error) {
	if d == nil {
		return nil, fmt.Errorf("funcs[%d] is missing", index)
	}
	if d.Name == "" {
		return nil, fmt.Errorf("funcs[%d] has no name", index)
	}

	if d.Variadic != nil && (*d.Variadic < 0 || *d.Variadic >= len(d.Params)) {
		return nil, fmt.Errorf("%s declares a rest parameter at position %d, outside its %d parameters", d.Name, *d.Variadic, len(d.Params))
	}

	digest, err := d.digest()
	if err != nil {
		return nil, fmt.Errorf("fingerprinting %s: %w", d.Name, err)
	}

	fn := &Func{
		Name:     d.Name,
		Kind:     d.Kind,
		Params:   d.Params,
		Variadic: d.Variadic,
		Promise:  d.Promise,
		Nodes:    make([]Node, 0, len(d.Nodes)),
		Digest:   digest,
	}
	for i, dn := range d.Nodes {
		if dn == nil {
			return nil, fmt.Errorf("node %d of %s is missing", i, d.Name)
		}
		node, err := dn.toNode()
		if err != nil {
			return nil, fmt.Errorf("node %d of %s: %w", i, d.Name, err)
		}
		fn.Nodes = append(fn.Nodes, node)
	}
	return fn, nil
}

func (d *decodeNode) toNode() (Node, error) {
	switch d.Kind {
	case NodeLet:
		if err := d.check("target", "source"); err != nil {
			return nil, err
		}
		source, err := requireExpr(d.Source, "source")
		if err != nil {
			return nil, err
		}
		return &LetNode{Target: d.Target, Source: source}, nil
	case NodeCall:
		if err := d.check("target", "callee", "args", "guard"); err != nil {
			return nil, err
		}
		args, err := toExprs(d.Args)
		if err != nil {
			return nil, err
		}
		return &CallNode{Target: d.Target, Callee: d.Callee, Args: args, Guard: d.Guard}, nil
	case NodeSlotWrite:
		if err := d.check("object", "slot", "value"); err != nil {
			return nil, err
		}
		object, err := requireExpr(d.Object, "written object")
		if err != nil {
			return nil, err
		}
		value, err := d.Value.toExpr()
		if err != nil {
			return nil, err
		}
		return &SlotWriteNode{Object: object, Slot: d.Slot, Value: value}, nil
	case NodeThrow:
		if err := d.check("errorType", "value"); err != nil {
			return nil, err
		}
		value, err := d.Value.toExpr()
		if err != nil {
			return nil, err
		}
		return &ThrowNode{ErrorType: d.ErrorType, Value: value}, nil
	case NodeReturn:
		if err := d.check("value"); err != nil {
			return nil, err
		}
		value, err := requireExpr(d.Value, "returned value")
		if err != nil {
			return nil, err
		}
		return &ReturnNode{Value: value}, nil
	case NodeBranch:
		if err := d.check(); err != nil {
			return nil, err
		}
		return &BranchNode{}, nil
	case NodeOpaque:
		if err := d.check("text"); err != nil {
			return nil, err
		}
		if len(d.Text) == 0 {
			return nil, fmt.Errorf("the step text is missing")
		}
		for i, text := range d.Text {
			if text == "" {
				return nil, fmt.Errorf("step text %d is empty", i)
			}
		}
		return &OpaqueNode{Text: d.Text}, nil
	default:
		return nil, fmt.Errorf("kind %q names no node", d.Kind)
	}
}

// toExpr converts a decoded expression. A nil receiver is an operand the graph
// left out, which requireExpr rejects wherever the shape needs one.
func (d *decodeExpr) toExpr() (Expr, error) {
	if d == nil {
		return nil, nil
	}
	switch d.Kind {
	case ExprVar:
		if err := d.check("var"); err != nil {
			return nil, err
		}
		return &VarExpr{Var: d.Var}, nil
	case ExprThis:
		if err := d.check(); err != nil {
			return nil, err
		}
		return &ThisExpr{}, nil
	case ExprLit:
		if err := d.check(); err != nil {
			return nil, err
		}
		return &LitExpr{}, nil
	case ExprCall:
		if err := d.check("callee", "args"); err != nil {
			return nil, err
		}
		args, err := toExprs(d.Args)
		if err != nil {
			return nil, err
		}
		return &CallExpr{Callee: d.Callee, Args: args}, nil
	case ExprSlot:
		if err := d.check("object", "slot"); err != nil {
			return nil, err
		}
		object, err := requireExpr(d.Object, "read object")
		if err != nil {
			return nil, err
		}
		return &SlotExpr{Object: object, Slot: d.Slot}, nil
	case ExprProp:
		if err := d.check("object"); err != nil {
			return nil, err
		}
		object, err := requireExpr(d.Object, "read object")
		if err != nil {
			return nil, err
		}
		return &PropExpr{Object: object}, nil
	case ExprAlloc:
		if err := d.check("args"); err != nil {
			return nil, err
		}
		args, err := toExprs(d.Args)
		if err != nil {
			return nil, err
		}
		return &AllocExpr{Args: args}, nil
	default:
		return nil, fmt.Errorf("kind %q names no expression", d.Kind)
	}
}

// requireExpr converts an operand the shape cannot do without. what names the
// operand's role in the error.
func requireExpr(d *decodeExpr, what string) (Expr, error) {
	e, err := d.toExpr()
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("the %s is missing", what)
	}
	return e, nil
}

// toExprs converts an argument list, where a missing entry is an error because
// an argument's position is what the analysis reads it by.
func toExprs(ds []*decodeExpr) ([]Expr, error) {
	if len(ds) == 0 {
		return nil, nil
	}
	exprs := make([]Expr, 0, len(ds))
	for i, d := range ds {
		e, err := requireExpr(d, fmt.Sprintf("argument %d", i))
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}
	return exprs, nil
}

// digestLen is how much of the fingerprint Func.Digest keeps. Twelve hex digits
// distinguish the roughly twelve hundred algorithms in one graph with room to
// spare, and stay short enough to read in curated.json.
const digestLen = 12

// digest fingerprints one serialized algorithm. It changes when any step,
// parameter, or kind does. It does not change when cfg.json's key order or
// whitespace does. So a curated entry naming a digest is flagged for re-review
// by a spec bump that rewrote its algorithm, and by nothing else.
//
// The fingerprint is taken over the decode type rather than over Func because
// Node is a sealed interface. encoding/json writes no tag for the variant an
// interface holds, so two different steps could fingerprint alike.
//
// The decode types hold only strings, ints, bools, and slices and pointers to
// those, so the marshal cannot fail. The error is returned rather than dropped
// in case a later field can, which leaves that path untestable as it stands.
func (d *decodeFunc) digest() (string, error) {
	encoded, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:digestLen], nil
}
