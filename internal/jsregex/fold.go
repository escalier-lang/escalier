package jsregex

import (
	"fmt"
	"regexp/syntax"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// fold.go makes a pattern with the `i` flag and without the `u` flag match the characters
// JavaScript matches, which Go's own `(?i)` does not do.
//
// JavaScript compares two characters under `i` by their uppercase forms. Go treats every
// character in a Unicode case orbit as equal, which is a wider rule. The Kelvin sign
// `\u212A` shares an orbit with `k` and `K` in Go, but its uppercase form is itself, so
// `/k/i` does not match it in JavaScript.
//
// So Compile never hands Go the `i` flag without `u`. It spells out every case JavaScript
// matches, and Go compiles the result as a case-sensitive pattern:
//
//  1. The rewriter translates the body to RE2 syntax. It folds and complements each negated
//     class on the spot with foldedComplement, since JavaScript folds a class's characters
//     before it takes the complement. `/[^a]/i` becomes `[^Aa]`. Folding after the
//     complement would match everything. The complement of `a` holds `A`, and folding `A`
//     adds `a` back.
//  2. Compile parses the result without `i` and passes the tree to foldTree.
//  3. foldTree replaces each literal rune with a class of its cases, and widens each class
//     to the cases of the runes it holds. `/ab/i` becomes `[Aa][Bb]` and `/[a-c]/i`
//     becomes `[A-Ca-c]`. `/k/i` becomes `[Kk]`, leaving out the Kelvin sign.
//  4. Compile prints the tree back to RE2 source for Go to compile.
//
// caseGroups holds the characters JavaScript treats as cases of one another, computed once
// from jsCanonical.

// jsCanonical returns the rune JavaScript compares r by when a pattern with the `i` flag and
// without the `u` flag matches it. That is r's full uppercase form, read with upper. r
// compares as itself when that form is more than one UTF-16 code unit, or when it is ASCII
// and r is not. upper is not safe for concurrent use.
//
// Go's case folding is wider. It puts the Kelvin sign `K` with `k`, but the Kelvin sign is its
// own uppercase form, so `/k/i` does not match it in JavaScript. It also puts `ᾀ` with `ᾈ`,
// but the full uppercase form of each is the two characters `ἈΙ`, so `/ᾀ/i` does not match
// `ᾈ` either.
func jsCanonical(upper cases.Caser, r rune) rune {
	s := upper.String(string(r))
	u, size := utf8.DecodeRuneInString(s)
	if size != len(s) || u > 0xFFFF {
		return r
	}
	if r >= 0x80 && u < 0x80 {
		return r
	}
	return u
}

// caseGroups returns, for each rune that has another case, the runes with its jsCanonical
// form, itself included. The second result lists those runes in order.
var caseGroups = sync.OnceValues(func() (map[rune][]rune, []rune) {
	upper := cases.Upper(language.Und)
	groups := map[rune][]rune{}
	var cased []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.SimpleFold(r) == r {
			continue
		}
		group := []rune{r}
		for m := unicode.SimpleFold(r); m != r; m = unicode.SimpleFold(m) {
			if jsCanonical(upper, m) == jsCanonical(upper, r) {
				group = append(group, m)
			}
		}
		if len(group) > 1 {
			groups[r] = group
			cased = append(cased, r)
		}
	}
	return groups, cased
})

// foldTree rewrites re in place to match every case of each character it matches, the way a
// JavaScript pattern with the `i` flag and without the `u` flag does. re has to be parsed
// without the `i` flag, and every negated class in it has to be folded and complemented
// already.
func foldTree(re *syntax.Regexp) {
	switch re.Op {
	case syntax.OpLiteral:
		flags := re.Flags &^ syntax.FoldCase
		subs := make([]*syntax.Regexp, 0, len(re.Rune))
		for _, r := range re.Rune {
			subs = append(subs, &syntax.Regexp{Op: syntax.OpCharClass, Flags: flags, Rune: foldRanges(literalRanges(r, re.Flags))})
		}
		re.Op, re.Flags, re.Rune, re.Sub = syntax.OpConcat, flags, nil, subs
		return
	case syntax.OpCharClass:
		re.Rune = foldRanges(re.Rune)
	}
	for _, sub := range re.Sub {
		foldTree(sub)
	}
}

