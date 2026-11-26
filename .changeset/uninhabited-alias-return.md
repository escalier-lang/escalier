---
"@escalier/cli": patch
---

A function written to return a recursive alias that no finite value inhabits, such as `type Next = {next: Next}`, reports that a call to it never returns. This is the same report a function gets when its return type is inferred without a base case.
