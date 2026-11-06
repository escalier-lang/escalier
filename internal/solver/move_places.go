package solver

import (
	"fmt"
	"math"
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
	switch e := e.(type) {
	case *ast.IdentExpr:
		if c.varIDs.Of(e) <= 0 {
			return movePlace{}, false, false
		}
		return movePlace{root: c.varIDs.Of(e)}, false, true
	case *ast.MemberExpr:
		if e.Prop == nil || e.Prop.Name == "" {
			return movePlace{}, false, false
		}
		base, dynamic, ok := c.chainPlace(e.Object)
		if !ok {
			return movePlace{}, false, false
		}
		if dynamic {
			return base, true, true
		}
		return extendPlace(base, e.Prop.Name), false, true
	case *ast.IndexExpr:
		base, dynamic, ok := c.chainPlace(e.Object)
		if !ok {
			return movePlace{}, false, false
		}
		if dynamic {
			return base, true, true
		}
		if name, ok := c.indexSegment(e.Index); ok {
			return extendPlace(base, name), false, true
		}
		// A dynamic index is approximated by its container.
		return base, true, true
	}
	return movePlace{}, false, false
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

// extendPlace returns base with one more named field segment appended, copying the
// path so sibling places built from the same base never share backing storage.
func extendPlace(base movePlace, field string) movePlace {
	return extendPlaceSeg(base, placeSeg{kind: namedSeg, name: field})
}

// extendPlaceSeg returns base with seg appended, copying the path as extendPlace does.
func extendPlaceSeg(base movePlace, seg placeSeg) movePlace {
	path := make([]placeSeg, len(base.path)+1)
	copy(path, base.path)
	path[len(base.path)] = seg
	return movePlace{root: base.root, path: path}
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
			out = append(out, patElement{pat: elem, seg: placeSeg{kind: namedSeg, name: strconv.Itoa(pos)}})
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
			f(extendPlace(at, prop.Name), appendSeg(nil, prop.Name), prop.Type)
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
