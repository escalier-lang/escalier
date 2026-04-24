package liveness

import "slices"

// AliasMutability tracks whether a variable holds a mutable or immutable
// reference within an alias set.
type AliasMutability int

const (
	AliasImmutable AliasMutability = iota
	AliasMutable
)

// SetID uniquely identifies an alias set within an AliasTracker.
type SetID int

// AliasSet tracks a group of variables that reference the same underlying
// value. Each value created at runtime gets its own AliasSet. Variables
// join an alias set when assigned from another variable in the set.
type AliasSet struct {
	ID       SetID
	Members  map[VarID]AliasMutability // variable → whether it holds a mut ref
	Origin   VarID                     // the variable that created the value
	IsStatic bool                      // true if this value has 'static lifetime
}

// AliasTracker manages alias sets for a function body.
// A variable may belong to multiple alias sets when assigned from different
// values depending on control flow (conditional aliasing, Phase 7.4).
type AliasTracker struct {
	nextID    SetID
	Sets      map[SetID]*AliasSet // SetID → AliasSet
	VarToSets map[VarID][]SetID   // variable → which alias sets it belongs to
}

// NewAliasTracker creates a new empty AliasTracker.
func NewAliasTracker() *AliasTracker {
	return &AliasTracker{
		nextID:    0,
		Sets:      make(map[SetID]*AliasSet),
		VarToSets: make(map[VarID][]SetID),
	}
}

// NewValue creates a fresh alias set for a newly created value (e.g. a
// literal, constructor call, or function returning a fresh value).
//
// Example:
//
//	val items: mut Array<number> = [1, 2, 3]  // NewValue(items, AliasMutable)
//	                                          // → creates set {items(mut)}, origin=items
func (a *AliasTracker) NewValue(v VarID, mut AliasMutability) {
	a.nextID++
	id := a.nextID
	set := &AliasSet{
		ID:      id,
		Members: map[VarID]AliasMutability{v: mut},
		Origin:  v,
	}
	a.Sets[id] = set
	a.VarToSets[v] = append(a.VarToSets[v], id)
}

// AddAlias adds a variable to the alias set of another variable.
//
// Example:
//
//	val items: mut Array<number> = [1, 2, 3]      // set: {items(mut)}
//	val alias: Array<number> = items              // AddAlias(alias, items, AliasImmutable)
//	                                              // → set: {items(mut), alias(imm)}
func (a *AliasTracker) AddAlias(target VarID, source VarID, mut AliasMutability) {
	for _, setID := range a.VarToSets[source] {
		set := a.Sets[setID]
		set.Members[target] = mut
		// Only append if target doesn't already belong to this set,
		// avoiding duplicate entries from overlapping alias sources.
		if !slices.Contains(a.VarToSets[target], setID) {
			a.VarToSets[target] = append(a.VarToSets[target], setID)
		}
	}
}

// Reassign removes a variable from its current alias sets and either
// adds it to the alias sets of newSource (if non-nil) or creates a
// fresh alias set (if newSource is nil, meaning assigned a fresh value).
//
// Example (reassign to existing source):
//
//	val a: mut Array<number> = [1, 2]      // set1: {a(mut)}
//	val b: mut Array<number> = [3, 4]      // set2: {b(mut)}
//	var x: Array<number> = a               // set1: {a(mut), x(imm)}
//	x = b                                  // Reassign(x, &b, AliasImmutable)
//	                                       // → set1: {a(mut)}, set2: {b(mut), x(imm)}
//
// Example (reassign to fresh value):
//
//	var x: Array<number> = a               // set1: {a(mut), x(imm)}
//	x = [5, 6]                             // Reassign(x, nil, AliasImmutable)
//	                                       // → set1: {a(mut)}, set3: {x(imm)}
func (a *AliasTracker) Reassign(v VarID, newSource *VarID, mut AliasMutability) {
	// Remove from current alias sets
	for _, setID := range a.VarToSets[v] {
		if set, ok := a.Sets[setID]; ok {
			delete(set.Members, v)
		}
	}
	a.VarToSets[v] = nil

	if newSource != nil {
		// Add to the source's alias sets
		a.AddAlias(v, *newSource, mut)
	} else {
		// Create a fresh alias set
		a.NewValue(v, mut)
	}
}