// foldedComplement returns an RE2 class matching what the negated JavaScript class `[^…]`
// matches under the `i` flag without the `u` flag. class is the RE2 source of the class
// without its `^`. JavaScript folds the class's characters and then takes the complement, so
// `[^a]` matches neither `a` nor `A`. It returns the error RE2 reports when class does not
// parse.
func foldedComplement(class string) (string, error) {
	re, err := syntax.Parse(class, syntax.Perl)
	if err != nil {
		return "", err
	}
	var pairs []rune
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			pairs = append(pairs, literalRanges(r, re.Flags)...)
		}
	case syntax.OpAnyChar:
		pairs = []rune{0, unicode.MaxRune}
	default:
		pairs = re.Rune
	}
	return spellClass(complementRanges(foldRanges(pairs))), nil
}

// foldRanges returns the rune ranges in pairs, each a low and a high bound, widened to every
// case of the runes they hold, the way a JavaScript pattern with the `i` flag and without
// the `u` flag reads them.
func foldRanges(pairs []rune) []rune {
	groups, cased := caseGroups()
	out := slices.Clone(pairs)
	for j := 0; j+1 < len(pairs); j += 2 {
		lo, hi := pairs[j], pairs[j+1]
		for k := sort.Search(len(cased), func(k int) bool { return cased[k] >= lo }); k < len(cased) && cased[k] <= hi; k++ {
			out = append(out, pairsOf(groups[cased[k]])...)
		}
	}
	return mergeRanges(out)
}

// complementRanges returns the ranges of the runes the merged ranges in pairs leave out.
func complementRanges(pairs []rune) []rune {
	var out []rune
	next := rune(0)
	for j := 0; j+1 < len(pairs); j += 2 {
		if pairs[j] > next {
			out = append(out, next, pairs[j]-1)
		}
		next = pairs[j+1] + 1
	}
	if next <= unicode.MaxRune {
		out = append(out, next, unicode.MaxRune)
	}
	return out
}

// spellClass returns the RE2 source of a class matching the ranges in pairs.
func spellClass(pairs []rune) string {
	if len(pairs) == 0 {
		return emptyClass
	}
	var b strings.Builder
	b.WriteByte('[')
	for j := 0; j+1 < len(pairs); j += 2 {
		fmt.Fprintf(&b, `\x{%x}-\x{%x}`, pairs[j], pairs[j+1])
	}
	b.WriteByte(']')
	return b.String()
}

// literalRanges returns the runes a literal r parsed with flags matches, as merged ranges.
// Go's parser turns a class holding exactly two cases of one character, such as `[ßẞ]`,
// into a literal with the FoldCase flag, which matches both.
func literalRanges(r rune, flags syntax.Flags) []rune {
	runes := []rune{r}
	if flags&syntax.FoldCase != 0 {
		for m := unicode.SimpleFold(r); m != r; m = unicode.SimpleFold(m) {
			runes = append(runes, m)
		}
	}
	return mergeRanges(pairsOf(runes))
}

// pairsOf returns a one-rune range for each rune of runes.
func pairsOf(runes []rune) []rune {
	out := make([]rune, 0, 2*len(runes))
	for _, r := range runes {
		out = append(out, r, r)
	}
	return out
}

// mergeRanges sorts the rune ranges in pairs, each a low and a high bound, and joins the
// ones that overlap or touch.
func mergeRanges(pairs []rune) []rune {
	type rng struct{ lo, hi rune }
	rs := make([]rng, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		rs = append(rs, rng{pairs[i], pairs[i+1]})
	}
	slices.SortFunc(rs, func(a, b rng) int { return int(a.lo - b.lo) })
	out := make([]rune, 0, len(pairs))
	for _, r := range rs {
		if last := len(out) - 1; last > 0 && r.lo <= out[last]+1 {
			out[last] = max(out[last], r.hi)
			continue
		}
		out = append(out, r.lo, r.hi)
	}
	return out
}
