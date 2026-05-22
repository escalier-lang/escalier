# `std:*` pseudo-packages

ECMAScript / language-level builtins. The resolver maps `std:foo` to
`std/foo.esc` in this directory. Multi-word packages use underscores
(`std:typed_arrays` → `std/typed_arrays.esc`).

See `planning/builtins/implementation_plan.md` §2.2.
