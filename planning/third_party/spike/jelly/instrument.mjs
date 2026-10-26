// instrument.mjs copies a work directory's drivers and node_modules into
// <work-dir>/instrumented, rewriting every JavaScript file so that running a
// driver records which call site entered which function. tracer.cjs holds the
// runtime the rewritten code calls, and compare.mjs diffs what it records
// against Jelly's static call graph.
//
// Two rewrites cover it:
//
//   - A call or `new` expression becomes `__dyn.r(__dyn.p(site), call)`. The
//     first argument pushes the site before the call's own operands run, so a
//     call nested in them pushes and pops above it, and the callee finds the
//     site on top when its body starts. The second pops it.
//   - Every function body starts with `__dyn.f(fn)`, which records an edge
//     from the site on top to the function.
//
// A site and a function are named the way Jelly names them, as the file and
// the start and end positions of the node, so the two graphs meet on equal
// keys.
//
// Usage: node instrument.mjs <work-dir>

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const [workDir] = process.argv.slice(2);
const require = createRequire(path.join(workDir, "package.json"));
const babel = require("@babel/core");

const out = path.join(workDir, "instrumented");
const skip = new Set(["@cs-au-dk", "typescript", "@babel", "@types", ".bin", ".package-lock.json"]);

function copy(from, to) {
  fs.mkdirSync(to, { recursive: true });
  for (const entry of fs.readdirSync(from, { withFileTypes: true })) {
    const src = path.join(from, entry.name);
    const dst = path.join(to, entry.name);
    if (entry.isDirectory()) copy(src, dst);
    else if (entry.isFile()) fs.copyFileSync(src, dst);
  }
}

fs.mkdirSync(out, { recursive: true });
fs.writeFileSync(path.join(out, "package.json"), fs.readFileSync(path.join(workDir, "package.json")));
copy(path.join(workDir, "drivers"), path.join(out, "drivers"));
for (const entry of fs.readdirSync(path.join(workDir, "node_modules"), { withFileTypes: true })) {
  if (skip.has(entry.name)) continue;
  const src = path.join(workDir, "node_modules", entry.name);
  if (entry.isDirectory()) copy(src, path.join(out, "node_modules", entry.name));
}

const loc = (file, node) =>
  `${file}:${node.loc.start.line}:${node.loc.start.column + 1}:${node.loc.end.line}:${node.loc.end.column + 1}`;

// The plugin records positions before it rewrites anything, so a site and a
// function keep the coordinates of the file Jelly read.
function plugin(file) {
  return ({ types: t }) => {
    const dyn = (name) => t.memberExpression(t.memberExpression(t.identifier("globalThis"), t.identifier("__dyn")), t.identifier(name));
    const enter = (node) => t.callExpression(dyn("f"), [t.stringLiteral(loc(file, node))]);
    return {
      visitor: {
        Function(p) {
          const node = p.node;
          if (!node.loc || node._dyn) return;
          node._dyn = true;
          if (t.isBlockStatement(node.body)) {
            node.body.body.unshift(t.expressionStatement(enter(node)));
          } else {
            node.body = t.sequenceExpression([enter(node), node.body]);
          }
        },
        "CallExpression|NewExpression|OptionalCallExpression": {
          exit(p) {
            const node = p.node;
            if (!node.loc || node._dyn) return;
            if (t.isImport(node.callee) || t.isSuper(node.callee)) return;
            node._dyn = true;
            const push = t.callExpression(dyn("p"), [t.stringLiteral(loc(file, node))]);
            const wrapped = t.callExpression(dyn("r"), [push, node]);
            wrapped._dyn = true;
            push._dyn = true;
            p.replaceWith(wrapped);
            p.skip();
          },
        },
      },
    };
  };
}

let done = 0;
let failed = 0;
function rewrite(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      rewrite(full);
      continue;
    }
    if (!/\.(c|m)?js$/.test(entry.name)) continue;
    const file = path.relative(out, full);
    const code = fs.readFileSync(full, "utf8");
    try {
      const result = babel.transformSync(code, {
        filename: full,
        babelrc: false,
        configFile: false,
        sourceType: "unambiguous",
        compact: false,
        retainLines: false,
        parserOpts: { allowReturnOutsideFunction: true, errorRecovery: false },
        plugins: [plugin(file)],
      });
      fs.writeFileSync(full, result.code);
      done++;
    } catch (err) {
      failed++;
      console.error(`not instrumented: ${file}: ${err.message.split("\n")[0]}`);
    }
  }
}
rewrite(path.join(out, "drivers"));
rewrite(path.join(out, "node_modules"));
console.error(`instrumented ${done} files, ${failed} left as they were`);
