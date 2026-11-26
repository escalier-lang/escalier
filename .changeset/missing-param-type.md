---
"@escalier/cli": patch
---

A closure parameter with no annotation now needs its context to supply a type. The context can be a call argument, an annotated binding, a store into a typed field, or the return of a function with a written return type. A parameter with neither reports `parameter 'p' has no type, and nothing the closure flows into supplies one`.
