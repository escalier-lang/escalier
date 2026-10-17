package jsregex

import (
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/escalier-lang/escalier/internal/set"
)

// Shadowed is an alternative that can never take part in a match, because an alternative
// before it in the same alternation always matches first. JavaScript tries alternatives left
// to right and keeps the first that leads to a whole match, so in `/^(a|ab).*$/` the `a`
// always wins and `ab` is never tried to the end.
type Shadowed struct {
	// Start and End are the alternative's byte offsets in the literal's source. The `ab` of
	// `/^(a|ab).*$/` spans 5 to 7.
	Start, End int
	// Text is the alternative's source.
	Text string
	// By is the earlier alternative that always matches first. It is empty when no single
	// earlier alternative does and only the earlier alternatives together cover this one.
	By string
}

// maxProductStates caps the pairs of DFA states one containment check explores. A check
// that reaches the cap gives up, and the alternative it was checking is not reported.
const maxProductStates = 4096

// Shadowed returns the alternatives of the pattern that can never take part in a match, in
// source order.
//
// An alternative counts as shadowed only when the check below proves it. Patterns the check
// cannot read report nothing. Those are a pattern with the `i` flag, an alternation that a
// quantifier repeats, and an alternative whose match depends on the text around it.
func (p *Pattern) Shadowed() []Shadowed {
	p.shadowOnce.Do(func() { p.shadowed = p.findShadowed() })
	return p.shadowed
}

// findShadowed computes what Shadowed returns.
//
// An alternative A_k of an alternation `A_0|…|A_n` is shadowed when every string that A_k
// followed by the rest of the pattern can match is also matched by some earlier A_j followed
// by the rest. The rest is the continuation: the source after the alternation up to the end
// of each enclosing branch. The engine reaches the alternation at some position and tries
// A_0 first, then A_1, and so on. It backtracks into A_k only when every earlier alternative
// fails to finish the match from that position, which the containment rules out.
func (p *Pattern) findShadowed() []Shadowed {
	if strings.ContainsRune(p.flags, 'i') {
		return nil
	}
	var out []Shadowed
	for _, g := range parseAlternations(p.body) {
		if len(g.branches) < 2 || g.repeated() {
			continue
		}
		cont := g.continuation(p.body)
		for k := 1; k < len(g.branches); k++ {
			alt := g.branches[k].text(p.body)
			if found, ok := p.shadowedBy(alt, g.branches[:k], cont); ok {
				out = append(out, Shadowed{
					Start: 1 + g.branches[k].start,
					End:   1 + g.branches[k].end,
					Text:  alt,
					By:    found,
				})
			}
		}
	}
	slices.SortFunc(out, func(a, b Shadowed) int { return a.Start - b.Start })
	return out
}

// shadowedBy reports whether alt followed by cont is covered by the earlier alternatives
// followed by cont. It returns the one earlier alternative that covers alt alone, or "" when
// only their union does.
func (p *Pattern) shadowedBy(alt string, earlier []span, cont string) (string, bool) {
	texts := make([]string, len(earlier))
	for i, e := range earlier {
		texts[i] = e.text(p.body)
	}
	for _, t := range append([]string{alt, cont}, texts...) {
		if p.readsPosition(t) {
			return "", false
		}
	}
	sub, ok := p.language(alt, cont)
	if !ok {
		return "", false
	}
	for _, t := range texts {
		if sup, ok := p.language(t, cont); ok && contains(sup, sub) {
			return t, true
		}
	}
	if len(texts) < 2 {
		return "", false
	}
	sup, ok := p.language(strings.Join(texts, "|"), cont)
	if ok && contains(sup, sub) {
		return "", true
	}
	return "", false
}

// readsPosition reports whether src tests where in the string it sits, which a language of
// src read from the alternation's position cannot see. `^` tests for the start of the
// string, `\b` and `\B` test the character before, and `$` under the `m` flag tests for a
// line terminator.
func (p *Pattern) readsPosition(src string) bool {
	multiline := strings.ContainsRune(p.flags, 'm')
	reads := false
	forEachToken(src, func(_ int, tok string, inClass bool) {
		if inClass {
			return
		}
		switch tok {
		case "^", `\b`, `\B`:
			reads = true
		case "$":
			reads = reads || multiline
		}
	})
	return reads
}

