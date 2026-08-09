// One-off script: set the native GitHub "blocked by" issue-dependency
// relationships between the MLstruct PR-tracking issues (#1058–#1069) per the
// dependency graph in 07-implementation-plan.md.
//
// WHY A SCRIPT: GitHub's issue-dependencies API (shipped Aug 2025) is not exposed
// through the GitHub MCP tools, and the Claude cloud session that created the
// issues cannot reach api.github.com directly. Run this from an environment whose
// token can call the REST API (e.g. your local machine / Desktop).
//
// USAGE:
//   GITHUB_TOKEN=<a token with `issues:write` on escalier-lang/escalier> \
//     node planning/ml_struct/set-issue-dependencies.mjs
//
// The token needs write access to issues on the repo. A classic PAT with `repo`
// scope, a fine-grained PAT with Issues: read&write, or `gh auth token` all work.
// The script is idempotent: an already-set dependency (HTTP 422) is treated as OK.

const REPO = "escalier-lang/escalier";
const TOKEN = process.env.GITHUB_TOKEN || process.env.GH_TOKEN;

if (!TOKEN) {
  console.error("Set GITHUB_TOKEN (or GH_TOKEN) to a token with issues:write on " + REPO);
  process.exit(1);
}

// PR label -> { number (for URLs/endpoints), id (global database id for the body) }.
const ISSUE = {
  PR1:  { number: 1058, id: 5100228159 },
  PR2:  { number: 1059, id: 5100228447 },
  PR3:  { number: 1060, id: 5100228800 },
  PR4:  { number: 1061, id: 5100229044 },
  PR5:  { number: 1062, id: 5100229541 },
  PR6:  { number: 1063, id: 5100229784 },
  PR7:  { number: 1064, id: 5100229997 },
  PR8:  { number: 1065, id: 5100230202 },
  PR9:  { number: 1066, id: 5100230439 },
  PR10: { number: 1067, id: 5100230760 },
  PR11: { number: 1068, id: 5100231075 },
  PR12: { number: 1069, id: 5100231276 },
};

// dependent PR -> list of PRs that block it (the "blocked by" edges), reversed
// from the graph edges in 07-implementation-plan.md.
const BLOCKED_BY = {
  PR3:  ["PR1"],
  PR4:  ["PR3"],
  PR5:  ["PR3", "PR4", "PR2"],
  PR6:  ["PR5"],
  PR7:  ["PR3", "PR5"],
  PR8:  ["PR5"],
  PR9:  ["PR5"],
  PR10: ["PR5"],
  PR11: ["PR5", "PR10"],
  PR12: ["PR6", "PR7", "PR8", "PR9", "PR10", "PR11"],
};

async function addBlockedBy(dependent, blocker) {
  const num = ISSUE[dependent].number;
  const res = await fetch(
    `https://api.github.com/repos/${REPO}/issues/${num}/dependencies/blocked_by`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${TOKEN}`,
        Accept: "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ issue_id: ISSUE[blocker].id }),
    }
  );
  const tag = `${dependent} (#${num}) blocked_by ${blocker} (#${ISSUE[blocker].number})`;
  if (res.ok) {
    console.log(`  ok     ${tag}`);
  } else if (res.status === 422) {
    console.log(`  exists ${tag}`); // already set — idempotent
  } else {
    const text = await res.text();
    console.error(`  FAIL   ${tag} -> HTTP ${res.status}: ${text}`);
  }
}

for (const [dependent, blockers] of Object.entries(BLOCKED_BY)) {
  for (const blocker of blockers) {
    await addBlockedBy(dependent, blocker);
  }
}

console.log("done.");
