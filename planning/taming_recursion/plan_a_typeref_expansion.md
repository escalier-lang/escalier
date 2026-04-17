# Plan A: Expand at the TypeRefType match site, not in a catch-all retry

## Goal

Replace the catch-all retry loop at the bottom of `unifyPruned` (lines 1189-1203)
with explicit `TypeRefType` handling. This makes expansion predictable: it only
happens for the type kind that needs it, and only once per unification call.

## Background

Currently, when `unifyPruned` fails to match any explicit case, it falls through to
a retry loop that calls `ExpandType(ctx, t, 1)` on both sides. If either side
expanded (detected by pointer inequality), it recurses into `unifyWithDepth` with
`depth + 1`. This has two problems:

1. `ExpandType` always allocates new objects, so `expandedT1 != t1` is always true
   for expandable types, even when the result is structurally identical.
2. Non-`TypeRefType` types can also trigger expansion (e.g. `CondType`, `KeyOfType`,
   `IndexType`), but these are already handled in their own cases within
   `ExpandType`'s visitor. The retry loop re-expands types that don't need it.

## Analysis of what reaches the retry loop

The retry loop is reached when neither `t1` nor `t2` matches any of the explicit
cases in `unifyPruned`. After reviewing all cases, the scenarios that fall through
to the retry are:

1. **TypeRefType + TypeRefType (different alias)** — Lines 506-510 have a TODO and
   do nothing, so these fall through.
2. **TypeRefType + any non-TypeRef concrete type** — e.g. `TypeRefType` vs
   `ObjectType`, `PrimType`, `LitType`, `UnionType`, etc. There is no case that
   handles "one side is a TypeRefType, the other is something else."
3. **TypeOfType + any type** — `TypeOfType` (e.g. `typeof obj`) has no explicit case
   in `unifyPruned` and relies on the retry loop's `ExpandType` to resolve it.
4. **Unrecognized type combinations** — Any pair of types that doesn't match the
   listed cases (e.g. `NullType` + `ObjectType`). These should be genuine
   unification errors.

In cases 1-3, the retry loop expands the types and retries. In case 4, expansion
doesn't change either type, so the retry doesn't fire and the function falls through
to the final `CannotUnifyTypesError`.

## Discovered issues from prototyping

### Issue 1: `expandTypeRef` fails on non-alias TypeRefTypes

`expandTypeRef` (expand_type.go:1617) returns an `UnknownTypeError` when the
TypeRefType refers to something that isn't a type alias (e.g. enum variant `RGB`).
The old `ExpandType` also errored in this case but returned `NeverType`, which
unifies with everything. A new `tryExpandTypeRef` helper is needed that returns
`(type, bool)` instead — returning `(nil, false)` when resolution fails, so the
caller can fall through gracefully.

### Issue 2: Stack overflow from recursive `unifyWithDepth` calls

Calling `unifyWithDepth(ctx, expandedT1, t2, depth+1)` for each TypeRefType
expansion adds a full `unifyWithDepth` → `unifyPruned` frame to the call stack.
For types with many properties (React SVG elements: 200+ properties), each property
may be a TypeRefType that triggers another expansion round. Since property-by-property
unification uses `c.Unify()` (which resets depth to 0), the depth counter cannot
prevent stack overflow — the stack overflows before `maxUnifyDepth` is reached.

**Fix**: Use an in-place expansion loop instead of recursive calls. See the plan
below for the structure that avoids adding indentation to the existing case-matching
code.

### Issue 3: TypeOfType also relies on the retry loop

`TypeOfType` (e.g. `typeof obj`) has no explicit case in `unifyPruned`. The retry
loop expands it via `ExpandType`, which resolves the `typeof` expression to the
value's type. Without the retry loop, `ObjectType` vs `TypeOfType` (found in the
getter/setter tests) fails with `CannotUnifyTypesError`.

**Fix**: Add a targeted expansion for non-TypeRefType expandable types using
`ExpandType(ctx, t, 0)` (count=0 skips TypeRef expansion, since those are already
handled). Only retry when the type actually changed (to avoid the pointer-inequality
trap with ObjectTypes).

Note: `ExpandType` with count=0 does NOT expand TypeRefTypes (since
`expandTypeRefsCount == 0` returns nil in the visitor). It DOES expand TypeOfType,
CondType, and other non-TypeRef expandable types. For plain ObjectTypes with only
TypeRefType properties, `ExpandType(obj, 0)` returns the same pointer since nothing
inside changes — so the pointer-inequality check is reliable here.

