package webidl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ptr(s string) *string { return &s }

func scalar(name string, nullable bool) *TypeRef {
	return &TypeRef{Name: name, Nullable: nullable}
}

// TestConvertArtifact exercises the three signals the WebIDL path adds over a
// plain .d.ts conversion: name-based receiver mutability, [SameObject]
// borrow tagging, and [NewObject] owned returns. The expected output is the
// full rendered class so a regression shows the exact line that drifted.
func TestConvertArtifact(t *testing.T) {
	t.Parallel()

	artifact := Artifact{
		Spec: "sample",
		Interfaces: []Interface{{
			Name:        "Widget",
			Inheritance: ptr("Node"),
			Members: []Member{
				// [SameObject] readonly attribute -> borrowing getter.
				{Kind: "attribute", Name: "children", Type: scalar("NodeList", false), Readonly: true, SameObject: true},
				// writable nullable attribute -> getter + setter.
				{Kind: "attribute", Name: "label", Type: scalar("DOMString", true)},
				// `set*` name -> mut self, certain.
				{Kind: "operation", Name: "setLabel", Return: scalar("undefined", false),
					Args: []Arg{{Name: "value", Type: scalar("DOMString", false)}}},
				// [NewObject] + query name -> owned mut return, uncertain receiver.
				{Kind: "operation", Name: "queryItems", NewObject: true,
					Return: &TypeRef{Name: "sequence", Args: []TypeRef{{Name: "Widget"}}}},
				// no name tier matches -> tier-7 default (mut self), flagged.
				{Kind: "operation", Name: "frobnicate", Return: scalar("boolean", false)},
				// static [NewObject] factory -> owned mut return, no receiver.
				{Kind: "operation", Name: "create", Static: true, NewObject: true, Return: scalar("Widget", false)},
			},
		}},
	}

	want := `// Generated from WebIDL spec "sample" by internal/webidl.
// Prototype output: receiver mutability is heuristic; ownership comes
// from [NewObject]/[SameObject]; throws come from spec algorithms.

declare class Widget extends Node {
    get children(self) -> NodeList,  // [SameObject] result borrows from self; candidate for a self lifetime
    get label(self) -> string | null,
    set label(mut self, value: string | null),
    setLabel(mut self, value: string) -> undefined,
    queryItems(mut self) -> mut Array<Widget>,  // receiver-mut uncertain (tier-7 default); [NewObject] caller owns result
    frobnicate(mut self) -> boolean,  // receiver-mut uncertain (tier-7 default)
    static create() -> mut Widget,  // [NewObject] caller owns a fresh value
}

`

	require.Equal(t, want, ConvertArtifact(artifact))
}

// TestConvertArtifactThrows checks that a throws map keyed "Iface.method"
// renders a `throws` clause on the matching operation and leaves others bare.
// The exceptions come from the spec-algorithm extractor, not the WebIDL.
func TestConvertArtifactThrows(t *testing.T) {
	t.Parallel()

	artifact := Artifact{
		Spec: "sample",
		Interfaces: []Interface{{
			Name: "Doc",
			Members: []Member{
				{Kind: "operation", Name: "createElement", NewObject: true, Return: scalar("Element", false),
					Args: []Arg{{Name: "name", Type: scalar("DOMString", false)}}},
				{Kind: "operation", Name: "hasFeature", Return: scalar("boolean", false)},
			},
		}},
	}
	throws := map[string][]string{
		"Doc.createElement": {"InvalidCharacterError", "NotSupportedError"},
	}

	got := ConvertArtifactThrows(artifact, throws)
	require.Contains(t, got,
		"createElement(mut self, name: string) -> mut Element throws InvalidCharacterError | NotSupportedError,")
	// hasFeature has no throws-map entry, so no clause. `has` is a
	// non-mutating prefix, so the receiver is `self`.
	require.Contains(t, got, "hasFeature(self) -> boolean,")
	require.NotContains(t, got, "hasFeature(self) -> boolean throws")
}

// TestMapType covers the WebIDL->Escalier type mapping: scalars, generics,
// unions, and nullability.
func TestMapType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   *TypeRef
		want string
	}{
		{"nil", nil, "unknown"},
		{"DOMString", scalar("DOMString", false), "string"},
		{"nullable interface", scalar("Node", true), "Node | null"},
		{"unsigned long", scalar("unsigned long", false), "number"},
		{"sequence", &TypeRef{Name: "sequence", Args: []TypeRef{{Name: "DOMString"}}}, "Array<string>"},
		{"promise", &TypeRef{Name: "Promise", Args: []TypeRef{{Name: "undefined"}}}, "Promise<undefined>"},
		{"record", &TypeRef{Name: "record", Args: []TypeRef{{Name: "DOMString"}, {Name: "any"}}}, "Record<string, unknown>"},
		{"union", &TypeRef{Union: true, Args: []TypeRef{{Name: "Event"}, {Name: "undefined"}}}, "Event | undefined"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, MapType(tc.in))
		})
	}
}
