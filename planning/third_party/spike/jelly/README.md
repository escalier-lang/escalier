# Jelly prototype scripts

These scripts run the Jelly prototype that
[../../jelly_prototype.md](../../jelly_prototype.md) reports on. They write
nothing into the repository except the two result files named below.

- `run.sh <work-dir>` installs Jelly, TypeScript and the ten packages into the
  work directory, writes a driver per package, and runs Jelly over each one.
  The work directory must be outside the repository.
- `gen-driver.mjs` writes one package's driver from its `.d.ts`. The header
  comment lists what a driver calls and the limits it applies.
- `report.mjs` counts a package's own call sites by how many callees Jelly gave
  each, or lists the sites whose source line matches a pattern.
- `sites.mjs` looks up every call site in `sites.json`, the call sites the
  hand-lowered fixtures in [../hand/](../hand/) leave unresolved.

`counts.jsonl` holds `report.mjs`'s counts for the ten packages, and
`sites.txt` holds `sites.mjs`'s output, both from the run the report
describes. Regenerate them with:

```sh
work=/path/outside/the/repo
planning/third_party/spike/jelly/run.sh "$work"
for p in mitt uuid eventemitter3 lru-cache immer express rxjs date-fns lodash-es zod; do
  node planning/third_party/spike/jelly/report.mjs "$work" "$p"
done > planning/third_party/spike/jelly/counts.jsonl
node planning/third_party/spike/jelly/sites.mjs "$work" > planning/third_party/spike/jelly/sites.txt
```

`sites.json` also records each site's outcome. The harness in
[../hand/](../hand/) reads it to compute the ladder in its report.
