---
"@escalier/cli": patch
---

A closure's parameters now take their types from the function type it flows into before its body is checked. That function type can come from a call argument, an annotated binding, a store into a typed field, a return under a written return type, or an overload set whose arms agree on the parameter. Reading a `&mut` field, or a union field with object members, through an immutable borrow now keeps the borrow immutable.
