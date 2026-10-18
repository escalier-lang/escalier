package jsregex

import (
	"regexp/syntax"
	"slices"
	"sort"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

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

// foldLikeJS widens each rune-reading instruction of prog to read every case of the runes it
// reads, the way a JavaScript pattern with the `i` flag and without the `u` flag does. prog
// has to be compiled without the `i` flag.
func foldLikeJS(prog *syntax.Prog) {
	groups, cased := caseGroups()
	for i := range prog.Inst {
		inst := &prog.Inst[i]
		var pairs []rune
		switch {
		case inst.Op == syntax.InstRune1, inst.Op == syntax.InstRune && len(inst.Rune) == 1:
			pairs = []rune{inst.Rune[0], inst.Rune[0]}
		case inst.Op == syntax.InstRune:
			pairs = slices.Clone(inst.Rune)
		default:
			continue
		}
		n := len(pairs)
		for j := 0; j < n; j += 2 {
			lo, hi := pairs[j], pairs[j+1]
			for k := sort.Search(len(cased), func(k int) bool { return cased[k] >= lo }); k < len(cased) && cased[k] <= hi; k++ {
				for _, m := range groups[cased[k]] {
					pairs = append(pairs, m, m)
				}
			}
		}
		inst.Op = syntax.InstRune
		inst.Rune = mergeRanges(pairs)
		inst.Arg = 0
	}
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
