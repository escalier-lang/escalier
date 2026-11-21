// lower.mjs is the throwaway front end of the third-party source-analysis
// spike. It loads one package with the TypeScript compiler API, lowers every
// function body it finds to the control-flow IR that internal/ecma262/cfg.go
// reads, and writes two files:
//
//   cfg.json   the lowered functions, in the cfg.json schema
//   meta.json  the package's API members, their parameter types, and every
//              call the lowering could not resolve, with the reason
//
// Usage: node lower.mjs <typescript-module-dir> <config.json> <package> <out-dir>
//
// See planning/third_party/spike_findings.md for what the spike measures.

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const [tsDir, configPath, pkgName, outDir] = process.argv.slice(2);
const ts = createRequire(path.join(tsDir, "noop.js"))("typescript");
const config = JSON.parse(fs.readFileSync(configPath, "utf8"))[pkgName];
const root = path.resolve(path.dirname(configPath), config.root);

// ---------------------------------------------------------------------------
// Program

const options = {
  allowJs: true,
  checkJs: false,
  noEmit: true,
  target: ts.ScriptTarget.ESNext,
  module: ts.ModuleKind.ESNext,
  moduleResolution: ts.ModuleResolutionKind.Bundler,
  skipLibCheck: true,
  types: [],
  lib: ["lib.esnext.d.ts", "lib.dom.d.ts"],
  strict: true,
  noResolve: false,
  maxNodeModuleJsDepth: 0,
};
const roots = config.entries.map((e) => path.join(root, e));
const program = ts.createProgram(roots, options);
const checker = program.getTypeChecker();

const isPackageFile = (sf) =>
  sf.fileName.startsWith(root + path.sep) &&
  !sf.fileName.includes(`${path.sep}node_modules${path.sep}`) &&
  !/[\\/](tests?|__tests__|spec)[\\/]|\.test\.|\.spec\./.test(sf.fileName);
const isLibFile = (sf) => program.isSourceFileDefaultLibrary(sf);
const packageFiles = program.getSourceFiles().filter(isPackageFile);
const implFiles = packageFiles.filter((sf) => !sf.isDeclarationFile);

const rel = (f) => path.relative(root, f);
const stemOf = (f) => rel(f).replace(/\.d\.[cm]?ts$|\.[cm]?[jt]sx?$/, "");

// ---------------------------------------------------------------------------
// Naming

const isFunctionLike = (n) =>
  ts.isFunctionDeclaration(n) ||
  ts.isFunctionExpression(n) ||
  ts.isArrowFunction(n) ||
  ts.isMethodDeclaration(n) ||
  ts.isConstructorDeclaration(n) ||
  ts.isGetAccessorDeclaration(n) ||
  ts.isSetAccessorDeclaration(n);

function propName(name) {
  if (!name) return undefined;
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name) || ts.isStringLiteral(name) || ts.isNumericLiteral(name))
    return name.text;
  return undefined;
}

// accessText renders a property-access chain such as `X.prototype.m` or
// `module.exports.m`, or returns undefined for anything computed.
function accessText(e) {
  if (ts.isIdentifier(e)) return e.text;
  if (e.kind === ts.SyntaxKind.ThisKeyword) return "this";
  if (ts.isPropertyAccessExpression(e)) {
    const head = accessText(e.expression);
    return head && `${head}.${e.name.text}`;
  }
  return undefined;
}

// qualName names a function the way its declaration does, so a `.d.ts`
// declaration and its JavaScript implementation meet on the same key.
function qualName(fn) {
  const p = fn.parent;
  const classOf = (c) => (c.name ? c.name.text : "default");
  if (ts.isFunctionDeclaration(fn)) return fn.name ? fn.name.text : "default";
  if (ts.isMethodDeclaration(fn) || ts.isConstructorDeclaration(fn) || ts.isGetAccessorDeclaration(fn) || ts.isSetAccessorDeclaration(fn)) {
    const member = ts.isConstructorDeclaration(fn) ? "constructor" : propName(fn.name);
    if (ts.isClassLike(p)) return `${classOf(p)}.${member}`;
    if (ts.isObjectLiteralExpression(p)) {
      const holder = objectHolderName(p);
      return holder ? `${holder}.${member}` : undefined;
    }
  }
  if (ts.isFunctionExpression(fn) || ts.isArrowFunction(fn)) {
    if (ts.isVariableDeclaration(p) && ts.isIdentifier(p.name)) return p.name.text;
    if (ts.isPropertyDeclaration(p) && ts.isClassLike(p.parent)) return `${classOf(p.parent)}.${propName(p.name)}`;
    if (ts.isPropertyAssignment(p) && ts.isObjectLiteralExpression(p.parent)) {
      const holder = objectHolderName(p.parent);
      return holder ? `${holder}.${propName(p.name)}` : undefined;
    }
    if (ts.isBinaryExpression(p) && p.operatorToken.kind === ts.SyntaxKind.EqualsToken && p.right === fn) {
      const target = accessText(p.left);
      if (target) {
        return target
          .replace(/^(module\.)?exports\./, "")
          .replace(/\.prototype\./, ".");
      }
    }
    if (ts.isExportAssignment(p)) return "default";
  }
  return undefined;
}

