# Escalier language reference

Escalier is a programming language that compiles to JavaScript. It keeps
TypeScript's structural core and its interop story, and departs from it in a
number of places. The largest ones:

- Values are immutable by default, and ownership of reference-shaped values is
  tracked so that guarantee holds.
- Object, tuple, and function types are exact by default.
- Classes are nominal and enums are variant types.
- `any` is gone; negation `~T` is a real type; a `throws` clause is checked; and
  a promise carries its rejection type as `Promise<T, E>`.

The pages below cover the rest.

| | |
|---|---|
| [00 Types](00_types.md) | Primitives, objects, unions, negation, classes, generics, type-level operators |
| [01 Destructuring](01_destructuring.md) | Patterns in bindings and parameters; `val`, `var`, and `mut` |
| [02 Functions](02_functions.md) | Signatures, inference, generics, `throws`, async, overloading, generators |
| [03 Imports](03_imports.md) | Pseudo-packages, npm packages, file-scoped imports; no ambient globals |
| [04 Pattern Matching](04_pattern_matching.md) | `match`, `if val`, `val … else`, narrowing, exhaustiveness |
| [05 Enums](05_enums.md) | Variant types and their namespaces |
| [06 Error Handling](06_error_handling.md) | `throws`, `try`/`catch`, async rejection |
| [07 Exact Types](07_exact_types.md) | Exact by default, the `open` marker, interop |
| [08 Mutability](08_mutability.md) | `mut`, `readonly`, deep mutability, exclusivity |
| [09 Ownership](09_ownership.md) | Owned and borrowed values, moves, lifetimes, and how this compares to Rust |
| [10 Code Organization](10_code_organization.md) | Packages, namespaces, bundling |

## Scope of these documents

They describe the language as the checker in
[internal/solver/](../internal/solver/) defines it. Where a feature is designed
but not built, the page says so at the point it matters. The design documents the
pages draw on live under [planning/](../planning/).