// language compiles alt followed by cont into the program the containment check walks. It
// reports false when the source does not compile.
func (p *Pattern) language(alt, cont string) (*syntax.Prog, bool) {
	flags := strings.Map(func(r rune) rune {
		if strings.ContainsRune("msu", r) {
			return r
		}
		return -1
	}, p.flags)
	compiled, _ := Compile("/(?:" + alt + ")(?:" + cont + ")/" + flags)
	if compiled == nil {
		return nil, false
	}
	re, err := syntax.Parse(compiled.re.String(), syntax.Perl)
	if err != nil {
		return nil, false
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return nil, false
	}
	return prog, true
}

// span is a half-open range of byte offsets in a regex body.
type span struct{ start, end int }

func (s span) text(body string) string { return body[s.start:s.end] }

// altGroup is a group of a regex body, or the whole body, split into the branches its
// top-level `|` separates.
type altGroup struct {
	// open is the offset of the group's `(`, and -1 for the whole body.
	open int
	// end is the offset of the group's `)`, and the body's length for the whole body.
	end      int
	branches []span
	parent   *altGroup
	// quantified reports whether a quantifier follows the group's `)`.
	quantified bool
	// branchStart is where the branch being read starts, used only while parsing.
	branchStart int
}

// repeated reports whether a quantifier applies to g or to a group around it.
func (g *altGroup) repeated() bool {
	for x := g; x != nil; x = x.parent {
		if x.quantified {
			return true
		}
	}
	return false
}

// continuation returns the source that follows g up to the end of the pattern: the rest of
// the branch around g, then the rest of each enclosing branch in turn. repeated must be
// false, since a quantifier would repeat part of what follows.
func (g *altGroup) continuation(body string) string {
	var b strings.Builder
	for x := g; x.parent != nil; x = x.parent {
		b.WriteString(body[x.end+1 : x.parent.branchAt(x.open).end])
	}
	return b.String()
}

// branchAt returns the branch of g that holds the offset at.
func (g *altGroup) branchAt(at int) span {
	for _, br := range g.branches {
		if br.start <= at && at < br.end {
			return br
		}
	}
	return g.branches[len(g.branches)-1]
}

// parseAlternations returns every group of body and the whole body as its first entry, each
// split into its branches.
func parseAlternations(body string) []*altGroup {
	root := &altGroup{open: -1}
	groups := []*altGroup{root}
	cur := root
	forEachToken(body, func(i int, tok string, inClass bool) {
		if inClass {
			return
		}
		switch tok {
		case "(":
			start := i + 1
			if rest := body[i:]; strings.HasPrefix(rest, "(?:") {
				start = i + len("(?:")
			} else if strings.HasPrefix(rest, "(?<") {
				start = i + strings.IndexByte(rest, '>') + 1
			}
			g := &altGroup{open: i, parent: cur, branchStart: start}
			groups = append(groups, g)
			cur = g
		case "|":
			cur.branches = append(cur.branches, span{cur.branchStart, i})
			cur.branchStart = i + 1
		case ")":
			if cur.parent == nil {
				return
			}
			cur.branches = append(cur.branches, span{cur.branchStart, i})
			cur.end = i
			cur.quantified = i+1 < len(body) && strings.IndexByte("*+?{", body[i+1]) >= 0
			cur = cur.parent
		}
	})
	root.branches = append(root.branches, span{root.branchStart, len(body)})
	root.end = len(body)
	return groups
}

// contains reports whether every string sub matches from the start, or has a prefix that
// sub matches, also has a prefix that sup matches. A whole-string test passes when any such
// prefix reaches the end of the pattern, so these are the strings that let the match finish.
// It reports false when the check reaches maxProductStates.
//
// It walks the two programs' DFAs together. A DFA state is the set of instructions the NFA
// can be at after reading the same input. Characters are read one class at a time, where a
// class is a range of runes every instruction treats alike.
func contains(sup, sub *syntax.Prog) bool {
	reps := runeClasses(sup, sub)
	type node struct {
		sub, sup         []uint32
		subDone, supDone bool
	}
	keyOf := func(n node) string {
		return pcsKey(n.sub) + "|" + pcsKey(n.sup) + "|" + strconv.FormatBool(n.subDone) + strconv.FormatBool(n.supDone)
	}
	seen := set.NewSet[string]()
	queue := []node{{sub: []uint32{uint32(sub.Start)}, sup: []uint32{uint32(sup.Start)}}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		key := keyOf(n)
		if seen.Contains(key) {
			continue
		}
		if seen.Len() >= maxProductStates {
			return false
		}
		seen.Add(key)
		subC, ok := closure(sub, n.sub)
		if !ok {
			return false
		}
		supC, ok := closure(sup, n.sup)
		if !ok {
			return false
		}
		subAccepts := n.subDone || subC.match || subC.matchAtEnd
		supAccepts := n.supDone || supC.match || supC.matchAtEnd
		if subAccepts && !supAccepts {
			return false
		}
		supDone := n.supDone || supC.match
		subDone := n.subDone || subC.match
		// Once sup has matched a prefix, every longer string has that prefix too.
		if supDone {
			continue
		}
		// Once sub can neither match nor read on, no longer string is one of its.
		if len(subC.states) == 0 && !subDone {
			continue
		}
		for _, r := range reps {
			queue = append(queue, node{
				sub:     step(sub, subC.states, r),
				sup:     step(sup, supC.states, r),
				subDone: subDone,
				supDone: supDone,
			})
		}
	}
	return true
}

