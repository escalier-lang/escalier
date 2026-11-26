---
"@escalier/cli": patch
---

A module-level function or method may write `_` in its signature only in its error type, as in `throws _`, `throws Array<_>` or `Promise<T, _>`. A `_` anywhere else reports an error that suggests the inferred type to write, and an error-type `_` that infers a type holding a lifetime is rejected.
