// Stage 1 of the WebIDL -> Escalier pipeline.
//
// Reads the curated WebIDL that @webref/idl bundles for every W3C/WHATWG
// spec, parses each spec with webidl2, and writes one JSON artifact per
// spec into the output directory. The artifacts are a small, stable
// intermediate representation (IR) consumed by the Go converter in
// internal/webidl. Keeping the IR narrow means the Go side never has to
// know anything about webidl2's AST shape, and the IR can be regenerated
// whenever @webref/idl is bumped.
//
// Usage:
//   node extract.mjs <out-dir> [spec-name ...]
//
// With no spec names, every spec in @webref/idl is emitted. With one or
// more spec names (e.g. `dom`, `html`), only those are emitted — handy
// for prototyping against a small slice.
//
// The IR captures exactly the signals the converter needs to answer the
// three questions the .d.ts source can't:
//   - readonly attribute        -> getter-only (non-mutating receiver)
//   - [SameObject]              -> the value is owned by the host; the
//                                  returned reference borrows from `self`
//                                  (a lifetime tie). Dropped by the TS
//                                  .d.ts generator.
//   - [NewObject]               -> a freshly allocated, caller-owned value
//                                  (no lifetime tie to the receiver).
//                                  Also dropped by the .d.ts generator.
//   - [PutForwards=x]           -> a readonly attribute whose assignment
//                                  forwards to member x (a hidden setter).

import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import * as idl from "@webref/idl";

// convType turns a webidl2 idlType node into the IR's structured type.
// The IR type is recursive so the Go side can map generics and unions
// without re-parsing a flattened string.
function convType(t) {
  if (!t) {
    return { union: false, name: "undefined", args: [], nullable: false };
  }
  const nullable = !!t.nullable;
  if (t.union) {
    return {
      union: true,
      name: "",
      args: t.idlType.map(convType),
      nullable,
    };
  }
  if (t.generic) {
    const inner = Array.isArray(t.idlType) ? t.idlType : [t.idlType];
    return {
      union: false,
      name: t.generic,
      args: inner.map(convType),
      nullable,
    };
  }
  // Base type: t.idlType is a plain string here.
  return { union: false, name: t.idlType, args: [], nullable };
}

// extAttrNames returns the set of extended-attribute names on a node, plus
// the PutForwards target if present (the one ext-attr that carries a value
// we care about).
function extInfo(node) {
  const names = new Set();
  let putForwards = null;
  for (const ea of node.extAttrs ?? []) {
    names.add(ea.name);
    if (ea.name === "PutForwards" && ea.rhs) {
      putForwards = ea.rhs.value;
    }
  }
  return { names, putForwards };
}

function convMember(m) {
  switch (m.type) {
    case "attribute": {
      const { names, putForwards } = extInfo(m);
      return {
        kind: "attribute",
        name: m.name,
        type: convType(m.idlType),
        readonly: !!m.readonly,
        static: m.special === "static",
        sameObject: names.has("SameObject"),
        newObject: names.has("NewObject"),
        putForwards,
      };
    }
    case "operation": {
      if (!m.name) {
        return null; // stringifiers / unnamed specials: skip for the prototype.
      }
      const { names } = extInfo(m);
      const retExt = extInfo({ extAttrs: m.idlType?.extAttrs ?? [] });
      return {
        kind: "operation",
        name: m.name,
        static: m.special === "static",
        special: m.special ?? "",
        newObject: names.has("NewObject") || retExt.names.has("NewObject"),
        return: convType(m.idlType),
        args: (m.arguments ?? []).map((a) => ({
          name: a.name,
          type: convType(a.idlType),
          optional: !!a.optional,
          variadic: !!a.variadic,
        })),
      };
    }
    case "constructor": {
      return {
        kind: "constructor",
        args: (m.arguments ?? []).map((a) => ({
          name: a.name,
          type: convType(a.idlType),
          optional: !!a.optional,
          variadic: !!a.variadic,
        })),
      };
    }
    case "const":
      return {
        kind: "const",
        name: m.name,
        type: convType(m.idlType),
        value: m.value?.value ?? null,
      };
    default:
      // iterable/maplike/setlike/field and other shapes are out of scope
      // for the prototype; the converter notes them as TODO.
      return { kind: "unsupported", memberType: m.type };
  }
}

function convInterface(node) {
  return {
    name: node.name,
    inheritance: node.inheritance ?? null,
    partial: !!node.partial,
    mixin: node.type === "interface mixin",
    members: (node.members ?? []).map(convMember).filter(Boolean),
  };
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length === 0) {
    console.error("usage: node extract.mjs <out-dir> [spec-name ...]");
    process.exit(1);
  }
  const outDir = args[0];
  const only = new Set(args.slice(1));
  await mkdir(outDir, { recursive: true });

  const parsedBySpec = await idl.parseAll();
  let specCount = 0;
  let ifaceCount = 0;

  for (const [spec, ast] of Object.entries(parsedBySpec)) {
    if (only.size > 0 && !only.has(spec)) {
      continue;
    }
    const interfaces = [];
    const includes = [];
    for (const node of ast) {
      if (node.type === "interface" || node.type === "interface mixin") {
        interfaces.push(convInterface(node));
      } else if (node.type === "includes") {
        includes.push({ target: node.target, mixin: node.includes });
      }
      // dictionaries / enums / typedefs / callbacks are out of scope for
      // the prototype.
    }
    if (interfaces.length === 0) {
      continue;
    }
    const artifact = { spec, interfaces, includes };
    await writeFile(
      join(outDir, `${spec}.json`),
      JSON.stringify(artifact, null, 2) + "\n",
    );
    specCount++;
    ifaceCount += interfaces.length;
  }

  console.error(
    `wrote ${specCount} spec artifacts (${ifaceCount} interfaces) to ${outDir}`,
  );
}

await main();
