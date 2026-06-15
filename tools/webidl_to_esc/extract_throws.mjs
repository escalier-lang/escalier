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

// BRIDGE handles terse delegating methods. The DOM spec defines methods like
// `removeChild` as a one-line delegation ("the removeChild(child) method
// steps are to return the result of pre-removing child"), which webref does
// NOT emit as an operation-named algorithm node — so their throws live only
// in the concept algorithm they call. webref carries no machine-readable
// method->concept link either: the method dfn's single outgoing link is a dev
// example, not the delegation. So the delegation target is curated here,
// keyed by the concept algorithm's href, and the closure attaches each
// concept's throw set to the public method. The key is "Interface.method"
// using the *declaring* interface (a mixin like ParentNode for querySelector);
// the Go converter resolves that against folded members by member origin.
const BRIDGE = {
  "Document.createElementNS": "https://dom.spec.whatwg.org/#internal-createelementns-steps",
  "Node.appendChild": "https://dom.spec.whatwg.org/#concept-node-pre-insert",
  "Node.insertBefore": "https://dom.spec.whatwg.org/#concept-node-pre-insert",
  "Node.removeChild": "https://dom.spec.whatwg.org/#concept-node-pre-remove",
  "Node.replaceChild": "https://dom.spec.whatwg.org/#concept-node-replace",
  "ParentNode.querySelector": "https://dom.spec.whatwg.org/#scope-match-a-selectors-string",
  "ParentNode.querySelectorAll": "https://dom.spec.whatwg.org/#scope-match-a-selectors-string",
};

// methodSet reads the `dfns` extract (ed/dfns/<spec>.json) and returns the set
// of "Interface.method" names declared as methods. Used only to validate that
// every BRIDGE key names a real method, catching typos and spec drift.
function methodSet(dir) {
  const names = new Set();
  for (const file of readdirSync(dir)) {
    if (!file.endsWith(".json")) continue;
    const j = JSON.parse(readFileSync(join(dir, file), "utf8"));
    for (const d of j.dfns ?? []) {
      if (d.type !== "method") continue;
      // A method dfn carries the declaring interface in `for` (["Document"])
      // and the signature(s) in `linkingText` (["createElementNS(...)"]).
      const sig = (d.linkingText ?? [])[0] ?? "";
      const name = /^([A-Za-z][\w]*)\(/.exec(sig);
      if (!name) continue;
      for (const iface of d.for ?? []) {
        names.add(`${iface}.${name[1]}`);
      }
    }
  }
  return names;
}

// exceptionsIn pulls exception names from a throwing step. webref marks the
// thrown DOMException name inconsistently: `data-link-type="exception"` on
// some specs, `data-link-type="idl"` on others (e.g. NamespaceError uses
// "exception" but InvalidCharacterError uses "idl" in the same algorithm).
// So match the visible link text by shape instead: an <a> whose text is an
// *Error name. "DOMException" ends in "Exception", so it is excluded.
// Returns [] for a non-throwing step.
function exceptionsIn(html) {
  if (!THROW_MARKERS.test(html)) return [];
  const names = new Set();
  for (const m of html.matchAll(/>([A-Z][A-Za-z]*Error)<\/a>/g)) {
    names.add(m[1]);
  }
  // TypeError / RangeError are sometimes plain text rather than links.
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
  const [dir, outPath, dfnsDir] = process.argv.slice(2);
  if (!dir) {
    console.error("usage: node extract_throws.mjs <algorithms-dir> [out.json] [dfns-dir]");
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

  // Apply the curated bridge for terse delegating methods. Each entry unions
  // the concept algorithm's closed throw set into the public method's key.
  let bridged = 0;
  for (const [method, href] of Object.entries(BRIDGE)) {
    const set = closure.get(href);
    if (!set || set.size === 0) {
      console.error(`bridge: ${method} -> ${href} has no throws (cross-spec helper not loaded?)`);
      continue;
    }
    result[method] = [...new Set([...(result[method] ?? []), ...set])].sort();
    bridged++;
  }

  // Validate bridge keys against the dfns extract, if provided: every BRIDGE
  // method must name a real method definition.
  if (dfnsDir) {
    const methods = methodSet(dfnsDir);
    for (const method of Object.keys(BRIDGE)) {
      if (!methods.has(method)) {
        console.error(`bridge: ${method} is not a method in the dfns extract (typo or spec drift?)`);
      }
    }
  }

  if (outPath) {
    writeFileSync(outPath, JSON.stringify(result, null, 2) + "\n");
  }

  // Coverage report on stderr.
  console.error(`algorithms loaded:        ${all.length}`);
  console.error(`operation-named:          ${opCount}`);
  console.error(`  with >=1 throw:         ${opThrowing}`);
  console.error(`bridged terse methods:    ${bridged}/${Object.keys(BRIDGE).length}`);
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
