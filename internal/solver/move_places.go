package solver

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file holds the place model the ownership events and the dataflow pass share. A place
// is a root binding plus a path of field and element segments, such as `pair.a` or `xs[0]`.
// Two places conflict when one path is a prefix of the other under one root, so a move of
// `pair.a` conflicts with a read of `pair.a.id` and of `pair`, and not with a read of
// `pair.b`.

// placeSeg is one step of a movePlace path: a field reached from the place before it.
// The kind tags how the field is named, so a rest element's segment never matches a
// string field of the same text. The tag leaves room for a symbol-keyed segment too.
// Adding that kind is part of the M7 computed-key work.
type placeSeg struct {
	kind placeSegKind
	name string
}

type placeSegKind uint8

const (
	// namedSeg names a field by its string name — a static member `pair.a`, a
	// string-literal index `obj["a"]`, or an element `xs[0]`, whose name is `0`. The
	// segment's name holds that field name.
	namedSeg placeSegKind = iota
	// restSeg names every element of a tuple from a position on, the part a rest
	// element such as the `...rest` of `[a, ...rest]` takes. Its name is the position
	// followed by `...`, as in `1...`.
	restSeg
)

// movePlace identifies a storage location: a root binding plus a path of field segments
// reached from it by static member or constant-index access. An element `xs[0]` is the
// segment `0`, the name a tuple pattern gives it too. The empty path is the whole
// binding. A move of `pair.a` is the place
// {root: pair, path: [a]}; a later read of `pair.a.id` is {root: pair, path: [a, id]},
// and the two conflict because one path is a prefix of the other. A read of `pair.b`
// is {root: pair, path: [b]}, disjoint from `pair.a`, so a partial move of `pair.a`
// leaves it usable.
type movePlace struct {
	root liveness.VarID
	path []placeSeg
}

// exprPlace returns the place a place-expression names: a binding, or a chain of
// static member and constant index accesses rooted at one binding. ok is false for
// anything that is not such a place, such as a call result or a literal. indexSegment
// decides which indices are constant, so `xs[0]` is the place `xs[0]`, the same place a
// tuple pattern's first element names.
//
// A dynamic index falls back to its container place, since the checker cannot prove
// two dynamic indices disjoint. `arr[i]` and `arr[j]` both resolve to `arr`, so a move
// through one conservatively blocks a use through the other. The container overlaps
// each of its elements, so `arr[i]` also overlaps `arr[0]`. A link after a dynamic index
// adds nothing, so `arr[i].x` is the place `arr` too.
//
// An optional link names the place the plain link does, so `a?.b` is the place `a.b`.
// Whenever the link reads anything, it reads that place.
//
// The places of an index chain depend on the inferred type of each index, so a caller
// asks for them after the chain is inferred.
func (c *checker) exprPlace(e ast.Expr) (movePlace, bool) {
	p, _, ok := c.chainPlace(e)
	return p, ok
}

// chainPlace returns the place exprPlace names for e, whether the chain e roots holds a
// dynamic index, and whether e names a place at all. A link after a dynamic index adds no
// segment, since it reaches into an element the place does not name. So `xs[i].x` with
// `i: number` is the place `xs`. An index whose type is one integer literal is not dynamic,
// so `xs[i].x` with `i: 0` is the place `xs[0].x`. A `val i = 0` gives i that type.
func (c *checker) chainPlace(e ast.Expr) (movePlace, bool, bool) {
	chain := chainOf(e, c.indexSegment)
	root, isIdent := chain.root.(*ast.IdentExpr)
	if !isIdent || c.varIDs.Of(root) <= 0 {
		return movePlace{}, false, false
	}

	var path []placeSeg
	dynamic := false
	for _, link := range chain.links {
		if link.key == nil && link.seg.name == "" {
			return movePlace{}, false, false
		}
		// A dynamic index is approximated by its container.
		dynamic = dynamic || link.dynamic
		if !dynamic {
			path = append(path, link.seg)
		}
	}
	return movePlace{root: c.varIDs.Of(root), path: slices.Clip(path)}, dynamic, true
}

// placeChain is a member and index chain such as `a.b[0].c`. root is the expression the
// chain starts from, which is `a` here. links lists the chain's member and index reads from
// the root outward, so `.b` comes first and `.c` last. Any expression other than a member
// or index read is a chain with no links whose root is the expression itself.
type placeChain struct {
	root  ast.Expr
	links []placeLink
}

