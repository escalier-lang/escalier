package webidl

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/interop"
)

// ConvertArtifact renders one spec artifact to Escalier `.esc` source.
//
// Each WebIDL interface becomes a `declare class`. Instance members carry a
// `self` / `mut self` receiver chosen by interop.ClassifyMethodByName, so the
// WebIDL path makes the same name-based mutability decisions as the .d.ts
// path. The two signals the .d.ts source drops are surfaced as annotations:
//   - [NewObject] return  -> wrapped in `mut` (caller owns a fresh value)
//   - [SameObject] getter -> tagged as borrowing from `self` (a lifetime tie)
//
// The output is a review aid, not a drop-in: it stamps the hard-to-infer
// signals for a human to confirm, exactly as the dts_to_esc converter does.
func ConvertArtifact(a Artifact) string {
	return ConvertArtifactThrows(a, nil)
}

// renderCtx threads the per-class state the member emitters need: the
// enclosing interface name and the optional throws map keyed by
// "Interface.method" (produced by tools/webidl_to_esc/extract_throws.mjs).
type renderCtx struct {
	iface  string
	throws map[string][]string
}

// ConvertArtifactThrows is ConvertArtifact plus an exception map: when
// throws["Iface.method"] is set, that operation renders a `throws` clause.
// The map comes from the spec-algorithm extractor, not from the WebIDL.
func ConvertArtifactThrows(a Artifact, throws map[string][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Generated from WebIDL spec %q by internal/webidl.\n", a.Spec)
	b.WriteString("// Prototype output: receiver mutability is heuristic; ownership comes\n")
	b.WriteString("// from [NewObject]/[SameObject]; throws come from spec algorithms.\n\n")

	for _, iface := range merged(a) {
		writeClass(&b, iface, renderCtx{iface: iface.Name, throws: throws})
		b.WriteString("\n")
	}
	return b.String()
}

// merged folds partial interfaces and `includes` mixins into their base
// interface so each base renders as a single class. Cross-spec mixins are
// out of scope for the prototype; an unresolved mixin is noted on the class.
func merged(a Artifact) []Interface {
	bases := map[string]*Interface{}
	mixins := map[string]Interface{}
	var order []string

	for _, iface := range a.Interfaces {
		if iface.Mixin {
			mixins[iface.Name] = iface
			continue
		}
		if existing, ok := bases[iface.Name]; ok {
			existing.Members = append(existing.Members, iface.Members...)
			continue
		}
		copyIface := iface
		bases[iface.Name] = &copyIface
		order = append(order, iface.Name)
	}

	for _, inc := range a.Includes {
		base, ok := bases[inc.Target]
		if !ok {
			continue
		}
		if mixin, ok := mixins[inc.Mixin]; ok {
			base.Members = append(base.Members, mixin.Members...)
		}
	}

	out := make([]Interface, 0, len(order))
	for _, name := range order {
		out = append(out, *bases[name])
	}
	return out
}

func writeClass(b *strings.Builder, iface Interface, ctx renderCtx) {
	header := "declare class " + iface.Name
	if iface.Inheritance != nil {
		header += " extends " + *iface.Inheritance
	}
	b.WriteString(header + " {\n")

	// Instance members first, then statics, so the class reads top-down from
	// "what an instance can do" to "what the constructor exposes".
	for _, m := range iface.Members {
		if m.Static {
			continue
		}
		writeInstanceMember(b, m, ctx)
	}
	for _, m := range iface.Members {
		if !m.Static {
			continue
		}
		writeStaticMember(b, m, ctx)
	}
	b.WriteString("}\n")
}

func writeInstanceMember(b *strings.Builder, m Member, ctx renderCtx) {
	switch m.Kind {
	case "attribute":
		writeAttribute(b, m)
	case "operation":
		writeOperation(b, m, ctx)
	case "constructor":
		fmt.Fprintf(b, "    constructor(%s),\n", renderArgs(m.Args))
	case "const":
		fmt.Fprintf(b, "    readonly %s: %s,\n", m.Name, MapType(m.Type))
	case "unsupported":
		fmt.Fprintf(b, "    // TODO: unsupported WebIDL member %q\n", m.MemberType)
	}
}

func writeStaticMember(b *strings.Builder, m Member, ctx renderCtx) {
	switch m.Kind {
	case "attribute":
		ro := ""
		if m.Readonly {
			ro = "readonly "
		}
		fmt.Fprintf(b, "    %sstatic %s: %s,\n", ro, m.Name, MapType(m.Type))
	case "operation":
		ret := MapType(m.Return)
		note := ""
		if m.NewObject {
			ret = "mut " + ret
			note = "  // [NewObject] caller owns a fresh value"
		}
		fmt.Fprintf(b, "    static %s(%s) -> %s%s,%s\n", m.Name, renderArgs(m.Args), ret, throwsClause(m.Name, ctx), note)
	case "const":
		fmt.Fprintf(b, "    readonly static %s: %s,\n", m.Name, MapType(m.Type))
	}
}

// throwsClause returns a ` throws E1 | E2` suffix when the throws map has an
// entry for this interface/method, or "" otherwise. The exceptions come from
// the spec-algorithm extractor, since WebIDL carries no exception info.
func throwsClause(name string, ctx renderCtx) string {
	excs := ctx.throws[ctx.iface+"."+name]
	if len(excs) == 0 {
		return ""
	}
	return " throws " + strings.Join(excs, " | ")
}

// writeAttribute renders a WebIDL attribute as a getter, plus a setter when
// the attribute is writable. A readonly+[PutForwards] attribute is readonly
// at the slot but assignment forwards to a member, so it gets a setter too.
func writeAttribute(b *strings.Builder, m Member) {
	typ := MapType(m.Type)
	borrow := ""
	if m.SameObject {
		borrow = "  // [SameObject] result borrows from self; candidate for a self lifetime"
	}
	fmt.Fprintf(b, "    get %s(self) -> %s,%s\n", m.Name, typ, borrow)

	if !m.Readonly {
		fmt.Fprintf(b, "    set %s(mut self, value: %s),\n", m.Name, typ)
	} else if m.PutForwards != nil {
		fmt.Fprintf(b, "    set %s(mut self, value: %s),  // [PutForwards=%s]\n", m.Name, typ, *m.PutForwards)
	}
}

func writeOperation(b *strings.Builder, m Member, ctx renderCtx) {
	mut, ok := interop.ClassifyMethodByName(m.Name)
	if !ok {
		// No name tier matched: fall to the tier-7 default (mutating) and
		// flag it so a reviewer can confirm. WebIDL carries no method-level
		// mutation signal, so this is the honest default.
		mut = true
	}
	recv := "self"
	if mut {
		recv = "mut self"
	}

	ret := MapType(m.Return)
	if m.NewObject {
		ret = "mut " + ret
	}

	note := ""
	switch {
	case !ok && m.NewObject:
		note = "  // receiver-mut uncertain (tier-7 default); [NewObject] caller owns result"
	case !ok:
		note = "  // receiver-mut uncertain (tier-7 default)"
	case m.NewObject:
		note = "  // [NewObject] caller owns result"
	}

	fmt.Fprintf(b, "    %s(%s) -> %s%s,%s\n", m.Name, joinReceiver(recv, m.Args), ret, throwsClause(m.Name, ctx), note)
}

func joinReceiver(recv string, args []Arg) string {
	rendered := renderArgs(args)
	if rendered == "" {
		return recv
	}
	return recv + ", " + rendered
}

func renderArgs(args []Arg) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		name := a.Name
		if a.Variadic {
			parts = append(parts, "..."+name+": Array<"+MapType(a.Type)+">")
			continue
		}
		opt := ""
		if a.Optional {
			opt = "?"
		}
		parts = append(parts, fmt.Sprintf("%s%s: %s", name, opt, MapType(a.Type)))
	}
	return strings.Join(parts, ", ")
}

