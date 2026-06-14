// Stage 1b of the WebIDL -> Escalier pipeline: extract per-operation throw
// sets from webref's structured spec algorithms.
//
// WebIDL carries no exception information. The exceptions an operation can
// throw live in the spec's prose *algorithms*, which webref publishes as
// structured JSON (one file per spec under ed/algorithms/). This tool:
//
//   1. Loads a directory of those algorithm files.
//   2. Indexes every algorithm by its href.
//   3. For each algorithm, extracts the exceptions it throws *directly* and
//      the other algorithms it *calls* (steps link to them by href).
//   4. Computes the transitive throw closure over that call graph, because a
//      method's throw usually lives in a helper algorithm it invokes, not in
//      its own steps.
//   5. Emits a map of `Interface/method` -> sorted throw set, plus a coverage
//      report on stderr.
//
// Usage:
//   node extract_throws.mjs <algorithms-dir> [out.json]
//
// The output feeds the Go converter (webidl_to_esc -throws <out.json>), which
// renders a `throws` clause on each operation.

import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

// flattenSteps yields every step's html, descending into nested sub-steps.
function flattenSteps(steps, out) {
  for (const s of steps ?? []) {
    if (s.html) out.push(s.html);
    if (s.steps) flattenSteps(s.steps, out);
  }
  return out;
}

// THROW_MARKERS detect that a step is a throwing step rather than one that
// merely mentions an exception (e.g. "catch", or prose describing one).
const THROW_MARKERS = /#dfn-throw|#concept-throw|>\s*throw[s]?\s*</i;

// exceptionsIn pulls exception names from a throwing step: DOMException
// subtypes are `data-link-type="exception"` links; TypeError / RangeError are
// referenced by name. Returns [] for a non-throwing step.
function exceptionsIn(html) {
  if (!THROW_MARKERS.test(html)) return [];
  const names = new Set();
  for (const m of html.matchAll(
    /data-link-type="exception"[^>]*>([^<]+)<\/a>/g,
  )) {
    names.add(m[1].trim());
  }
  for (const m of html.matchAll(/\b(TypeError|RangeError)\b/g)) {
    names.add(m[1]);
  }
  return [...names];
}

// hrefsIn returns every href a step links to. Those that match an algorithm
// href become call-graph edges.
function hrefsIn(html) {
  const out = [];
  for (const m of html.matchAll(/href="([^"]+)"/g)) out.push(m[1]);
  return out;
}

function loadAlgorithms(dir) {
  const byHref = new Map();
  const all = [];
  for (const file of readdirSync(dir)) {
    if (!file.endsWith(".json")) continue;
    const j = JSON.parse(readFileSync(join(dir, file), "utf8"));
    for (const a of j.algorithms ?? []) {
      a._spec = j.spec?.shortname ?? file.replace(/\.json$/, "");
      all.push(a);
      if (a.href) byHref.set(a.href, a);
    }
  }
  return { byHref, all };
}

function main() {
  const [dir, outPath] = process.argv.slice(2);
  if (!dir) {
    console.error("usage: node extract_throws.mjs <algorithms-dir> [out.json]");
    process.exit(1);
  }

  const { byHref, all } = loadAlgorithms(dir);

  // Per-algorithm direct throws and call edges (edges restricted to hrefs
  // that are themselves algorithms).
  const direct = new Map(); // href -> Set(exception)
  const edges = new Map(); // href -> Set(href)
  let externalEdges = 0;
  for (const a of all) {
    if (!a.href) continue;
    const dThrows = new Set();
    const dEdges = new Set();
    for (const html of flattenSteps(a.steps, [])) {
      for (const e of exceptionsIn(html)) dThrows.add(e);
      for (const h of hrefsIn(html)) {
        if (byHref.has(h)) dEdges.add(h);
        else if (/#/.test(h)) externalEdges++;
      }
    }
    direct.set(a.href, dThrows);
    edges.set(a.href, dEdges);
  }

  // Transitive closure: throws[h] = direct[h] U union(throws[e] for e in edges[h]).
  // Iterate to a fixpoint; the graph has cycles, so a worklist over all nodes
  // until nothing changes is the simplest correct approach.
  const closure = new Map();
  for (const h of direct.keys()) closure.set(h, new Set(direct.get(h)));
  let changed = true;
  while (changed) {
    changed = false;
    for (const [h, set] of closure) {
      for (const e of edges.get(h) ?? []) {
        for (const exc of closure.get(e) ?? []) {
          if (!set.has(exc)) {
            set.add(exc);
            changed = true;
          }
        }
      }
    }
  }

  // Map operation-named algorithms (Interface/member(...)) to their throw set.
  const result = {};
  let opCount = 0;
  let opThrowing = 0;
  const excFreq = new Map();
  for (const a of all) {
    const m = /^([A-Za-z][\w]*)\/([A-Za-z][\w]*)/.exec(a.name ?? "");
    if (!m) continue;
    opCount++;
    const key = `${m[1]}.${m[2]}`;
    const throws = [...(closure.get(a.href) ?? [])].sort();
    if (throws.length === 0) continue;
    opThrowing++;
    result[key] = throws;
    for (const e of throws) excFreq.set(e, (excFreq.get(e) ?? 0) + 1);
  }

  if (outPath) {
    writeFileSync(outPath, JSON.stringify(result, null, 2) + "\n");
  }

  // Coverage report on stderr.
  console.error(`algorithms loaded:        ${all.length}`);
  console.error(`operation-named:          ${opCount}`);
  console.error(`  with >=1 throw:         ${opThrowing}`);
  console.error(`unresolved external edges:${externalEdges} (cross-spec helpers not loaded)`);
  console.error(`exception frequency:`);
  for (const [e, n] of [...excFreq].sort((a, b) => b[1] - a[1])) {
    console.error(`  ${String(n).padStart(4)}  ${e}`);
  }
  console.error(`examples:`);
  for (const k of [
    "Element.querySelector",
    "Element.setAttribute",
    "Node.removeChild",
    "Node.appendChild",
    "Range.setStart",
    "DOMTokenList.add",
  ]) {
    if (result[k]) console.error(`  ${k}: ${result[k].join(" | ")}`);
  }
}

main();