// placeLink is one member or index read of a placeChain.
type placeLink struct {
	// expr is the MemberExpr or IndexExpr of the link, and object is the receiver it reads.
	expr   ast.Expr
	object ast.Expr
	// key is the index key of an IndexExpr link, and nil for a MemberExpr link.
	key ast.Expr
	// seg is the segment the link names. A member link names its property. An index link
	// names the segment indexSegment gives its key.
	seg placeSeg
	// dynamic marks a link that names no segment. That is a member with no property, or an
	// index whose key names no segment, such as `xs[i]` with `i: number`.
	dynamic bool
	// closes marks a link that reads something other than a property its receiver declares,
	// such as a getter or a method. The place a chain names ends before such a link.
	closes bool
}

// chainOf returns the member and index chain e roots. indexSegment names the segment of
// each index key the way checker.indexSegment does. When it is nil, each index link keeps
// an empty segment and is not marked dynamic. No link is marked as closing. A nil e is a
// chain with a nil root.
func chainOf(e ast.Expr, indexSegment func(ast.Expr) (string, bool)) placeChain {
	var links []placeLink
	for {
		switch x := e.(type) {
		case *ast.MemberExpr:
			link := placeLink{expr: x, object: x.Object, dynamic: x.Prop == nil}
			if x.Prop != nil {
				link.seg = fieldSeg(x.Prop.Name)
			}
			links = append(links, link)
			e = x.Object
		case *ast.IndexExpr:
			link := placeLink{expr: x, object: x.Object, key: x.Index}
			if indexSegment != nil {
				name, ok := indexSegment(x.Index)
				link.seg, link.dynamic = fieldSeg(name), !ok
			}
			links = append(links, link)
			e = x.Object
		default:
			slices.Reverse(links)
			return placeChain{root: e, links: links}
		}
	}
}

// fieldChain returns the chain chainOf returns for e, with each index link's segment
// resolved and each closing link marked, as linkCloses decides. It checks links from the
// root outward and stops at the first link that ends the place the chain names. That is a
// link that is dynamic, has an empty name, or closes.
func (c *checker) fieldChain(e ast.Expr) placeChain {
	chain := chainOf(e, c.indexSegment)
	for i := range chain.links {
		link := &chain.links[i]
		if link.dynamic || link.seg.name == "" {
			break
		}

		link.closes = c.linkCloses(*link)
		if link.closes {
			break
		}
	}
	return chain
}

// linkCloses reports whether link, a link with a segment name, reads something other than a
// property its receiver declares, such as a getter or a method.
//
// An index that names one element of a receiver read by position does not close, as
// `xs[0]` and `xs["0"]` do not off an array. The element is part of the place though the
// receiver's type declares no property of that name.
func (c *checker) linkCloses(link placeLink) bool {
	_, isIndex := canonicalIndex(link.seg.name)
	recv := c.info.TypeOf(link.object)
	element := link.key != nil && isIndex && recv != nil && c.readsByPosition(recv)
	return !element && !c.readsAsField(link.object, link.seg.name)
}

// firstDynamic returns the position in links of the first link marked dynamic, and -1 when
// no link is.
func (ch placeChain) firstDynamic() int {
	return slices.IndexFunc(ch.links, func(link placeLink) bool { return link.dynamic })
}

// maxElementIndex is the largest index indexSegment gives its own place. canonicalIndex
// reads an element's name back as an int, which holds every index up to this bound on
// any platform.
const maxElementIndex = math.MaxInt32

// indexSegment returns the name of the place segment an index key names, and false for
// a dynamic key. Two keys name a segment:
//
//   - A string literal, as constStringKey reads it. `obj["a"]` names the field `a`.
//   - A key whose inferred type is one non-negative integer literal. `xs[0]` names the
//     segment `0`, and so does `xs[i]` with `i: 0` or `val i = 0`. The segment is the
//     integer's decimal digits, the name a tuple pattern gives the element at that
//     position.
//
// Any other key is dynamic, such as `i: number`, `i: 0 | 1`, `xs[-1]`, or `xs[1.5]`. A key
// with no recorded type is dynamic too.
//
// The first answer for a key with a recorded type is kept in Info and returned for every
// later call, so each place built from the key names the same segment.
func (c *checker) indexSegment(index ast.Expr) (string, bool) {
	if name, ok := constStringKey(index); ok {
		return name, true
	}
	if lit, ok := index.(*ast.LiteralExpr); ok {
		num, isNum := lit.Lit.(*ast.NumLit)
		if !isNum {
			return "", false
		}
		return elementName(num.Value)
	}

	t := c.info.TypeOf(index)
	if t == nil {
		return "", false
	}
	if seg, ok := c.info.indexSegments[index]; ok {
		return seg.name, seg.ok
	}

	name, ok := "", false
	if key, bound := c.boundValueType(t); bound {
		if lit, isLit := key.(*soltype.LitType); isLit {
			if num, isNum := lit.Lit.(*soltype.NumLit); isNum {
				name, ok = elementName(num.Value)
			}
		}
	}

	if c.info.indexSegments == nil {
		c.info.indexSegments = map[ast.Expr]indexSegmentAnswer{}
	}
	c.info.indexSegments[index] = indexSegmentAnswer{name: name, ok: ok}
	return name, ok
}

