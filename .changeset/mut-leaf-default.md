---
"@escalier/cli": patch
---

A `mut` binding in a destructuring pattern no longer keeps `undefined` in its type when its default fills that case in, and it keeps the literal members of a union it widens.
