// Appends the merged Navigator to core.esc: one declaration serving every
// environment, with `@env("window")` on the parts only a page has.
//
// Run after emit.mjs, which writes core.esc. Running emit.mjs again drops this
// section, so the two go together.
//
// The committed tree cannot show this yet. Its `Navigator` extends
// `NavigatorAutomationInformation` alone, having dropped the other ten mixins,
// which is the bug in #1648. So the mixin list comes from TypeScript and the
// member text from the tree's own mixin declarations, which are all present.
//
//   WORK=/tmp/hoist OUT=.. node merge-navigator.mjs

import { readFileSync, writeFileSync } from "node:fs";

const S = process.env.WORK ?? ".";
const OUT = process.env.OUT ?? "..";
const TS = process.env.TS ??
  "../../../../node_modules/.pnpm/typescript@5.8.2/node_modules/typescript/lib";

const dom = readFileSync(`${TS}/lib.dom.d.ts`, "utf8").split("\n");
const worker = readFileSync(`${TS}/lib.webworker.d.ts`, "utf8").split("\n");
const esc = readFileSync(`${S}/dom.esc`, "utf8").split("\n");

function supertypes(lines, name) {
  const at = lines.findIndex((l) => new RegExp(`^interface ${name}\\b`).test(l));
  const ext = at < 0 ? null : /extends ([^{]+)\{/.exec(lines[at]);
  return ext ? ext[1].split(",").map((s) => s.trim().replace(/<.*/, "")) : [];
}

function ownMembers(lines, name, pattern) {
  const at = lines.findIndex((l) => pattern.test(l));
  if (at < 0) throw new Error(`not found: ${name}`);
  const out = [];
  for (let i = at + 1; i < lines.length && lines[i] !== "}"; i++) {
    if (/^\s*(\/\*\*|\*|\*\/)/.test(lines[i])) continue;
    const m = /^    (?:readonly |static )?([A-Za-z_$][\w$]*)\s*[?(<:]/.exec(lines[i]);
    if (m) out.push({ name: m[1], text: lines[i] });
  }
  return out;
}

const windowMixins = supertypes(dom, "Navigator");
const workerMixins = supertypes(worker, "WorkerNavigator");
const sharedMixins = windowMixins.filter((m) => workerMixins.includes(m));
const windowOnlyMixins = windowMixins.filter((m) => !workerMixins.includes(m));

const ownWindow = ownMembers(dom, "Navigator", /^interface Navigator extends/).map((m) => m.name);
const ownWorker = ownMembers(worker, "WorkerNavigator", /^interface WorkerNavigator extends/)
  .map((m) => m.name);
const ownWindowOnly = ownWindow.filter((n) => !ownWorker.includes(n));

const mixinMembers = (m) =>
  ownMembers(esc, m, new RegExp(`^export declare interface ${m} `));
const coveredByMixins = windowOnlyMixins.reduce((n, m) => n + mixinMembers(m).length, 0);

const ownLines = ownMembers(esc, "Navigator", /^export declare class Navigator extends/)
  .filter((m) => ownWindowOnly.includes(m.name)).length;

const header = [
  "// ---------------------------------------------------------------------------",
  "// The merged Navigator, and the mixins it is built from.",
  "//",
  "// `navigator` above is typed against this. It sits in web:core because the",
  "// global exists in every environment, which means web:core imports the",
  "// packages the window-only members name.",
  "//",
  `// WorkerNavigator is a strict subset of Navigator: ${workerMixins.length} of the ${windowMixins.length} mixins`,
  `// and ${ownWorker.length} of the ${ownWindow.length} own members, with no member they share differing in`,
  "// signature and nothing worker-only. So one declaration serves every",
  "// environment and WorkerNavigator goes away.",
  "//",
  `// The environment lands in two places. The ${windowOnlyMixins.length} mixins only a page has carry`,
  `// \`@env("window")\` where they are declared, which covers ${coveredByMixins} members without`,
  `// annotating any of them. The other ${ownWindowOnly.length} are Navigator's own and are tagged`,
  `// individually, across ${ownLines} lines because two of them are overload sets.`,
  "//",
  "// Doc comments are dropped here for readability. Planning artifact, generated",
  "// by scripts/merge-navigator.mjs.",
  "",
];

const out = [...header];
for (const m of windowMixins) {
  if (!sharedMixins.includes(m)) out.push('@env("window")');
  out.push(`export declare interface ${m} {`);
  for (const mem of mixinMembers(m)) out.push(mem.text);
  out.push("}", "");
}

out.push('@js("Navigator")');
out.push("export declare class Navigator implements");
out.push(`    ${sharedMixins.join(", ")},`);
out.push(`    ${windowOnlyMixins.join(", ")} {`);
for (const mem of ownMembers(esc, "Navigator", /^export declare class Navigator extends/)) {
  if (mem.name === "prototype" || mem.name === "constructor") continue;
  if (ownWindowOnly.includes(mem.name)) out.push('    @env("window")');
  out.push(mem.text);
}
out.push("}");

const corePath = `${OUT}/core.esc`;
const core = readFileSync(corePath, "utf8").replace(/\n+$/, "\n");
writeFileSync(corePath, core + "\n" + out.join("\n") + "\n");
console.log(
  `mixins: ${sharedMixins.length} shared, ${windowOnlyMixins.length} window-only covering ${coveredByMixins} members; ` +
  `own: ${ownWindow.length - ownWindowOnly.length} shared, ${ownWindowOnly.length} window-only`,
);
