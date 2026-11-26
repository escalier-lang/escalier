---
"@escalier/cli": patch
---

The checker no longer infers the type of a function or method parameter from how the body uses it. A parameter with no written type reports "parameter 'p' has no type; write its type", and the body checks it as an error type, so its uses report nothing further. A closure parameter still takes its type from the function type the closure flows into. The "Write the inferred signature" code action no longer writes parameter types, and offers nothing for a function with an untyped parameter.
