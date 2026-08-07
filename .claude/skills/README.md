# Project skills

`dev-task` is Escalier's own skill. Everything else in this directory is vendored
from [mattpocock/skills](https://github.com/mattpocock/skills) and should not be
edited in place — local edits are lost on the next re-sync.

## Vendored: mattpocock/skills

- Upstream: https://github.com/mattpocock/skills
- Commit: `84fdeffd12f2ee307994d1eb6feb48173b6e0502`
- Vendored: 2026-08-07
- Plugin version at that commit: 1.2.3
- License: MIT, see [LICENSE-mattpocock-skills](./LICENSE-mattpocock-skills)

The 25 skills vendored here are exactly the ones listed in the upstream
`.claude-plugin/plugin.json`, which is the released set. Upstream also carries
`skills/in-progress/`, `skills/misc/`, and `skills/deprecated/` trees that the
plugin does not ship. Those are not vendored.

Two changes were made while copying:

1. **Flattened.** Upstream groups skills under `skills/engineering/` and
   `skills/productivity/`. Claude Code discovers project skills at
   `.claude/skills/<name>/SKILL.md`, one level deep, so the category directories
   are dropped. Upstream's own `scripts/link-skills.sh` flattens the same way.
2. **Dropped `agents/openai.yaml`.** Each upstream skill carries a small YAML
   file giving Codex a display name and short description. This repo drives these
   skills through Claude Code, which does not read that file.

Nothing inside any `SKILL.md` was modified.

### Run the setup skill once

The engineering skills expect per-repo configuration — which issue tracker to
use, the triage label vocabulary, and where domain docs live. Run
`/setup-matt-pocock-skills` once to create it. `/to-spec`, `/to-tickets`, and
`/triage` will tell you to run it if you skip this.

### Re-syncing

```sh
git clone --depth 1 https://github.com/mattpocock/skills.git /tmp/mp-skills
```

Then, for each entry in `/tmp/mp-skills/.claude-plugin/plugin.json`'s `skills`
array, replace `.claude/skills/<basename>/` with the upstream directory and
delete its `agents/` subdirectory. Copy `LICENSE` over
`LICENSE-mattpocock-skills`, and update the commit, date, and plugin version
recorded above.

Re-check the `skills` array on each sync rather than assuming this list is still
current — upstream promotes skills out of `in-progress/` and retires others.

### Skills

**Engineering, user-invoked** — `ask-matt`, `grill-with-docs`, `triage`,
`improve-codebase-architecture`, `setup-matt-pocock-skills`, `to-spec`,
`to-tickets`, `implement`, `wayfinder`

**Engineering, model-invoked** — `prototype`, `diagnosing-bugs`, `research`,
`tdd`, `domain-modeling`, `codebase-design`, `code-review`,
`resolving-merge-conflicts`, `wizard`

**Productivity, user-invoked** — `grill-me`, `handoff`, `teach`,
`to-questionnaire`, `wait-what`

**Productivity, model-invoked** — `grilling`, `writing-for-agents`

A user-invoked skill runs only when you type it. A model-invoked skill can also
be reached for automatically when the task fits.
