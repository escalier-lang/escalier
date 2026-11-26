---
"@escalier/cli": patch
---

Test an annotated `match` arm's discriminant at runtime, so the arm no longer matches a value of another union member, and report an annotation that the generated code can't test.
