---
"@escalier/cli": patch
---

Reject a call whose callback writes a value that the same call borrows as mutable, such as a `sort` compare function that pushes to the array being sorted.