// indexSegmentAnswer is one answer of indexSegment: the segment name, and whether the key
// names a segment at all.
type indexSegmentAnswer struct {
	name string
	ok   bool
}

// elementName returns the segment name of the element at position n, and false when n is
// not a non-negative integer no larger than maxElementIndex.
func elementName(n float64) (string, bool) {
	if n < 0 || n > maxElementIndex || n != math.Trunc(n) {
		return "", false
	}
	return strconv.Itoa(int(n)), true
}

// fieldSeg returns the named segment for the field or element name.
func fieldSeg(name string) placeSeg {
	return placeSeg{kind: namedSeg, name: name}
}

// extendPlace returns base with one more named field segment appended, copying the
// path so sibling places built from the same base never share backing storage.
func extendPlace(base movePlace, field string) movePlace {
	return extendPlaceSeg(base, fieldSeg(field))
}

// extendPlaceSeg returns base with seg appended, copying the path as extendPlace does.
func extendPlaceSeg(base movePlace, seg placeSeg) movePlace {
	return movePlace{root: base.root, path: appendPath(base.path, []placeSeg{seg})}
}

// placesOverlap reports whether two places reach any of the same data. They must be rooted at
// the same binding, and one field path must be a prefix of the other. `x.a` and `x.a.b` overlap
// because the second sits inside the first. `x.a` and `x.b` do not.
func placesOverlap(a, b movePlace) bool {
	return a.root == b.root && pathPrefixRelated(a.path, b.path)
}

// placeKey returns a string that identifies the place p. Each segment is encoded as its
// kind and its length-prefixed name, so the encoding is injective for any field name:
// `{root, [a, b]}` and `{root, [ab]}` stay distinct regardless of which bytes a name
// contains, since the length prefix delimits each name rather than a separator the name
// might itself include.
func placeKey(p movePlace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d", p.root)
	for _, seg := range p.path {
		fmt.Fprintf(&b, "|%d:%d:%s", seg.kind, len(seg.name), seg.name)
	}
	return b.String()
}

// pathPrefixRelated reports whether two field paths under one root conflict: one is
// a prefix of the other. Moving `pair.a` conflicts with a use of `pair.a` itself
// when the paths are equal, of a field beneath it like `pair.a.id` when the move
// path is a prefix, and of the whole `pair` when the use path is a prefix, since the
// read would expose the moved field. A use of the disjoint sibling `pair.b` shares
// no prefix and does not conflict.
//
// A rest element's segment `1...` stands for every element from position 1 on, so it
// matches the element segment `1` or `2` and any other rest segment, and not `0`.
func pathPrefixRelated(a, b []placeSeg) bool {
	n := min(len(a), len(b))
	for i := range n {
		if !segmentsOverlap(a[i], b[i]) {
			return false
		}
	}
	return true
}

// segmentsOverlap reports whether two segments can name the same field or element. Equal
// segments do, and so do a rest segment `i...` and an element at position i or later.
func segmentsOverlap(a, b placeSeg) bool {
	if a == b {
		return true
	}
	aFrom, aRest := restSegmentStart(a)
	bFrom, bRest := restSegmentStart(b)
	switch {
	case aRest && bRest:
		return true
	case aRest && b.kind == namedSeg:
		j, isElement := canonicalIndex(b.name)
		return isElement && j >= aFrom
	case bRest && a.kind == namedSeg:
		j, isElement := canonicalIndex(a.name)
		return isElement && j >= bFrom
	}
	return false
}

