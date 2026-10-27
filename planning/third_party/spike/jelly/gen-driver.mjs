// gen-driver.mjs writes a Jelly driver for one package from the package's
// `.d.ts`. Jelly analyzes a whole program, so a library needs a client that
// constructs its classes and calls its methods before `this` inside them is an
// instance rather than an unknown value. The driver is that client.
//
// The driver imports the package's public entry, then:
//
//   - calls every exported function, and every method on what it returns;
//   - constructs every exported class and calls every method on the instance;
//   - calls every function-valued property of an exported object.
//
// It stops two levels below the export. Each argument is a placeholder of the
// parameter's declared type, and a function-typed argument is a fresh arrow, so
// a callback the library calls has one known target.
//
// Usage: node gen-driver.mjs <work-dir> <package> <out-base> [export,...]
//
// The optional list restricts the driver to those top-level exports. A package
// whose full surface is too large for Jelly to analyze in one run, as zod's is,
// is driven through the exports a question needs.
//
// <work-dir> holds node_modules with the package, its types, and typescript.
// The driver is written to <out-base>.cjs when the types use `export =` and
// to <out-base>.mjs otherwise, and the script prints the path it wrote.

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const [workDir, pkg, outBase, onlyList] = process.argv.slice(2);
const only = onlyList ? new Set(onlyList.split(",")) : null;
const ts = createRequire(path.join(workDir, "package.json"))("typescript");

const probe = path.join(workDir, "__probe.ts");
const probeText = `import * as M from ${JSON.stringify(pkg)};\nexport { M };\n`;
const host = ts.createCompilerHost({});
const readFile = host.readFile;
host.readFile = (f) => (f === probe ? probeText : readFile(f));
const fileExists = host.fileExists;
host.fileExists = (f) => f === probe || fileExists(f);
const program = ts.createProgram(
  [probe],
  {
    module: ts.ModuleKind.NodeNext,
    moduleResolution: ts.ModuleResolutionKind.NodeNext,
    target: ts.ScriptTarget.ESNext,
    strict: true,
    skipLibCheck: true,
    noEmit: true,
  },
  host,
);
const checker = program.getTypeChecker();
const importDecl = program.getSourceFile(probe).statements[0];
const modSym = checker.getSymbolAtLocation(importDecl.moduleSpecifier);
if (!modSym) throw new Error(`cannot resolve types for ${pkg}`);

const exportEquals = modSym.exports?.get("export=");
const commonjs = !!exportEquals;

const lines = [];
let counter = 0;
const seenTypes = new Set();
const MAX_DEPTH = 2;

const isPrim = (t) =>
  !!(t.flags & (ts.TypeFlags.StringLike | ts.TypeFlags.NumberLike | ts.TypeFlags.BooleanLike | ts.TypeFlags.BigIntLike | ts.TypeFlags.ESSymbolLike));

