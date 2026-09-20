// Assigns each global a package and writes the .esc files.
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

import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
const S = process.env.WORK ?? ".";
const OUT = process.env.OUT ?? "..";
mkdirSync(OUT, { recursive: true });

const globals = JSON.parse(readFileSync(`${S}/globals.json`, "utf8"));

const ALL = ["window", "dedicated_worker", "shared_worker", "service_worker"];
const WORKERS = ["dedicated_worker", "shared_worker", "service_worker"];

const SCOPE_ENVS = {
  Window: ["window"],
  GlobalEventHandlers: ["window"],
  WindowEventHandlers: ["window"],
  WindowLocalStorage: ["window"],
  WindowSessionStorage: ["window"],
  WindowOrWorkerGlobalScope: ALL,
  AnimationFrameProvider: ["window", "dedicated_worker"],
  FontFaceSource: WORKERS,
  MessageEventTarget: ["dedicated_worker"],
  WorkerGlobalScope: WORKERS,
  DedicatedWorkerGlobalScope: ["dedicated_worker"],
  SharedWorkerGlobalScope: ["shared_worker"],
  ServiceWorkerGlobalScope: ["service_worker"],
};

// Package assignment. A member whose type carries a package qualifier goes to
// that package. The rest are assigned by family and listed here so every choice
// is visible in one place.
const PACKAGE_OF = {
  // web:core. Portable surface with no browser type in the signature.
  atob: "core", btoa: "core", structuredClone: "core", queueMicrotask: "core",
  reportError: "core", setTimeout: "core", clearTimeout: "core",
  setInterval: "core", clearInterval: "core", origin: "core",
  isSecureContext: "core", crossOriginIsolated: "core",
  requestAnimationFrame: "core", cancelAnimationFrame: "core",
  requestIdleCallback: "dom", cancelIdleCallback: "dom",
  onerror: "core", onrejectionhandled: "core", onunhandledrejection: "core",
  onlanguagechange: "core", onoffline: "core", ononline: "core",
  onmessage: "core", onmessageerror: "core", postMessage: "core",
  self: "core",

  // Family packages named by the qualifier already in the signature.
  fetch: "fetch",
  caches: "cache",
  crypto: "crypto",
  indexedDB: "indexeddb",
  performance: "performance",
  localStorage: "storage", sessionStorage: "storage",
  createImageBitmap: "canvas",

  // web:worker. The worker-only surface.
  importScripts: "worker", fonts: "worker", clients: "worker",
  registration: "worker", serviceWorker: "worker", skipWaiting: "worker",
  onactivate: "worker", onfetch: "worker", oninstall: "worker",
  onnotificationclick: "worker", onnotificationclose: "worker",
  onpush: "worker", onpushsubscriptionchange: "worker",
  onconnect: "worker", onrtctransform: "worker",
};

// Members whose form differs by environment go to the package that owns each
// form rather than to one package.
const SPLIT = {
  location: { window: "dom", worker: "worker" },
  navigator: { window: "dom", worker: "worker" },
  close: { window: "dom", worker: "worker" },
  name: { window: "dom", worker: "worker" },
};

// The event-map machinery is a property of each scope rather than a global a
// program calls, so it is reported and not hoisted.
const NOT_HOISTED = new Set(["addEventListener", "removeEventListener", "dispatchEvent"]);

function envsOf(scopes) {
  const s = new Set();
  for (const sc of scopes) for (const e of SCOPE_ENVS[sc] ?? []) s.add(e);
  return ALL.filter((e) => s.has(e));
}

function envDecorator(envs) {
  if (envs.length === ALL.length) return null;
  if (envs.length === 3 && WORKERS.every((w) => envs.includes(w))) return '@env("worker")';
  return `@env(${envs.map((e) => `"${e}"`).join(", ")})`;
}

function packageFor(name, envs) {
  if (SPLIT[name]) return envs.includes("window") ? SPLIT[name].window : SPLIT[name].worker;
  return PACKAGE_OF[name] ?? "dom";
}

// pkg -> list of rendered declarations
const files = new Map();
const notes = { notHoisted: [], divergent: [], splitAcrossPackages: [] };

for (const g of globals) {
  if (NOT_HOISTED.has(g.name)) {
    notes.notHoisted.push(g.name);
    continue;
  }
  const variants = g.variants.map((v) => ({ ...v, envs: envsOf(v.scopes) }));
  if (variants.length > 1) notes.divergent.push(g.name);

  const pkgs = new Set();
  for (const v of variants) {
    const pkg = packageFor(g.name, v.envs);
    pkgs.add(pkg);
    const dec = envDecorator(v.envs);
    const lines = [];
    if (dec) lines.push(dec);
    lines.push(`@js("${g.name}")`);
    for (const sig of v.text.split("\n")) lines.push(hoist(g.name, sig));
    if (!files.has(pkg)) files.set(pkg, []);
    files.get(pkg).push({ name: g.name, envs: v.envs, body: lines.join("\n") });
  }
  if (pkgs.size > 1) notes.splitAcrossPackages.push(`${g.name} -> ${[...pkgs].join(", ")}`);
}

// Turn one class or interface member into its top-level form. A method drops the
// `self` receiver and becomes a `fn`. A `readonly` property becomes a `val` and
// anything else a `var`.
function hoist(name, sig) {
  const method = new RegExp(`^${name}(<[^(]*)?\\(`).exec(sig);
  if (method) {
    let rest = sig.slice(method[0].length);
    rest = rest.replace(/^(mut |)self(, )?/, "");
    return `export declare fn ${name}${method[1] ?? ""}(${rest}`;
  }
  const ro = new RegExp(`^readonly ${name}\\s*:\\s*(.*)$`).exec(sig);
  if (ro) return `export declare val ${name}: ${ro[1]}`;
  const prop = new RegExp(`^${name}\\??\\s*:\\s*(.*)$`).exec(sig);
  if (prop) return `export declare var ${name}: ${prop[1]}`;
  return `// UNCONVERTED: ${sig}`;
}

const ORDER = ["core", "dom", "worker", "canvas", "fetch", "cache", "crypto",
  "indexeddb", "performance", "storage"];

for (const pkg of ORDER) {
  const decls = files.get(pkg);
  if (!decls) continue;
  decls.sort((a, b) => a.name.localeCompare(b.name));
  const header = `// web:${pkg} — globals hoisted out of the scope classes.\n` +
    `//\n// Generated by the analysis in README.md. Planning artifact, not a\n` +
    `// generated-tree file. ${decls.length} declarations.\n`;
  writeFileSync(`${OUT}/${pkg}.esc`, header + "\n" + decls.map((d) => d.body).join("\n\n") + "\n");
}

console.log("files:", [...files.keys()].map((k) => `${k}=${files.get(k).length}`).join(" "));
console.log("not hoisted:", notes.notHoisted.join(", "));
console.log("divergent:", notes.divergent.length, notes.divergent.join(", "));
console.log("split across packages:", notes.splitAcrossPackages.join(" | "));
writeFileSync(`${S}/notes.json`, JSON.stringify(notes, null, 2));