function objectHolderName(obj) {
  const p = obj.parent;
  if (ts.isVariableDeclaration(p) && ts.isIdentifier(p.name)) return p.name.text;
  if (ts.isBinaryExpression(p) && p.right === obj) return accessText(p.left)?.replace(/^(module\.)?exports\.?/, "");
  if (ts.isExportAssignment(p)) return "default";
  return undefined;
}

// Every function-like node in the package's implementation files gets an id.
const fnIds = new Map(); // node -> id
const fnByKey = new Map(); // stem#qual -> node
const fnNodes = [];
const usedIds = new Set();
for (const sf of implFiles) {
  const visit = (n) => {
    if (isFunctionLike(n) && n.body) {
      const q = qualName(n);
      const { line, character } = sf.getLineAndCharacterOfPosition(n.getStart());
      let id = `${stemOf(sf.fileName)}#${q ?? `<anon@${line + 1}:${character + 1}>`}`;
      if (usedIds.has(id)) id = `${id}@${line + 1}:${character + 1}`;
      usedIds.add(id);
      fnIds.set(n, id);
      fnNodes.push(n);
      if (q) {
        const key = `${stemOf(sf.fileName)}#${q}`;
        if (!fnByKey.has(key)) fnByKey.set(key, n);
      }
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
}

// ---------------------------------------------------------------------------
// Resolution

function resolveAlias(sym) {
  let s = sym;
  for (let i = 0; s && s.flags & ts.SymbolFlags.Alias && i < 10; i++) {
    try {
      s = checker.getAliasedSymbol(s);
    } catch {
      break;
    }
  }
  return s;
}

// implOfDecl finds the function body a declaration names. A declaration with a
// body is its own implementation. A `.d.ts` declaration is matched to the
// implementation file with the same stem and the same qualified name.
function implOfDecl(decl) {
  if (!decl) return undefined;
  if (isFunctionLike(decl) && decl.body && fnIds.has(decl)) return decl;
  if ((ts.isVariableDeclaration(decl) || ts.isPropertyDeclaration(decl) || ts.isPropertyAssignment(decl)) && decl.initializer) {
    const init = skipOuter(decl.initializer);
    if (isFunctionLike(init) && fnIds.has(init)) return init;
  }
  if (ts.isBinaryExpression(decl) || ts.isPropertyAccessExpression(decl)) {
    const bin = ts.isBinaryExpression(decl) ? decl : decl.parent;
    if (ts.isBinaryExpression(bin)) {
      const right = skipOuter(bin.right);
      if (isFunctionLike(right) && fnIds.has(right)) return right;
    }
  }
  if (ts.isExportAssignment(decl)) {
    const e = skipOuter(decl.expression);
    if (isFunctionLike(e) && fnIds.has(e)) return e;
  }
  const sf = decl.getSourceFile();
  if (sf.isDeclarationFile && isPackageFile(sf)) {
    let q;
    const p = decl.parent;
    const nm = propName(decl.name);
    if (ts.isFunctionDeclaration(decl)) q = nm ?? "default";
    else if (ts.isConstructorDeclaration(decl) && ts.isClassLike(p)) q = `${p.name?.text}.constructor`;
    else if ((ts.isMethodDeclaration(decl) || ts.isPropertyDeclaration(decl) || ts.isGetAccessorDeclaration(decl) || ts.isSetAccessorDeclaration(decl)) && ts.isClassLike(p))
      q = `${p.name?.text}.${nm}`;
    if (q) return fnByKey.get(`${stemOf(sf.fileName)}#${q}`);
  }
  return undefined;
}

function skipOuter(e) {
  while (ts.isParenthesizedExpression(e) || ts.isAsExpression(e) || ts.isNonNullExpression(e) || ts.isSatisfiesExpression?.(e) || ts.isTypeAssertionExpression(e))
    e = e.expression;
  return e;
}

// builtinKey maps a declaration in TypeScript's own lib files to the canonical
// ECMA-262 name internal/ecma262 keys its facts by.
function builtinKey(decl) {
  const nm = propName(decl.name);
  if (!nm) return undefined;
  const p = decl.parent;
  if (ts.isSourceFile(p)) return nm;
  if (ts.isModuleBlock(p)) return `${p.parent.name.text}.${nm}`;
  if (!ts.isInterfaceDeclaration(p)) return undefined;
  let owner = p.name.text;
  if (owner.endsWith("Constructor")) return `${owner.slice(0, -"Constructor".length)}.${nm}`;
  if (["Math", "JSON", "Atomics"].includes(owner)) return `${owner}.${nm}`;
  owner = owner.replace(/^Readonly(Array|Map|Set)$/, "$1");
  if (/^(Int8|Uint8|Uint8Clamped|Int16|Uint16|Int32|Uint32|Float32|Float64|BigInt64|BigUint64)Array$/.test(owner)) owner = "TypedArray";
  return `${owner}.prototype.${nm}`;
}

// ---------------------------------------------------------------------------
// Types

const primitiveFlags =
  ts.TypeFlags.StringLike | ts.TypeFlags.NumberLike | ts.TypeFlags.BooleanLike | ts.TypeFlags.BigIntLike |
  ts.TypeFlags.ESSymbolLike | ts.TypeFlags.Undefined | ts.TypeFlags.Null | ts.TypeFlags.Void | ts.TypeFlags.Never | ts.TypeFlags.EnumLike;

function isPrimitiveType(t) {
  if (!t) return false;
  if (t.isUnion()) return t.types.every(isPrimitiveType);
  return (t.flags & primitiveFlags) !== 0;
}

function isFunctionType(t) {
  if (!t) return false;
  if (t.isUnion()) return t.types.some(isFunctionType);
  return t.getCallSignatures().length > 0;
}

function typeKind(t) {
  if (!t || t.flags & (ts.TypeFlags.Any | ts.TypeFlags.Unknown)) return "any";
  if (isPrimitiveType(t)) return "prim";
  if (isFunctionType(t)) return "fn";
  return "obj";
}

// ---------------------------------------------------------------------------
// Lowering

const lit = { kind: "lit" };
const v = (name) => ({ kind: "var", var: name });
const UNKNOWN = v("free:?");

const symNames = new Map();
let symCounter = 0;
function nameOf(sym, fallback) {
  if (!sym) return `${fallback}$${symCounter++}`;
  let n = symNames.get(sym);
  if (!n) {
    n = `${sym.name}$${symCounter++}`;
    symNames.set(sym, n);
  }
  return n;
}

const contains = (outer, inner) => inner.pos >= outer.pos && inner.end <= outer.end && inner.getSourceFile() === outer.getSourceFile();

class Lowering {
  constructor(fn, mode) {
    this.fn = fn;
    this.mode = mode; // "impl" binds `this` to param 0; "api" uses ThisExpr
    this.nodes = [];
    this.temps = 0;
    this.unresolved = [];
    this.callbackSites = 0;
    this.params = [];
    this.paramSyms = new Map();
    this.inlined = [];
  }

  temp() {
    return `t$${this.temps++}`;
  }

  emit(node) {
    this.nodes.push(node);
  }

  // The function whose `this` an expression sees, which is the nearest
  // enclosing non-arrow function.
  thisOwner(node) {
    let p = node.parent;
    while (p && !(isFunctionLike(p) && !ts.isArrowFunction(p))) p = p.parent;
    return p;
  }

  lowerThis(node) {
    if (this.thisOwner(node) !== this.fn) return UNKNOWN;
    return this.mode === "impl" ? v("this$") : { kind: "this" };
  }

  run() {
    const fn = this.fn;
    if (this.mode === "impl") this.params.push("this$");
    fn.parameters.forEach((p, i) => {
      const name = ts.isIdentifier(p.name) ? nameOf(checker.getSymbolAtLocation(p.name), p.name.text) : `$p${i}`;
      this.params.push(name);
      if (p.dotDotDotToken) this.variadic = this.params.length - 1;
      if (!ts.isIdentifier(p.name)) this.bindPattern(p.name, v(name));
      // A default value is lowered for its calls and otherwise ignored: the
      // binding keeps the parameter's origin. See spike_findings.md.
      if (p.initializer) this.lowerDiscard(p.initializer);
    });
    if (ts.isBlock(fn.body)) {
      this.lowerStmt(fn.body);
      this.emit({ kind: "return", value: lit });
    } else {
      this.emit({ kind: "return", value: this.lowerExpr(fn.body) });
    }
  }

  // Closures are lowered into the function that creates them. Their own
  // parameters bind to an unknown value and their returns are dropped, while
  // their reads and writes of the enclosing scope are charged as written.
  inline(fn) {
    for (const p of fn.parameters) {
      if (ts.isIdentifier(p.name)) this.emit({ kind: "let", target: nameOf(checker.getSymbolAtLocation(p.name), p.name.text), source: UNKNOWN });
      else this.bindPattern(p.name, UNKNOWN);
      if (p.initializer) this.lowerDiscard(p.initializer);
    }
    const savedInline = this.inlining;
    this.inlining = true;
    if (fn.body) {
      if (ts.isBlock(fn.body)) this.lowerStmt(fn.body);
      else this.lowerDiscard(fn.body);
    }
    this.inlining = savedInline;
  }

  bindPattern(pattern, source) {
    if (ts.isIdentifier(pattern)) {
      this.emit({ kind: "let", target: nameOf(checker.getSymbolAtLocation(pattern), pattern.text), source });
      return;
    }
    for (const el of pattern.elements) {
      if (ts.isOmittedExpression(el)) continue;
      if (el.initializer) this.lowerDiscard(el.initializer);
      this.bindPattern(el.name, { kind: "prop", object: source });
    }
  }

  lowerDiscard(e) {
    this.lowerExpr(e);
  }

  lowerStmt(s) {
    if (!s) return;
    if (ts.isBlock(s) || ts.isSourceFile(s)) {
      s.statements.forEach((x) => this.lowerStmt(x));
    } else if (ts.isVariableStatement(s)) {
      s.declarationList.declarations.forEach((d) => this.lowerVarDecl(d));
    } else if (ts.isExpressionStatement(s)) {
      this.lowerDiscard(s.expression);
    } else if (ts.isReturnStatement(s)) {
      const value = s.expression ? this.lowerExpr(s.expression) : lit;
      if (!this.inlining) this.emit({ kind: "return", value });
    } else if (ts.isIfStatement(s)) {
      this.lowerDiscard(s.expression);
      this.emit({ kind: "branch" });
      this.lowerStmt(s.thenStatement);
      this.lowerStmt(s.elseStatement);
    } else if (ts.isForStatement(s)) {
      if (s.initializer) {
        if (ts.isVariableDeclarationList(s.initializer)) s.initializer.declarations.forEach((d) => this.lowerVarDecl(d));
        else this.lowerDiscard(s.initializer);
      }
      if (s.condition) this.lowerDiscard(s.condition);
      if (s.incrementor) this.lowerDiscard(s.incrementor);
      this.emit({ kind: "branch" });
      this.lowerStmt(s.statement);
    } else if (ts.isForOfStatement(s) || ts.isForInStatement(s)) {
      const coll = this.lowerExpr(s.expression);
      const element = ts.isForOfStatement(s) ? { kind: "prop", object: coll } : lit;
      if (ts.isVariableDeclarationList(s.initializer)) {
        s.initializer.declarations.forEach((d) => this.bindPattern(d.name, element));
      } else {
        this.assignTo(s.initializer, element);
      }
      this.emit({ kind: "branch" });
      this.lowerStmt(s.statement);
    } else if (ts.isWhileStatement(s) || ts.isDoStatement(s)) {
      this.lowerDiscard(s.expression);
      this.emit({ kind: "branch" });
      this.lowerStmt(s.statement);
    } else if (ts.isSwitchStatement(s)) {
      this.lowerDiscard(s.expression);
      this.emit({ kind: "branch" });
      for (const c of s.caseBlock.clauses) {
        if (ts.isCaseClause(c)) this.lowerDiscard(c.expression);
        c.statements.forEach((x) => this.lowerStmt(x));
      }
    } else if (ts.isTryStatement(s)) {
      this.lowerStmt(s.tryBlock);
      if (s.catchClause) {
        if (s.catchClause.variableDeclaration) this.bindPattern(s.catchClause.variableDeclaration.name, UNKNOWN);
        this.lowerStmt(s.catchClause.block);
      }
      this.lowerStmt(s.finallyBlock);
    } else if (ts.isThrowStatement(s)) {
      this.emit({ kind: "throw", value: this.lowerExpr(s.expression) });
    } else if (ts.isLabeledStatement(s)) {
      this.lowerStmt(s.statement);
    } else if (ts.isFunctionDeclaration(s)) {
      if (s.body) this.inline(s);
    } else if (ts.isWithStatement(s)) {
      this.emit({ kind: "opaque", text: ["with statement"] });
      this.lowerStmt(s.statement);
    } else if (
      ts.isEmptyStatement(s) || ts.isBreakStatement(s) || ts.isContinueStatement(s) || ts.isClassDeclaration(s) ||
      ts.isInterfaceDeclaration(s) || ts.isTypeAliasDeclaration(s) || ts.isEnumDeclaration(s) || ts.isImportDeclaration(s) ||
      ts.isExportDeclaration(s) || ts.isModuleDeclaration(s) || ts.isDebuggerStatement(s)
    ) {
      // Nothing a mutation analysis reads. A nested class's members are
      // lowered as functions of their own.
    } else {
      this.emit({ kind: "opaque", text: [`statement ${ts.SyntaxKind[s.kind]}`] });
    }
  }

  lowerVarDecl(d) {
    if (!d.initializer) {
      if (ts.isIdentifier(d.name)) this.emit({ kind: "let", target: nameOf(checker.getSymbolAtLocation(d.name), d.name.text), source: lit });
      return;
    }
    this.bindPattern(d.name, this.lowerExpr(d.initializer));
  }

  // assignTo lowers a write of value into target.
  assignTo(target, value) {
    target = skipOuter(target);
    if (ts.isIdentifier(target)) {
      const sym = checker.getSymbolAtLocation(target);
      if (this.isLocal(sym)) this.emit({ kind: "let", target: this.varName(sym, target.text), source: value });
      // A write to a module-level binding reassigns state the analysis does
      // not track.
      return;
    }
    if (ts.isPropertyAccessExpression(target) || ts.isElementAccessExpression(target)) {
      const object = this.lowerExpr(target.expression);
      if (ts.isElementAccessExpression(target)) this.lowerDiscard(target.argumentExpression);
      this.emit({ kind: "call", callee: "Set", args: [object, lit, value], guard: "plain" });
      return;
    }
    if (ts.isObjectLiteralExpression(target) || ts.isArrayLiteralExpression(target)) {
      const props = ts.isObjectLiteralExpression(target) ? target.properties : target.elements;
      for (const p of props) {
        const inner = ts.isPropertyAssignment(p) ? p.initializer : ts.isShorthandPropertyAssignment(p) ? p.name : ts.isSpreadElement(p) || ts.isSpreadAssignment(p) ? p.expression : p;
        this.assignTo(inner, { kind: "prop", object: value });
      }
      return;
    }
    this.emit({ kind: "opaque", text: [`assignment target ${ts.SyntaxKind[target.kind]}`] });
  }

  isLocal(sym) {
    const decl = sym?.valueDeclaration ?? sym?.declarations?.[0];
    return !!decl && contains(this.fn, decl);
  }

  varName(sym, text) {
    if (this.paramSyms.has(sym)) return this.paramSyms.get(sym);
    return nameOf(sym, text);
  }

  lowerIdent(id) {
    if (id.text === "undefined") return lit;
    const sym = checker.getSymbolAtLocation(id);
    if (!sym) return UNKNOWN;
    if (this.isLocal(sym)) return v(this.varName(sym, id.text));
    const decl = resolveAlias(sym)?.valueDeclaration;
    const impl = decl && implOfDecl(decl);
    if (impl) return v(`fn:${fnIds.get(impl)}`);
    return v(`free:${id.text}`);
  }

  lowerExpr(e) {
    if (!e) return lit;
    e = skipOuter(e);
    const K = ts.SyntaxKind;
    if (ts.isIdentifier(e)) return this.lowerIdent(e);
    if (e.kind === K.ThisKeyword) return this.lowerThis(e);
    if (e.kind === K.SuperKeyword) return this.lowerThis(e);
    if (ts.isLiteralExpression(e) || e.kind === K.TrueKeyword || e.kind === K.FalseKeyword || e.kind === K.NullKeyword || ts.isNoSubstitutionTemplateLiteral(e))
      return lit;
    if (ts.isTemplateExpression(e)) {
      e.templateSpans.forEach((s) => this.lowerDiscard(s.expression));
      return lit;
    }
    if (ts.isPropertyAccessExpression(e) || ts.isElementAccessExpression(e)) {
      const object = this.lowerExpr(e.expression);
      if (ts.isElementAccessExpression(e)) this.lowerDiscard(e.argumentExpression);
      const getter = this.getterOf(e);
      if (getter) {
        const t = this.temp();
        this.emit({ kind: "call", target: t, callee: `impl:${fnIds.get(getter)}`, args: [object], guard: "plain" });
        return v(t);
      }
      return { kind: "prop", object };
    }
    if (ts.isCallExpression(e)) return this.lowerCall(e);
    if (ts.isNewExpression(e)) return this.lowerNew(e);
    if (ts.isTaggedTemplateExpression(e)) {
      if (ts.isTemplateExpression(e.template)) e.template.templateSpans.forEach((s) => this.lowerDiscard(s.expression));
      return this.unresolvedCall(e, "dynamic", [], e.tag);
    }
    if (ts.isObjectLiteralExpression(e)) {
      const args = [];
      for (const p of e.properties) {
        if (ts.isPropertyAssignment(p)) {
          if (ts.isComputedPropertyName(p.name)) this.lowerDiscard(p.name.expression);
          args.push(this.lowerExpr(p.initializer));
        } else if (ts.isShorthandPropertyAssignment(p)) args.push(this.lowerIdent(p.name));
        else if (ts.isSpreadAssignment(p)) args.push({ kind: "prop", object: this.lowerExpr(p.expression) });
        else if (isFunctionLike(p) && p.body) {
          this.inline(p);
          args.push(v(`fn:${fnIds.get(p)}`));
        }
      }
      return { kind: "alloc", args };
    }
    if (ts.isArrayLiteralExpression(e)) {
      return {
        kind: "alloc",
        args: e.elements.map((x) => (ts.isSpreadElement(x) ? { kind: "prop", object: this.lowerExpr(x.expression) } : this.lowerExpr(x))),
      };
    }
    if (ts.isFunctionExpression(e) || ts.isArrowFunction(e)) {
      this.inline(e);
      return v(`fn:${fnIds.get(e)}`);
    }
    if (ts.isClassExpression(e)) return v("fn:class");
    if (ts.isBinaryExpression(e)) return this.lowerBinary(e);
    if (ts.isConditionalExpression(e)) {
      this.lowerDiscard(e.condition);
      return this.either(e.whenTrue, e.whenFalse);
    }
    if (ts.isPrefixUnaryExpression(e) || ts.isPostfixUnaryExpression(e)) {
      if (e.operator === K.PlusPlusToken || e.operator === K.MinusMinusToken) this.assignTo(e.operand, lit);
      else this.lowerDiscard(e.operand);
      return lit;
    }
    if (ts.isDeleteExpression(e)) {
      const target = skipOuter(e.expression);
      if (ts.isPropertyAccessExpression(target) || ts.isElementAccessExpression(target)) {
        const object = this.lowerExpr(target.expression);
        this.emit({ kind: "call", callee: "DeletePropertyOrThrow", args: [object, lit], guard: "plain" });
      }
      return lit;
    }
    if (ts.isTypeOfExpression(e) || ts.isVoidExpression(e)) {
      this.lowerDiscard(e.expression);
      return lit;
    }
    if (ts.isAwaitExpression(e) || ts.isYieldExpression(e)) {
      this.lowerDiscard(e.expression);
      return UNKNOWN;
    }
    if (ts.isSpreadElement(e)) return { kind: "prop", object: this.lowerExpr(e.expression) };
    if (e.kind === K.OmittedExpression) return lit;
    if (ts.isMetaProperty(e)) return UNKNOWN;
    if (ts.isRegularExpressionLiteral(e)) return { kind: "alloc", args: [] };
    this.emit({ kind: "opaque", text: [`expression ${ts.SyntaxKind[e.kind]}`] });
    return UNKNOWN;
  }

  // either joins two values into one name, which is how a value that may be
  // one of several reaches the origin map.
  either(...exprs) {
    const t = this.temp();
    for (const x of exprs) this.emit({ kind: "let", target: t, source: this.lowerExpr(x) });
    return v(t);
  }

  lowerBinary(e) {
    const K = ts.SyntaxKind;
    const op = e.operatorToken.kind;
    if (op === K.EqualsToken) {
      const value = this.lowerExpr(e.right);
      this.assignTo(e.left, value);
      return value;
    }
    if (op >= K.FirstCompoundAssignment && op <= K.LastCompoundAssignment) {
      const logical = op === K.BarBarEqualsToken || op === K.AmpersandAmpersandEqualsToken || op === K.QuestionQuestionEqualsToken;
      const value = logical ? this.either(e.left, e.right) : (this.lowerDiscard(e.right), lit);
      this.assignTo(e.left, value);
      return value;
    }
    if (op === K.BarBarToken || op === K.QuestionQuestionToken || op === K.AmpersandAmpersandToken) return this.either(e.left, e.right);
    if (op === K.CommaToken) {
      this.lowerDiscard(e.left);
      return this.lowerExpr(e.right);
    }
    this.lowerDiscard(e.left);
    this.lowerDiscard(e.right);
    return lit;
  }

  getterOf(access) {
    const sym = checker.getSymbolAtLocation(ts.isPropertyAccessExpression(access) ? access.name : access.argumentExpression);
    const decl = sym?.declarations?.find((d) => ts.isGetAccessorDeclaration(d) && d.body);
    return decl && fnIds.has(decl) ? decl : undefined;
  }

  lowerArgs(args) {
    return (args ?? []).map((a) => this.lowerExpr(a));
  }

  unresolvedCall(e, cat, args, calleeNode) {
    const text = (calleeNode ?? e).getText().slice(0, 80).replace(/\s+/g, " ");
    this.unresolved.push({ cat, text });
    const t = this.temp();
    this.emit({ kind: "call", target: t, callee: `?${cat}:${text}`, args, guard: "plain" });
    return v(t);
  }

  lowerCall(e) {
    const callee = skipOuter(e.expression);
    const K = ts.SyntaxKind;

    if (callee.kind === K.SuperKeyword) {
      const sig = checker.getResolvedSignature(e);
      const ctor = sig?.declaration && implOfDecl(sig.declaration);
      const args = this.lowerArgs(e.arguments);
      if (ctor) {
        this.emit({ kind: "call", callee: `impl:${fnIds.get(ctor)}`, args: [this.lowerThis(e), ...args], guard: "plain" });
        return lit;
      }
      return this.unresolvedCall(e, "super-external", [this.lowerThis(e), ...args], callee);
    }
    if (callee.kind === K.ImportKeyword) {
      this.lowerArgs(e.arguments);
      return UNKNOWN;
    }

    // `f.call(thisArg, ...)` and `f.apply(thisArg, args)` on a function the
    // package defines call that function directly.
    if (ts.isPropertyAccessExpression(callee) && (callee.name.text === "call" || callee.name.text === "apply")) {
      const target = this.calleeImpl(callee.expression);
      if (target) {
        const [thisArg, ...rest] = this.lowerArgs(e.arguments);
        const args = callee.name.text === "call" ? rest : rest.map((r) => ({ kind: "prop", object: r }));
        const t = this.temp();
        this.emit({ kind: "call", target: t, callee: `impl:${fnIds.get(target)}`, args: [thisArg ?? lit, ...args], guard: "plain" });
        return v(t);
      }
    }

    let recv = lit;
    let calleeSymNode = callee;
    if (ts.isPropertyAccessExpression(callee)) {
      recv = this.lowerExpr(callee.expression);
      calleeSymNode = callee.name;
    } else if (ts.isElementAccessExpression(callee)) {
      recv = this.lowerExpr(callee.expression);
      this.lowerDiscard(callee.argumentExpression);
      calleeSymNode = callee.argumentExpression;
    } else if (!ts.isIdentifier(callee)) {
      const fnVal = this.lowerExpr(callee);
      const args = this.lowerArgs(e.arguments);
      if (fnVal.kind === "var" && fnVal.var.startsWith("fn:")) return UNKNOWN; // an immediately invoked closure, already inlined
      return this.unresolvedCall(e, "dynamic", args, callee);
    }
    const args = this.lowerArgs(e.arguments);
    return this.lowerResolvedCall(e, calleeSymNode, recv, args);
  }

  calleeImpl(expr) {
    const sym = checker.getSymbolAtLocation(ts.isPropertyAccessExpression(expr) ? expr.name : expr);
    const s = resolveAlias(sym);
    for (const d of s?.declarations ?? []) {
      const impl = implOfDecl(d);
      if (impl) return impl;
    }
    return undefined;
  }

  lowerResolvedCall(e, nameNode, recv, args) {
    const rawSym = checker.getSymbolAtLocation(nameNode);
    const sym = resolveAlias(rawSym);
    const calleeText = e.expression;

    // A call through a parameter or a local holding a function value.
    if (ts.isIdentifier(nameNode) && sym && this.isLocal(sym)) {
      const decl = sym.valueDeclaration;
      if (decl && ts.isParameter(decl)) {
        if (decl.parent === this.fn) {
          this.callbackSites++;
          this.unresolved.push({ cat: "callback-param", text: calleeText.getText().slice(0, 80) });
          const t = this.temp();
          this.emit({ kind: "call", target: t, callee: this.varName(sym, nameNode.text), args, guard: "plain" });
          return v(t);
        }
        return this.unresolvedCall(e, "closure-param", args, calleeText);
      }
      const impl = decl && implOfDecl(decl);
      if (impl) return UNKNOWN; // a local closure, inlined where it is created
      if (decl && ts.isFunctionDeclaration(decl)) return UNKNOWN;
      return this.unresolvedCall(e, "local-value", args, calleeText);
    }

    // The checker's own pick among overloads and declarations.
    const sig = checker.getResolvedSignature(e);
    const decls = [sig?.declaration, ...(sym?.declarations ?? [])].filter(Boolean);
    for (const d of decls) {
      const impl = implOfDecl(d);
      if (impl) {
        const t = this.temp();
        this.emit({ kind: "call", target: t, callee: `impl:${fnIds.get(impl)}`, args: [recv, ...args], guard: "plain" });
        return v(t);
      }
    }
    const decl = decls.find((d) => !ts.isJSDocSignature?.(d)) ?? decls[0];
    if (!decl) {
      const t = checker.getTypeAtLocation(e.expression);
      return this.unresolvedCall(e, t.flags & ts.TypeFlags.Any ? "untyped" : "no-declaration", [recv, ...args], calleeText);
    }
    const sf = decl.getSourceFile();
    if (isLibFile(sf)) {
      const key = builtinKey(decl);
      if (key) {
        const t = this.temp();
        this.emit({ kind: "call", target: t, callee: `builtin:${key}`, args: [recv, ...args], guard: "plain" });
        return v(t);
      }
      return this.unresolvedCall(e, "builtin-unkeyed", [recv, ...args], calleeText);
    }
    if (isPackageFile(sf)) {
      const kind = sf.isDeclarationFile ? "dts-unjoined" : isFunctionLike(decl) || ts.isPropertyDeclaration(decl) || ts.isVariableDeclaration(decl) ? "package-value" : "interface-dispatch";
      return this.unresolvedCall(e, ts.isMethodSignature(decl) || ts.isPropertySignature(decl) ? "interface-dispatch" : kind, [recv, ...args], calleeText);
    }
    return this.unresolvedCall(e, "external-package", [recv, ...args], calleeText);
  }

  lowerNew(e) {
    const args = this.lowerArgs(e.arguments);
    const obj = { kind: "alloc", args: [] };
    const sig = checker.getResolvedSignature(e);
    const sym = resolveAlias(checker.getSymbolAtLocation(e.expression));
    const decls = [sig?.declaration, ...(sym?.declarations ?? [])].filter(Boolean);
    const t = this.temp();
    this.emit({ kind: "let", target: t, source: obj });
    for (const d of decls) {
      let impl = implOfDecl(d);
      if (!impl && ts.isClassLike(d)) impl = d.members.find((m) => ts.isConstructorDeclaration(m) && m.body);
      if (!impl && ts.isClassLike(d)) return v(t); // a class with no constructor of its own
      if (impl) {
        this.emit({ kind: "call", callee: `impl:${fnIds.get(impl)}`, args: [v(t), ...args], guard: "plain" });
        return v(t);
      }
    }
    const decl = decls[0];
    if (decl && isLibFile(decl.getSourceFile())) return v(t); // a builtin constructor allocates
    this.unresolvedCall(e, decl ? "external-package" : "untyped", [lit, ...args], e.expression);
    return v(t);
  }
}

// ---------------------------------------------------------------------------
// API members

// apiMembers walks the package's entry exports and lists every function and
// method a caller can reach in one step. A class or object contributes its
// members, and anything else exported contributes itself.
function apiMembers() {
  const members = [];
  const seen = new Set();
  const add = (name, decl, receiver, declaredSym) => {
    const key = `${name}`;
    if (seen.has(key)) return;
    seen.add(key);
    const impl = decl && implOfDecl(decl);
    const sigDecl = decl && (isFunctionLike(decl) || ts.isMethodSignature(decl) || ts.isFunctionDeclaration(decl)) ? decl : impl;
    let params = [];
    let ret = "any";
    const type = declaredSym && checker.getTypeOfSymbol(declaredSym);
    const sig = sigDecl ? checker.getSignatureFromDeclaration(sigDecl) : type?.getCallSignatures()[0];
    if (sig) {
      params = sig.getParameters().map((p) => typeKind(checker.getTypeOfSymbol(p)));
      ret = typeKind(checker.getReturnTypeOfSignature(sig));
    }
    if (decl && ts.isGetAccessorDeclaration(decl)) params = [];
    members.push({ name, impl: impl ? fnIds.get(impl) : null, receiver, params, ret, declFile: decl ? rel(decl.getSourceFile().fileName) : null });
  };

  const isPublic = (sym) => {
    const n = sym.name;
    if (n.startsWith("_") || n.startsWith("#") || n.startsWith("__")) return false;
    const d = sym.valueDeclaration ?? sym.declarations?.[0];
    if (!d) return true;
    const mods = ts.canHaveModifiers(d) ? ts.getModifiers(d) ?? [] : [];
    return !mods.some((m) => m.kind === ts.SyntaxKind.PrivateKeyword || m.kind === ts.SyntaxKind.ProtectedKeyword);
  };

  const funcDeclOf = (sym) => {
    for (const d of sym.declarations ?? []) {
      if (implOfDecl(d)) return d;
    }
    return sym.valueDeclaration ?? sym.declarations?.[0];
  };

  const usesThis = (fn) => {
    let found = false;
    const visit = (n) => {
      if (found) return;
      if (n.kind === ts.SyntaxKind.ThisKeyword) {
        let p = n.parent;
        while (p && !(isFunctionLike(p) && !ts.isArrowFunction(p))) p = p.parent;
        if (p === fn) found = true;
      }
      ts.forEachChild(n, visit);
    };
    if (fn.body) visit(fn.body);
    return found;
  };

  const memberOf = (owner, sym, instance) => {
    if (!isPublic(sym)) return;
    const d = funcDeclOf(sym);
    if (!d) return;
    const t = checker.getTypeOfSymbol(sym);
    const isFn = isFunctionLike(d) || ts.isMethodSignature(d) || isFunctionType(t);
    if (!isFn) return;
    const impl = implOfDecl(d);
    const receiver = instance || (impl ? usesThis(impl) && !ts.isArrowFunction(impl) : false);
    add(`${owner}.${sym.name}`, d, receiver, sym);
  };

  for (const entry of config.entries) {
    const sf = program.getSourceFile(path.join(root, entry));
    const modSym = sf && checker.getSymbolAtLocation(sf);
    if (!modSym) continue;
    for (const ex of checker.getExportsOfModule(modSym)) {
      const sym = resolveAlias(ex);
      if (!sym || ex.name.startsWith("_")) continue;
      const decl = funcDeclOf(sym);
      if (!decl) continue;
      if (sym.flags & ts.SymbolFlags.Class) {
        const classDecl = sym.declarations.find(ts.isClassLike);
        const inst = checker.getDeclaredTypeOfSymbol(sym);
        for (const p of checker.getPropertiesOfType(inst)) memberOf(ex.name, p, true);
        for (const p of checker.getPropertiesOfType(checker.getTypeOfSymbol(sym))) if (p.name !== "prototype") memberOf(ex.name, p, false);
        if (classDecl) {
          const ctor = classDecl.members.find((m) => ts.isConstructorDeclaration(m));
          if (ctor) add(`${ex.name}.constructor`, ctor, false, undefined);
        }
        continue;
      }
      const t = checker.getTypeOfSymbol(sym);
      if (t.getCallSignatures().length > 0 || isFunctionLike(decl)) {
        add(ex.name, decl, false, sym);
        // A function exported with methods attached, as express's
        // `createApplication`, is also an object of methods.
      }
      if (t.getCallSignatures().length === 0 && (sym.flags & (ts.SymbolFlags.Variable | ts.SymbolFlags.ValueModule | ts.SymbolFlags.Property))) {
        for (const p of checker.getPropertiesOfType(t)) memberOf(ex.name, p, false);
      }
    }
  }
  return members;
}

// ---------------------------------------------------------------------------
// Main

const funcs = [];
const unresolved = {};
const callbackSites = {};
for (const fn of fnNodes) {
  const id = fnIds.get(fn);
  const impl = new Lowering(fn, "impl");
  impl.run();
  funcs.push({ name: `impl:${id}`, kind: "abstract-op", params: impl.params, variadic: impl.variadic ?? null, promise: false, nodes: impl.nodes });
  unresolved[id] = impl.unresolved;
  callbackSites[id] = impl.callbackSites;
}

const members = apiMembers();
for (const m of members) {
  if (!m.impl) continue;
  const fn = fnNodes.find((n) => fnIds.get(n) === m.impl);
  const api = new Lowering(fn, m.receiver ? "api" : "impl");
  api.run();
  if (!m.receiver) {
    // A function with no receiver keeps the impl calling convention, so its
    // parameter 0 is the unused `this$`. The API copy drops it.
    api.params.shift();
    if (api.variadic !== undefined) api.variadic--;
    api.nodes = api.nodes.map((n) => n);
  }
  m.api = `api:${m.name}`;
  funcs.push({ name: m.api, kind: m.receiver ? "builtin-method" : "builtin-static", params: api.params, variadic: api.variadic ?? null, promise: false, nodes: api.nodes });
}

for (const f of funcs) if (f.variadic === null) delete f.variadic;

fs.mkdirSync(outDir, { recursive: true });
fs.writeFileSync(path.join(outDir, "cfg.json"), JSON.stringify({ specTarget: `${pkgName}@${config.version}`, funcs }));
fs.writeFileSync(
  path.join(outDir, "meta.json"),
  JSON.stringify({ package: pkgName, version: config.version, files: implFiles.length, functions: fnNodes.length, members, unresolved, callbackSites }, null, 1),
);
console.log(`${pkgName}: ${implFiles.length} files, ${fnNodes.length} functions, ${members.length} API members, ${members.filter((m) => m.impl).length} joined`);
