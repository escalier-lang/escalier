package solver

import (
	"fmt"
	"iter"
	"sync/atomic"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// provTable is a checker's Prov. It holds the entries this checker records, layered
// over the table of the checker it was forked from. A lookup reads this layer first
// and then each parent in turn, so a fork starts with every entry its parent holds
// without copying them.
//
// A table with a child is frozen. Its entries are shared with every child, and two
// children can run concurrently, so set panics on a frozen table rather than let one
// child's write reach another.
type provTable struct {
	own    Prov
	parent *provTable
	frozen atomic.Bool
}

func newProvTable() *provTable {
	return &provTable{own: Prov{}}
}

// child returns an empty table layered over p, and freezes p.
//
// It writes p only when p is not yet frozen. A table children are taken from
// concurrently must be frozen before it is shared, so those calls only read it.
func (p *provTable) child() *provTable {
	if !p.frozen.Load() {
		p.frozen.Store(true)
	}
	return &provTable{own: Prov{}, parent: p}
}

// freeze marks p as shared, so a later set panics.
func (p *provTable) freeze() {
	p.frozen.Store(true)
}

// get returns t's origin from the nearest layer holding one.
func (p *provTable) get(t soltype.Type) (Origin, bool) {
	for layer := p; layer != nil; layer = layer.parent {
		if o, ok := layer.own[t]; ok {
			return o, true
		}
	}
	return nil, false
}

// set records o as t's origin in p's own layer. It panics when p is frozen.
func (p *provTable) set(t soltype.Type, o Origin) {
	if p.frozen.Load() {
		panic(fmt.Sprintf("provTable: write to a frozen table for %p", t))
	}
	p.own[t] = o
}

// restore puts t's own-layer entry back to prev, or removes it when had is false. It
// undoes a set, so it reads and writes only p's own layer.
func (p *provTable) restore(t soltype.Type, prev Origin, had bool) {
	if had {
		p.own[t] = prev
	} else {
		delete(p.own, t)
	}
}

// NodeFor returns the AST node that minted t, when one was recorded.
func (p *provTable) NodeFor(t soltype.Type) (ast.Node, bool) {
	o, _ := p.get(t)
	if o, ok := o.(FromAST); ok {
		return o.Node, true
	}
	return nil, false
}

// all yields each type with an origin and the origin a lookup returns for it. A type
// recorded in more than one layer is yielded once, with the nearest layer's origin.
func (p *provTable) all() iter.Seq2[soltype.Type, Origin] {
	return func(yield func(soltype.Type, Origin) bool) {
		for layer := p; layer != nil; layer = layer.parent {
			for t, o := range layer.own {
				if shadowed(p, layer, t) {
					continue
				}
				if !yield(t, o) {
					return
				}
			}
		}
	}
}

// shadowed reports whether a layer from top down to layer, excluding layer, holds t.
func shadowed(top, layer *provTable, t soltype.Type) bool {
	for l := top; l != layer; l = l.parent {
		if _, ok := l.own[t]; ok {
			return true
		}
	}
	return false
}

// size returns the number of types all yields.
func (p *provTable) size() int {
	n := 0
	for range p.all() {
		n++
	}
	return n
}
