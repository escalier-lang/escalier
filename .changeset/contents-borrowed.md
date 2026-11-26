---
"@escalier/cli": patch
---

Reject a call that borrows a collection such as an array, map or set as mutable while a borrow of one of its elements is still live. A borrow of an object field or a tuple slot does not conflict, since no callee can remove one.