// restSegmentStart returns the position a rest segment such as `1...` starts at, and false
// for any other segment.
func restSegmentStart(seg placeSeg) (int, bool) {
	if seg.kind != restSeg {
		return 0, false
	}
	return canonicalIndex(strings.TrimSuffix(seg.name, "..."))
}

// writePlaceSeg appends one segment of a place's path to b. A segment whose name is a valid
// identifier renders as `.name`. An element segment renders as `[0]`, the way `xs[0]` is
// written, and a rest segment renders as `[1...]`. Any other segment renders in bracket
// notation as `["name"]`.
func writePlaceSeg(b *strings.Builder, seg placeSeg) {
	_, isElement := canonicalIndex(seg.name)
	switch {
	case seg.kind == restSeg || isElement:
		b.WriteByte('[')
		b.WriteString(seg.name)
		b.WriteByte(']')
	case isDotPlaceSegment(seg.name):
		b.WriteByte('.')
		b.WriteString(seg.name)
	default:
		b.WriteByte('[')
		b.WriteString(strconv.Quote(seg.name))
		b.WriteByte(']')
	}
}

// elementStyle says how writePath renders a segment that names a tuple element.
type elementStyle uint8

const (
	// bracketElements renders an element the way writePlaceSeg does, as `[0]` or `[1...]`.
	bracketElements elementStyle = iota
	// dotElements renders an element in dot form, as `.0` or `.1...`.
	dotElements
)

// writePath appends each segment of path to b the way writePlaceSeg writes it, so a field
// whose name is not an identifier renders in bracket notation, as `obj["a.b"]` does. style
// says how a segment that names a tuple element renders. That is a position such as `0`, or
// a rest element's `1...`, which stands for every element from 1 on.
func writePath(b *strings.Builder, path []placeSeg, style elementStyle) {
	for _, seg := range path {
		_, isElement := canonicalIndex(strings.TrimSuffix(seg.name, "..."))
		if style == dotElements && isElement {
			b.WriteByte('.')
			b.WriteString(seg.name)
			continue
		}
		writePlaceSeg(b, seg)
	}
}

// isDotPlaceSegment reports whether a field name can be rendered with dot notation:
// a valid Escalier identifier, with a leading letter or underscore and
// letter/underscore/digit runes thereafter. Any other name, such as `a.b` or
// `foo-bar` from a constant-string index, renders in bracket notation instead.
func isDotPlaceSegment(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// patternPlaceWalk walks a pattern over the place its source value is read from, and passes
// each binding leaf the place that leaf reads. A field pattern reads the place of the object
// it is drawn from, extended by the field's name.
type patternPlaceWalk struct {
	// leaf receives each IdentPat or ObjShorthandPat that binds a name, with the place it
	// reads.
	leaf func(node ast.Node, at movePlace)
	// objectRest receives the name an object rest element `...rest` binds, with the place of
	// the object the rest is drawn from. A rest element whose pattern is not a name is walked
	// like any other pattern. objectRest may be nil when skipRests is set.
	objectRest func(ident *ast.IdentPat, at movePlace)
	// skipRests skips every rest element, in an object pattern and in a tuple pattern.
	skipRests bool
	// elementPlaces extends the place of each tuple or extractor element by its position. It
	// also walks the object of an instance pattern and the arguments of an extractor pattern.
	// When it is unset, each tuple element reads the tuple's own place, and an instance or
	// extractor pattern binds nothing.
	elementPlaces bool
	// tuple receives the place of each tuple pattern's value and the segment flatElements
	// gives each of its elements, when elementPlaces is set. A tuple pattern nested in a rest
	// element is not passed, since its elements belong to the enclosing tuple. It may be nil.
	tuple func(at movePlace, parts []placeSeg)
}

// walk passes each leaf pat binds the place it reads out of the place `at`.
func (w *patternPlaceWalk) walk(pat ast.Pat, at movePlace) {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		w.leaf(pat, at)
	case *ast.ObjectPat:
		for _, elem := range pat.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				w.leaf(e, extendPlace(at, e.Key.Name))
			case *ast.ObjKeyValuePat:
				w.walk(e.Value, extendPlace(at, e.Key.Name))
			case *ast.ObjRestPat:
				if w.skipRests {
					continue
				}
				if ident, ok := e.Pattern.(*ast.IdentPat); ok {
					w.objectRest(ident, at)
					continue
				}
				w.walk(e.Pattern, at)
			}
		}
	case *ast.TuplePat:
		if w.tuple != nil && w.elementPlaces {
			flat := flatElements(pat.Elems, 0)
			parts := make([]placeSeg, len(flat))
			for i, elem := range flat {
				parts[i] = elem.seg
			}
			w.tuple(at, parts)
		}
		w.elements(pat.Elems, at)
	case *ast.ExtractorPat:
		if w.elementPlaces {
			w.elements(pat.Args, at)
		}
	case *ast.InstancePat:
		if w.elementPlaces {
			w.walk(pat.Object, at)
		}
	case *ast.RestPat:
		if !w.skipRests {
			w.walk(pat.Pattern, at)
		}
	}
}

