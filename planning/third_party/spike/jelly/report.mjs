// report.mjs reads one Jelly run and reports what it resolved inside the
// package. It counts the package's own call sites by how many callees Jelly
// gave each one, and it maps every position back through the package's source
// maps, so a site reads as the original source file and line.
//
// Usage:
//   node report.mjs <work-dir> <package>            counts, as one JSON line
//   node report.mjs <work-dir> <package> <pattern>  every call site whose
//                                                   source text matches
//                                                   pattern, with its callees
//
// A call with no callee is unresolved when Jelly's --zeros report lists it,
// and a call into native or external code otherwise.

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const [workDir, pkg, pattern] = process.argv.slice(2);
const require = createRequire(path.join(workDir, "package.json"));
const { TraceMap, originalPositionFor } = require("@jridgewell/trace-mapping");

const run = JSON.parse(fs.readFileSync(path.join(workDir, "runs", `${pkg}.json`), "utf8"));
const log = fs.readFileSync(path.join(workDir, "runs", `${pkg}.log`), "utf8");

const unresolved = new Set();
for (const m of log.matchAll(/^Call with zero callees at (.*):(\d+):(\d+):(\d+):(\d+)$/gm)) {
  unresolved.add(`${path.relative(workDir, m[1])}:${m[2]}:${m[3]}`);
}

const pkgDir = `node_modules/${pkg}/`;
const sources = new Map();
const maps = new Map();

function textAt(file, l1, c1, l2, c2) {
  if (!sources.has(file)) sources.set(file, fs.readFileSync(path.join(workDir, file), "utf8").split("\n"));
  const lines = sources.get(file);
  const text = l1 === l2 ? lines[l1 - 1].slice(c1 - 1, c2 - 1) : lines[l1 - 1].slice(c1 - 1);
  return text.replace(/\s+/g, " ").slice(0, 90);
}

// original maps a generated position to the source the package was built
// from, or keeps the generated position when the file has no source map. It
// returns the location and the text of that source line.
function original(file, line, col) {
  if (!maps.has(file)) {
    const mapFile = path.join(workDir, file + ".map");
    maps.set(file, fs.existsSync(mapFile) ? new TraceMap(fs.readFileSync(mapFile, "utf8")) : null);
  }
  const map = maps.get(file);
  const generated = { where: `${file.replace(pkgDir, "")}:${line}`, line: null };
  if (!map) return generated;
  const pos = originalPositionFor(map, { line, column: col - 1 });
  if (!pos.source) return generated;
  const index = map.resolvedSources.indexOf(pos.source);
  const content = map.sourcesContent?.[index];
  const text = content ? content.split("\n")[pos.line - 1]?.trim().slice(0, 90) : null;
  return { where: `${pos.source.replace(/^(\.\.\/|\.\/)+/, "").replace(/^.*node_modules\//, "")}:${pos.line}`, line: text };
}

function site(loc) {
  const [f, l1, c1, l2, c2] = loc.split(":").map(Number);
  const file = run.files[f];
  const o = original(file, l1, c1);
  return { file, l1, c1, key: `${file}:${l1}:${c1}`, text: o.line ?? textAt(file, l1, c1, l2, c2), where: o.where };
}

const functions = new Map(Object.entries(run.functions).map(([id, loc]) => [Number(id), loc]));
const callees = new Map();
for (const [call, fn] of run.call2fun) {
  if (!callees.has(call)) callees.set(call, []);
  callees.get(call).push(fn);
}

const counts = { sites: 0, one: 0, several: 0, unresolved: 0, native: 0 };
const matches = [];
for (const [id, loc] of Object.entries(run.calls)) {
  const s = site(loc);
  if (!s.file.startsWith(pkgDir)) continue;
  counts.sites++;
  const targets = callees.get(Number(id)) ?? [];
  if (targets.length === 1) counts.one++;
  else if (targets.length > 1) counts.several++;
  else if (unresolved.has(s.key)) counts.unresolved++;
  else counts.native++;
  if (pattern && new RegExp(pattern).test(s.text)) {
    const named = targets.map((t) => {
      const fs_ = site(functions.get(t));
      return fs_.where;
    });
    const shown = named.length > 6 ? `${named.length} targets: ${named.slice(0, 6).join(", ")}, ...` : named.join(", ");
    const status = targets.length ? shown : unresolved.has(s.key) ? "UNRESOLVED" : "native or external";
    matches.push(`${s.where}  ${s.text}\n    -> ${status}`);
  }
}

if (pattern) {
  console.log(matches.join("\n"));
} else {
  console.log(JSON.stringify({ package: pkg, ...counts }));
}
