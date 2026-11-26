---
"@escalier/cli": patch
---

Report a compiler crash as an internal-error diagnostic that names the file and phase instead of a Go panic, and throw a `TypeError` at runtime when a value doesn't match a literal in a destructuring pattern.