### Issue 4: Type parameter references expanded to their constraints

**This is the most fundamental issue.** `TypeRefType` is used to represent both
type alias references (`type Foo = ...`) and type parameter references (`<T>`).
For type parameters, the `TypeAlias` field on the `TypeRefType` is set to a
`TypeAlias` whose `Type` is the parameter's constraint (e.g. `unknown` for
unbounded `<T>`).

When TypeRefType expansion runs BEFORE the case-matching logic, expanding
`TypeRefType(T)` resolves the TypeAlias and returns the constraint `unknown`. This
destroys the type parameter's identity, breaking union member matching.

**Example failure** (`ClassWithGenericMethod` test):

```escalier
class Box(value: number) {
    value,
    getValue<T>(self, default: T) -> number | T {
        if self.value != 0 { return self.value }
        else { return default }
    }
}
```

When checking the return type, the checker unifies the inferred return type
`number | T` with the declared return type `number | T`. This enters the
`UnionType, _` case which iterates over the left union's members:

- `number` vs `number | T` → matches `number` member → OK
- `TypeRefType(T)` vs `number | T` → enters `_, UnionType` case, which probes:
  - `TypeRefType(T)` vs `number` → fails
  - `TypeRefType(T)` vs `TypeRefType(T)` → same-alias case → succeeds!

**Old code (working):** The retry loop runs AFTER the case-matching logic. By the
time `TypeRefType(T)` reaches the retry loop, the union case has already matched
it via same-alias TypeRefType comparison. The retry loop's expansion of `T` to
`unknown` is never reached for this case.

**New code (broken when expansion is before cases):** The in-place expansion loop
runs BEFORE the union cases. `TypeRefType(T)` is expanded to `unknown` before the
`_, UnionType` case gets a chance to match it. Then `unknown` (top type) can't be
assigned to any member of `number | T`, since `UnknownType` on the left side always
errors (line 290).

**Root cause:** The `TypeAlias` struct has no way to distinguish a regular type
alias from a type parameter constraint. Both look identical:

```go
// Regular type alias: type Foo = unknown
TypeAlias{Type: unknown, TypeParams: [], Exported: false}

// Type parameter constraint: <T> (implicit unknown constraint)
TypeAlias{Type: unknown, TypeParams: [], Exported: false}
```

**Fix:** Position the expansion AFTER the case-matching logic, not before it.
The case-matching code gets a chance to handle TypeRefTypes via same-alias
comparison and union member matching first. Only when no case matches do we expand
and retry. This is the approach used in the plan below.

## Plan (revised)

### Step 1: Add `tryExpandTypeRef` helper

Add a new method to `Checker` (in expand_type.go) that attempts to expand a
TypeRefType without erroring:

```go
func (c *Checker) tryExpandTypeRef(ctx Context, t *type_system.TypeRefType) (type_system.Type, bool) {
    typeAlias := t.TypeAlias
    if typeAlias == nil {
        typeAlias = resolveQualifiedTypeAlias(ctx, t.Name)
    }
    if typeAlias == nil {
        return nil, false
    }

    expandedType := typeAlias.Type

    // Don't expand nominal object types
    if obj, ok := expandedType.(*type_system.ObjectType); ok && obj.Nominal {
        return nil, false
    }

    // Handle type parameter substitution if the type is generic
    if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {
        substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
        expandedType = SubstituteTypeParams(typeAlias.Type, substitutions)
    }

    return expandedType, true
}
```

### Step 2: Extract case-matching into `unifyMatched`, add expansion loop in `unifyPruned`

Rename the current `unifyPruned` to `unifyMatched` (the function that contains all
the explicit type-matching cases). Then rewrite `unifyPruned` as a small loop that:
1. Calls `unifyMatched` with the current types
2. If matching succeeds, returns immediately
3. If matching fails, tries expanding TypeRefTypes and TypeOfTypes
4. If anything expanded, retries from step 1 with the expanded types
5. If nothing expanded, returns the original error