// MapType renders a WebIDL type as an Escalier type string. The scalar table
// is the standard WebIDL->JS mapping; generics fold to their Escalier
// equivalents; unions join with `|`; nullable appends `| null`.
func MapType(t *TypeRef) string {
	if t == nil {
		return "unknown"
	}
	var s string
	if t.Union {
		parts := make([]string, len(t.Args))
		for i := range t.Args {
			parts[i] = MapType(&t.Args[i])
		}
		s = strings.Join(parts, " | ")
	} else {
		s = mapNamed(t)
	}
	if t.Nullable {
		s += " | null"
	}
	return s
}

func mapNamed(t *TypeRef) string {
	switch t.Name {
	case "sequence", "FrozenArray", "ObservableArray":
		return "Array<" + MapType(&t.Args[0]) + ">"
	case "Promise":
		return "Promise<" + MapType(&t.Args[0]) + ">"
	case "record":
		return "Record<" + MapType(&t.Args[0]) + ", " + MapType(&t.Args[1]) + ">"
	}
	if len(t.Args) > 0 {
		parts := make([]string, len(t.Args))
		for i := range t.Args {
			parts[i] = MapType(&t.Args[i])
		}
		return t.Name + "<" + strings.Join(parts, ", ") + ">"
	}
	return mapScalar(t.Name)
}

var scalarMap = map[string]string{
	"DOMString":           "string",
	"USVString":           "string",
	"ByteString":          "string",
	"CSSOMString":         "string",
	"boolean":             "boolean",
	"byte":                "number",
	"octet":               "number",
	"short":               "number",
	"unsigned short":      "number",
	"long":                "number",
	"unsigned long":       "number",
	"long long":           "number",
	"unsigned long long":  "number",
	"float":               "number",
	"unrestricted float":  "number",
	"double":              "number",
	"unrestricted double": "number",
	"bigint":              "bigint",
	"any":                 "unknown",
	"object":              "unknown",
	"undefined":           "undefined",
	"void":                "undefined",
}

func mapScalar(name string) string {
	if mapped, ok := scalarMap[name]; ok {
		return mapped
	}
	// An interface / dictionary / enum reference: pass the name through.
	return name
}
