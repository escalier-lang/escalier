# `std:*` pseudo-packages

Escalier standard-library pseudo-packages. The resolver maps `std:foo`
to `std/foo.esc` in this directory. Multi-word packages use underscores
(`std:typed_arrays` → `std/typed_arrays.esc`).

Most entries here are ECMAScript / language-level builtins (carrying
`@js("...")`), but the namespace is not limited to those — non-builtin
Escalier-authored stdlib code lives alongside them.

See `planning/builtins/implementation_plan.md` §2.2.