```go
func (c *Checker) unifyPruned(ctx Context, t1, t2 type_system.Type, depth int) []Error {
    for attempt := 0; attempt < maxUnifyDepth; attempt++ {
        errors := c.unifyMatched(ctx, t1, t2, depth)
        if len(errors) == 0 {
            return nil
        }

        // Try expanding TypeRefTypes
        expanded := false
        if ref1, ok := t1.(*type_system.TypeRefType); ok {
            if exp, ok := c.tryExpandTypeRef(ctx, ref1); ok {
                t1 = exp
                expanded = true
            }
        }
        if ref2, ok := t2.(*type_system.TypeRefType); ok {
            if exp, ok := c.tryExpandTypeRef(ctx, ref2); ok {
                t2 = exp
                expanded = true
            }
        }
        if expanded {
            continue
        }

        // Try expanding TypeOfType and other non-TypeRef expandable types
        expandedT1, _ := c.ExpandType(ctx, t1, 0)
        expandedT2, _ := c.ExpandType(ctx, t2, 0)
        if expandedT1 != t1 || expandedT2 != t2 {
            t1 = expandedT1
            t2 = expandedT2
            continue
        }

        // Nothing could be expanded, return the original error
        return errors
    }
    return []Error{&CannotUnifyTypesError{T1: t1, T2: t2}}
}
```

The existing case-matching code moves to `unifyMatched` with no changes to its
body or indentation. The do-nothing different-alias case (lines 505-511) and the
old retry loop (lines 1189-1203) are both deleted. The final `CannotUnifyTypesError`
at the end of `unifyMatched` remains — it signals to `unifyPruned` that no case
matched, triggering the expansion logic.

### Step 3: Audit other ExpandType calls in unify.go

There are two other `ExpandType` calls in the case-matching code that are unrelated
to the retry loop and should be preserved:

- **Line 345-346**: `KeyOfType` expansion — both sides are `KeyOfType`, expand their
  inner types. Keep as-is.
- **Line 1055**: Union + ObjectType — expands each union member to check if it's an
  `ObjectType`. Keep as-is.

### Step 4: Evaluate whether `maxUnifyDepth` can be reduced or removed

After this change, the loop counter in `unifyPruned` controls expansion retries.
`maxUnifyDepth` should be kept as the loop bound until Plan B (visited-set) is
implemented. After Plan B, `maxUnifyDepth` can be removed entirely.

## Testing strategy

1. **Run the full test suite** — This is a behavioral refactor; all existing tests
   should pass without changes.
2. **Verify depth behavior** — Add debug logging (temporary) to count the maximum
   loop iterations reached during the test suite. After this change, the max should
   correspond to the actual nesting depth of type alias chains.
3. **Test TypeRefType + TypeRefType (different alias)** — Write a test with two
   different type aliases that resolve to the same structural type and verify they
   unify:
   ```escalier
   type Foo = { x: number }
   type Bar = { x: number }
   val a: Foo = ...
   val b: Bar = a  // should succeed
   ```
4. **Test TypeRefType + concrete type** — Write a test where one side is a type
   alias and the other is an inline type:
   ```escalier
   type Foo = { x: number }
   val a: Foo = { x: 5 }  // TypeRefType vs ObjectType
   ```
5. **Test generic methods** — Verify `ClassWithGenericMethod` and
   `ObjectWithGenericMethods` pass, confirming type parameter identity is preserved
   through union matching.
6. **Test React types** — Verify `TestImportInferenceScript/NamespaceImportReact`
   passes without stack overflow, confirming the loop-based approach handles large
   types.

## Risks

- **Missed cases**: If there are non-`TypeRefType` types that relied on the retry
  loop for expansion (e.g. a `CondType` or `IndexType` that somehow reaches
  `unifyPruned` unexpanded), those would now fail with `CannotUnifyTypesError`
  instead of being retried. Mitigation: run the full test suite and React type
  tests to catch these. The `ExpandType(ctx, t, 0)` fallback at the bottom of the
  loop handles most of these.
- **Double expansion**: The TypeRefType + TypeRefType (different alias) case
  expands both `t1` and `t2` if both are TypeRefTypes. If the expanded form is
  itself a `TypeRefType`, it will be expanded again on the next loop iteration.
  This is correct behavior but relies on the loop counter to prevent cycles with
  recursive aliases (which will be properly handled by Plan B).
- **Wasted case matching**: `TypeRefType + ObjectType` falls through all cases in
  `unifyMatched` before being expanded in the `unifyPruned` loop. This is
  functionally correct but slightly less efficient than early expansion. The cost
  is negligible since case matching is cheap compared to type expansion.
