// compare.mjs diffs the call edges a traced driver run recorded against
// Jelly's static call graph for the same driver, over the package's own call
// sites. It is the stand-in for Jelly's own soundness check, which needs
// graal-nodejs.
//
// Each edge the run saw, from a site in the package to a function, falls in
// one bucket:
//
//   found     Jelly has the edge.
//   silent    Jelly gives the site other targets but not this one. These are
//             the misses that matter, since nothing marks the site as open.
//             The counts split them by where the missed function is: the
//             driver, which stands for the caller's own code, or a package.
//   visible   Jelly gives the site no target at all, so the site is open
//             already.
//   unmatched the site or the function is one Jelly's output does not name,
//             so the two graphs cannot be compared there.
//
// Usage: node compare.mjs <work-dir> <package> [--list]

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const [workDir, pkg, flag] = process.argv.slice(2);
const require = createRequire(path.join(workDir, "package.json"));
const { TraceMap, originalPositionFor } = require("@jridgewell/trace-mapping");

const run = JSON.parse(fs.readFileSync(path.join(workDir, "runs", `${pkg}.json`), "utf8"));
const trace = JSON.parse(fs.readFileSync(path.join(workDir, "traces", `${pkg}.json`), "utf8"));
const pkgDir = `node_modules/${pkg}/`;

const key = (loc) => {
  const [f, ...rest] = loc.split(":");
  return `${run.files[Number(f)]}:${rest.join(":")}`;
};
const calls = new Map(Object.entries(run.calls).map(([id, loc]) => [Number(id), key(loc)]));
const functions = new Map(Object.entries(run.functions).map(([id, loc]) => [Number(id), key(loc)]));
const knownFunctions = new Set(functions.values());

const targets = new Map();
for (const site of calls.values()) targets.set(site, new Set());
for (const [call, fn] of run.call2fun) targets.get(calls.get(call)).add(functions.get(fn));

const maps = new Map();
function where(k) {
  const [file, line, col] = k.split(":");
  if (!maps.has(file)) {
    const mapFile = path.join(workDir, file + ".map");
    maps.set(file, fs.existsSync(mapFile) ? new TraceMap(fs.readFileSync(mapFile, "utf8")) : null);
  }
  const map = maps.get(file);
  const short = (f, l) => `${f.replace(/^(\.\.\/|\.\/)+/, "").replace(/^.*node_modules\//, "")}:${l}`;
  if (!map) return short(file, line);
  const pos = originalPositionFor(map, { line: Number(line), column: Number(col) - 1 });
  return pos.source ? short(pos.source, pos.line) : short(file, line);
}

const buckets = { found: [], silent: [], visible: [], unmatched: [] };
for (const [site, fn] of trace) {
  if (!site.startsWith(pkgDir)) continue;
  const static_ = targets.get(site);
  if (!static_ || !knownFunctions.has(fn)) buckets.unmatched.push([site, fn]);
  else if (static_.has(fn)) buckets.found.push([site, fn]);
  else if (static_.size > 0) buckets.silent.push([site, fn]);
  else buckets.visible.push([site, fn]);
}

const compared = buckets.found.length + buckets.silent.length + buckets.visible.length;
const sitesSeen = new Set(trace.filter(([s]) => s.startsWith(pkgDir)).map(([s]) => s)).size;
console.log(
  JSON.stringify({
    package: pkg,
    edges: compared + buckets.unmatched.length,
    sites: sitesSeen,
    found: buckets.found.length,
    silent: buckets.silent.length,
    silentToCaller: buckets.silent.filter(([, fn]) => fn.startsWith("drivers/")).length,
    visible: buckets.visible.length,
    unmatched: buckets.unmatched.length,
  }),
);
if (flag === "--list") {
  for (const name of ["silent", "visible", "unmatched"]) {
    for (const [site, fn] of buckets[name]) {
      const had = [...(targets.get(site) ?? [])].map(where);
      console.log(`${name}: ${where(site)} -> ${where(fn)}${had.length ? `  (Jelly: ${had.slice(0, 4).join(", ")}${had.length > 4 ? ", ..." : ""})` : ""}`);
    }
  }
}