// elements walks the element patterns of a tuple or extractor pattern over the place `at`.
// With elementPlaces set, each element reads `at` extended by the segment flatElements
// gives it. Without it, every element reads `at`.
func (w *patternPlaceWalk) elements(elems []ast.Pat, at movePlace) {
	if !w.elementPlaces {
		for _, elem := range elems {
			w.walk(elem, at)
		}
		return
	}

	for _, elem := range flatElements(elems, 0) {
		if elem.inRest && w.skipRests {
			continue
		}
		w.walk(elem.pat, extendPlaceSeg(at, elem.seg))
	}
}

// patElement is one element of a tuple or extractor pattern that flatElements returns. pat
// is the pattern that binds the element, and seg is the segment naming the element's place
// within the value the pattern destructures. inRest marks an element written inside a rest
// element.
type patElement struct {
	pat    ast.Pat
	seg    placeSeg
	inRest bool
}

// flatElements returns the elements elems lists, the first of which sits at position from.
// Element i is named by the segment `i`. A rest element at position i whose pattern is a
// tuple pattern contributes that tuple's elements, from position i on, so `a` in
// `[_, ...[a]]` is element `1`. Any other rest element at position i is named by `i...`,
// which stands for every element from i on, and its pat is the rest's own pattern.
func flatElements(elems []ast.Pat, from int) []patElement {
	var out []patElement
	for i, elem := range elems {
		pos := from + i
		rest, isRest := elem.(*ast.RestPat)
		if !isRest {
			out = append(out, patElement{pat: elem, seg: fieldSeg(strconv.Itoa(pos))})
			continue
		}

		if tuple, ok := rest.Pattern.(*ast.TuplePat); ok {
			for _, inner := range flatElements(tuple.Elems, pos) {
				inner.inRest = true
				out = append(out, inner)
			}
			continue
		}

		out = append(out, patElement{pat: rest.Pattern, seg: placeSeg{kind: restSeg, name: strconv.Itoa(pos) + "..."}, inRest: true})
	}
	return out
}

// forEachRestField passes f each part an object rest binding of type t takes out of the
// place `at`. When t is an object type or a borrow of one, f receives the place of each
// field that type lists, the field's path within the binding, and the field's type.
// Otherwise f receives `at` itself, a nil path, and t.
func forEachRestField(t soltype.Type, at movePlace, f func(src movePlace, destPath []placeSeg, partT soltype.Type)) {
	obj, isObj := objectThroughBorrow(t)
	if !isObj {
		f(at, nil, t)
		return
	}
	for _, elem := range obj.Elems {
		if prop, isProp := elem.(*soltype.PropertyElem); isProp {
			f(extendPlace(at, prop.Name), []placeSeg{fieldSeg(prop.Name)}, prop.Type)
		}
	}
}

// objectThroughBorrow returns the object type t is, or the object type a RefType t wraps.
// ok is false when neither is an ObjectType.
func objectThroughBorrow(t soltype.Type) (*soltype.ObjectType, bool) {
	if r, isRef := t.(*soltype.RefType); isRef {
		t = r.Inner
	}
	obj, ok := t.(*soltype.ObjectType)
	return obj, ok
}

// pathHasPrefix reports whether `prefix` is a prefix of `full`: every segment of `prefix` matches
// `full` at the same index, so an empty prefix matches any path and equal paths match.
func pathHasPrefix(full, prefix []placeSeg) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		if prefix[i] != full[i] {
			return false
		}
	}
	return true
}

// appendPath returns `base` with the segments of `suffix` appended, copying so the result
// shares no backing storage with either input.
func appendPath(base, suffix []placeSeg) []placeSeg {
	out := make([]placeSeg, len(base)+len(suffix))
	copy(out, base)
	copy(out[len(base):], suffix)
	return out
}
