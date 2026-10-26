// tracer.cjs is the runtime instrument.mjs's rewritten code calls. Preload it
// with `node --require tracer.cjs <driver>`. It writes the call edges it saw
// to the file TRACE_OUT names, as JSON pairs of a call site and a function.
//
// The stack holds the sites of the calls in progress. A function entered while
// the top site is still unclaimed was entered by that call, directly or
// through a native function such as Array.prototype.map, and the edge is
// recorded. The entry then claims the site, so a later entry before the next
// call, such as a getter the callee's body runs, is not charged to it.

const fs = require("node:fs");

const stack = [];
const claimed = [];
const edges = new Set();

globalThis.__dyn = {
  p(site) {
    stack.push(site);
    claimed.push(false);
    return stack.length;
  },
  r(depth, value) {
    stack.length = depth - 1;
    claimed.length = depth - 1;
    return value;
  },
  f(fn) {
    const top = stack.length - 1;
    if (top < 0 || claimed[top]) return;
    claimed[top] = true;
    edges.add(`${stack[top]}\t${fn}`);
  },
};

function write() {
  const out = process.env.TRACE_OUT;
  if (!out) return;
  fs.writeFileSync(out, JSON.stringify([...edges].map((e) => e.split("\t"))));
}
process.on("exit", write);

// A driver can leave a server or a timer running, so the process ends a few
// seconds after the driver's own code finishes.
setTimeout(() => process.exit(0), Number(process.env.TRACE_SECONDS ?? 3) * 1000);
