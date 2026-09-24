// Walks the scope interfaces in TypeScript's lib.dom.d.ts and lib.webworker.d.ts.
//
// Part of the analysis behind ../README.md. Run the three in order from this
// directory:
//
//   mkdir -p /tmp/hoist
//   git show <ref>:internal/interop/data/web/dom.window.esc    > /tmp/hoist/dom.esc
//   git show <ref>:internal/interop/data/web/worker.worker.esc > /tmp/hoist/worker.esc
//   WORK=/tmp/hoist node extract.mjs
//   WORK=/tmp/hoist node merge.mjs
//   WORK=/tmp/hoist OUT=.. node emit.mjs
//
// WORK is the scratch directory the scripts pass data through. OUT is where
// emit.mjs writes the .esc files. TS points at TypeScript's lib directory and
// defaults to the one in node_modules.

import { readFileSync, writeFileSync } from "node:fs";

const S = process.env.WORK ?? ".";
const TS = process.env.TS ??
  "../../../../node_modules/.pnpm/typescript@5.8.2/node_modules/typescript/lib";
const libs = {
  dom: readFileSync(`${TS}/lib.dom.d.ts`, "utf8").split("\n"),
  worker: readFileSync(`${TS}/lib.webworker.d.ts`, "utf8").split("\n"),
};

// Pull one `interface X ... { ... }` block out of a lib, returning its member
// lines and the interfaces it extends.
function iface(lines, name) {
  const start = lines.findIndex((l) => new RegExp(`^interface ${name}\\b`).test(l));
  if (start < 0) return null;
  const header = lines[start];
  const ext = /extends ([^{]+)\{/.exec(header);
  const extendsList = ext
    ? ext[1].split(",").map((s) => s.trim().replace(/<.*/, "")).filter(Boolean)
    : [];
  const body = [];
  for (let i = start + 1; i < lines.length; i++) {
    if (lines[i] === "}") break;
    body.push(lines[i]);
  }
  return { extendsList, body };
}

// A member line is one that starts a declaration at four-space indent and is
// not a doc comment. Continuation lines of a wrapped signature are skipped.
function members(body) {
  const out = [];
  for (const line of body) {
    if (!/^    \S/.test(line)) continue;
    if (/^\s*(\/\*|\*|\/\/)/.test(line)) continue;
    const m = /^    (?:readonly )?(?:get |set )?([A-Za-z_$][\w$]*)\s*[?(<:]/.exec(line);
    if (!m) continue;
    out.push({ name: m[1], text: line.trim() });
  }
  return out;
}

// The scopes to walk, and the environments each one contributes.
const SCOPES = {
  dom: {
    Window: ["window"],
  },
  worker: {
    DedicatedWorkerGlobalScope: ["dedicated_worker"],
    SharedWorkerGlobalScope: ["shared_worker"],
    ServiceWorkerGlobalScope: ["shared_worker_placeholder"],
  },
};
// ServiceWorkerGlobalScope fixed below; keeping the map literal simple.
SCOPES.worker.ServiceWorkerGlobalScope = ["service_worker"];

const ALL = ["window", "dedicated_worker", "shared_worker", "service_worker"];
const WORKERS = ["dedicated_worker", "shared_worker", "service_worker"];

// A mixin contributes the environments of whatever includes it, except where
// the mixin itself is the broader statement.
const MIXIN_ENVS = {
  WindowOrWorkerGlobalScope: ALL,
  WorkerGlobalScope: WORKERS,
};

// name -> {envs:Set, scopes:Set, text}
const index = new Map();

function record(name, text, envs, scope) {
  if (!index.has(name)) index.set(name, { envs: new Set(), scopes: new Set(), texts: new Set() });
  const e = index.get(name);
  for (const v of envs) e.envs.add(v);
  e.scopes.add(scope);
  e.texts.add(text);
}

function walk(libName, scopeName, envs, seen = new Set()) {
  if (seen.has(scopeName)) return;
  seen.add(scopeName);
  const def = iface(libs[libName], scopeName);
  if (!def) return;
  const effective = MIXIN_ENVS[scopeName] ?? envs;
  for (const m of members(def.body)) record(m.name, m.text, effective, scopeName);
  for (const parent of def.extendsList) {
    if (parent === "EventTarget") continue;
    walk(libName, parent, effective, seen);
  }
}

for (const [libName, scopes] of Object.entries(SCOPES)) {
  for (const [scopeName, envs] of Object.entries(scopes)) {
    walk(libName, scopeName, envs, new Set());
  }
}

// Top-level globals, which are TypeScript's flattened view of each lib's scope.
const flat = { dom: new Set(), worker: new Set() };
for (const [libName, lines] of Object.entries(libs)) {
  for (const line of lines) {
    const m = /^declare (?:function|var) ([A-Za-z_$][\w$]*)/.exec(line);
    if (m) flat[libName].add(m[1]);
  }
}

const rows = [...index.entries()]
  .map(([name, v]) => ({
    name,
    envs: ALL.filter((e) => v.envs.has(e)),
    scopes: [...v.scopes].sort(),
    inDomGlobals: flat.dom.has(name),
    inWorkerGlobals: flat.worker.has(name),
    texts: [...v.texts],
  }))
  .sort((a, b) => a.name.localeCompare(b.name));

writeFileSync(
  `${S}/scope-members.json`,
  JSON.stringify(rows, null, 2),
);
console.log(`members: ${rows.length}`);
console.log(`dom flattened globals: ${flat.dom.size}, worker: ${flat.worker.size}`);
const byEnv = {};
for (const r of rows) {
  const k = r.envs.join("+") || "(none)";
  byEnv[k] = (byEnv[k] ?? 0) + 1;
}
console.log(byEnv);
