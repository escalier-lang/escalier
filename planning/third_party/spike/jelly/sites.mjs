// sites.mjs looks up, in each package's Jelly run, the call sites the
// hand-lowered fixtures could not resolve. sites.json lists them, each with a
// pattern that matches the site's original source line.
//
// Usage: node sites.mjs <work-dir> [package]

import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";

const here = path.dirname(new URL(import.meta.url).pathname);
const [workDir, only] = process.argv.slice(2);
const sites = JSON.parse(fs.readFileSync(path.join(here, "sites.json"), "utf8"));

for (const s of sites) {
  if (only && s.package !== only) continue;
  if (!fs.existsSync(path.join(workDir, "runs", `${s.package}.json`))) {
    console.log(`## ${s.package}: ${s.site}\n    (no Jelly run)\n`);
    continue;
  }
  const out = execFileSync("node", [path.join(here, "report.mjs"), workDir, s.package, s.pattern], { encoding: "utf8" }).trim();
  console.log(`## ${s.package}: ${s.site}\n${out || "    (pattern matched no call site)"}\n`);
}
