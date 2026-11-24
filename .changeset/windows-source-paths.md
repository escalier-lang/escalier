---
'@escalier/cli': patch
---

Fix `escalier build` on Windows, which compiled no files because it read source paths with backslashes.