// placeholder renders a value of type t for an argument. An object type gets
// a literal with a value for each property, so a constructor that requires an
// option, such as lru-cache's `max`, does not throw before the code under it
// runs. A run under tracer.cjs only sees code that runs.
const MAX_LITERAL_PROPS = 40;
function placeholder(t, depth = 0) {
  if (!t) return "undefined";
  if (t.isUnion()) {
    const nonNull = t.types.filter((u) => !(u.flags & (ts.TypeFlags.Undefined | ts.TypeFlags.Null)));
    const fn = nonNull.find((u) => u.getCallSignatures().length > 0);
    if (fn) return placeholder(fn, depth);
    // A union of an options object and an instance, as lru-cache's
    // `Options | LRUCache`, takes the options object.
    const plain = nonNull.find((u) => !(u.getSymbol()?.flags & ts.SymbolFlags.Class));
    if (plain) return placeholder(plain, depth);
    if (nonNull.length) return placeholder(nonNull[0], depth);
    return "undefined";
  }
  if (t.flags & ts.TypeFlags.StringLike) return t.isStringLiteral() ? JSON.stringify(t.value) : '"x"';
  if (t.flags & ts.TypeFlags.NumberLike) return t.isNumberLiteral() ? String(t.value) : "1";
  if (t.flags & ts.TypeFlags.BooleanLike) return "true";
  if (t.flags & ts.TypeFlags.BigIntLike) return "1n";
  if (t.getCallSignatures().length > 0) return "(...a) => a[0]";
  if (checker.isArrayType?.(t) || checker.isTupleType?.(t)) {
    const el = checker.getTypeArguments(t)[0];
    return depth < 2 && el ? `[${placeholder(el, depth + 1)}]` : "[{}]";
  }
  const name = t.getSymbol()?.getName();
  if (name === "Date") return "new Date()";
  if (name === "Map") return "new Map()";
  if (name === "Set") return "new Set()";
  if (name === "Uint8Array") return "new Uint8Array(16)";
  if (name === "RegExp") return "/x/";
  if (depth >= 2 || !(t.flags & ts.TypeFlags.Object)) return "{}";
  const props = checker
    .getPropertiesOfType(t)
    .filter((p) => !/^(_|#|__@)/.test(p.getName()))
    .slice(0, MAX_LITERAL_PROPS);
  const fields = props.map((p) => {
    const pt = checker.getTypeOfSymbolAtLocation(p, p.valueDeclaration ?? importDecl);
    return `${JSON.stringify(p.getName())}: ${placeholder(pt, depth + 1)}`;
  });
  return fields.length ? `{ ${fields.join(", ")} }` : "{}";
}

function argsFor(sig) {
  return sig
    .getParameters()
    .map((p) => {
      const decl = p.valueDeclaration;
      const t = checker.getTypeOfSymbolAtLocation(p, decl ?? importDecl);
      if (decl && ts.isParameter(decl) && decl.dotDotDotToken) {
        const el = checker.getIndexTypeOfType?.(t, ts.IndexKind.Number) ?? undefined;
        return placeholder(el);
      }
      return placeholder(t);
    })
    .join(", ");
}

const access = (expr, name) => (/^[A-Za-z_$][\w$]*$/.test(name) ? `${expr}.${name}` : `${expr}[${JSON.stringify(name)}]`);

// exercise emits calls on expr, a value of type t.
function exercise(expr, t, depth) {
  if (depth > MAX_DEPTH || !t || isPrim(t)) return;
  const ctor = t.getConstructSignatures()[0];
  if (ctor) {
    const v = `v${counter++}`;
    lines.push(`let ${v}; try { ${v} = new ${expr}(${argsFor(ctor)}); } catch {}`);
    exerciseMembers(v, checker.getReturnTypeOfSignature(ctor), depth + 1);
  }
  const call = t.getCallSignatures()[0];
  if (call) {
    const r = `r${counter++}`;
    lines.push(`let ${r}; try { ${r} = ${expr}(${argsFor(call)}); } catch {}`);
    const ret = checker.getReturnTypeOfSignature(call);
    if (!isPrim(ret)) exerciseMembers(r, ret, depth + 1);
  }
  // Below the module itself, a plain function's own properties, such as
  // lodash's `bind.placeholder`, are metadata rather than API, so only a
  // class's static side is walked. A module exported as a function, as
  // express is, keeps its properties, since they are the rest of its API.
  if (ctor || !call || depth === 0) exerciseMembers(expr, t, depth, !!ctor);
}

// A returned object with this many members is a facade over the whole
// package, such as lodash's chain wrapper. Walking it repeats the package
// through one object and multiplies the analysis without adding a target.
const MAX_MEMBERS = 150;

// exerciseMembers calls every function-valued property of expr and reads
// every accessor.
function exerciseMembers(expr, t, depth, isClassValue = false) {
  if (depth > MAX_DEPTH || !t || isPrim(t)) return;
  // A generic type has one type per instantiation, so the type's own symbol
  // is the key. One exercise per declared type is enough for a static
  // analysis that merges calling contexts anyway.
  const key = t.getSymbol() ?? t.aliasSymbol ?? t;
  if (depth > 0 && seenTypes.has(key)) return;
  seenTypes.add(key);
  // Methods that tear state down, such as `off` or `clear`, run last, so
  // the state the others build is still there when a reader runs.
  const teardown = /^(remove|off|delete|clear|dispose|unsubscribe|reset|destroy|close|complete|error|abort|purge)/i;
  const props = [...checker.getPropertiesOfType(t)].sort((a, b) => teardown.test(a.getName()) - teardown.test(b.getName()));
  if (depth > 0 && props.length > MAX_MEMBERS) return;
  // Approximate interpretation runs the driver, so a method that reads state
  // another method writes, as `emit` reads what `on` registered, sees it only
  // if it runs afterwards. Calling an instance's methods a second time gives
  // every method a turn after every other.
  const repeat = [];
  for (const p of props) {
    const name = p.getName();
    if (name.startsWith("_") || name.startsWith("#") || name.startsWith("__@") || name === "prototype" || name === "constructor") continue;
    if (isClassValue && name === "prototype") continue;
    const decl = p.valueDeclaration ?? p.declarations?.[0];
    // A member TypeScript's own lib declares, such as Array.prototype.map on
    // a returned array, is not the package's code.
    if (decl && program.isSourceFileDefaultLibrary(decl.getSourceFile())) continue;
    const pt = checker.getTypeOfSymbolAtLocation(p, decl ?? importDecl);
    const target = access(expr, name);
    if (decl && (ts.isGetAccessorDeclaration(decl) || p.flags & ts.SymbolFlags.GetAccessor)) {
      lines.push(`try { void ${target}; } catch {}`);
      continue;
    }
    const sig = pt.getCallSignatures()[0];
    if (sig && !pt.getConstructSignatures().length) {
      const r = `r${counter++}`;
      lines.push(`let ${r}; try { ${r} = ${target}(${argsFor(sig)}); } catch {}`);
      if (depth > 0) repeat.push(`try { ${target}(${argsFor(sig)}); } catch {}`);
      if (depth + 1 <= MAX_DEPTH) {
        const ret = checker.getReturnTypeOfSignature(sig);
        if (!isPrim(ret)) exerciseMembers(r, ret, depth + 1);
      }
    } else if (pt.getConstructSignatures().length && depth === 0) {
      exercise(target, pt, depth + 1);
    } else if (depth === 0 && !isPrim(pt)) {
      // An exported object, such as express's `response` prototype, is
      // the receiver its methods are written against.
      exerciseMembers(target, pt, depth + 1);
    }
  }
  lines.push(...repeat);
}

const head = commonjs ? `const M = require(${JSON.stringify(pkg)});` : `import * as M from ${JSON.stringify(pkg)};`;
const modType = commonjs ? checker.getTypeOfSymbol(checker.resolveExternalModuleSymbol(modSym)) : undefined;

if (commonjs) {
  exercise("M", modType, 0);
} else {
  const exports = checker.getExportsOfModule(modSym);
  // A default export beside named ones is usually the whole package again, as
  // lodash's chainable `_` is. Exercising it repeats every named export
  // through one object and multiplies the analysis without adding a target.
  const isValue = (e) => !!((e.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(e) : e).flags & ts.SymbolFlags.Value);
  const named = exports.some((e) => e.getName() !== "default" && isValue(e));
  for (const ex of exports) {
    if (ex.getName().startsWith("_")) continue;
    if (named && ex.getName() === "default") continue;
    if (only && !only.has(ex.getName())) continue;
    let sym = ex;
    if (sym.flags & ts.SymbolFlags.Alias) sym = checker.getAliasedSymbol(sym);
    if (!(sym.flags & ts.SymbolFlags.Value)) continue;
    const t = checker.getTypeOfSymbol(sym);
    exercise(access("M", ex.getName()), t, 0);
  }
}

const outFile = outBase + (commonjs ? ".cjs" : ".mjs");
fs.writeFileSync(outFile, [`// Generated by gen-driver.mjs for ${pkg}. Do not edit.`, head, ...lines, ""].join("\n"));
console.log(outFile);
console.error(`${pkg}: ${lines.length} statements`);
