---
"@escalier/cli": patch
---

Reject a call whose callback can remove an object from a collection that the same call borrows as mutable, such as a `sort` compare function that pops from the array of objects being sorted.
