---
"@escalier/cli": patch
---

`&obj.a`, where the field `a` holds a borrow, borrows the field's referent instead of the borrow itself. A written return type such as `-> &'a T` now accepts it, as the inferred signature said it would. A `&mut obj.a` through an immutable borrow in `a` reports an error.
