// Package webidl is the stage-2 half of the WebIDL -> Escalier pipeline.
// It ingests the JSON intermediate representation emitted by
// tools/webidl_to_esc/extract.mjs and renders Escalier `.esc` declarations.
//
// The package is deliberately decoupled from internal/dts_parser: the JSON
// IR is the only contract, so the Node extractor can be regenerated against
// a newer @webref/idl without touching Go. The one piece of shared logic is
// interop.ClassifyMethodByName, reused so the WebIDL path makes the same
// name-based receiver-mutability decisions as the .d.ts path.
package webidl

// Artifact is one spec's worth of IR — the top-level shape of each JSON
// file written by the extractor.
type Artifact struct {
	Spec       string      `json:"spec"`
	Interfaces []Interface `json:"interfaces"`
	Includes   []Include   `json:"includes"`
}

// Include records an `X includes M;` statement: mixin M's members are mixed
// into interface X.
type Include struct {
	Target string `json:"target"`
	Mixin  string `json:"mixin"`
}

// Interface is a WebIDL `interface` or `interface mixin`. Partial pieces and
// mixins are merged into their base by the converter before rendering.
type Interface struct {
	Name        string   `json:"name"`
	Inheritance *string  `json:"inheritance"`
	Partial     bool     `json:"partial"`
	Mixin       bool     `json:"mixin"`
	Members     []Member `json:"members"`
}

// Member covers attributes, operations, constructors, and constants. The
// active fields depend on Kind; the JSON omits the rest.
type Member struct {
	Kind string `json:"kind"` // "attribute" | "operation" | "constructor" | "const" | "unsupported"
	Name string `json:"name"`

	// Attribute / operation shared.
	Type   *TypeRef `json:"type"`   // attribute type
	Static bool     `json:"static"` // static member of the interface

	// Attribute-only.
	Readonly    bool    `json:"readonly"`
	SameObject  bool    `json:"sameObject"`  // [SameObject]: value owned by host, borrows from self
	NewObject   bool    `json:"newObject"`   // [NewObject]: freshly allocated, caller-owned
	PutForwards *string `json:"putForwards"` // [PutForwards=x]: assignment forwards to .x

	// Operation-only.
	Special string   `json:"special"`
	Return  *TypeRef `json:"return"`
	Args    []Arg    `json:"args"`

	// Const-only.
	Value *string `json:"value"`

	// Unsupported-only: the original webidl2 member type, for a TODO note.
	MemberType string `json:"memberType"`

	// Origin is the interface or mixin that declared this member, set by the
	// converter's merge step. It is the lookup key for the throws map when a
	// member is folded into a concrete interface from a mixin — e.g.
	// querySelector renders under Element but its throws are keyed
	// "ParentNode.querySelector". Not part of the JSON IR.
	Origin string `json:"-"`
}

// Arg is one operation/constructor argument.
type Arg struct {
	Name     string   `json:"name"`
	Type     *TypeRef `json:"type"`
	Optional bool     `json:"optional"`
	Variadic bool     `json:"variadic"`
}

// TypeRef is a structured WebIDL type. Union members and generic arguments
// live in Args; the Go converter maps this to an Escalier type string
// without re-parsing.
type TypeRef struct {
	Union    bool      `json:"union"`
	Name     string    `json:"name"` // base or generic name; "" when Union
	Args     []TypeRef `json:"args"`
	Nullable bool      `json:"nullable"`
}