// ReassignMulti removes a variable from its current alias sets and adds
// it to the alias sets of all provided sources. This is used for conditional
// aliasing where a variable may alias one of several sources.
func (a *AliasTracker) ReassignMulti(v VarID, sources []VarID, mut AliasMutability) {
	// Remove from current alias sets
	for _, setID := range a.VarToSets[v] {
		if set, ok := a.Sets[setID]; ok {
			delete(set.Members, v)
		}
	}
	a.VarToSets[v] = nil

	for _, source := range sources {
		a.AddAlias(v, source, mut)
	}
}

// MergeAliasSets merges the alias sets of two variables into a single
// set. All members of both sets become members of the merged set, and
// VarToSets is updated so every affected variable points to the merged
// set. The second set is removed from Sets.
//
// Example (linked list construction):
//
//	val head: mut Node = Node {}     // set1: {head(mut)}
//	var current: mut Node = head     // set1: {head(mut), current(mut)}
//	val next: mut Node = Node {}     // set2: {next(mut)}
//	current.next = next              // MergeAliasSets(current, next)
//	                                 // → set1: {head(mut), current(mut), next(mut)}
//	current = next                   // Reassign(current, &next, AliasMutable)
//	                                 // → set1: {head(mut), next(mut), current(mut)}
func (a *AliasTracker) MergeAliasSets(v1 VarID, v2 VarID) {
	sets1 := a.VarToSets[v1]
	sets2 := a.VarToSets[v2]
	if len(sets1) == 0 || len(sets2) == 0 {
		return
	}

	// Use the first set of v1 as the target. When v1 belongs to multiple
	// sets (e.g. from conditional aliasing: `let x = if cond { a } else { b }`
	// followed by `obj.prop = x`), we pick an arbitrary set as the merge
	// target. This is sufficient because callers like the transition checker
	// iterate all alias sets of a variable via GetAliasSets — the merged
	// set will be discovered regardless of which one we chose as the target.
	targetID := sets1[0]
	target := a.Sets[targetID]

	// Merge all sets of v2 into the target
	for _, setID := range sets2 {
		if setID == targetID {
			continue
		}
		source := a.Sets[setID]
		if source == nil {
			continue
		}
		for member, mut := range source.Members {
			target.Members[member] = mut
			// Update VarToSets: replace setID with targetID, deduplicating
			// to avoid accumulating multiple entries for the same set when
			// a member already belongs to targetID (e.g. from a prior merge).
			seen := make(map[SetID]bool)
			newSets := make([]SetID, 0, len(a.VarToSets[member]))
			for _, sid := range a.VarToSets[member] {
				id := sid
				if id == setID {
					id = targetID
				}
				if !seen[id] {
					seen[id] = true
					newSets = append(newSets, id)
				}
			}
			a.VarToSets[member] = newSets
		}
		delete(a.Sets, setID)
	}
}

// GetAliasSets returns all alias sets that v belongs to. A variable may
// belong to multiple sets due to conditional aliasing (Phase 7.4).
//
// Example (conditional aliasing):
//
//	val a: mut Array<number> = [1, 2]              // set1: {a(mut)}
//	val b: mut Array<number> = [3, 4]              // set2: {b(mut)}
//	val x: Array<number> = if cond { a } else { b }  // set1: {a(mut), x(imm)}, set2: {b(mut), x(imm)}
//	GetAliasSets(x)                                   // → [set1, set2]
func (a *AliasTracker) GetAliasSets(v VarID) []*AliasSet {
	setIDs := a.VarToSets[v]
	result := make([]*AliasSet, 0, len(setIDs))
	for _, id := range setIDs {
		if set, ok := a.Sets[id]; ok {
			result = append(result, set)
		}
	}
	return result
}
