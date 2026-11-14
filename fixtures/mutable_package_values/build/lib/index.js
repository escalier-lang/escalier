export function elapsed(temp1) {
  const start = temp1;
  performance.mark("end");
  return performance.now() - start;
}
export function newID() {
  return crypto.randomUUID();
}
//# sourceMappingURL=./index.js.map