// closed is the result of following every instruction that reads no input.
type closed struct {
	// states holds the instructions that read a rune, sorted.
	states []uint32
	// match reports whether the program can match here.
	match bool
	// matchAtEnd reports whether it can match here when no input follows, through a `$`.
	matchAtEnd bool
}

// closure follows from pcs every instruction that reads no input. It reports false on an
// empty-width test other than the end of the text, which the check does not model.
func closure(prog *syntax.Prog, pcs []uint32) (closed, bool) {
	var out closed
	states := set.NewSet[uint32]()
	for _, atEnd := range []bool{false, true} {
		seen := set.NewSet[uint32]()
		stack := slices.Clone(pcs)
		for len(stack) > 0 {
			pc := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen.Contains(pc) {
				continue
			}
			seen.Add(pc)
			inst := prog.Inst[pc]
			switch inst.Op {
			case syntax.InstAlt, syntax.InstAltMatch:
				stack = append(stack, inst.Out, inst.Arg)
			case syntax.InstCapture, syntax.InstNop:
				stack = append(stack, inst.Out)
			case syntax.InstEmptyWidth:
				if syntax.EmptyOp(inst.Arg) != syntax.EmptyEndText {
					return closed{}, false
				}
				if atEnd {
					stack = append(stack, inst.Out)
				}
			case syntax.InstMatch:
				if atEnd {
					out.matchAtEnd = true
				} else {
					out.match = true
				}
			case syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
				if !atEnd {
					states.Add(pc)
				}
			}
		}
	}
	out.states = states.ToSlice()
	slices.Sort(out.states)
	return out, true
}

// step returns the instructions that follow those of states that read r.
func step(prog *syntax.Prog, states []uint32, r rune) []uint32 {
	next := set.NewSet[uint32]()
	for _, pc := range states {
		inst := prog.Inst[pc]
		if readsRune(&inst, r) {
			next.Add(inst.Out)
		}
	}
	out := next.ToSlice()
	slices.Sort(out)
	return out
}

// readsRune reports whether the rune-reading instruction inst reads r.
func readsRune(inst *syntax.Inst, r rune) bool {
	switch inst.Op {
	case syntax.InstRuneAny:
		return true
	case syntax.InstRuneAnyNotNL:
		return r != '\n'
	default:
		return inst.MatchRune(r)
	}
}

// runeClasses returns one rune from each range of runes that every instruction of progs
// treats alike, so reading those runes covers every distinct way the programs can step.
func runeClasses(progs ...*syntax.Prog) []rune {
	bounds := set.FromSlice([]rune{0, '\n', '\n' + 1})
	for _, prog := range progs {
		for _, inst := range prog.Inst {
			switch inst.Op {
			case syntax.InstRune1:
				bounds.Add(inst.Rune[0])
				bounds.Add(inst.Rune[0] + 1)
			case syntax.InstRune:
				if len(inst.Rune) == 1 {
					bounds.Add(inst.Rune[0])
					bounds.Add(inst.Rune[0] + 1)
					continue
				}
				for i := 0; i+1 < len(inst.Rune); i += 2 {
					bounds.Add(inst.Rune[i])
					bounds.Add(inst.Rune[i+1] + 1)
				}
			}
		}
	}
	out := slices.DeleteFunc(bounds.ToSlice(), func(r rune) bool { return r > unicode.MaxRune })
	slices.Sort(out)
	return out
}

// pcsKey spells a sorted list of instructions as a map key.
func pcsKey(pcs []uint32) string {
	var b strings.Builder
	for i, pc := range pcs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(uint64(pc), 10))
	}
	return b.String()
}
