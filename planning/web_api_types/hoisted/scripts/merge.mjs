// Joins the converted Escalier member text to the environments extract.mjs found.
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

const esc = {
  dom: readFileSync(`${S}/dom.esc`, "utf8").split("\n"),
  worker: readFileSync(`${S}/worker.esc`, "utf8").split("\n"),
};

// Pull the member lines of one `export declare interface|class X` block, keeping
// the doc comment that precedes each so the hoisted form carries it.
function block(lines, name) {
  const start = lines.findIndex((l) =>
    new RegExp(`^export declare (interface|class) ${name}(\\s|<|\\{)`).test(l),
  );
  if (start < 0) return null;
  const body = [];
  for (let i = start + 1; i < lines.length; i++) {
    if (lines[i] === "}") break;
    body.push(lines[i]);
  }
  return body;
}

// A member starts at four-space indent and is not part of a doc comment.
function members(body) {
  const out = [];
  const setters = new Set();
  let doc = [];
  for (const line of body) {
    if (/^\s*(\/\*\*|\*|\*\/)/.test(line)) { doc.push(line); continue; }
    if (!/^    \S/.test(line)) { doc = []; continue; }
    const trimmed = line.trim().replace(/,$/, "");

    // Escalier renders an accessor pair as `get x(self) -> T` and
    // `set x(mut self, v: U)`. Normalise the getter to a property so the
    // hoist sees one shape, and note the setter so the property is writable.
    const setter = /^set ([A-Za-z_$][\w$]*)\(/.exec(trimmed);
    if (setter) { setters.add(setter[1]); doc = []; continue; }
    const getter = /^get ([A-Za-z_$][\w$]*)\([^)]*\)\s*->\s*(.*)$/.exec(trimmed);
    if (getter) {
      out.push({ name: getter[1], text: `readonly ${getter[1]}: ${getter[2]}`, doc: doc.join("\n") });
      doc = [];
      continue;
    }

    const m = /^(?:readonly |static )?([A-Za-z_$][\w$]*)\s*[?(<:]/.exec(trimmed);
    if (!m) { doc = []; continue; }
    out.push({ name: m[1], text: trimmed, doc: doc.join("\n") });
    doc = [];
  }
  for (const m of out) {
    if (setters.has(m.name)) m.text = m.text.replace(/^readonly /, "");
  }
  return out;
}

const SCOPE_SOURCE = {
  Window: "dom",
  WindowOrWorkerGlobalScope: "dom",
  WindowEventHandlers: "dom",
  WindowLocalStorage: "dom",
  WindowSessionStorage: "dom",
  GlobalEventHandlers: "dom",
  AnimationFrameProvider: "dom",
  FontFaceSource: "dom",
  MessageEventTarget: "dom",
  WorkerGlobalScope: "worker",
  DedicatedWorkerGlobalScope: "worker",
  SharedWorkerGlobalScope: "worker",
  ServiceWorkerGlobalScope: "worker",
};

// scope -> member name -> {text, doc}
const byScope = {};
for (const [scope, lib] of Object.entries(SCOPE_SOURCE)) {
  const body = block(esc[lib], scope);
  if (!body) {
    console.error(`missing scope: ${scope}`);
    continue;
  }
  byScope[scope] = {};
  for (const m of members(body)) {
    if (m.name === "prototype" || m.name === "constructor") continue;
    if (byScope[scope][m.name]) {
      byScope[scope][m.name].text += "\n" + m.text; // overload set
    } else {
      byScope[scope][m.name] = { text: m.text, doc: m.doc };
    }
  }
}

const envRows = JSON.parse(readFileSync(`${S}/scope-members.json`, "utf8"));
const envByName = new Map(envRows.map((r) => [r.name, r]));

// addEventListener and removeEventListener are the scope's own event-map
// machinery rather than globals a program calls, so they are reported apart.
const EVENT_API = new Set(["addEventListener", "removeEventListener", "dispatchEvent"]);

const merged = [];
const seen = new Set();
for (const [scope, ms] of Object.entries(byScope)) {
  for (const [name, m] of Object.entries(ms)) {
    const key = `${scope}::${name}`;
    if (seen.has(key)) continue;
    seen.add(key);
    merged.push({ scope, name, text: m.text, doc: m.doc });
  }
}

// name -> the scopes that declare it, its texts, and the computed environments
const globals = new Map();
for (const row of merged) {
  if (!globals.has(row.name)) {
    globals.set(row.name, { name: row.name, scopes: [], variants: new Map() });
  }
  const g = globals.get(row.name);
  g.scopes.push(row.scope);
  g.variants.set(row.text, [...(g.variants.get(row.text) ?? []), row.scope]);
}

const out = [...globals.values()]
  .map((g) => {
    const e = envByName.get(g.name);
    return {
      name: g.name,
      envs: e ? e.envs : [],
      scopes: g.scopes.sort(),
      eventApi: EVENT_API.has(g.name),
      divergent: g.variants.size > 1,
      variants: [...g.variants.entries()].map(([text, scopes]) => ({ text, scopes })),
    };
  })
  .sort((a, b) => a.name.localeCompare(b.name));

writeFileSync(`${S}/globals.json`, JSON.stringify(out, null, 2));

const noEnv = out.filter((g) => g.envs.length === 0).map((g) => g.name);
console.log(`globals: ${out.length}`);
console.log(`divergent across scopes: ${out.filter((g) => g.divergent).length}`);
console.log(`event-api members: ${out.filter((g) => g.eventApi).length}`);
console.log(`no env computed (${noEnv.length}): ${noEnv.slice(0, 12).join(", ")}`);
